#!/usr/bin/env python3
"""Run the full synthetic HTTP suite against a private local DB and dedicated Redis.

Requires the labelled local PostgreSQL from acceptance.py and a freshly built
--binary. Never reuses a shared Redis database. Keeps evidence/database; stops
only the application and Redis created by this run. No live providers are used.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import secrets
import signal
import socket
import subprocess
import time

ROOT = Path(__file__).resolve().parents[2]


def load(name, file):
    spec = importlib.util.spec_from_file_location(name, ROOT / '.github/geili' / file)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def private_write(path, value):
    path.write_text(json.dumps(value, indent=2) + '\n')
    path.chmod(0o600)


def free_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True)
    args = parser.parse_args()
    binary = Path(args.binary).resolve(strict=True)
    base = load('acceptance', 'acceptance.py')
    validation = load('validation', 'subscription-v2-validation.py')
    pg = base.PROJECT + '-postgres'
    assert base.run(['docker', 'inspect', '--format', '{{index .Config.Labels "geili.acceptance"}}', pg]).strip() == 'local'
    stamp = datetime.now(timezone.utc).strftime('%Y%m%d%H%M%S') + '_' + secrets.token_hex(3)
    db = 'acceptance_final_' + stamp
    redis = 'geili-final-' + stamp.replace('_', '-')
    private = ROOT / 'deploy/.secrets/final-business-acceptance' / stamp
    private.mkdir(parents=True, mode=0o700)
    runtime = private / 'runtime'
    runtime.mkdir(mode=0o700)
    cfg = base.local_env()
    redis_port, app_port, mock_port = free_port(), free_port(), free_port()
    url = f'http://127.0.0.1:{app_port}'
    report = {'identity': validation.source_identity(), 'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
        'database': db, 'redis': redis, 'gates': [], 'passed': False}
    private_write(private / 'report.json', report)
    subprocess.run(['docker', 'exec', pg, 'createdb', '-U', 'acceptance', db], check=True)
    subprocess.run(['docker', 'run', '-d', '--name', redis, '--label', 'geili.acceptance=local',
        '-p', f'127.0.0.1:{redis_port}:6379', 'redis:8.4-alpine'], check=True, stdout=subprocess.DEVNULL)
    app = None
    signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
    try:
        env = {**os.environ, 'AUTO_SETUP': 'true', 'SERVER_HOST': '127.0.0.1', 'SERVER_PORT': str(app_port),
            'DATA_DIR': str(runtime), 'DATABASE_HOST': '127.0.0.1', 'DATABASE_PORT': str(base.PG_PORT),
            'DATABASE_USER': 'acceptance', 'DATABASE_DBNAME': db, 'DATABASE_PASSWORD': cfg['password'],
            'DATABASE_SSLMODE': 'disable', 'REDIS_HOST': '127.0.0.1', 'REDIS_PORT': str(redis_port), 'REDIS_DB': '0',
            'ADMIN_EMAIL': 'admin@subscription-lab.invalid', 'ADMIN_PASSWORD': cfg['admin_password'],
            'JWT_SECRET': cfg['password'] * 2, 'TOTP_ENCRYPTION_KEY': secrets.token_hex(32), 'TZ': 'Asia/Shanghai'}
        with open(private / 'server.log', 'w', opener=lambda p, flags: os.open(p, flags, 0o600)) as log:
            app = subprocess.Popen([str(binary)], cwd=runtime, env=env, stdout=log, stderr=subprocess.STDOUT)
            for _ in range(200):
                if app.poll() is not None:
                    raise RuntimeError('isolated application exited; inspect private server.log')
                try:
                    if base.request('/health', base=url)[0] == 200:
                        break
                except Exception:
                    pass
                time.sleep(.2)
            else:
                raise RuntimeError('isolated application health timeout')
            q = "INSERT INTO settings(key,value) SELECT 'admin_compliance_acknowledgement:'||id,'{\"version\":\"v2026.06.10\",\"user_agent\":\"isolated final acceptance\"}' FROM users WHERE email='admin@subscription-lab.invalid' ON CONFLICT DO NOTHING;"
            base.run(['docker', 'exec', '-i', pg, 'psql', '-XAt', '-v', 'ON_ERROR_STOP=1', '-U', 'acceptance', '-d', db], input=q)
            for script in ('subscription-v2-comprehensive.py', 'final-delta-acceptance.py'):
                path = private / (script + '.log')
                print('RUN ' + script, flush=True)
                with open(path, 'w', opener=lambda p, flags: os.open(p, flags, 0o600)) as log:
                    result = subprocess.run(['python3', str(ROOT / '.github/geili' / script), '--base', url,
                        '--database', db, '--project', base.PROJECT, '--mock-port', str(mock_port)], stdout=log, stderr=subprocess.STDOUT)
                report['gates'].append({'name': script, 'exit_code': result.returncode, 'log_sha256': hashlib.sha256(path.read_bytes()).hexdigest()})
                private_write(private / 'report.json', report)
                print(('PASS ' if result.returncode == 0 else 'FAIL ') + script, flush=True)
                if result.returncode:
                    break
            report['final_identity'] = validation.source_identity()
            report['source_unchanged'] = report['identity'] == report['final_identity']
            report['passed'] = len(report['gates']) == 2 and all(g['exit_code'] == 0 for g in report['gates']) and report['source_unchanged']
    finally:
        if app is not None:
            app.terminate()
            try:
                app.wait(timeout=20)
            except subprocess.TimeoutExpired:
                app.kill()
                app.wait()
        subprocess.run(['docker', 'stop', redis], check=True, stdout=subprocess.DEVNULL)
        report['owned_resources_stopped'] = True
        private_write(private / 'report.json', report)
        print('REPORT ' + str(private / 'report.json'), flush=True)
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
