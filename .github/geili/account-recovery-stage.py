#!/usr/bin/env python3
"""Guarded candidate-bound account recovery Stage checks; mock-only, no deployment.

Default is read-only dry-run. --execute requires the canonical Stage image/revision,
exclusive lease, Linux root and namespace-loopback mock. Exact IDs are persisted
before cleanup. This runner intentionally reports its unverified external gates.
"""
import argparse
import fcntl
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import math
import subprocess
import threading
from concurrent.futures import ThreadPoolExecutor
import time
from datetime import datetime, timezone
from urllib.parse import urlsplit

MODEL = 'claude-opus-5'
OTHER_MODEL = 'claude-sonnet-5-5'
PROTOCOLS = ('messages', 'responses', 'chat/completions')
TABLES = ('account_probe_leases_geili', 'account_probe_logs_geili', 'account_runtime_geili')
EXTERNAL_UNVERIFIED = ['actual Cloudflare edge timeout', 'real suppliers', 'payments', 'production deployment']
REMAINING = []
# The old-image discovery completed only225 mock calls in180s including20
# warmups. Allow the unchanged1000-sample gate to finish on this shared host.
BENCH_BATCH_DEADLINE_SECONDS = 1200
# Recovery selection is capped at30s, below the existing45s first edge heartbeat.
# Test that deadline separately from a55s mock supplier with synthetic Cf-Ray.
STREAM_REQUEST = r'''
import json,sys,time,urllib.request,urllib.error
p=json.load(sys.stdin)
h={'Content-Type':'application/json','Authorization':'Bearer '+p['token'],'Cf-Ray':'synthetic-stage-recovery'}
r=urllib.request.Request(p['url'],data=json.dumps(p['data']).encode(),headers=h,method='POST')
t=time.monotonic();beats=[];lines=[];size=0
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
try:q=opener.open(r,timeout=75)
except urllib.error.HTTPError as e:q=e
with q:
 while True:
  line=q.readline()
  if not line:break
  size+=len(line)
  if size>1048576:raise RuntimeError('synthetic stream exceeds bounded capture')
  text=line.decode();lines.append(text)
  if text.startswith(':') or text.strip() in ('event: ping','event: keepalive'):beats.append(time.monotonic()-t)
 print(json.dumps([q.status,''.join(lines),{'elapsed':time.monotonic()-t,'heartbeats':beats}]))
'''




BENCH_REQUEST = r'''
import json,sys,time,os,urllib.request,urllib.error
from concurrent.futures import ThreadPoolExecutor,as_completed
p=json.load(sys.stdin)
journal=open(p['journal'],'x') if p.get('journal') else None
if journal:os.chmod(p['journal'],0o600)
def call(index):
 payload={'model':p['model'],'stream':True,'max_tokens':16,'messages':[{'role':'user','content':'fixture_performance_'+str(index)}]}
 h={'Content-Type':'application/json','Authorization':'Bearer '+p['token']}
 r=urllib.request.Request(p['url'],data=json.dumps(payload).encode(),headers=h)
 t=time.monotonic();status=None;ok=False
 try:
  try:q=urllib.request.build_opener(urllib.request.ProxyHandler({})).open(r,timeout=40)
  except urllib.error.HTTPError as e:q=e
  with q:
   status=q.status;raw=q.read(1048577).decode();events=[]
   for frame in raw.replace('\r\n','\n').split('\n\n'):
    data='\n'.join(x[5:].lstrip() for x in frame.splitlines() if x.startswith('data:'))
    if data:events.append(json.loads(data))
   ok=status==200 and len(raw)<=1048576 and any(e.get('type')=='message_stop' for e in events) and not any(e.get('error') or e.get('type')=='error' for e in events)
 except Exception:pass
 return {'index':index,'latency':time.monotonic()-t,'status':status,'ok':ok}
def collect(pool,indices,phase):
 futures=[pool.submit(call,index) for index in indices];rows=[]
 for future in as_completed(futures):
  row=future.result();rows.append(row)
  if journal:
   journal.write(json.dumps(dict(row,phase=phase))+'\n');journal.flush()
 return rows
with ThreadPoolExecutor(max_workers=4) as pool:
 warm=collect(pool,range(-20,0),'warmup')
 t=time.monotonic();rows=collect(pool,range(1000),'measured');elapsed=time.monotonic()-t
if journal:journal.close()
print(json.dumps({'warmup_failed':sum(not r['ok'] for r in warm),'elapsed_seconds':elapsed,'latencies_seconds':[r['latency'] for r in rows],'response_statuses':[r['status'] for r in rows],'failed_responses':sum(not r['ok'] for r in rows)}))
'''


def performance_summary(batches):
    result = {'passed': False, 'concurrency': 4, 'samples_per_batch': 1000, 'batches': 4}
    stats = {}
    for mode in ('baseline', 'candidate'):
        rows = [row for row in batches if row.get('mode') == mode]
        if len(rows) != 2 or any(len(row.get('latencies_seconds', [])) != 1000 for row in rows):
            return dict(result, failure='exactly two batches of1000 per image required')
        latencies = [v for row in rows for v in row['latencies_seconds']]
        elapsed = sum(row.get('elapsed_seconds', 0) for row in rows)
        if (not math.isfinite(elapsed) or elapsed <= 0 or any(type(v) not in (float, int) or not math.isfinite(v) or v <= 0 for v in latencies)):
            return dict(result, failure='invalid timing; no samples may be discarded')
        statuses = [status for row in rows for status in row.get('response_statuses', [])]
        failed = sum(row.get('failed_responses', 0) + row.get('warmup_failed', 0) for row in rows)
        stats[mode] = {'samples': len(latencies), 'p95_ms': sorted(latencies)[math.ceil(.95 * len(latencies)) - 1] * 1000,
                       'throughput_per_second': len(latencies) / elapsed, 'failed_responses': failed,
                       'all_http200': len(statuses) == 2000 and all(status == 200 for status in statuses)}
    result.update(modes=stats, p95_delta_ms=stats['candidate']['p95_ms'] - stats['baseline']['p95_ms'],
                  throughput_ratio=stats['candidate']['throughput_per_second'] / stats['baseline']['throughput_per_second'])
    result['passed'] = (result['p95_delta_ms'] <= 10 and result['throughput_ratio'] >= .98 and
                        all(row['all_http200'] and row['failed_responses'] == 0 for row in stats.values()))
    return result


