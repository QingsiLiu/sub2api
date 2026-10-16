#!/usr/bin/env python3
"""Candidate-bound Gemini supplier failover checks using namespace-local mocks.

Defaults to canonical read-only guards. --execute owns isolated Stage fixtures
and a same-digest replica; no payments, real suppliers, deployment or production
mutation. Performance and real-provider acceptance remain separate gates.
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
import time
from urllib.parse import urlsplit

MODEL = 'gemini-4-argon'
TEXT = 'supplier acceptance OK'
KIND = 'gemini-supplier-stage-v1'
PROTOCOLS = ('responses', 'chat/completions', 'messages', 'native')
SCENARIOS = ('same-group', 'cross-group', 'exhausted', 'ordinary')
TABLES = ('account_probe_leases_geili', 'account_probe_logs_geili', 'account_runtime_geili')


def load_ops(directory):
    path = Path(directory).resolve(strict=True) / 'accept-two-instance-cache-stage.py'
    spec = importlib.util.spec_from_file_location('gemini_supplier_canonical_cache', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def validate_options(args):
    for field in ('expected_revision', 'expected_production_revision'):
        if not re.fullmatch(r'[a-f0-9]{40}', getattr(args, field, '')):
            raise RuntimeError('full candidate and production revisions required')
    for field in ('expected_image', 'expected_production_image'):
        if not re.fullmatch(r'ghcr\.io/qingsiliu/sub2api@sha256:[a-f0-9]{64}', getattr(args, field, '')):
            raise RuntimeError('immutable candidate and production image digests required')
    ports = (args.mock_port, args.replica_port)
    if any(type(p) is not int or not 1024 <= p <= 65535 or p == 8080 for p in ports) or len(set(ports)) != 2:
        raise RuntimeError('separate nonprivileged Stage namespace ports required')
    if sum(bool(getattr(args, field)) for field in ('execute', 'restore', 'dry_run')) > 1:
        raise RuntimeError('execute, restore and dry-run are mutually exclusive')
    args.dry_run = not (args.execute or args.restore)


def exact_ids(values):
    if not values or any(type(i) is not int or i <= 0 for i in values) or len(set(values)) != len(values):
        raise RuntimeError('positive unique exact fixture IDs required')
    return ','.join(map(str, sorted(values)))


def mock_url(base, label):
    if not re.fullmatch(r'http://127\.0\.0\.1:[0-9]{4,5}', base) or not re.fullmatch(r'[a-z0-9_-]+', label):
        raise RuntimeError('mock accounts require owned namespace loopback and an exact label')
    return base + '/gemini-supplier/' + label


def route(target):
    return re.fullmatch(r'/gemini-supplier/([a-z0-9_-]+)/(.+)', urlsplit(target).path)


def upstream_kind(endpoint):
    if re.fullmatch(r'(?:v1/)?messages', endpoint):
        return 'messages'
    if re.fullmatch(r'v1beta/models/' + re.escape(MODEL) + r':(?:streamGenerateContent|generateContent)', endpoint):
        return 'native'
    return None


def payload(protocol, marker, stream):
    if protocol == 'native':
        action = 'streamGenerateContent?alt=sse' if stream else 'generateContent'
        return '/v1beta/models/' + MODEL + ':' + action, {
            'contents': [{'role': 'user', 'parts': [{'text': marker}]}], 'generationConfig': {'maxOutputTokens': 16}}
    value = {'model': MODEL, 'stream': stream, 'prompt_cache_key': marker}
    if protocol == 'responses':
        value.update(input=marker, max_output_tokens=16)
    else:
        value.update(messages=[{'role': 'user', 'content': marker}], max_tokens=16)
    return '/v1/' + protocol, value


def events(body):
    if not isinstance(body, str):
        return []
    result = []
    for frame in body.replace('\r\n', '\n').split('\n\n'):
        data = '\n'.join(line[5:].lstrip() for line in frame.splitlines() if line.startswith('data:'))
        if not data:
            continue
        if data == '[DONE]':
            result.append({'type': '[DONE]'})
        else:
            try:
                item = json.loads(data)
            except ValueError:
                return []
            if not isinstance(item, dict):
                return []
            result.append(item)
    return result


def complete(protocol, response, stream):
    if response[0] != 200 or TEXT not in json.dumps(response[1]):
        return False
    if not stream:
        body = response[1]
        if not isinstance(body, dict) or body.get('error'):
            return False
        if protocol == 'responses':
            return body.get('status') == 'completed'
        if protocol == 'messages':
            return body.get('stop_reason') == 'end_turn'
        if protocol == 'chat/completions':
            return any(c.get('finish_reason') == 'stop' for c in body.get('choices', []))
        return any(c.get('finishReason') == 'STOP' for c in body.get('candidates', []))
    rows = events(response[1])
    if not rows or any(e.get('error') or e.get('type') in ('error', 'response.failed') for e in rows):
        return False
    terminal = {'responses': 'response.completed', 'messages': 'message_stop', 'chat/completions': '[DONE]'}
    if protocol in terminal:
        return sum(e.get('type') == terminal[protocol] for e in rows) == 1
    return any(c.get('finishReason') == 'STOP' for e in rows for c in e.get('candidates', []))


def expected_modes(scenario):
    return ['ordinary'] if scenario == 'ordinary' else ['balance', 'capacity'] + ([] if scenario == 'exhausted' else ['healthy'])


def fixture_type(cache, source):
    class SupplierFixture(cache.fixture_type(source)):
        def prepare(self):
            super().prepare()
            self.snapshot.update(gemini_supplier_runner=KIND, gemini_supplier_accounts={}, gemini_supplier_runtime_restored=False)
            cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)

        def start_mock(self):
            super().start_mock()
            fixture, original = self, self.server.RequestHandlerClass
            self.modes, self.calls = {}, []

            class SupplierMock(original):
                def do_POST(self):
                    match = route(self.path)
                    if not match or match.group(1) not in fixture.modes:
                        return self.reply({'error': {'message': 'unknown owned mock route'}}, 404)
                    size = int(self.headers.get('Content-Length', '0'))
                    if not 0 < size <= 1048576:
                        return self.reply({'error': {'message': 'invalid synthetic body size'}}, 400)
                    body = json.loads(self.rfile.read(size))
                    label, endpoint = match.groups()
                    kind = upstream_kind(endpoint)
                    if kind is None:
                        return self.reply({'error': {'message': 'unknown synthetic inference endpoint'}}, 404)
                    mode = fixture.modes[label]
                    with fixture.lock:
                        fixture.calls.append({'label': label, 'mode': mode, 'path': urlsplit(self.path).path})
                    messages = {'balance': (403, '用户额度不足，剩余额度: $0'),
                                'capacity': (400, '当前号池渠道暂时无可用资源，请稍后重试'),
                                'ordinary': (400, 'Invalid request: unsupported fixture parameter')}
                    if mode in messages:
                        status, message = messages[mode]
                        raw = json.dumps({'error': {'message': message, 'type': 'server_error'}}).encode()
                        self.send_response(status)
                        self.send_header('Content-Type', 'application/json')
                        self.send_header('x-request-id', 'synthetic-' + label)
                        self.send_header('Content-Length', str(len(raw)))
                        self.end_headers()
                        self.wfile.write(raw)
                        return
                    native = kind == 'native'
                    stream = ':streamGenerateContent' in endpoint if native else bool(body.get('stream'))
                    if native:
                        result = {'candidates': [{'content': {'role': 'model', 'parts': [{'text': TEXT}]}, 'finishReason': 'STOP', 'index': 0}],
                                  'usageMetadata': {'promptTokenCount': 10, 'candidatesTokenCount': 2, 'totalTokenCount': 12}, 'modelVersion': MODEL}
                        rows = [result]
                    else:
                        result = {'id': 'msg_supplier_synthetic', 'type': 'message', 'role': 'assistant', 'model': MODEL,
                                  'content': [{'type': 'text', 'text': TEXT}], 'stop_reason': 'end_turn',
                                  'usage': {'input_tokens': 10, 'output_tokens': 2}}
                        rows = [{'type': 'message_start', 'message': dict(result, content=[], usage={'input_tokens': 10, 'output_tokens': 0})},
                                {'type': 'content_block_start', 'index': 0, 'content_block': {'type': 'text', 'text': ''}},
                                {'type': 'content_block_delta', 'index': 0, 'delta': {'type': 'text_delta', 'text': TEXT}},
                                {'type': 'content_block_stop', 'index': 0},
                                {'type': 'message_delta', 'delta': {'stop_reason': 'end_turn'}, 'usage': {'output_tokens': 2}},
                                {'type': 'message_stop'}]
                    if not stream:
                        return self.reply(result)
                    raw = ''.join(('event: ' + row['type'] + '\n' if not native else '') + 'data: ' + json.dumps(row) + '\n\n' for row in rows).encode()
                    self.send_response(200)
                    self.send_header('Content-Type', 'text/event-stream')
                    self.send_header('Content-Length', str(len(raw)))
                    self.end_headers()
                    self.wfile.write(raw)

            self.server.RequestHandlerClass = SupplierMock

        def setup(self):
            self.start_replica()
            self.owner = self.user('gemini-supplier')
            self.api('/admin/users/' + str(self.owner['id']) + '/balance',
                     {'balance': 100, 'operation': 'set', 'notes': 'Owned mock-only Gemini acceptance; no payment'}, self.admin)

        def group(self, label):
            return self.api('/admin/groups', {'name': 'Gemini supplier ' + label + ' ' + self.suffix,
                'platform': 'gemini', 'rate_multiplier': 1, 'subscription_rate_multiplier': 1,
                'subscription_enabled': False, 'model_allowlist': {'enabled': True, 'models': [MODEL]},
                'model_pricing': [{'models': [MODEL], 'billing_mode': 'token', 'input_price': .000001, 'output_price': .000002}]}, self.admin)

        def new_case(self, label, scenario):
            groups = [self.group(label + '-a')]
            if scenario in ('cross-group', 'exhausted'):
                groups.append(self.group(label + '-b'))
            accounts = []
            for priority, mode in enumerate(expected_modes(scenario)):
                route_label = label + '-' + mode
                self.modes[route_label] = mode
                ids = [g['id'] for g in groups] if mode != 'healthy' else [groups[-1]['id']]
                account = self.api('/admin/accounts', {'name': 'Gemini supplier ' + route_label + ' ' + self.suffix,
                    'platform': 'gemini', 'type': 'apikey', 'credentials': {'api_key': 'synthetic-gemini-only',
                    'base_url': mock_url(self.mock_base, route_label), 'model_mapping': {MODEL: MODEL},
                    'pool_mode': True, 'pool_mode_retry_count': 5}, 'group_ids': ids, 'priority': priority, 'concurrency': 4}, self.admin)
                self.snapshot['gemini_supplier_accounts'][str(account['id'])] = account['name']
                cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)
                accounts.append(account)
            if scenario == 'ordinary':
                route_label = label + '-backup'
                self.modes[route_label] = 'healthy'
                account = self.api('/admin/accounts', {'name': 'Gemini supplier ' + route_label + ' ' + self.suffix,
                    'platform': 'gemini', 'type': 'apikey', 'credentials': {'api_key': 'synthetic-gemini-only',
                    'base_url': mock_url(self.mock_base, route_label), 'model_mapping': {MODEL: MODEL}},
                    'group_ids': [groups[0]['id']], 'priority': 9, 'concurrency': 4}, self.admin)
                self.snapshot['gemini_supplier_accounts'][str(account['id'])] = account['name']
                cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)
            key = self.api('/keys', {'name': 'Gemini supplier ' + label, 'billing_source': 'balance',
                'routing_mode': 'composite', 'group_ids': [g['id'] for g in groups], 'quota': 1}, self.owner['token'])
            return key, accounts

        def eventually(self, name, callback, seconds=30):
            deadline = time.monotonic() + seconds
            while True:
                result = callback()
                if result:
                    return result
                if time.monotonic() >= deadline:
                    raise RuntimeError(name + ' did not become ready')
                time.sleep(.2)

        def check_accounting(self, label, key, successful):
            key_id = int(key['id'])
            query = "SELECT json_build_object('logs',(SELECT COUNT(*) FROM usage_logs WHERE api_key_id=" + str(key_id) + "),'receipts',(SELECT COUNT(*) FROM usage_settlement_receipts WHERE api_key_id=" + str(key_id) + "),'settled',(SELECT COUNT(*) FROM usage_settlement_receipts WHERE api_key_id=" + str(key_id) + " AND state='settled' AND delivered_at IS NOT NULL))"
            if successful:
                row = self.eventually(label + ' settled', lambda: (lambda r: r if r['settled'] else None)(self.sql(query)[0]))
                self.check(label + ': one final usage log and one settled receipt', row == {'logs': 1, 'receipts': 1, 'settled': 1}, row)
                exact = self.sql("SELECT json_build_object('valid',r.charged_amount=u.actual_cost AND r.user_id=u.user_id AND r.api_key_id=u.api_key_id AND r.usage_request_id=u.request_id,'dedup',(SELECT COUNT(*) FROM usage_billing_dedup d WHERE d.request_id=r.request_id AND d.api_key_id=r.api_key_id AND d.request_fingerprint=r.request_fingerprint)) FROM usage_settlement_receipts r JOIN usage_logs u ON u.id=r.usage_log_id WHERE r.api_key_id=" + str(key_id) + ';')
                self.check(label + ': receipt identity, amount and dedup agree', len(exact) == 1 and exact[0]['valid'] is True and exact[0]['dedup'] == 1, exact)
            else:
                self.check(label + ': failure creates no successful usage or settlement', self.sql(query)[0] == {'logs': 0, 'receipts': 0, 'settled': 0})

        def client_error_rows(self, key_id):
            exact_ids([key_id])
            # The legacy endpoint supports exact Key filtering and excludes
            # recovered upstream events. request-errors ignores api_key_id.
            rows = self.api('/admin/ops/errors?view=all&api_key_id=' + str(key_id), token=self.admin).get('items')
            if not isinstance(rows, list) or any(r.get('api_key_id') != key_id
                    or type(r.get('client_status_code')) is not int or r['client_status_code'] < 400 for r in rows):
                raise RuntimeError('client error query returned unrelated or recovered rows')
            return rows

        def check_errors(self, label, key, expected_status, expected_upstream):
            rows = self.eventually(label + ' error log', lambda: self.client_error_rows(key['id']))
            self.check(label + ': one client error row', len(rows) == 1)
            row = rows[0]
            self.check(label + ': list distinguishes client and final upstream', row.get('client_status_code') == expected_status and row.get('upstream_status_code') == expected_upstream and row['status_code'] == expected_upstream)
            detail = self.api('/admin/ops/request-errors/' + str(row['id']), token=self.admin)
            history = json.loads(detail.get('upstream_errors') or '[]')
            self.check(label + ': detail final context and attempt order', detail.get('client_status_code') == expected_status and detail.get('upstream_status_code') == expected_upstream and [r['upstream_status_code'] for r in history] == ([400] if expected_status == 400 else [403, 400]), {'attempt_statuses': [r.get('upstream_status_code') for r in history]})
            self.check(label + ': actual endpoint replaces platform default', detail.get('upstream_endpoint', '').startswith('/gemini-supplier/') and detail['upstream_endpoint'].endswith(urlsplit(history[-1].get('upstream_url', '')).path))
            self.check(label + ': every failure retains account, supplier request ID and sanitized URL', all(
                type(r.get('account_id')) is int and r['account_id'] > 0 and r.get('upstream_request_id', '').startswith('synthetic-')
                and urlsplit(r.get('upstream_url', '')).hostname == '127.0.0.1'
                and not urlsplit(r.get('upstream_url', '')).query for r in history))
            if expected_status == 503:
                self.check(label + ': classified supplier reasons preserved', [r.get('reason') for r in history] == ['supplier_balance_exhausted', 'supplier_capacity_unavailable'])

        def run_checks(self):
            self.synthetic_requests = 0
            for secondary in (False, True):
                for protocol in PROTOCOLS:
                    for scenario in SCENARIOS:
                        label = ('replica' if secondary else 'primary') + '-' + protocol.replace('/', '-') + '-' + scenario
                        key, accounts = self.new_case(label, scenario)
                        path, data = payload(protocol, 'synthetic-' + self.suffix + '-' + label, secondary)
                        before = len(self.calls)
                        response = (self.replica_request if secondary else self.request)(path, data, key['key'])
                        self.synthetic_requests += 1
                        actual = self.calls[before:]
                        self.check(label + ': exact bounded upstream attempts', [r['mode'] for r in actual] == expected_modes(scenario), {'modes': [r['mode'] for r in actual]})
                        successful = scenario in ('same-group', 'cross-group')
                        if successful:
                            self.check(label + ': complete single client answer', complete(protocol, response, secondary))
                        else:
                            expected = 400 if scenario == 'ordinary' else 503
                            self.check(label + ': final HTTP status', response[0] == expected, {'status': response[0]})
                            if scenario == 'exhausted':
                                self.check(label + ': stable unavailable code', 'upstream_unavailable' in json.dumps(response[1]))
                            self.check_errors(label, key, expected, 400)
                        self.check_accounting(label, key, successful)
                        if successful:
                            request_errors = self.client_error_rows(key['id'])
                            self.check(label + ': recovered attempts are not failed client requests', request_errors == [])
                        ids = exact_ids([a['id'] for a in accounts])
                        state = self.sql("SELECT json_build_object('id',id,'status',status,'schedulable',schedulable) FROM accounts WHERE id IN (" + ids + ');')
                        self.check(label + ': no persistent automatic debt marking', all(r['status'] == 'active' and r['schedulable'] for r in state))
            self.check('protected Stage configuration and production unchanged', self.protected_state_unchanged())

        def restore(self):
            if not self.snapshot:
                return
            self.guard()
            names = self.snapshot.get('gemini_supplier_accounts', {})
            ids = self.fixture_ids['accounts']
            if ids:
                if self.snapshot.get('gemini_supplier_runner') != KIND:
                    raise RuntimeError('restore snapshot belongs to another runner')
                selected = exact_ids(ids)
                rows = self.sql("SELECT json_build_object('id',id,'name',name) FROM accounts WHERE id IN (" + selected + ');')
                if len(rows) != len(ids) or any(names.get(str(row['id'])) != row['name'] for row in rows):
                    raise RuntimeError('exact owned Gemini account identity differs; refuse cleanup')
                if self.fixture_ids['keys']:
                    self.sql("UPDATE api_keys SET status='inactive',updated_at=NOW() WHERE id IN (" + exact_ids(self.fixture_ids['keys']) + ') AND user_id IN (' + exact_ids(self.fixture_ids['users']) + ');')
                self.sql("UPDATE accounts SET status='inactive',schedulable=FALSE,auto_pause_on_expired=TRUE,expires_at=NOW()-INTERVAL '1 day',updated_at=NOW() WHERE id IN (" + selected + ');')
                self.cleanup_replica()
                self.eventually('owned probe leases drain', lambda: not self.sql("SELECT json_build_object('n',COUNT(*)) FROM account_probe_leases_geili WHERE account_id IN (" + selected + ') AND expires_at>NOW();')[0]['n'], 40)
                self.sql('BEGIN;\n' + '\n'.join('DELETE FROM ' + table + ' WHERE account_id IN (' + selected + ');' for table in TABLES) + '\nCOMMIT;')
            self.snapshot['gemini_supplier_runtime_restored'] = True
            cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)
            return super().restore()
    return SupplierFixture


def main(argv=None):
    early = argparse.ArgumentParser(add_help=False)
    early.add_argument('--ops-bin', required=True)
    known, remaining = early.parse_known_args(argv)
    cache = load_ops(known.ops_bin)
    parser = cache.b.candidate_args()
    parser.add_argument('--replica-port', type=int, default=18519)
    args = parser.parse_args(remaining)
    validate_options(args)
    cache.b.validate_args(args)
    fixture = fixture_type(cache, cache.b.stage.load_source(args.source_root))(args)
    if args.dry_run:
        return 0 if fixture.execute_billing() else 1
    with (cache.b.stage.LAB / '.v2-synthetic.lock').open('a') as lock:
        os.chmod(lock.name, 0o600)
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
        passed = fixture.execute_billing()
    if fixture.snapshot:
        result = {'kind': KIND, 'revision': args.expected_revision, 'image': args.expected_image,
            'runner_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
            'canonical_ops_sha256': hashlib.sha256(Path(cache.__file__).read_bytes()).hexdigest(),
            'checks': fixture.checks, 'synthetic_requests': getattr(fixture, 'synthetic_requests', 0),
            'restored': fixture.snapshot.get('restored', False),
            'runtime_restored': fixture.snapshot.get('gemini_supplier_runtime_restored', False),
            'replica_removed': fixture.snapshot.get('cache_replica', {}).get('removed', not fixture.fixture_ids['accounts']),
            'synthetic_only': True, 'payments_executed': False, 'real_suppliers_executed': False,
            'acceptance_executed': not bool(args.restore), 'production_ready': False,
            'external_unverified': ['real suppliers', 'payments', 'production deployment', 'performance gate']}
        result['passed'] = passed and result['restored'] and result['runtime_restored'] and result['replica_removed']
        result['matrix_complete'] = result['passed'] and result['synthetic_requests'] == 32 and not bool(args.restore)
        if not args.restore:
            result['passed'] = result['passed'] and result['matrix_complete']
        filename = 'gemini-supplier-restore-result.json' if args.restore else 'gemini-supplier-result.json'
        cache.b.stage.private_json(fixture.private / filename, result)
        print('RESULT ' + str(fixture.private / filename), flush=True)
        passed = result['passed']
    return 0 if passed else 1


if __name__ == '__main__':
    raise SystemExit(main())
