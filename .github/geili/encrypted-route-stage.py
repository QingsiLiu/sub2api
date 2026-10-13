#!/usr/bin/env python3
"""Candidate-bound synthetic encrypted Responses acceptance on guarded Stage.

Read-only by default. --execute creates only exact-ID synthetic fixtures, changes
one Stage settings key temporarily, and starts an owned same-digest replica.
Uses canonical ops guards/lease/network-namespace mock and cleanup; never deploys,
never requests real suppliers, and never prints bearer tokens/settings values.
"""
import argparse
from contextlib import contextmanager
import select
import fcntl
import hashlib
import importlib.util
import json
import math
from concurrent.futures import ThreadPoolExecutor
import os
from pathlib import Path
import re
import signal
import subprocess
import time

SETTING = 'openai_encrypted_route'
MODEL = 'gpt-6-astra'
NONCE = 9007199254740993


def load_ops(directory):
    path = Path(directory).resolve(strict=True) / 'accept-two-instance-cache-stage.py'
    spec = importlib.util.spec_from_file_location('encrypted_stage_cache', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def settings_mode(rows):
    row = next((r for r in rows if r['key'] == SETTING), None)
    if row is None:
        return 'off'
    value = json.loads(row['value'])
    if not isinstance(value, dict) or value.get('mode') not in ('off', 'observe', 'protect'):
        raise RuntimeError('invalid pre-existing encrypted route settings')
    return value['mode']


def synthetic_response(payload, label, suffix):
    marker = json.dumps(payload.get('input'), ensure_ascii=False)
    cipher = 'synthetic-encrypted-' + suffix
    if 'seed-tools' in marker:
        output = [{'type': 'function_call_output', 'call_id': 'synthetic-call',
                   'output': [{'type': 'encrypted_content', 'encrypted_content': cipher + '-tools'}]}]
    elif 'seed-reasoning' in marker:
        output = [{'type': 'reasoning', 'id': 'rs_synthetic', 'encrypted_content': cipher + '-reasoning',
                   'summary': [{'type': 'summary_text', 'text': 'retained readable summary'}]}]
    else:
        output = [{'type': 'message', 'id': 'msg_synthetic', 'role': 'assistant',
                   'content': [{'type': 'output_text', 'text': 'acceptance ok ' + label}]}]
    if 'seed-tools' in marker or 'seed-reasoning' in marker:
        output.append({'type': 'message', 'id': 'msg_seed', 'role': 'assistant',
                       'content': [{'type': 'output_text', 'text': 'synthetic state delivered ' + label}]})
    return {'id': 'resp_synthetic_' + label, 'object': 'response', 'status': 'completed',
            'model': MODEL, 'output': output, 'usage': {'input_tokens': 1000, 'output_tokens': 100}}


def error_code(response):
    body = response[1] if len(response) > 1 else None
    error = body.get('error') if isinstance(body, dict) else None
    return error.get('code') if isinstance(error, dict) else None


def response_diagnostic(response):
    """Shape-only diagnostics; never retain bodies, messages or arbitrary values."""
    body = response[1] if len(response) > 1 else None
    def code(value):
        return value if isinstance(value, str) and re.fullmatch(r'[A-Za-z0-9_.-]{1,80}', value) else None
    top = body if isinstance(body, dict) else {}
    error = top.get('error')
    nested = error if isinstance(error, dict) else {}
    embedded = top.get('response')
    embedded = embedded if isinstance(embedded, dict) else {}
    embedded_error = embedded.get('error')
    embedded_error = embedded_error if isinstance(embedded_error, dict) else {}
    duration = response[2] if len(response) > 2 else None
    return {'status': response[0], 'body_type': type(body).__name__,
            'top_level_type': code(top.get('type')), 'top_level_code': code(top.get('code')),
            'error_value_type': type(error).__name__, 'error_type': code(nested.get('type')),
            'error_code': code(nested.get('code')), 'response_error_code': code(embedded_error.get('code')),
            'http_elapsed_seconds': duration if isinstance(duration, (int, float)) and math.isfinite(duration) and duration >= 0 else None}


def restored_setting_sql(rows, literal):
    row = next((r for r in rows if r['key'] == SETTING), None)
    if row is None:
        return 'DELETE FROM settings WHERE key=' + literal(SETTING) + ';'
    if type(row.get('id')) is not int or row['id'] <= 0:
        raise RuntimeError('invalid original settings row identity')
    return ('UPDATE settings SET value=' + literal(row['value']) + ',updated_at=' + literal(row['updated_at']) +
            ' WHERE id=' + str(row['id']) + ' AND key=' + literal(SETTING) + ';')



def performance_summary(batches):
    """Aggregate every measured sample; never discard outliers or failed calls."""
    result = {'passed': False, 'concurrency': 4, 'warmup_per_batch': 20,
              'p95_delta_limit_ms': 10, 'throughput_ratio_minimum': .98}
    stats = {}
    for mode in ('off', 'protect'):
        rows = [r for r in batches if r.get('mode') == mode]
        if len(rows) != 2 or any(len(r.get('latencies_seconds', [])) < 300 for r in rows):
            result['failure'] = 'insufficient_samples: require two batches of at least300 per mode'
            return result
        latencies = [v for row in rows for v in row['latencies_seconds']]
        elapsed = sum(row.get('elapsed_seconds', 0) for row in rows)
        if (elapsed <= 0 or not math.isfinite(elapsed) or any(not isinstance(v, (int, float))
                or not math.isfinite(v) or v <= 0 for v in latencies)):
            result['failure'] = 'invalid_or_zero_timings'
            return result
        ordered = sorted(latencies)
        stats[mode] = {'samples': len(ordered), 'elapsed_seconds': elapsed,
                       'throughput_per_second': len(ordered) / elapsed,
                       'p95_ms': ordered[math.ceil(.95 * len(ordered)) - 1] * 1000,
                       'failed_responses': sum(r.get('failed_responses', 0) for r in rows)}
    result.update(modes=stats, p95_delta_ms=stats['protect']['p95_ms'] - stats['off']['p95_ms'],
                  throughput_ratio=stats['protect']['throughput_per_second'] / stats['off']['throughput_per_second'])
    result['passed'] = (result['p95_delta_ms'] <= 10 and result['throughput_ratio'] >= .98
                        and all(r['failed_responses'] == 0 for r in stats.values()))
    if not result['passed']:
        result['failure'] = 'performance gate failed; investigate variance and rerun with more samples, never relabel as pass'
    return result


def source_cache_keys(rows, users):
    if any(type(u) is not int or u <= 0 for u in users):
        raise RuntimeError('invalid synthetic source ownership')
    keys = set()
    for row in rows:
        uid, digest = row.get('user_id'), row.get('cipher_digest')
        if type(uid) is not int or uid not in users or not isinstance(digest, str) or not re.fullmatch('[0-9a-f]{64}', digest):
            raise RuntimeError('source cleanup outside exact synthetic ownership')
        keys.add('openai_encrypted_route_geili:v2:' + str(uid) + ':' + digest)
    return sorted(keys)


@contextmanager
def bounded_source_lock(command):
    """Own only this finite psql process; server idle timeout bounds orphan locks."""
    proc = subprocess.Popen(command, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                            stderr=subprocess.DEVNULL, text=True, bufsize=1)
    try:
        proc.stdin.write("BEGIN; SET LOCAL lock_timeout='1s'; SET LOCAL statement_timeout='1s'; "
                         "SET LOCAL idle_in_transaction_session_timeout='3s'; "
                         "LOCK TABLE ONLY openai_encrypted_sources_geili IN ACCESS EXCLUSIVE MODE;\n"
                         "\\echo GEILI_SOURCE_LOCK_READY\n")
        proc.stdin.flush()
        deadline = time.monotonic() + 2
        ready_bytes = b''
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0 or not select.select([proc.stdout], [], [], remaining)[0]:
                raise RuntimeError('owned source lock readiness deadline exceeded')
            chunk = os.read(proc.stdout.fileno(), 1024)
            if not chunk or len(ready_bytes) + len(chunk) > 4096:
                raise RuntimeError('owned source lock failed before readiness')
            ready_bytes += chunk
            if b'GEILI_SOURCE_LOCK_READY' in ready_bytes.splitlines():
                break
        yield
    finally:
        try:
            if proc.poll() is None:
                try:
                    proc.stdin.write('COMMIT;\n')
                    proc.stdin.flush()
                except (BrokenPipeError, OSError):
                    pass
            proc.stdin.close()
            proc.stdin = None
            proc.communicate(timeout=2)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.communicate(timeout=1)
            raise RuntimeError('owned source lock release deadline exceeded') from None
        if proc.returncode != 0:
            raise RuntimeError('owned source lock process failed')


def fixture_type(cache, source):
    class EncryptedFixture(cache.fixture_type(source)):
        def prepare(self):
            super().prepare()
            rows = self.sql('SELECT row_to_json(s) FROM settings s ORDER BY key;')
            # Root-only canonical snapshot may contain settings secrets; report never does.
            self.snapshot['encrypted_settings_snapshot'] = rows
            self.snapshot['encrypted_settings_restored'] = False
            cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)
            if settings_mode(rows) != 'off':
                raise RuntimeError('require pre-existing mode off; refuse disrupting another protected user')
            columns = self.sql("SELECT json_build_object('column',column_name) FROM information_schema.columns "
                "WHERE table_schema='public' AND table_name='settings';")
            self.check('actual Stage settings schema matches checked source',
                {r['column'] for r in columns} == {'id', 'key', 'value', 'updated_at'})
            exists = self.sql("SELECT json_build_object('ok',to_regclass('public.openai_encrypted_sources_geili') IS NOT NULL);")[0]['ok']
            if not exists:
                raise RuntimeError('candidate encrypted source migration is missing')

        def set_policy(self, mode):
            if mode not in ('off', 'protect') or self.owner['id'] not in self.fixture_ids['users']:
                raise RuntimeError('policy may protect only this run synthetic user')
            self.guard()
            policy = {'mode': mode, 'protected_user_ids': [self.owner['id']], 'reasoning_recovery_enabled': True}
            self.sql('INSERT INTO settings(key,value,updated_at) VALUES(' +
                cache.b.stage.literal(SETTING) + ',' + cache.b.stage.literal(json.dumps(policy)) +
                ',NOW()) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW();')
            # Both applications cache policy for 5 seconds. No instance restart or Redis flush.
            time.sleep(6)

        def start_mock(self):
            super().start_mock()
            original = self.server.RequestHandlerClass
            fixture = self
            self.encrypted_attempts = []

            class EncryptedMock(original):
                def do_POST(self):
                    label = next((label for label in ('A', 'B') if self.path.startswith('/encrypted-' + label.lower() + '/')), None)
                    if label is None:
                        return super().do_POST()
                    raw = self.rfile.read(int(self.headers.get('Content-Length', '0')))
                    payload = json.loads(raw)
                    if cache.responses_capability_probe(payload):
                        return self.reply({'id': 'resp_probe', 'object': 'response', 'status': 'completed',
                            'output': [{'type': 'function_call', 'id': 'fc_probe', 'call_id': 'call_probe',
                                        'name': 'probe_ping', 'arguments': '{"ok":true}'}],
                            'usage': {'input_tokens': 1, 'output_tokens': 1}}, 200)
                    with fixture.lock:
                        fixture.encrypted_attempts.append({'label': label, 'payload': payload,
                            'raw_nonce_preserved': bool(re.search(rb'"fixture_large_integer"\s*:\s*9007199254740993(?:\s*[,}])', raw))})
                    marker = json.dumps(payload.get('input'))
                    if 'synthetic-unknown-cipher' in marker or (label == 'B' and 'synthetic-encrypted-' in marker):
                        return self.reply({'error': {'code': 'invalid_encrypted_content', 'type': 'invalid_request_error',
                            'message': 'Synthetic encrypted content could not be decrypted or decoded.'}}, 400)
                    return self.reply(synthetic_response(payload, label, fixture.suffix), 200)
            self.server.RequestHandlerClass = EncryptedMock

        def setup(self):
            self.owner = self.user('encrypted-route')
            self.api('/admin/users/' + str(self.owner['id']) + '/balance',
                {'balance': 10, 'operation': 'set', 'notes': 'Synthetic encrypted route acceptance'}, self.admin)
            self.groups, self.accounts = {}, {}
            for label, multiplier in [('A', 1), ('B', 2)]:
                group = self.api('/admin/groups', {'name': 'Encrypted ' + label + ' ' + self.suffix,
                    'platform': 'openai', 'rate_multiplier': multiplier, 'subscription_rate_multiplier': 1,
                    'model_pricing': [{'models': [MODEL], 'billing_mode': 'token',
                                      'input_price': .000001, 'output_price': .000002}]}, self.admin)
                self.groups[label] = group
                self.accounts[label] = self.api('/admin/accounts', {'name': 'Encrypted ' + label + ' ' + self.suffix,
                    'platform': 'openai', 'type': 'apikey', 'concurrency': 10, 'priority': 1,
                    'group_ids': [group['id']], 'extra': {'openai_responses_mode': 'force_responses'},
                    'credentials': {'api_key': 'synthetic-fixture-key', 'base_url': self.mock_base + '/encrypted-' + label.lower(),
                                    'model_mapping': {MODEL: MODEL}}}, self.admin)
            self.seed_key = self.new_key('source', [self.groups['A']['id']])
            self.cross_key = self.new_key('cross-key', [self.groups['B']['id'], self.groups['A']['id']])
            self.set_policy('protect')

        def new_key(self, name, groups):
            return self.api('/keys', {'name': name + ' ' + self.suffix, 'billing_source': 'balance',
                'routing_mode': 'composite', 'group_ids': groups}, self.owner['token'])

        def call_encrypted(self, key, items, label, secondary=False, nonce=False):
            payload = {'model': MODEL, 'input': items, 'stream': False,
                       'prompt_cache_key': 'encrypted-' + self.suffix + '-' + label}
            if nonce:
                payload['fixture_large_integer'] = NONCE
            return (self.replica_request if secondary else self.request)('/v1/responses', payload, key['key'])

        def inference_count(self):
            with self.lock:
                return len(self.encrypted_attempts)

        def audit_usage(self, key, expected, group):
            deadline = time.monotonic() + 10
            while True:
                rows = self.sql("SELECT json_build_object('n',COUNT(*),'wrong_group',COUNT(*) FILTER(WHERE group_id<>" +
                    str(int(group)) + "),'multiplier',MIN(rate_multiplier),'charged',COALESCE(SUM(actual_cost),0)) "
                    "FROM usage_logs WHERE api_key_id=" + str(int(key['id'])) + ';')[0]
                if rows['n'] >= expected or time.monotonic() >= deadline:
                    return rows
                time.sleep(.1)

        def verified_replica_request(self, opaque, label, primary_pid):
            # The immediately preceding guard_replica verified this exact PID,
            # container ownership/image/network; no repeat Docker/PG guards consume
            # the finite source lock window. Revalidate after releasing the lock.
            payload = {'url': 'http://127.0.0.1:' + str(self.args.replica_port) + '/v1/responses',
                       'data': {'model': MODEL, 'input': opaque, 'stream': False,
                                'prompt_cache_key': 'encrypted-' + self.suffix + '-' + label},
                       'token': self.cross_key['key'], 'method': None}
            value = cache.b.stage.command(['nsenter', '-t', str(primary_pid), '-n', 'python3', '-c', cache.NETNS_REQUEST],
                                          input=json.dumps(payload))
            return json.loads(value)

        def verification_fault_check(self, opaque):
            self.guard()
            counts = self.sql("SELECT json_build_object('n',COUNT(*),'owned',COUNT(*) FILTER(WHERE user_id=" +
                str(int(self.owner['id'])) + ')) FROM openai_encrypted_sources_geili;')[0]
            if counts != {'n': 2, 'owned': 2}:
                raise RuntimeError('refuse source table lock with unrelated Stage rows')
            records = self.sql("SELECT json_build_object('user_id',user_id,'cipher_digest',cipher_digest) "
                "FROM openai_encrypted_sources_geili WHERE user_id=" + str(int(self.owner['id'])) +
                " AND cipher_digest=encode(sha256(convert_to(" + cache.b.stage.literal('synthetic-encrypted-' + self.suffix + '-tools') +
                ",'UTF8')),'hex');")
            keys = source_cache_keys(records, self.fixture_ids['users'])
            if len(keys) != 1:
                raise RuntimeError('expected exact owned tool source digest before fault')
            app = self.inspect()[0]
            env = dict(v.split('=', 1) for v in app['Config'].get('Env', []) if '=' in v)
            cache.b.stage.validate_redis_environment(env)
            if cache.b.stage.REDIS == 'sub2api-redis':
                raise RuntimeError('refuse production Redis source fault check')
            password = env.get('REDIS_PASSWORD')
            if password is not None and (not isinstance(password, str) or any(c in password for c in '\n\r\0')):
                raise RuntimeError('invalid Stage Redis authentication environment')
            private_env = dict(os.environ)
            private_env.pop('REDISCLI_AUTH', None)
            auth_args = []
            if password:
                private_env['REDISCLI_AUTH'] = password
                auth_args = ['-e', 'REDISCLI_AUTH']
            hint = subprocess.run(['docker', 'exec', *auth_args, cache.b.stage.REDIS, 'redis-cli', '-n', '0', '--raw', 'EXISTS', *keys],
                                  env=private_env, text=True, capture_output=True, timeout=2)
            ready = hint.returncode == 0 and hint.stdout.strip() == '1'
            self.check('actual owned tool source Redis hint exists before verification fault', ready)
            if not ready:
                raise RuntimeError('owned source hint not ready; refuse masking fault setup')
            self.guard_replica()
            verified_pid = self.replica_primary_pid
            before = self.inference_count()
            with bounded_source_lock(['docker', 'exec', '-i', cache.b.stage.PG, 'psql', '-XqAt',
                    '-v', 'ON_ERROR_STOP=1', '-U', cache.b.stage.DATABASE, '-d', cache.b.stage.DATABASE]):
                ready_at = time.monotonic()
                failed = self.verified_replica_request(opaque, 'verification-fault', verified_pid)
                ready_to_response = time.monotonic() - ready_at
                delta = self.inference_count() - before
                self.check('cached source verification failure returns503 with zero upstream calls',
                    failed[0] == 503 and error_code(failed) == 'encrypted_session_verification_unavailable' and delta == 0,
                    dict(response_diagnostic(failed), inference_delta=delta, lock_ready_to_response_seconds=ready_to_response))
                usage = self.audit_usage(self.cross_key, 1, self.groups['A']['id'])
                self.check('verification fault creates no additional usage', usage['n'] == 1, usage)
            self.guard_replica()
            if self.replica_primary_pid != verified_pid:
                raise RuntimeError('verified primary namespace PID changed during source lock')
            resumed = self.call_encrypted(self.cross_key, opaque, 'verification-resumed', True)
            delta = self.inference_count() - before
            self.check('released source verification lock resumes original account once',
                resumed[0] == 200 and delta == 1 and self.encrypted_attempts[-1]['label'] == 'A',
                {'status': resumed[0], 'inference_delta': delta})
            usage = self.audit_usage(self.cross_key, 2, self.groups['A']['id'])
            self.check('resumed request adds exactly one actual original group charge',
                usage['n'] == 2 and usage['wrong_group'] == 0 and float(usage['multiplier']) == 1, usage)

        def warm_policy_processes(self):
            # A cold settings cache may safely use off for its first caller.
            # Warm both processes with isolated plaintext, then allow the one
            # bounded background refresh to finish before producing ciphertext.
            results = []
            for label, request in (('primary', self.request), ('secondary', self.replica_request)):
                payload = {'model': MODEL, 'input': [{'role': 'user', 'content': 'synthetic plaintext policy warmup'}],
                           'stream': False, 'prompt_cache_key': 'policy-warmup-' + self.suffix + '-' + label}
                results.append(request('/v1/responses', payload, self.seed_key['key']))
            time.sleep(1.1)
            ready = all(response[0] == 200 for response in results)
            self.check('both processes complete stateless policy warmup before protected functional checks', ready,
                       {'statuses': [response[0] for response in results], 'refresh_wait_seconds': 1.1})
            if not ready:
                raise RuntimeError('policy warmup failed before encrypted source seeding')

        def run_checks(self):
            self.start_replica()
            self.warm_policy_processes()
            tools = self.call_encrypted(self.seed_key, [{'role': 'user', 'content': 'seed-tools'}], 'seed-tools')
            reasoning = self.call_encrypted(self.seed_key, [{'role': 'user', 'content': 'seed-reasoning'}], 'seed-reasoning')
            self.check('primary seed output records synthetic encrypted items', tools[0] == reasoning[0] == 200)
            self.check('primary seed delivers both exact synthetic encrypted items before provenance checks',
                'synthetic-encrypted-' + self.suffix + '-tools' in json.dumps(tools[1])
                and 'synthetic-encrypted-' + self.suffix + '-reasoning' in json.dumps(reasoning[1]))
            metadata = self.sql("SELECT json_build_object('n',COUNT(*),'account',MIN(account_id),'group',MIN(group_id),"
                "'invalid',COUNT(*) FILTER(WHERE conflict OR LENGTH(cipher_digest)<>64 OR LENGTH(backend_fingerprint)<>64)) "
                "FROM openai_encrypted_sources_geili WHERE user_id=" + str(int(self.owner['id'])) + ';')[0]
            self.check('two exact origins persisted in real PostgreSQL', metadata['n'] == 2 and metadata['invalid'] == 0
                and metadata['account'] == self.accounts['A']['id'] and metadata['group'] == self.groups['A']['id'], metadata)
            opaque = [{'type': 'function_call', 'call_id': 'synthetic-call', 'name': 'read_file', 'arguments': '{}'}] + tools[1]['output'] + [{'role': 'user', 'content': 'continue opaque'}]
            before = self.inference_count()
            result = self.call_encrypted(self.cross_key, opaque, 'cross-key-opaque', True)
            self.check('second same-digest instance and different Key preserve original authorized group/account',
                result[0] == 200 and self.inference_count() == before + 1 and self.encrypted_attempts[-1]['label'] == 'A')
            usage = self.audit_usage(self.cross_key, 1, self.groups['A']['id'])
            self.check('pinned request bills actual original group once', usage['n'] == 1 and usage['wrong_group'] == 0
                and float(usage['multiplier']) == 1, usage)
            self.verification_fault_check(opaque)
            self.api('/keys/' + str(self.cross_key['id']), {'group_ids': [self.groups['B']['id']]}, self.owner['token'], 'PUT')
            before = self.inference_count()
            denied = self.call_encrypted(self.cross_key, opaque, 'revoked', True)
            self.check('revoked source group produces409 without upstream call', denied[0] == 409
                and error_code(denied) == 'encrypted_session_route_conflict' and self.inference_count() == before,
                {'status': denied[0], 'error_code': error_code(denied), 'inference_delta': self.inference_count() - before})
            self.api('/keys/' + str(self.cross_key['id']), {'group_ids': [self.groups['B']['id'], self.groups['A']['id']]}, self.owner['token'], 'PUT')
            before = self.inference_count()
            unknown = self.call_encrypted(self.cross_key, [{'type': 'reasoning', 'id': 'rs_unknown',
                'encrypted_content': 'synthetic-unknown-cipher', 'summary': []}], 'unknown', True)
            seen = self.encrypted_attempts[before:]
            self.check('unknown historical cipher is never stripped on retries', unknown[0] >= 400 and bool(seen)
                and all('synthetic-unknown-cipher' in json.dumps(r['payload']) for r in seen), {'attempts': len(seen), 'status': unknown[0]})
            recovery_key = self.new_key('recovery', [self.groups['A']['id'], self.groups['B']['id']])
            self.api('/admin/accounts/' + str(self.accounts['A']['id']) + '/schedulable', {'schedulable': False}, self.admin)
            disabled = self.sql("SELECT json_build_object('schedulable',schedulable) FROM accounts WHERE id=" + str(self.accounts['A']['id']) + ';')[0]
            self.check('original source actually disabled by scheduling API in PostgreSQL', disabled['schedulable'] is False)
            before = self.inference_count()
            recovered = self.call_encrypted(recovery_key, reasoning[1]['output'] + [{'role': 'user', 'content': 'continue reasoning'}],
                                            'recover-disabled', True, True)
            seen = self.encrypted_attempts[before:]
            self.check('disabled original reasoning source automatically falls back once to authorized B', recovered[0] == 200
                and len(seen) == 1 and seen[0]['label'] == 'B')
            self.check('recovery retains summary and raw large integer, removes only reasoning cipher/id',
                seen[0]['raw_nonce_preserved'] and seen[0]['payload']['input'][0].get('summary') == reasoning[1]['output'][0]['summary']
                and 'encrypted_content' not in seen[0]['payload']['input'][0] and 'id' not in seen[0]['payload']['input'][0])
            usage = self.audit_usage(recovery_key, 1, self.groups['B']['id'])
            self.check('recovery settles exactly once under actual fallback group and multiplier', usage['n'] == 1
                and usage['wrong_group'] == 0 and float(usage['multiplier']) == 2 and float(usage['charged']) > 0, usage)
            self.set_policy('off')
            self.api('/admin/accounts/' + str(self.accounts['A']['id']) + '/schedulable', {'schedulable': True}, self.admin)
            enabled = self.sql("SELECT json_build_object('schedulable',schedulable) FROM accounts WHERE id=" + str(self.accounts['A']['id']) + ';')[0]
            self.check('original source scheduling state restored before ordinary and performance checks', enabled['schedulable'] is True)
            before = self.inference_count()
            normal = self.call_encrypted(self.cross_key, [{'role': 'user', 'content': 'ordinary off request'}], 'off', True)
            self.check('mode off leaves existing ordered-group scheduling behavior', normal[0] == 200
                and self.inference_count() == before + 1 and self.encrypted_attempts[-1]['label'] == 'B')
            self.sample_performance()

        def sample_performance(self):
            # A separate Key ensures bulk performance settlements cannot pollute
            # the functional checks' exactly-one usage assertions.
            key = self.new_key('performance-only', [self.groups['A']['id']])
            batches = []
            self.performance = {'batches': batches, 'summary': {'passed': False, 'failure': 'incomplete'}}
            sample_count = self.args.performance_samples
            for round_index, modes in enumerate((('off', 'protect'), ('protect', 'off'))):
                for mode in modes:
                    self.guard()
                    self.set_policy(mode)
                    def call(index):
                        began = time.monotonic()
                        try:
                            # Primary host request has no secondary netns subprocess
                            # startup cost. Payload is ordinary plaintext in all modes.
                            response = self.call_encrypted(key, [{'role': 'user', 'content': 'synthetic plain performance'}],
                                'perf-' + str(round_index) + '-' + mode + '-' + str(index))
                            body = response[1]
                            ok = (response[0] == 200 and isinstance(body, dict) and body.get('status') == 'completed'
                                  and 'acceptance ok A' in json.dumps(body) and 'encrypted_content' not in json.dumps(body))
                            status = response[0]
                        except Exception:
                            ok, status = False, None
                        return {'latency_seconds': time.monotonic() - began, 'ok': ok, 'status': status}
                    with ThreadPoolExecutor(max_workers=4) as pool:
                        warmup = list(pool.map(call, range(-20, 0)))
                        self.check('performance warmup complete ' + str(round_index) + '/' + mode,
                                   all(r['ok'] for r in warmup), {'requests': len(warmup)})
                        began = time.monotonic()
                        samples = list(pool.map(call, range(sample_count)))
                        elapsed = time.monotonic() - began
                    batch = {'round': round_index, 'mode': mode, 'concurrency': 4, 'warmup_requests': 20,
                        'elapsed_seconds': elapsed, 'latencies_seconds': [r['latency_seconds'] for r in samples],
                        'response_statuses': [r['status'] for r in samples],
                        'failed_responses': sum(not r['ok'] for r in samples),
                        'throughput_per_second': len(samples) / elapsed if elapsed > 0 else None}
                    batches.append(batch)
                    cache.b.stage.private_json(self.private / 'encrypted-performance.json', {
                        'revision': self.args.expected_revision, 'image': self.args.expected_image,
                        'runner_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                        'synthetic_only': True, **self.performance})
            self.performance['summary'] = performance_summary(batches)
            cache.b.stage.private_json(self.private / 'encrypted-performance.json', {
                'revision': self.args.expected_revision, 'image': self.args.expected_image,
                'runner_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                'synthetic_only': True, **self.performance})
            self.check('normal plaintext performance p95 delta<=10ms and throughput loss<=2percent',
                       self.performance['summary']['passed'], self.performance['summary'])

        def cleanup_source_cache(self):
            self.guard()
            users = self.fixture_ids['users']
            previous = self.snapshot.get('encrypted_redis_sources', [])
            current = []
            if users:
                current = self.sql("SELECT json_build_object('user_id',user_id,'cipher_digest',cipher_digest) "
                    'FROM openai_encrypted_sources_geili WHERE user_id IN (' + ','.join(str(int(u)) for u in users) + ');')
            records = previous + current
            keys = source_cache_keys(records, users)
            self.snapshot['encrypted_redis_sources'] = records
            self.snapshot['encrypted_redis_restored'] = False
            # Persist exact deletion authority before Redis or PostgreSQL deletes.
            cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)
            self.cache_manifest_ready = True
            if keys:
                app = self.inspect()[0]
                env = dict(v.split('=', 1) for v in app['Config'].get('Env', []) if '=' in v)
                cache.b.stage.validate_redis_environment(env)
                redis_name = cache.b.stage.REDIS
                if redis_name == 'sub2api-redis':
                    raise RuntimeError('refuse production Redis source cleanup')
                password = env.get('REDIS_PASSWORD')
                if password is not None and (not isinstance(password, str) or any(c in password for c in '\n\r\0')):
                    raise RuntimeError('invalid Stage Redis authentication environment')
                # Docker inherits this environment variable by NAME. Credentials
                # never appear in argv, snapshots, command errors or reports.
                private_env = dict(os.environ)
                private_env.pop('REDISCLI_AUTH', None)
                auth_args = []
                if password:
                    private_env['REDISCLI_AUTH'] = password
                    auth_args = ['-e', 'REDISCLI_AUTH']
                for offset in range(0, len(keys), 128):
                    batch = keys[offset:offset + 128]
                    for operation in ('DEL', 'EXISTS'):
                        try:
                            result = subprocess.run(['docker', 'exec', *auth_args, redis_name,
                                'redis-cli', '-n', '0', '--raw', operation, *batch], env=private_env,
                                text=True, capture_output=True, timeout=30)
                            if result.returncode != 0 or not re.fullmatch(r'\d+\s*', result.stdout):
                                raise RuntimeError('Redis command failed')
                            if operation == 'EXISTS' and int(result.stdout.strip()) != 0:
                                raise RuntimeError('source keys remain')
                        except Exception:
                            raise RuntimeError('owned Stage Redis source cleanup failed; retry canonical restore') from None
            self.snapshot['encrypted_redis_restored'] = True
            cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)
            self.check('exact owned Stage Redis source cache keys removed', True, {'keys': len(keys)})

        def restore(self):
            if not self.snapshot:
                return
            errors = []
            try:
                self.guard()
                rows = self.snapshot.get('encrypted_settings_snapshot')
                if rows is not None:
                    self.sql(restored_setting_sql(rows, cache.b.stage.literal))
                    time.sleep(6)
                    restored = self.sql('SELECT row_to_json(s) FROM settings s ORDER BY key;')
                    self.snapshot['encrypted_settings_restored'] = restored == rows
                    self.check('full settings values identities and timestamps exactly restored', restored == rows)
                    cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)
            except BaseException as exc:
                errors.append(type(exc).__name__)
            try:
                self.cleanup_source_cache()
            except BaseException as exc:
                errors.append(type(exc).__name__)
            try:
                self.guard()
                users = self.fixture_ids['users']
                if users:
                    if not getattr(self, 'cache_manifest_ready', False):
                        raise RuntimeError('refuse losing source rows before durable cache cleanup manifest')
                    self.sql('DELETE FROM openai_encrypted_sources_geili WHERE user_id IN (' +
                             ','.join(str(int(u)) for u in users) + ');')
                    self.check('only this run source metadata removed', self.sql("SELECT json_build_object('n',COUNT(*)) "
                        'FROM openai_encrypted_sources_geili WHERE user_id IN (' + ','.join(str(int(u)) for u in users) + ');')[0]['n'] == 0)
            except BaseException as exc:
                errors.append(type(exc).__name__)
            try:
                super().restore()
            except BaseException as exc:
                errors.append(type(exc).__name__)
            if errors:
                self.snapshot['restored'] = False
                cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)
                raise RuntimeError('encrypted fixture cleanup incomplete: ' + ','.join(errors))
    return EncryptedFixture


