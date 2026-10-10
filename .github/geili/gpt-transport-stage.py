#!/usr/bin/env python3
"""Synthetic transport cooldown checks using the canonical ops Stage guards.

Requires --ops-bin pointing at the existing subscription-lab-ops/bin scripts.
Default is read-only; --execute uses exact-ID synthetic fixtures and an owned
second application from the same digest. No live supplier or payment calls.
"""
import argparse
import fcntl
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import signal
import socket
import time


def load_ops(directory):
    path = Path(directory).resolve(strict=True) / 'accept-two-instance-cache-stage.py'
    spec = importlib.util.spec_from_file_location('transport_stage_cache', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def fixture_type(cache, source):
    class TransportFixture(cache.fixture_type(source)):
        def start_mock(self):
            super().start_mock()
            original = self.server.RequestHandlerClass
            fixture = self
            self.transport_counts = {'oauth': 0, 'apikey': 0, 'http400': 0}
            self.gateway_transport_observations = {}
            self.transport_requests = 0
            self.apikey_healthy = False

            class TransportMock(original):
                def do_CONNECT(self):
                    with fixture.lock:
                        fixture.transport_counts['oauth'] += 1
                    self.send_response(400)
                    self.send_header('Content-Length', '0')
                    self.end_headers()

                def do_POST(self):
                    if self.path.startswith('/transport-fault/'):
                        with fixture.lock:
                            fixture.transport_counts['apikey'] += 1
                            healthy = fixture.apikey_healthy
                        if not healthy:
                            self.close_connection = True
                            self.connection.shutdown(socket.SHUT_RDWR)
                            self.connection.close()
                            return
                        self.path = '/upstream/' + self.path.removeprefix('/transport-fault/')
                    if self.path.startswith('/transport-http400/'):
                        with fixture.lock:
                            fixture.transport_counts['http400'] += 1
                        return self.reply({'error': {'type': 'invalid_request_error',
                                                   'message': 'Synthetic bad input'}}, 400)
                    return super().do_POST()
            self.server.RequestHandlerClass = TransportMock

        def api(self, path, data=None, token=None, method=None):
            result = super().api(path, data, token, method)
            if path == '/admin/proxies' and data is not None and isinstance(result, dict) and result.get('id'):
                self.snapshot.setdefault('transport_proxy_ids', []).append(result['id'])
                cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)
            return result

        def setup(self):
            user = self.user('transport-health')
            self.api('/admin/users/' + str(user['id']) + '/balance',
                     {'balance': 5, 'operation': 'set', 'notes': 'Synthetic transport acceptance'}, self.admin)
            proxy = self.api('/admin/proxies', {'name': 'Transport fixture ' + self.suffix,
                'protocol': 'http', 'host': '127.0.0.1', 'port': self.port}, self.admin)
            self.transport_cases = []
            self.model = 'gpt-5.5'
            for kind in ('oauth', 'apikey', 'http400'):
                group = self.api('/admin/groups', {'name': 'Transport ' + kind + ' ' + self.suffix,
                    'platform': 'openai', 'rate_multiplier': 1, 'subscription_rate_multiplier': 1,
                    'model_pricing': [{'models': [self.model], 'billing_mode': 'token',
                                      'input_price': .000001, 'output_price': .000002}]}, self.admin)
                credentials = {'api_key': 'synthetic-fixture-key', 'base_url': self.mock_base +
                    ('/transport-fault' if kind == 'apikey' else '/transport-http400'),
                    'model_mapping': {self.model: self.model}}
                payload = {'name': 'Transport A ' + kind + ' ' + self.suffix,
                    'platform': 'openai', 'type': 'apikey', 'credentials': credentials,
                    'extra': {'openai_responses_mode': 'force_responses'},
                    'group_ids': [group['id']], 'concurrency': 10, 'priority': 1}
                if kind == 'oauth':
                    payload.update(type='oauth', proxy_id=proxy['id'],
                        credentials={'access_token': 'synthetic-fixture-token',
                                     'expires_at': int(time.time()) + 3600,
                                     'model_mapping': {self.model: self.model}})
                account = self.api('/admin/accounts', payload, self.admin)
                self.api('/admin/accounts', {'name': 'Transport B ' + kind + ' ' + self.suffix,
                    'platform': 'openai', 'type': 'apikey', 'priority': 5, 'concurrency': 10,
                    'group_ids': [group['id']], 'extra': {'openai_responses_mode': 'force_responses'},
                    'credentials': {'api_key': 'synthetic-fixture-key', 'base_url': self.mock_base + '/upstream',
                                    'model_mapping': {self.model: self.model}}}, self.admin)
                key = self.api('/keys', {'name': 'Transport ' + kind, 'billing_source': 'balance',
                                        'routing_mode': 'single', 'group_id': group['id']}, user['token'])
                self.transport_cases.append({'kind': kind, 'group_id': group['id'], 'account': account, 'key': key})

        def call_transport(self, case, label, secondary=False):
            self.transport_requests += 1
            seed = 'transport-' + self.suffix + '-' + case['kind'] + '-' + label
            payload = {'model': self.model, 'input': [{'role': 'user', 'content': seed}],
                       'prompt_cache_key': seed, 'stream': False}
            caller = self.replica_request if secondary else self.request
            return caller('/v1/responses', payload, case['key']['key'])

        def transport_observations(self, case, expected=None):
            # Account creation also launches capability/privacy probes. Only
            # request-scoped Ops events measure gateway transport observations.
            deadline = time.monotonic() + 5
            while True:
                count = self.sql("SELECT json_build_object('n',COUNT(*)) FROM ops_error_logs o "
                    "CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(o.upstream_errors::jsonb)='array' "
                    "THEN o.upstream_errors::jsonb ELSE '[]'::jsonb END) a "
                    f"WHERE o.group_id={int(case['group_id'])} AND a->>'account_id'='{int(case['account']['id'])}' "
                    "AND a->>'kind'='request_error';")[0]['n']
                if expected is None or count >= expected or time.monotonic() >= deadline:
                    self.gateway_transport_observations[case['kind']] = count
                    return count
                time.sleep(.1)

        def completed_usage(self, case, expected):
            deadline = time.monotonic() + 5
            while True:
                count = self.sql("SELECT json_build_object('n',COUNT(*)) FROM usage_logs "
                    f"WHERE group_id={int(case['group_id'])} AND account_id={int(case['account']['id'])};")[0]['n']
                if count >= expected or time.monotonic() >= deadline:
                    return count
                time.sleep(.1)

        def cooldown(self, account_id):
            return self.sql("SELECT json_build_object('active',COALESCE(temp_unschedulable_until>NOW(),false),"
                "'remaining',EXTRACT(EPOCH FROM temp_unschedulable_until-NOW()),'reason',temp_unschedulable_reason) "
                f"FROM accounts WHERE id={int(account_id)};")[0]

        def check_response(self, label, response):
            self.check(label, response[0] == 200 and isinstance(response[1], dict)
                and response[1].get('status') == 'completed' and 'acceptance ok' in json.dumps(response[1]),
                {'status': response[0]})

        def run_checks(self):
            settings = self.sql("SELECT json_build_object('enabled',COALESCE((SELECT value FROM settings "
                "WHERE key='openai_advanced_scheduler_enabled'),'false'));")[0]
            self.check('advanced scheduler is disabled; legacy actually tested', settings['enabled'] == 'false')
            self.start_replica()
            for case in self.transport_cases:
                kind = case['kind']
                if kind == 'http400':
                    for i in range(3):
                        response = self.call_transport(case, str(i))
                        self.check('real HTTP400 returned without transport cooling ' + str(i), response[0] == 400)
                    self.check('HTTP400 account has no transport cooldown', not self.cooldown(case['account']['id'])['active'])
                    continue
                for i in range(3):
                    self.check_response(kind + ' failure recovered ' + str(i), self.call_transport(case, str(i), i == 0))
                self.check(kind + ' exactly three gateway transport observations', self.transport_observations(case, 3) == 3)
                state = self.cooldown(case['account']['id'])
                self.check(kind + ' durable60s cooldown after third attempt', state['active']
                    and 45 <= state['remaining'] <= 65 and 'openai_transport_health' in state['reason'], state)
                self.check_response(kind + ' secondary skips failed A', self.call_transport(case, 'blocked-secondary', True))
                self.check_response(kind + ' primary skips failed A', self.call_transport(case, 'blocked-primary'))
                self.check(kind + ' no additional gateway failures while cooled', self.transport_observations(case) == 3)
            self.apikey_healthy = True
            began = time.monotonic()
            while time.monotonic() - began < 70:
                if all(not self.cooldown(c['account']['id'])['active'] for c in self.transport_cases):
                    break
                time.sleep(1)
            self.check('cooldowns expire naturally without clearing database/cache', all(
                not self.cooldown(c['account']['id'])['active'] for c in self.transport_cases))
            for case in self.transport_cases[:2]:
                self.check_response(case['kind'] + ' fresh secondary can retry A after expiry',
                                    self.call_transport(case, 'after-expiry', True))
                self.check(case['kind'] + ' A selected again after expiry',
                    self.transport_observations(case, 4) == 4 if case['kind'] == 'oauth' else self.completed_usage(case, 1) == 1)
            apikey = self.transport_cases[1]
            for cycle in range(2):
                self.apikey_healthy = False
                for i in range(2):
                    self.check_response('apikey two failures recover ' + str(cycle) + '/' + str(i),
                                        self.call_transport(apikey, f'cycle-{cycle}-{i}'))
                self.check('two gateway failures do not cool ' + str(cycle),
                    self.transport_observations(apikey, 5 + cycle * 2) == 5 + cycle * 2
                    and not self.cooldown(apikey['account']['id'])['active'])
                self.apikey_healthy = True
                self.check_response('completed success resets streak ' + str(cycle),
                                    self.call_transport(apikey, 'success-' + str(cycle)))
                self.check('success belongs to A ' + str(cycle), self.completed_usage(apikey, cycle + 2) == cycle + 2)
            self.gateway_transport_observations = {c['kind']: self.transport_observations(c) for c in self.transport_cases}
            self.check('protected settings and production fingerprints unchanged', self.protected_state_unchanged())

        def restore(self):
            try:
                if self.snapshot:
                    self.guard()
                    for proxy_id in self.snapshot.get('transport_proxy_ids', []):
                        self.api('/admin/proxies/' + str(int(proxy_id)), {'status': 'inactive'}, self.admin, 'PUT')
            finally:
                # Always remove the owned replica and restore the base fixtures,
                # including when deactivating a synthetic proxy fails.
                super().restore()
    return TransportFixture