def private_client(command, payload, timeout):
    # Tokens travel only through stdin. Bounded clients may outlive the canonical
    # generic60s command limit for the explicit55s wait or1000-request batch.
    try:
        result = subprocess.run(command, input=json.dumps(payload), text=True, capture_output=True, timeout=timeout)
    except subprocess.TimeoutExpired:
        raise RuntimeError('owned Stage mock client deadline exceeded; preserve failed samples') from None
    if result.returncode:
        raise RuntimeError('owned Stage mock client failed; inspect root-only runner evidence')
    return result.stdout


def benchmark_client(command, payload):
    return json.loads(private_client(command, payload, BENCH_BATCH_DEADLINE_SECONDS))


def load_ops(directory):
    path = Path(directory).resolve(strict=True) / 'accept-two-instance-cache-stage.py'
    spec = importlib.util.spec_from_file_location('recovery_stage_cache', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def validate_guard_options(args):
    for name in ('expected_revision', 'expected_production_revision'):
        if not re.fullmatch(r'[0-9a-f]{40}', getattr(args, name, '')):
            raise RuntimeError('full candidate and production revisions required')
    for name in ('expected_image', 'expected_production_image'):
        if not re.fullmatch(r'ghcr\.io/qingsiliu/sub2api@sha256:[0-9a-f]{64}', getattr(args, name, '')):
            raise RuntimeError('immutable candidate and production digests required')
    for value in (args.mock_port, args.replica_port):
        if type(value) is not int or not 1024 <= value <= 65535:
            raise RuntimeError('nonprivileged namespace ports required')
    if args.mock_port == args.replica_port or 8080 in (args.mock_port, args.replica_port):
        raise RuntimeError('namespace ports must not collide with each other or the Stage server')
    if args.execute and args.restore or args.dry_run and (args.execute or args.restore):
        raise RuntimeError('execute, restore and explicit dry-run are mutually exclusive')
    baseline_image = getattr(args, 'baseline_image', None)
    baseline_revision = getattr(args, 'baseline_revision', None)
    if bool(baseline_image) != bool(baseline_revision):
        raise RuntimeError('baseline image and full revision must be supplied together')
    if baseline_image:
        if not re.fullmatch(r'ghcr\.io/qingsiliu/sub2api@sha256:[0-9a-f]{64}', baseline_image) or not re.fullmatch(r'[0-9a-f]{40}', baseline_revision):
            raise RuntimeError('explicit immutable baseline image and revision required')
        if baseline_image == args.expected_image or baseline_revision == args.expected_revision:
            raise RuntimeError('baseline must be the previous image, not the candidate or an off switch')
        port = getattr(args, 'baseline_port', 18520)
        if type(port) is not int or not 1024 <= port <= 65535 or port in (8080, args.mock_port, args.replica_port):
            raise RuntimeError('baseline requires a separate nonprivileged loopback port')
    if args.execute and not baseline_image:
        raise RuntimeError('--execute requires explicit baseline image/revision for the performance gate')
    args.dry_run = not (args.execute or args.restore)


def mock_account_url(base, label):
    if not re.fullmatch(r'http://127\.0\.0\.1:[0-9]{4,5}', base) or not re.fullmatch(r'[a-z0-9_-]+', label):
        raise RuntimeError('synthetic accounts must use this owned namespace loopback mock')
    return base + '/recovery/' + label


def recovery_route(target):
    # BaseHTTPRequestHandler.path includes the query. Anthropic appends
    # ?beta=true to both inference and health probes; route only the URL path.
    path = urlsplit(target).path
    return path, re.fullmatch(r'/recovery/([a-z0-9_-]+)/(?:v1/)?messages', path)


def exact_ids(ids):
    if not ids or any(type(i) is not int or i <= 0 for i in ids) or len(set(ids)) != len(ids):
        raise RuntimeError('cleanup requires positive unique exact synthetic account IDs')
    return ','.join(map(str, sorted(ids)))


def balance_latch_keys(ids):
    exact_ids(ids)
    return ['geili:account:balance_latch:' + str(i) for i in sorted(ids)]


def cleanup_runtime_sql(ids):
    selected = exact_ids(ids)
    # Caller has verified names, expired fixtures and drained every owned lease.
    return 'BEGIN;\n' + '\n'.join('DELETE FROM ' + table + ' WHERE account_id IN (' + selected + ');'
                                 for table in TABLES) + '\nCOMMIT;'


def is_probe(payload):
    return (isinstance(payload, dict) and payload.get('max_tokens') == 64
            and payload.get('messages') == [{'role': 'user', 'content': 'Say OK.'}]
            and not any(k in payload for k in ('tools', 'thinking', 'reasoning')))


def anthropic_sse(model, mode):
    events = [{'type': 'message_start', 'message': {'id': 'msg_synthetic_recovery', 'type': 'message',
               'role': 'assistant', 'model': model, 'content': [], 'usage': {'input_tokens': 1, 'output_tokens': 0}}}]
    if mode in ('empty_delta', 'bad_tool'):
        events.extend([{'type': 'content_block_start', 'index': 0, 'content_block':
                       {'type': 'tool_use', 'id': 'toolu_synthetic', 'name': 'fixture_ping', 'input': {}}},
                       {'type': 'content_block_delta', 'index': 0, 'delta': {'type': 'input_json_delta',
                        'partial_json': '' if mode == 'empty_delta' else '{"broken":'}}])
        reason = 'tool_use'
    else:
        events.extend([{'type': 'content_block_start', 'index': 0, 'content_block': {'type': 'text', 'text': ''}},
                       {'type': 'content_block_delta', 'index': 0, 'delta': {'type': 'text_delta', 'text': 'recovery acceptance OK'}}])
        reason = 'end_turn'
    events.extend([{'type': 'content_block_stop', 'index': 0},
                   {'type': 'message_delta', 'delta': {'stop_reason': reason}, 'usage': {'output_tokens': 1}},
                   {'type': 'message_stop'}])
    return ''.join('event: ' + e['type'] + '\ndata: ' + json.dumps(e) + '\n\n' for e in events).encode()


def sse_events(body):
    if not isinstance(body, str):
        return []
    out = []
    for frame in body.replace('\r\n', '\n').split('\n\n'):
        payload = '\n'.join(line[5:].lstrip() for line in frame.splitlines() if line.startswith('data:'))
        if not payload:
            continue
        if payload == '[DONE]':
            out.append({'type': '[DONE]'})
        else:
            try:
                item = json.loads(payload)
                if isinstance(item, dict):
                    out.append(item)
            except ValueError:
                return []
    return out


def completed(protocol, response):
    if response[0] != 200:
        return False
    events = sse_events(response[1])
    if not events or any(e.get('error') or e.get('type') in ('error', 'response.failed') for e in events):
        return False
    if protocol == 'messages':
        return any(e.get('type') == 'message_stop' for e in events)
    if protocol == 'responses':
        return any(e.get('type') == 'response.completed' for e in events)
    return any(e.get('type') == '[DONE]' for e in events) and any(
        c.get('finish_reason') for e in events for c in e.get('choices', []) if isinstance(c, dict))


def response_detail(response):
    # Preserve admission/terminal evidence without customer or error payloads.
    return {'status': response[0], 'event_types': [e.get('type') for e in sse_events(response[1])]}


def foreground_wait_ok(response):
    return (not completed('messages', response) and isinstance(response[2], dict)
            and 29 <= response[2].get('elapsed', -1) <= 36)


def heartbeat_wait_ok(response):
    timing = response[2] if len(response) > 2 and isinstance(response[2], dict) else {}
    beats = timing.get('heartbeats', [])
    return (completed('messages', response) and 54 <= timing.get('elapsed', -1) <= 65
            and bool(beats) and 40 <= beats[0] <= 50)


def fixture_type(cache, source):
    class RecoveryFixture(cache.fixture_type(source)):
        def prepare(self):
            super().prepare()
            self.snapshot.update(recovery_runtime_restored=False, recovery_runner_kind='account-recovery-stage-v1',
                                 recovery_account_names={})
            cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)

        def verify_schema(self):
            result = super().verify_schema()
            migration = Path(self.args.source_root) / 'backend/migrations/282_account_runtime_geili.sql'
            digest = hashlib.sha256(migration.read_text().strip().encode()).hexdigest()
            rows = self.sql("SELECT json_build_object('checksum',checksum) FROM schema_migrations WHERE filename='282_account_runtime_geili.sql';")
            if rows != [{'checksum': digest}]:
                raise RuntimeError('candidate account runtime migration hash does not match Stage')
            for table in TABLES:
                if not self.sql("SELECT json_build_object('ok',to_regclass('public." + table + "') IS NOT NULL);")[0]['ok']:
                    raise RuntimeError('missing candidate runtime table')
            result['account_runtime_migration_sha256'] = digest
            return result

        def start_mock(self):
            super().start_mock()
            fixture = self
            self.recovery_modes, self.recovery_events, self.recovery_unmatched_paths = {}, [], {}
            self.probe_release = threading.Event()
            original = self.server.RequestHandlerClass
            class RecoveryMock(original):
                def do_POST(self):
                    path, match = recovery_route(self.path)
                    if not match:
                        # Preserve bounded route evidence without headers, bodies
                        # or query values, which can contain private credentials.
                        with fixture.lock:
                            key = path[:256]
                            if key not in fixture.recovery_unmatched_paths and len(fixture.recovery_unmatched_paths) >= 64:
                                key = '<additional paths>'
                            fixture.recovery_unmatched_paths[key] = fixture.recovery_unmatched_paths.get(key, 0) + 1
                        return super().do_POST()
                    payload = json.loads(self.rfile.read(int(self.headers.get('Content-Length', '0'))))
                    label = match.group(1)
                    probe = is_probe(payload)
                    with fixture.lock:
                        mode = fixture.recovery_modes.get(label, 'healthy')
                        if not probe:
                            marker = json.dumps(payload.get('messages', []))
                            if 'fixture_empty_delta' in marker: mode = 'empty_delta'
                            elif 'fixture_bad_tool' in marker: mode = 'bad_tool'
                        fixture.recovery_events.append({'label': label, 'probe': probe, 'model': payload.get('model'),
                                                        'mode': mode, 'at': time.monotonic()})
                    if mode == 'hold_probe' and probe:
                        if not fixture.probe_release.wait(20):
                            return self.reply({'error': {'type': 'api_error', 'message': 'synthetic hold deadline'}}, 503)
                    if mode == 'probe_unhealthy' and probe:
                        return self.reply({'error': {'type': 'api_error', 'message': 'synthetic probe unavailable'}}, 503)
                    if mode == 'long_supplier' and not probe:
                        time.sleep(55)
                    if mode == 'balance':
                        return self.reply({'error': {'type': 'invalid_request_error', 'message': 'Your credit balance is too low'}}, 403)
                    model = payload.get('model', MODEL)
                    if payload.get('stream'):
                        body = anthropic_sse(model, mode)
                        self.send_response(200)
                        self.send_header('Content-Type', 'text/event-stream')
                        self.send_header('Content-Length', str(len(body)))
                        self.end_headers()
                        self.wfile.write(body)
                    else:
                        return self.reply({'id': 'msg_synthetic_recovery', 'type': 'message', 'role': 'assistant',
                            'model': model, 'content': [{'type': 'text', 'text': 'OK'}], 'stop_reason': 'end_turn',
                            'usage': {'input_tokens': 1, 'output_tokens': 1}}, 200)
            self.server.RequestHandlerClass = RecoveryMock

        def setup(self):
            self.start_replica()
            self.owner = self.user('account-recovery')
            self.api('/admin/users/' + str(self.owner['id']) + '/balance',
                     {'balance': 100, 'operation': 'set', 'notes': 'Synthetic mock-only recovery acceptance'}, self.admin)

        def new_case(self, label, mode='healthy', manual=False, pool=False, group=None, declare_allowlist=True):
            if type(declare_allowlist) is not bool:
                raise RuntimeError('fixture model declaration opt-out must be explicit boolean')
            group_payload = {'name': 'Recovery ' + label + ' ' + self.suffix,
                'platform': 'anthropic', 'rate_multiplier': 1, 'subscription_rate_multiplier': 1,
                'subscription_enabled': False, 'model_pricing': [{'models': [MODEL, OTHER_MODEL], 'billing_mode': 'token',
                    'input_price': .000001, 'output_price': .000002}]}
            if declare_allowlist:
                group_payload['model_allowlist'] = {'enabled': True, 'models': [MODEL, OTHER_MODEL]}
            group = group or self.api('/admin/groups', group_payload, self.admin)
            with self.lock: self.recovery_modes[label] = mode
            account = self.api('/admin/accounts', {'name': 'Recovery ' + label + ' ' + self.suffix,
                'platform': 'anthropic', 'type': 'apikey', 'credentials': {'api_key': 'synthetic-recovery-test-key',
                    'base_url': mock_account_url(self.mock_base, label), 'pool_mode': pool,
                    'model_mapping': {MODEL: MODEL, OTHER_MODEL: OTHER_MODEL}}, 'group_ids': [group['id']], 'concurrency': 4, 'priority': 1}, self.admin)
            self.snapshot['recovery_account_names'][str(account['id'])] = account['name']
            cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)
            if manual:
                self.api('/admin/accounts/' + str(account['id']) + '/schedulable', {'schedulable': False}, self.admin)
            key = self.api('/keys', {'name': 'Recovery ' + label + ' ' + self.suffix,
                'billing_source': 'balance', 'routing_mode': 'composite', 'group_ids': [group['id']]}, self.owner['token'])
            return {'label': label, 'group': group, 'account': account, 'key': key}

        def call(self, case, protocol='messages', marker='fixture_healthy', secondary=False, model=MODEL):
            payload = {'model': model, 'stream': True, 'max_tokens': 16,
                       'prompt_cache_key': self.suffix + '-' + case['label'] + '-' + marker}
            if protocol == 'responses': payload['input'] = [{'role': 'user', 'content': marker}]
            else: payload['messages'] = [{'role': 'user', 'content': marker}]
            return (self.replica_request if secondary else self.request)('/v1/' + protocol, payload, case['key']['key'])

        def timed_call(self, case):
            self.guard()
            payload = {'url': self.base + '/v1/messages', 'token': case['key']['key'],
                       'data': {'model': MODEL, 'stream': True, 'max_tokens': 16,
                                'messages': [{'role': 'user', 'content': 'fixture_timed_healthy'}]}}
            return json.loads(private_client(['python3', '-c', STREAM_REQUEST], payload, 90))

        def probe_count(self, labels):
            return sum(self.counts(label)['probes'] for label in labels)

        def wait_until(self, label, predicate, seconds=15):
            deadline = time.monotonic() + seconds
            while not predicate():
                if time.monotonic() >= deadline: raise RuntimeError(label + ' readiness deadline exceeded')
                time.sleep(.1)

        def lease_count(self, cases):
            ids = exact_ids([case['account']['id'] for case in cases])
            return self.sql("SELECT json_build_object('n',COUNT(*)) FROM account_probe_leases_geili WHERE account_id IN (" + ids + ');')[0]['n']

        def counts(self, label):
            with self.lock:
                rows = [e for e in self.recovery_events if e['label'] == label]
                return {'inference': sum(not e['probe'] for e in rows), 'probes': sum(e['probe'] for e in rows)}

        def account_state(self, case):
            return self.api('/admin/accounts/' + str(case['account']['id']), token=self.admin)

        def usage(self, case):
            return self.sql("SELECT json_build_object('n',COUNT(*),'wrong_group',COUNT(*) FILTER(WHERE group_id<>" +
                str(case['group']['id']) + "),'cost',COALESCE(SUM(actual_cost),0)) FROM usage_logs WHERE api_key_id=" +
                str(case['key']['id']) + ';')[0]

        def wait_usage(self, case, count):
            until = time.monotonic() + 15
            while True:
                row = self.usage(case)
                if row['n'] >= count or time.monotonic() >= until: return row
                time.sleep(.1)

        def run_checks(self):
            # Each case has its own group/key; no shared Stage account can serve it.
            for protocol in PROTOCOLS:
                case = self.new_case('tool-' + protocol.replace('/', '-'))
                good = self.call(case, protocol, 'fixture_empty_delta')
                self.check(protocol + ' empty argument delta completes', completed(protocol, good), {'status': good[0]})
                bad = self.call(case, protocol, 'fixture_bad_tool')
                events = sse_events(bad[1])
                self.check(protocol + ' truly malformed tool remains a failure', not completed(protocol, bad)
                    and (bad[0] >= 400 or any(e.get('error') or e.get('type') in ('error', 'response.failed') for e in events)),
                    {'status': bad[0], 'event_types': [e.get('type') for e in events]})

            self.probe_scope_checks()
            self.recoverable_catalog_checks()
            manual = self.new_case('manual', manual=True)
            unrelated = self.new_case('other-group', manual=True)
            resumed = self.call(manual)
            self.check('manually stopped same-group account is checked and resumes customer request', completed('messages', resumed)
                and self.account_state(manual)['runtime_state']['state'] == 'enabled',
                {**response_detail(resumed), 'calls': self.counts('manual')})
            self.check('recovery never probes another target group', self.counts('other-group') == {'inference': 0, 'probes': 0})
            row = self.wait_usage(manual, 1)
            self.check('automatic probe adds no customer usage row and actual group settles once', row['n'] == 1
                and row['wrong_group'] == 0 and float(row['cost']) > 0 and self.counts('manual') == {'inference': 1, 'probes': 1}, row)

            balance = self.new_case('balance', mode='balance', pool=True)
            failed = self.call(balance)
            state = self.account_state(balance)
            self.check('pool403 balance failure stops the entire account', not completed('messages', failed)
                and state['runtime_state']['balance_blocked'] and not state['schedulable'], self.counts('balance'))
            before = self.counts('balance')
            for path, data, method in [('/schedulable', {'schedulable': True}, None),
                                      ('/recover-state', {}, None), ('/clear-error', {}, None),
                                      ('', {'status': 'active'}, 'PUT')]:
                response = self.request('/api/v1/admin/accounts/' + str(balance['account']['id']) + path, data, self.admin, method)
                state = self.account_state(balance)
                self.check('legacy enable cannot bypass balance: ' + (path or 'update'),
                    state['runtime_state']['balance_blocked'] and not state['schedulable'] and state['status'] != 'active',
                    {'status': response[0]})
            denied = self.call(balance, secondary=True)
            self.check('second instance refuses stopped supplier without additional inference or probe',
                not completed('messages', denied) and self.counts('balance') == before, self.counts('balance'))
            response = self.request('/api/v1/admin/accounts/' + str(balance['account']['id']) + '/recheck',
                                    {'model': MODEL}, self.admin)
            self.check('unconfirmed recharge returns409 without supplier probe', response[0] == 409
                and self.counts('balance') == before, {'status': response[0]})
            with self.lock: self.recovery_modes['balance'] = 'healthy'
            usage_before = self.usage(balance)
            response = self.request('/api/v1/admin/accounts/' + str(balance['account']['id']) + '/recheck',
                                    {'model': MODEL, 'confirm_recharged': True}, self.admin)
            state = self.account_state(balance)
            self.check('confirmed recharge checks current account and restores availability', response[0] == 200
                and not state['runtime_state']['balance_blocked'] and state['runtime_state']['state'] == 'enabled',
                {'status': response[0], 'calls': self.counts('balance')})
            self.check('administrator probe has no customer usage', self.usage(balance) == usage_before)
            resumed = self.call(balance, secondary=True)
            self.check('replica sees recovered durable state without a restart', completed('messages', resumed))
            row = self.wait_usage(balance, 1)
            self.check('post-recharge customer request settles exactly once', row['n'] == 1 and row['wrong_group'] == 0, row)
            self.bounded_recovery_checks()
            self.timing_and_model_checks()
            self.performance_checks()

        def probe_scope_checks(self):
            # Same valid stopped account/key: media-output requests must not
            # create probes, then an ordinary text request must recover it.
            for protocol in PROTOCOLS:
                case = self.new_case('scope-' + protocol.replace('/', '-'), manual=True)
                payload = {'model': MODEL, 'stream': True, 'max_tokens': 16}
                if protocol == 'responses':
                    payload.update(input='fixture_scope_media', tools=[{'type': 'image_generation'}])
                else:
                    payload.update(messages=[{'role': 'user', 'content': 'fixture_scope_media'}], modalities=['audio'])
                denied = self.request('/v1/' + protocol, payload, case['key']['key'])
                self.check(protocol + ' media output cannot probe a stopped text account',
                    not completed(protocol, denied) and self.counts(case['label']) == {'inference': 0, 'probes': 0}
                    and self.usage(case)['n'] == 0 and not self.account_state(case)['schedulable'],
                    {**response_detail(denied), 'calls': self.counts(case['label'])})
                resumed = self.call(case, protocol)
                self.check(protocol + ' text scope still restores the same account and settles once',
                    completed(protocol, resumed) and self.counts(case['label']) == {'inference': 1, 'probes': 1}
                    and self.wait_usage(case, 1)['n'] == 1, response_detail(resumed))

        def recoverable_catalog_checks(self):
            # Exercise the real HTTP admission fix independently of an explicit
            # group allowlist. Only this stopped account declares model mappings.
            for status, protocol in (('inactive', 'messages'), ('error', 'responses')):
                case = self.new_case('catalog-' + status, declare_allowlist=False)
                response = self.request('/api/v1/admin/accounts/' + str(case['account']['id']),
                                        {'status': status}, self.admin, 'PUT')
                before = self.account_state(case)
                self.check('catalog ' + status + ' fixture is stopped without supplier calls', response[0] == 200
                    and before['status'] == status and self.counts(case['label']) == {'inference': 0, 'probes': 0},
                    {'status': response[0], 'account_status': before['status'], 'calls': self.counts(case['label'])})
                resumed = self.call(case, protocol)
                state = self.account_state(case)
                self.check(protocol + ' admits mapped model from ' + status + ' account and resumes after probe',
                    completed(protocol, resumed) and state['runtime_state']['state'] == 'enabled'
                    and state['status'] == 'active' and self.counts(case['label']) == {'inference': 1, 'probes': 1},
                    {**response_detail(resumed), 'account_status': state['status'], 'calls': self.counts(case['label'])})
                row = self.wait_usage(case, 1)
                self.check('catalog ' + status + ' recovery settles one customer row in actual group',
                    row['n'] == 1 and row['wrong_group'] == 0 and float(row['cost']) > 0, row)

        def bounded_recovery_checks(self):
            # Isolated group, five stopped candidates. Only three may be probed
            # in the first batch; the customer request owns one billable result.
            candidates = [self.new_case('auto-0', mode='hold_probe', manual=True)]
            candidates.extend(self.new_case('auto-' + str(i), mode='hold_probe', manual=True,
                                            group=candidates[0]['group']) for i in range(1, 5))
            labels = [case['label'] for case in candidates]
            self.probe_release.clear()
            with ThreadPoolExecutor(max_workers=1) as pool:
                pending = pool.submit(self.timed_call, candidates[0])
                try:
                    self.wait_until('three automatic probes', lambda: self.probe_count(labels) >= 3)
                    self.check('automatic first batch limits candidates to3 with real PostgreSQL leases',
                               self.probe_count(labels) == 3 and self.lease_count(candidates) == 3)
                    time.sleep(2)
                    self.check('blocked candidate batch cannot start a fourth probe', self.probe_count(labels) == 3)
                finally:
                    self.probe_release.set()
                response = pending.result(timeout=40)
            self.check('bounded candidate batch resumes one customer stream', completed('messages', response)
                       and self.probe_count(labels) == 3, response[2])
            row = self.wait_usage(candidates[0], 1)
            self.check('three automatic probes produce only one customer usage row', row['n'] == 1 and row['wrong_group'] == 0, row)

            # Four administrator probes split across primary and same-image
            # replica. PostgreSQL coordinates their global lease admission.
            cases = [self.new_case('global-' + str(i), mode='hold_probe', manual=True) for i in range(5)]
            labels = [case['label'] for case in cases]
            self.probe_release.clear()
            def recheck(index):
                case = cases[index]
                caller = self.replica_request if index % 2 else self.request
                return caller('/api/v1/admin/accounts/' + str(case['account']['id']) + '/recheck', {'model': MODEL}, self.admin)
            with ThreadPoolExecutor(max_workers=4) as pool:
                pending = [pool.submit(recheck, i) for i in range(4)]
                try:
                    self.wait_until('four cross-process probes', lambda: self.probe_count(labels) >= 4)
                    self.check('two processes admit exactly4 global probe leases', self.probe_count(labels) == 4 and self.lease_count(cases) == 4)
                    fifth = recheck(4)
                    self.check('fifth global probe is rejected before supplier request', fifth[0] == 503 and self.probe_count(labels) == 4,
                               {'status': fifth[0]})
                finally:
                    self.probe_release.set()
                responses = [future.result(timeout=35) for future in pending]
            self.check('four admitted cross-process probes complete and release leases', all(r[0] == 200 for r in responses)
                       and self.lease_count(cases) == 0)
            self.check('global administrator probe batch adds zero customer usage', all(self.usage(case)['n'] == 0 for case in cases))

        def timing_and_model_checks(self):
            case = self.new_case('model-isolation')
            state = {MODEL: {'blocked': True, 'health': 'unhealthy',
                            'next_check_at': '2099-01-01T00:00:00Z', 'next_probe_at': '2099-01-01T00:00:00Z'}}
            # Only this run's exact synthetic account is seeded. This checks real
            # scheduling/cache admission; natural threshold counting has separate
            # Go/PostgreSQL coverage and is not claimed by this seeded HTTP case.
            ident = exact_ids([case['account']['id']])
            literal = cache.b.stage.literal(json.dumps(state))
            self.sql('INSERT INTO account_runtime_geili(account_id,version,model_states) VALUES(' + ident + ',1,' + literal +
                     '::jsonb) ON CONFLICT(account_id) DO UPDATE SET version=account_runtime_geili.version+1,model_states=EXCLUDED.model_states;')
            before = self.counts(case['label'])
            denied = self.call(case, secondary=True)
            self.check('blocked model is rejected by second process without supplier calls', not completed('messages', denied)
                       and self.counts(case['label']) == before)
            good = self.call(case, model=OTHER_MODEL)
            self.check('a blocked model does not disable another healthy model', completed('messages', good))
            row = self.wait_usage(case, 1)
            self.check('isolated healthy model settles once', row['n'] == 1, row)

            delayed = self.new_case('background', mode='probe_unhealthy', manual=True)
            response = self.timed_call(delayed)
            self.check('actual foreground recovery wait ends at30s plus bounded overhead', foreground_wait_ok(response), response[2])
            self.check('foreground failed wait has no customer inference or charge', self.counts('background')['inference'] == 0
                       and self.usage(delayed)['n'] == 0)
            with self.lock: self.recovery_modes['background'] = 'healthy'
            self.wait_until('background recovery continuation', lambda: self.account_state(delayed)['runtime_state']['state'] == 'enabled', 50)
            self.check('background continues after foreground ends and does not bill probes', self.counts('background')['probes'] >= 2
                       and self.counts('background')['inference'] == 0 and self.usage(delayed)['n'] == 0)

            long_wait = self.new_case('heartbeat', mode='long_supplier')
            response = self.timed_call(long_wait)
            self.check('existing neutral heartbeat arrives around45s during55s mock supplier wait', heartbeat_wait_ok(response), response[2])
            self.check('synthetic edge heartbeat preserves single customer settlement', self.wait_usage(long_wait, 1)['n'] == 1)

        def start_baseline(self):
            self.guard()
            primary = self.inspect()[0]
            image = json.loads(cache.b.stage.command(['docker', 'image', 'inspect', self.args.baseline_image]))[0]
            if image.get('Config', {}).get('Labels', {}).get('org.opencontainers.image.revision') != self.args.baseline_revision:
                raise RuntimeError('pre-existing immutable baseline image has a different revision')
            cache.assert_replica_port_available(primary['State']['Pid'], self.args.baseline_port)
            name = cache.b.stage.PROJECT + '-recovery-baseline-' + self.suffix.lower()
            record = {'name': name, 'owner': self.suffix, 'volume': name + '-data',
                      'image': self.args.baseline_image, 'revision': self.args.baseline_revision, 'removed': False}
            self.snapshot['recovery_baseline'] = record
            cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)
            for command in (['docker', 'inspect', name], ['docker', 'volume', 'inspect', record['volume']]):
                if subprocess.run(command, capture_output=True).returncode == 0:
                    raise RuntimeError('refuse reusing baseline container or volume')
            env = cache.replica_env(primary, self.args.baseline_port)
            path = self.private / 'baseline.env'
            with path.open('x') as out:
                os.chmod(path, 0o600)
                out.write(''.join(k + '=' + v + '\n' for k, v in env.items()))
            cache.b.stage.command(['docker', 'volume', 'create', '--label', 'geili.cache_acceptance=' + record['owner'], record['volume']])
            record['id'] = cache.b.stage.command(['docker', 'run', '-d', '--name', name,
                '--label', 'geili.cache_acceptance=' + record['owner'], '--network', 'container:' + primary['Id'],
                '--env-file', str(path), '--mount', 'type=volume,source=' + record['volume'] + ',target=/app/data', record['image']]).strip()
            cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)
            self.wait_until('owned baseline health', lambda: self.baseline_health(), 75)

        def guard_baseline(self):
            self.guard()
            record = self.snapshot['recovery_baseline']
            row = json.loads(cache.b.stage.command(['docker', 'inspect', record['name']]))[0]
            if (not cache.container_owned(row, record['owner'], record['image'], record['revision']) or
                row['Id'] != record['id'] or not row.get('State', {}).get('Running') or
                row['HostConfig'].get('NetworkMode') != 'container:' + self.expected_identity[0]):
                raise RuntimeError('owned baseline image/revision/network identity differs')
            return self.inspect()[0]['State']['Pid']

        def baseline_health(self):
            pid = self.guard_baseline()
            payload = {'url': 'http://127.0.0.1:' + str(self.args.baseline_port) + '/health', 'data': None, 'token': None, 'method': None}
            try:
                return json.loads(cache.b.stage.command(['nsenter', '-t', str(pid), '-n', 'python3', '-c', cache.NETNS_REQUEST], input=json.dumps(payload)))[0] == 200
            except RuntimeError:
                return False

        def cleanup_baseline(self):
            record = self.snapshot.get('recovery_baseline')
            if not record or record.get('removed'): return
            if subprocess.run(['docker', 'inspect', record['name']], capture_output=True).returncode == 0:
                row = json.loads(cache.b.stage.command(['docker', 'inspect', record['name']]))[0]
                if not cache.container_owned(row, record['owner'], record['image'], record['revision']):
                    raise RuntimeError('refuse cleanup of non-owned baseline')
                cache.b.stage.command(['docker', 'rm', '-f', record['name']])
            if subprocess.run(['docker', 'volume', 'inspect', record['volume']], capture_output=True).returncode == 0:
                row = json.loads(cache.b.stage.command(['docker', 'volume', 'inspect', record['volume']]))[0]
                if not cache.volume_owned(row, record['owner']): raise RuntimeError('refuse cleanup of non-owned baseline volume')
                cache.b.stage.command(['docker', 'volume', 'rm', record['volume']])
            record['removed'] = True
            cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)

        def performance_checks(self):
            self.start_baseline()
            case = self.new_case('performance')
            batches = []
            self.performance = {'baseline_image': self.args.baseline_image, 'baseline_revision': self.args.baseline_revision,
                'candidate_image': self.args.expected_image, 'candidate_revision': self.args.expected_revision,
                'batch_order': ['baseline', 'candidate', 'candidate', 'baseline'], 'batches': batches,
                'batch_deadline_seconds': BENCH_BATCH_DEADLINE_SECONDS, 'batch_progress': []}
            for index, mode in enumerate(self.performance['batch_order']):
                pid = self.guard_baseline()
                port = self.args.baseline_port if mode == 'baseline' else 8080
                journal = 'account-recovery-performance-batch-' + str(index) + '-' + mode + '.jsonl'
                progress = {'mode': mode, 'journal': journal, 'state': 'running'}
                self.performance['batch_progress'].append(progress)
                cache.b.stage.private_json(self.private / 'account-recovery-performance.json', self.performance)
                payload = {'url': 'http://127.0.0.1:' + str(port) + '/v1/messages', 'token': case['key']['key'],
                           'model': MODEL, 'journal': str(self.private / journal)}
                try:
                    batch = benchmark_client(['nsenter', '-t', str(pid), '-n', 'python3', '-c', BENCH_REQUEST], payload)
                except (RuntimeError, ValueError):
                    progress['state'] = 'failed; partial samples retained in journal, not accepted'
                    cache.b.stage.private_json(self.private / 'account-recovery-performance.json', self.performance)
                    raise
                batch['mode'] = mode
                batches.append(batch)
                progress['state'] = 'complete'
                cache.b.stage.private_json(self.private / 'account-recovery-performance.json', self.performance)
            self.performance['summary'] = performance_summary(batches)
            cache.b.stage.private_json(self.private / 'account-recovery-performance.json', self.performance)
            self.check('healthy traffic four1000 batches all200 with p95 delta<=10ms and throughput ratio>=0.98',
                       self.performance['summary']['passed'], self.performance['summary'])
            self.check('performance4080 requests including warmups each settle once', self.wait_usage(case, 4080)['n'] == 4080)
            self.cleanup_baseline()

        def cleanup_balance_latches(self, ids):
            self.guard()
            keys = balance_latch_keys(ids)
            app = self.inspect()[0]
            env = dict(item.split('=', 1) for item in app['Config'].get('Env', []) if '=' in item)
            # The bounded transport Redis view shares the parent pool and DB;
            # canonical Stage guard currently requires its isolated DB0.
            cache.b.stage.validate_redis_environment(env)
            db = int(env.get('REDIS_DB', '0'))
            if cache.b.stage.REDIS == 'sub2api-redis': raise RuntimeError('refuse production Redis cleanup')
            password = env.get('REDIS_PASSWORD', '')
            if any(c in password for c in '\n\r\0'): raise RuntimeError('invalid private Redis authentication')
            child_env = dict(os.environ)
            child_env.pop('REDISCLI_AUTH', None)
            auth = []
            if password:
                child_env['REDISCLI_AUTH'] = password
                auth = ['-e', 'REDISCLI_AUTH']
            self.snapshot['recovery_balance_latch_keys'] = keys
            self.snapshot['recovery_balance_latches_restored'] = False
            cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)
            removed, before = 0, 0
            for start in range(0, len(keys), 64):
                batch = keys[start:start + 64]
                for step, operation in enumerate(('EXISTS', 'DEL', 'EXISTS')):
                    row = subprocess.run(['docker', 'exec', *auth, cache.b.stage.REDIS, 'redis-cli', '-n', str(db), '--raw', operation, *batch],
                                         env=child_env, text=True, capture_output=True, timeout=30)
                    if row.returncode != 0 or not re.fullmatch(r'[0-9]+\s*', row.stdout):
                        raise RuntimeError('exact owned Stage Redis latch cleanup failed')
                    count = int(row.stdout)
                    if step == 0: before += count
                    elif step == 1: removed += count
                    elif count != 0: raise RuntimeError('owned Stage Redis latches remain')
            # A separate full EXISTS confirms all batches, including initially empty ones.
            row = subprocess.run(['docker', 'exec', *auth, cache.b.stage.REDIS, 'redis-cli', '-n', str(db), '--raw', 'EXISTS', *keys],
                                 env=child_env, text=True, capture_output=True, timeout=30)
            if row.returncode != 0 or row.stdout.strip() != '0': raise RuntimeError('owned Stage Redis latch verification failed')
            self.snapshot['recovery_balance_latches_restored'] = True
            self.snapshot['recovery_balance_latch_cleanup'] = {'database': db, 'exact_keys': len(keys), 'before': before, 'removed': removed}
            cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)

        def restore(self):
            if not self.snapshot: return
            self.guard()
            self.cleanup_baseline()
            if self.snapshot.get('recovery_runner_kind') != 'account-recovery-stage-v1':
                raise RuntimeError('restore snapshot belongs to a different acceptance runner')
            names = self.snapshot.get('recovery_account_names', {})
            ids = self.fixture_ids['accounts']
            if ids:
                selected = exact_ids(ids)
                rows = self.sql('SELECT json_build_object(\'id\',id,\'name\',name) FROM accounts WHERE id IN (' + selected + ');')
                if len(rows) != len(ids) or any(names.get(str(row['id'])) != row['name'] for row in rows):
                    raise RuntimeError('refuse cleanup: exact recovery account identity differs')
                # Invalidate any in-flight grant before draining leases; expired
                # fixtures cannot be revived by the primary's background loop.
                if self.fixture_ids['keys']:
                    self.sql("UPDATE api_keys SET status='inactive',updated_at=NOW() WHERE id IN (" +
                             exact_ids(self.fixture_ids['keys']) + " ) AND user_id IN (" + exact_ids(self.fixture_ids['users']) + ");")
                self.sql("UPDATE accounts SET status='inactive',schedulable=FALSE,auto_pause_on_expired=TRUE,expires_at=NOW()-INTERVAL '1 day',updated_at=NOW() WHERE id IN (" + selected + ');')
                self.cleanup_replica()
                deadline = time.monotonic() + 40
                while self.sql("SELECT json_build_object('n',COUNT(*)) FROM account_probe_leases_geili WHERE account_id IN (" + selected + ') AND expires_at>NOW();')[0]['n']:
                    if time.monotonic() >= deadline:
                        raise RuntimeError('owned recovery probes did not drain; preserve snapshot and retry restore')
                    time.sleep(.2)
                self.cleanup_balance_latches(ids)
                self.sql(cleanup_runtime_sql(ids))
                left = [self.sql("SELECT json_build_object('n',COUNT(*)) FROM " + table + ' WHERE account_id IN (' + selected + ');')[0]['n'] for table in TABLES]
                if any(left): raise RuntimeError('owned runtime metadata remains')
            if not ids:
                self.snapshot['recovery_balance_latches_restored'] = True
                self.snapshot['recovery_balance_latch_cleanup'] = {'exact_keys': 0, 'removed': 0}
            self.snapshot['recovery_runtime_restored'] = True
            cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)
            return super().restore()
    return RecoveryFixture


