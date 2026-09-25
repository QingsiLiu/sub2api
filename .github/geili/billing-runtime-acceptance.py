#!/usr/bin/env python3
"""Isolated real-process settlement fault acceptance (no live providers).

Creates its OWN labelled PostgreSQL/Redis and two app processes with persistent
private volumes. SIGKILL, table locks, PostgreSQL stop/start and 128 parallel HTTP
requests are intentionally destructive only to those owned synthetic resources.
Evidence and logs stay private; sanitized summaries can be written with --report.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
from decimal import Decimal
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import threading
import time
import urllib.request
import urllib.error

ROOT = Path(__file__).resolve().parents[2]


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, ROOT / '.github/geili' / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def private_write(path, value):
    path.write_text(json.dumps(value, indent=2) + '\n')
    path.chmod(0o600)


def run(argv, **kw):
    return subprocess.run(argv, check=True, capture_output=True, text=True, **kw).stdout


def wait_for(label, predicate, timeout=120, interval=.2):
    start = time.monotonic()
    last = None
    while time.monotonic() - start < timeout:
        try:
            last = predicate()
            if last:
                return time.monotonic() - start
        except (OSError, subprocess.CalledProcessError):
            pass
        time.sleep(interval)
    raise AssertionError(label + ' deadline exceeded')


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--binary', required=True)
    p.add_argument('--report', help='sanitized JSON summary path')
    args = p.parse_args()
    binary = Path(args.binary).resolve(strict=True)
    v2 = load('runtime_v2', 'subscription-v2-acceptance.py')
    validation = load('runtime_validation', 'subscription-v2-validation.py')
    credentials = v2.BASE_HARNESS.local_env()  # synthetic local-only admin
    stamp = datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ') + '-' + secrets.token_hex(3)
    name = 'geili-billing-runtime-' + stamp.lower()
    pg, redis = name + '-postgres', name + '-redis'
    db = 'acceptance_billing_runtime'
    private = ROOT / 'deploy/.secrets/billing-runtime-acceptance' / stamp
    private.mkdir(parents=True, mode=0o700)
    pg_port, redis_port, mock_port = port(), port(), port()
    app_ports = [port(), port()]
    volumes = [private / 'app-a', private / 'app-b']
    for volume in volumes:
        volume.mkdir(mode=0o700)
    password = secrets.token_urlsafe(30)
    pg_env = private / 'postgres.env'
    pg_env.write_text(f'POSTGRES_USER=acceptance\nPOSTGRES_DB={db}\nPOSTGRES_PASSWORD={password}\n')
    pg_env.chmod(0o600)
    report = {'started_at': datetime.now(timezone.utc).isoformat(), 'identity': validation.source_identity(),
              'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(), 'checks': [], 'passed': False,
              'local_only': True, 'max_concurrent_http': 128}
    apps = [None, None]
    streams = []
    lock_proc = None
    fixture = None
    created = []
    model = v2.MODEL
    common = {**os.environ, 'AUTO_SETUP': 'true', 'SERVER_HOST': '127.0.0.1',
              'DATABASE_HOST': '127.0.0.1', 'DATABASE_PORT': str(pg_port), 'DATABASE_USER': 'acceptance',
              'DATABASE_DBNAME': db, 'DATABASE_PASSWORD': password, 'DATABASE_SSLMODE': 'disable',
              'DATABASE_MAX_OPEN_CONNS': '48', 'DATABASE_MAX_IDLE_CONNS': '12',
              'REDIS_HOST': '127.0.0.1', 'REDIS_PORT': str(redis_port), 'REDIS_DB': '0',
              'ADMIN_EMAIL': 'admin@subscription-lab.invalid', 'ADMIN_PASSWORD': credentials['admin_password'],
              'JWT_SECRET': password * 2, 'TOTP_ENCRYPTION_KEY': secrets.token_hex(32), 'TZ': 'Asia/Shanghai',
              'PRICING_REMOTE_URL': f'http://127.0.0.1:{mock_port}/pricing',
              'PRICING_HASH_URL': f'http://127.0.0.1:{mock_port}/pricing.sha256',
              'GATEWAY_USAGE_RECORD_WORKER_COUNT': '1', 'GATEWAY_USAGE_RECORD_QUEUE_SIZE': '1',
              'GATEWAY_USAGE_RECORD_OVERFLOW_POLICY': 'drop', 'GATEWAY_USAGE_RECORD_AUTO_SCALE_ENABLED': 'false'}

    def save():
        report['binary_unchanged'] = hashlib.sha256(binary.read_bytes()).hexdigest() == report['binary_sha256']
        private_write(private / 'report.json', report)
        if args.report:
            dest = Path(args.report)
            dest.parent.mkdir(parents=True, exist_ok=True)
            dest.write_text(json.dumps(report, indent=2) + '\n')

    def check(label, condition, detail=None):
        report['checks'].append({'name': label, 'passed': bool(condition), 'detail': detail})
        save()
        print(('PASS ' if condition else 'FAIL ') + label, flush=True)
        if not condition:
            raise AssertionError(label)

    def owned(container):
        assert container in created
        assert run(['docker', 'inspect', '--format', '{{index .Config.Labels "geili.runtime_fault"}}', container]).strip() == stamp

    def sql(q):
        owned(pg)
        out = run(['docker', 'exec', '-i', pg, 'psql', '-XAt', '-v', 'ON_ERROR_STOP=1', '-U', 'acceptance', '-d', db], input=q)
        return [json.loads(x) for x in out.splitlines() if x.startswith('{')]

    def healthy(i):
        if apps[i].poll() is not None:
            raise RuntimeError('app exited: inspect private log')
        try:
            with urllib.request.urlopen(f'http://127.0.0.1:{app_ports[i]}/health', timeout=1) as r:
                return r.status == 200
        except OSError:
            return False

    def start_app(i):
        stream = open(private / f'app-{i}-{len(streams)}.log', 'w', opener=lambda p, flags: os.open(p, flags, 0o600))
        streams.append(stream)
        env = {**common, 'SERVER_PORT': str(app_ports[i]), 'DATA_DIR': str(volumes[i]),
               'PRICING_DATA_DIR': str(volumes[i] / 'data')}
        apps[i] = subprocess.Popen([str(binary)], cwd=volumes[i], env=env, stdout=stream, stderr=subprocess.STDOUT)
        wait_for('app health', lambda: healthy(i), timeout=75)

    def kill_app(i):
        if apps[i] is not None and apps[i].poll() is None:
            apps[i].kill()
            apps[i].wait(timeout=10)
            assert apps[i].returncode == -9

    def counts():
        return sql("SELECT json_build_object('receipts',COUNT(*),'settled',COUNT(*) FILTER(WHERE state='settled'),'delivered',COUNT(*) FILTER(WHERE delivered_at IS NOT NULL),'failed_delivery',COUNT(*) FILTER(WHERE delivery_attempts>0 AND last_delivery_error IS NOT NULL),'amount',COALESCE(SUM(charged_amount) FILTER(WHERE state='settled'),0)) FROM usage_settlement_receipts;")[0]

    def call(i, key, content='runtime fixture', ident=None):
        payload = {'model': model, 'messages': [{'role': 'user', 'content': content}], 'max_tokens': 16}
        headers = {'Authorization': 'Bearer ' + key['key'], 'Content-Type': 'application/json'}
        if ident:
            headers['X-Client-Request-ID'] = ident
        req = urllib.request.Request(f'http://127.0.0.1:{app_ports[i]}/v1/chat/completions', data=json.dumps(payload).encode(), headers=headers)
        begin = time.monotonic()
        try:
            response = urllib.request.urlopen(req, timeout=90)
        except urllib.error.HTTPError as e:
            response = e
        with response:
            raw = response.read()
            return {'status': response.status, 'elapsed': time.monotonic()-begin, 'response_sha256': hashlib.sha256(raw).hexdigest()}

    try:
        for cname, image, extra in [(pg, 'postgres:18-alpine', ['--env-file', str(pg_env), '-p', f'127.0.0.1:{pg_port}:5432']),
                                   (redis, 'redis:8.4-alpine', ['-p', f'127.0.0.1:{redis_port}:6379'])]:
            run(['docker', 'run', '-d', '--name', cname, '--label', 'geili.acceptance=local', '--label', 'geili.runtime_fault='+stamp] + extra + [image])
            created.append(cname)
        wait_for('own PostgreSQL ready', lambda: subprocess.run(['docker', 'exec', pg, 'pg_isready', '-U', 'acceptance'], capture_output=True).returncode == 0, 40)
        start_app(0)
        sql("INSERT INTO settings(key,value) SELECT 'admin_compliance_acknowledgement:'||id,'{\"version\":\"v2026.06.10\",\"user_agent\":\"isolated fault acceptance\"}' FROM users WHERE email='admin@subscription-lab.invalid' ON CONFLICT DO NOTHING;")
        fixture = v2.Fixture(argparse.Namespace(base=f'http://127.0.0.1:{app_ports[0]}', database=db, project=name, mock_port=mock_port))
        fixture.start_mock()
        original = fixture.server.RequestHandlerClass
        upstream_entered, upstream_release = threading.Event(), threading.Event()

        class DelayedMock(original):
            def do_POST(self):
                if self.path.startswith('/upstream/'):
                    raw = self.rfile.read(int(self.headers.get('Content-Length', 0)))
                    if b'outage-final-metering' in raw:
                        upstream_entered.set()
                        if not upstream_release.wait(60):
                            return self.reply({'error': {'message': 'synthetic barrier timeout'}}, 503)
                    self.rfile = io.BytesIO(raw)
                return super().do_POST()
        fixture.server.RequestHandlerClass = DelayedMock
        fixture.setup()
        owner = fixture.user('runtime')
        fixture.pay(owner, fixture.quote(owner, 'month180', 'purchase', units=1))
        sub = fixture.subscription(owner)
        subkey = fixture.key(owner, sub['id'])
        fixture.api('/admin/users/'+str(owner['id'])+'/balance', {'balance': 100, 'operation': 'set', 'notes': 'synthetic fault test'}, fixture.admin)
        fixture.api('/admin/users/'+str(owner['id']), {'concurrency': 256}, fixture.admin, 'PUT')
        account = sql('SELECT json_build_object(\'id\',id) FROM accounts ORDER BY id DESC LIMIT 1;')[0]['id']
        fixture.api('/admin/accounts/'+str(account), {'concurrency': 256}, fixture.admin, 'PUT')
        balance = fixture.api('/keys', {'name': 'runtime balance', 'billing_source': 'balance', 'group_id': fixture.group['id']}, owner['token'])
        start_app(1)
        check('two real replicas same binary and independent persistent volumes', all(a.poll() is None for a in apps))
        # Only this owned database is locked; EXCLUSIVE permits read-only dashboards.
        lock_proc = subprocess.Popen(['docker', 'exec', '-i', pg, 'psql', '-XAt', '-v', 'ON_ERROR_STOP=1', '-U', 'acceptance', '-d', db], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, bufsize=1)
        lock_proc.stdin.write("BEGIN; LOCK TABLE usage_logs IN EXCLUSIVE MODE; SELECT 'LOCK_READY';\n")
        lock_proc.stdin.flush()
        while 'LOCK_READY' not in lock_proc.stdout.readline():
            if lock_proc.poll() is not None:
                raise RuntimeError('table lock failed')
        with ThreadPoolExecutor(max_workers=2) as pool:
            replies = list(pool.map(lambda pair: call(*pair), [(0,balance),(1,subkey)]))
        check('HTTP balance and subscription settle while detail table is locked', all(r['status']==200 for r in replies), replies)
        wait_for('two receipts settle', lambda: counts()['settled']==2, 20)
        check('blocked details absent with two financial receipts', counts()['delivered']==0, counts())
        wait_for('actual 15-second delivery timeout', lambda: counts()['failed_delivery']>=1, 35)
        kill_app(0);kill_app(1)
        check('both application processes actually SIGKILLed before unblock', all(a.returncode==-9 for a in apps))
        lock_proc.stdin.write('COMMIT;\n\\q\n');lock_proc.stdin.flush();lock_proc.wait(timeout=10);lock_proc=None
        recovered_start=time.monotonic();start_app(0);start_app(1)
        wait_for('restart delivers every locked receipt', lambda: counts()['delivered']==2, max(1,120-(time.monotonic()-recovered_start)))
        recovery_seconds=time.monotonic()-recovered_start
        check('SIGKILL restart recovers all details within120s without rebilling', recovery_seconds<=120 and counts()['amount']==.0024, {'recovery_seconds':recovery_seconds,**counts()})
        # Interrupt the OWN PostgreSQL after dispatch but before final mock usage.
        with ThreadPoolExecutor(max_workers=1) as pool:
            future=pool.submit(call,0,balance,'outage-final-metering')
            if not upstream_entered.wait(15):raise AssertionError('upstream dispatch barrier not reached')
            owned(pg);run(['docker','stop','--time','0',pg]);upstream_release.set()
            reply=future.result(timeout=80)
        wait_for('final usage fsync after client response',lambda:len(list((volumes[0]/'data'/'usage-settlement-ingress').glob('*.json')))==1,20)
        files=list((volumes[0]/'data'/'usage-settlement-ingress').glob('*.json'))
        check('final observed usage survives unavailable SQL in fsynced WAL', len(files)==1, {'http_status':reply['status'],'wal_files':len(files)})
        kill_app(0);kill_app(1)
        run(['docker','start',pg]);wait_for('own PostgreSQL restart',lambda:subprocess.run(['docker','exec',pg,'pg_isready','-U','acceptance'],capture_output=True).returncode==0,40)
        recovered_start=time.monotonic();start_app(0);start_app(1)
        wait_for('WAL replay becomes one billed delivered record',lambda:counts()['settled']==3 and counts()['delivered']==3,max(1,120-(time.monotonic()-recovered_start)))
        check('SQL outage plus SIGKILL replay preserves full metadata and one charge',counts()['amount']==.0036 and not list((volumes[0]/'data'/'usage-settlement-ingress').glob('*.json')),{'recovery_seconds':time.monotonic()-recovered_start,**counts()})
        start=time.monotonic()
        with ThreadPoolExecutor(max_workers=128) as pool:
            results=list(pool.map(lambda n:call(n%2,balance if n%2==0 else subkey,ident='runtime-burst-'+str(n)),range(128)))
        elapsed=time.monotonic()-start
        check('128 concurrent real HTTP requests succeed across two replicas',all(r['status']==200 for r in results),{'statuses':{str(s):sum(r['status']==s for r in results) for s in set(r['status'] for r in results)},'seconds':elapsed,'max_latency':max(r['elapsed'] for r in results)})
        wait_for('128 receipts delivered',lambda:counts()['settled']==131 and counts()['delivered']==131,120)
        final=sql(f"SELECT json_build_object('receipts',(SELECT COUNT(*) FROM usage_settlement_receipts WHERE user_id={owner['id']}),'logs',(SELECT COUNT(*) FROM usage_logs WHERE user_id={owner['id']}),'detail_tokens_exact',(SELECT COUNT(*) FROM usage_logs WHERE user_id={owner['id']} AND input_tokens=1000 AND output_tokens=100 AND actual_cost=.0012),'receipt_sum',(SELECT SUM(charged_amount) FROM usage_settlement_receipts WHERE user_id={owner['id']}),'log_sum',(SELECT SUM(actual_cost) FROM usage_logs WHERE user_id={owner['id']}),'balance',(SELECT balance FROM users WHERE id={owner['id']}),'subscription_used',(SELECT SUM(used_usd) FROM subscription_daily_usage WHERE subscription_id={sub['id']}));")[0]
        check('all131 details exact and financial sums conserve without duplicates',final['receipts']==131 and final['logs']==131 and final['detail_tokens_exact']==131 and Decimal(str(final['receipt_sum']))==Decimal('0.1572') and final['receipt_sum']==final['log_sum'] and Decimal(str(final['balance']))==Decimal('99.9208') and Decimal(str(final['subscription_used']))==Decimal('0.078'),final)
        identities=sql(f"SELECT json_build_object('financial',request_id,'key_id',api_key_id) FROM usage_settlement_receipts WHERE user_id={owner['id']} ORDER BY id;")
        report['financial_identity_sha256']=hashlib.sha256(json.dumps(identities,sort_keys=True).encode()).hexdigest()
        report['passed']=all(x['passed'] for x in report['checks'])
    except Exception as e:
        report['error_type']=type(e).__name__
        report['error']=str(e)[:500]
        raise
    finally:
        if lock_proc and lock_proc.poll() is None:
            lock_proc.terminate()
        for i in range(2):
            if apps[i] is not None and apps[i].poll() is None:
                apps[i].terminate()
                try:apps[i].wait(timeout=20)
                except subprocess.TimeoutExpired:kill_app(i)
        if fixture and fixture.server:
            fixture.server.shutdown();fixture.server.server_close()
        for container in reversed(created):
            owned(container)
            subprocess.run(['docker','stop','--time','2',container],capture_output=True)
        for stream in streams:stream.close()
        report['completed_at']=datetime.now(timezone.utc).isoformat()
        report['owned_resources_stopped']=True
        report['final_identity']=validation.source_identity()
        report['source_unchanged']=report['identity']==report['final_identity']
        report['binary_unchanged']=hashlib.sha256(binary.read_bytes()).hexdigest()==report['binary_sha256']
        report['passed']=report['passed'] and report['binary_unchanged']
        save()
        print('REPORT '+str(private/'report.json'),flush=True)
    return 0 if report['passed'] else 1


if __name__=='__main__':
    raise SystemExit(main())