def main():
    early = argparse.ArgumentParser(add_help=False)
    early.add_argument('--ops-bin', required=True)
    known, remaining = early.parse_known_args()
    cache = load_ops(known.ops_bin)
    parser = cache.b.candidate_args()
    parser.add_argument('--replica-port', type=int, default=18516)
    args = parser.parse_args(remaining)
    cache.b.validate_args(args)
    if not cache.valid_replica_port(args.replica_port, args.mock_port):
        raise RuntimeError('invalid replica port')
    fixture = fixture_type(cache, cache.b.stage.load_source(args.source_root))(args)
    if args.dry_run:
        return 0 if fixture.execute_billing() else 1
    with (cache.b.stage.LAB / '.v2-synthetic.lock').open('a') as lock:
        os.chmod(lock.name, 0o600)
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
        passed = fixture.execute_billing()
    if fixture.snapshot:
        result = {'revision': args.expected_revision, 'image': args.expected_image,
            'kind': 'gpt-transport-stage', 'passed': passed,
            'checks': len(fixture.checks), 'restored': fixture.snapshot.get('restored', False),
            'replica_removed': fixture.snapshot.get('cache_replica', {}).get('removed', False),
            'runner_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
            'synthetic_requests': getattr(fixture, 'transport_requests', 0),
            'mock_connections_including_background_probes': getattr(fixture, 'transport_counts', {}),
            'gateway_transport_observations': getattr(fixture, 'gateway_transport_observations', {}), 'synthetic_only': True,
            'production_ready': False}
        cache.b.stage.private_json(fixture.private / 'transport-result.json', result)
        print('RESULT ' + str(fixture.private / 'transport-result.json'), flush=True)
    return 0 if passed else 1


if __name__ == '__main__':
    raise SystemExit(main())