def main(argv=None):
    early = argparse.ArgumentParser(add_help=False)
    early.add_argument('--ops-bin', required=True)
    known, remaining = early.parse_known_args(argv)
    cache = load_ops(known.ops_bin)
    parser = cache.b.candidate_args()
    parser.add_argument('--replica-port', type=int, default=18519)
    parser.add_argument('--baseline-image')
    parser.add_argument('--baseline-revision')
    parser.add_argument('--baseline-port', type=int, default=18520)
    args = parser.parse_args(remaining)
    cache.b.validate_args(args)
    validate_guard_options(args)
    if not cache.valid_replica_port(args.replica_port, args.mock_port):
        raise RuntimeError('replica port must be a distinct nonprivileged namespace port')
    fixture = fixture_type(cache, cache.b.stage.load_source(args.source_root))(args)
    if args.dry_run: return 0 if fixture.execute_billing() else 1
    with (cache.b.stage.LAB / '.v2-synthetic.lock').open('a') as lock:
        os.chmod(lock.name, 0o600)
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
        passed = fixture.execute_billing()
    if fixture.snapshot:
        result = {'kind': 'account-recovery-stage-v1', 'revision': args.expected_revision, 'image': args.expected_image,
                  'runner_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                  'version': (Path(args.source_root) / 'backend/cmd/server/VERSION').read_text().strip(),
                  'canonical_ops_runner_sha256': hashlib.sha256(Path(cache.__file__).read_bytes()).hexdigest(),
                  'checked_at': datetime.now(timezone.utc).isoformat(), 'checks': fixture.checks,
                  'restored': fixture.snapshot.get('restored', False), 'runtime_restored': fixture.snapshot.get('recovery_runtime_restored', False),
                  'replica_removed': fixture.snapshot.get('cache_replica', {}).get('removed', False),
                  'synthetic_only': True, 'real_suppliers_executed': False, 'payments_executed': False,
                  'production_ready': False, 'matrix_complete': passed and not args.restore, 'acceptance_executed': not bool(args.restore), 'remaining': REMAINING,
                  'external_unverified': EXTERNAL_UNVERIFIED,
                  'performance': getattr(fixture, 'performance', None),
                  'baseline_removed': fixture.snapshot.get('recovery_baseline', {}).get('removed', False),
                  'balance_latches_restored': fixture.snapshot.get('recovery_balance_latches_restored', False),
                  'balance_latch_cleanup': fixture.snapshot.get('recovery_balance_latch_cleanup'),
                  'mock_calls_by_label': {label: fixture.counts(label) for label in getattr(fixture, 'recovery_modes', {})},
                  'mock_unmatched_paths': getattr(fixture, 'recovery_unmatched_paths', {}),
                  'timing_design': '30s recovery ends before45s first edge heartbeat; separate55s mock verifies physical heartbeat arrival',
                  'model_isolation_evidence': 'owned model block seeded; natural threshold counting validated by separate Go/PostgreSQL tests'}
        result['passed'] = passed and result['restored'] and result['runtime_restored'] and result['replica_removed'] and result['balance_latches_restored']
        if not args.restore:
            result['passed'] = result['passed'] and result['baseline_removed'] and bool(result['performance'] and result['performance']['summary']['passed'])
        passed = result['passed']
        filename = 'account-recovery-restore-result.json' if args.restore else 'account-recovery-result.json'
        cache.b.stage.private_json(fixture.private / filename, result)
        print('RESULT ' + str(fixture.private / filename), flush=True)
    return 0 if passed else 1


if __name__ == '__main__':
    raise SystemExit(main())