def main():
    early = argparse.ArgumentParser(add_help=False)
    early.add_argument('--ops-bin', required=True)
    known, remaining = early.parse_known_args()
    cache = load_ops(known.ops_bin)
    parser = cache.b.candidate_args()
    parser.add_argument('--replica-port', type=int, default=18517)
    parser.add_argument('--performance-samples', type=int, default=300)
    args = parser.parse_args(remaining)
    cache.b.validate_args(args)
    if not 300 <= args.performance_samples <= 1000:
        raise RuntimeError('performance samples must be300..1000 per batch')
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
        result = {'kind': 'encrypted-route-stage-v1', 'revision': args.expected_revision, 'image': args.expected_image,
            'runner_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), 'passed': passed,
            'checks': fixture.checks, 'restored': fixture.snapshot.get('restored', False),
            'settings_restored': fixture.snapshot.get('encrypted_settings_restored', False),
            'source_cache_removed': fixture.snapshot.get('encrypted_redis_restored', False),
            'replica_removed': fixture.snapshot.get('cache_replica', {}).get('removed', False),
            'synthetic_only': True, 'real_suppliers_executed': False, 'production_ready': False,
            'performance': getattr(fixture, 'performance', None)}
        cache.b.stage.private_json(fixture.private / 'encrypted-result.json', result)
        print('RESULT ' + str(fixture.private / 'encrypted-result.json'), flush=True)
    return 0 if passed else 1


if __name__ == '__main__':
    raise SystemExit(main())
