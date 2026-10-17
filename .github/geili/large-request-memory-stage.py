#!/usr/bin/env python3
"""Candidate-bound, mock-only large-body acceptance on canonical Stage.

Default --dry-run only reads identity/schema/resource guards. --execute creates
exact-ID synthetic fixtures, with zero billing multipliers and a mock bound to
loopback inside Stage's network namespace. It never deploys or changes settings.
"""
from __future__ import annotations

import argparse
import fcntl
import gzip
import hashlib
import http.client
import http.server
import importlib.util
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import threading
import time
import urllib.parse
import uuid
from datetime import datetime, timezone
from decimal import Decimal

MIB = 1 << 20
STAGE_MEMORY = 1536 * MIB
BODY_BUDGET = 128 * MIB
KIND = 'large-request-memory-stage-v1'
MODELS = {'anthropic': 'claude-opus-5-5', 'gemini': 'gemini-4-argon'}


def load_ops(directory):
    path = Path(directory).resolve(strict=True) / 'accept-billing-reliability-stage.py'
    spec = importlib.util.spec_from_file_location('memory_stage_billing', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def exact_body(protocol, marker, size):
    if protocol == 'anthropic':
        prefix = ('{"model":' + json.dumps(MODELS[protocol]) +
                  ',"max_tokens":16,"stream":true,"messages":[{"role":"user","content":"' + marker + ' ').encode()
        suffix = b'"}]}'
    else:
        prefix = ('{"contents":[{"role":"user","parts":[{"text":"' + marker + ' ').encode()
        suffix = b'"}]}]}'
    if size < len(prefix) + len(suffix):
        raise ValueError('body size smaller than synthetic JSON envelope')
    return prefix + b'x' * (size - len(prefix) - len(suffix)) + suffix


def sse(protocol, terminal=False):
    if protocol == 'gemini':
        obj = {'candidates': [{'content': {'role': 'model', 'parts': [{'text': 'memory acceptance OK'}]}}],
               'usageMetadata': {'promptTokenCount': 1, 'candidatesTokenCount': 1, 'totalTokenCount': 2}}
        if terminal:
            obj['candidates'][0]['finishReason'] = 'STOP'
        return ('data: ' + json.dumps(obj) + '\n\n').encode()
    if terminal:
        events = [{'type': 'content_block_stop', 'index': 0},
                  {'type': 'message_delta', 'delta': {'stop_reason': 'end_turn'}, 'usage': {'output_tokens': 1}},
                  {'type': 'message_stop'}]
    else:
        events = [{'type': 'message_start', 'message': {'id': 'msg_synthetic_memory', 'type': 'message',
                   'role': 'assistant', 'model': MODELS[protocol], 'content': [],
                   'usage': {'input_tokens': 1, 'output_tokens': 0}}},
                  {'type': 'content_block_start', 'index': 0, 'content_block': {'type': 'text', 'text': ''}},
                  {'type': 'content_block_delta', 'index': 0,
                   'delta': {'type': 'text_delta', 'text': 'memory acceptance OK'}}]
    return ''.join('event: ' + item['type'] + '\ndata: ' + json.dumps(item) + '\n\n' for item in events).encode()


class RawCall:
    """A bounded streaming client whose socket can be explicitly disconnected."""
    def __init__(self, fixture, label, protocol, body, encoding=None):
        self.fixture, self.label, self.protocol = fixture, label, protocol
        self.request_id = str(uuid.uuid4())
        self.payload, self.encoding = body, encoding
        self.status, self.headers, self.output, self.error = None, {}, bytearray(), None
        self.connection = self.sock = None
        self.ready, self.done = threading.Event(), threading.Event()
        self.cancelled = False
        self.thread = threading.Thread(target=self.run, daemon=True)

    def start(self):
        self.fixture.clients.append(self)
        self.thread.start()
        return self

    def run(self):
        self.fixture.client_enter()
        try:
            target = urllib.parse.urlsplit(self.fixture.base)
            self.connection = http.client.HTTPConnection(target.hostname, target.port, timeout=90)
            headers = {'Authorization': 'Bearer ' + self.fixture.keys[self.protocol]['key'],
                       'Content-Type': 'application/json', 'X-Request-ID': self.request_id,
                       'Connection': 'close'}
            if self.encoding:
                headers['Content-Encoding'] = self.encoding
            self.connection.connect()
            self.sock = self.connection.sock
            self.connection.request('POST', self.fixture.path(self.protocol), self.payload, headers)
            self.payload = None
            response = self.connection.getresponse()
            self.status, self.headers = response.status, dict(response.headers)
            while True:
                line = response.readline(65536)
                if not line:
                    break
                if len(self.output) + len(line) > 256 * 1024:
                    raise RuntimeError('synthetic response exceeded its bounded evidence size')
                self.output.extend(line)
                if line.startswith(b'data:') or self.status != 200:
                    self.ready.set()
            response.close()
        except BaseException as exc:
            self.error = type(exc).__name__ + ': ' + str(exc)
        finally:
            self.payload = None
            if self.connection:
                self.connection.close()
            self.fixture.client_exit()
            self.done.set()
            self.ready.set()

    def cancel(self):
        self.cancelled = True
        if self.sock:
            try:
                self.sock.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
        if self.connection:
            self.connection.close()

    def report(self):
        return {'label': self.label, 'protocol': self.protocol, 'request_id': self.request_id,
                'status': self.status, 'cancelled': self.cancelled, 'done': self.done.is_set(),
                'error': self.error, 'response_bytes': len(self.output),
                'response_sha256': hashlib.sha256(self.output).hexdigest()}


def fixture_type(b, source):
    class MemoryFixture(b.fixture_type(source)):
        def __init__(self, args):
            super().__init__(args)
            self.clients, self.expected, self.observations, self.unexpected = [], {}, [], []
            self.active_clients = self.max_clients = self.early_requests = 0
            self.sample_stop, self.samples, self.sample_errors = threading.Event(), [], []
            self.sampler = None
            self.overload_request_ids = []
            self.checked_at = datetime.now(timezone.utc).isoformat()

        def path(self, protocol):
            if protocol == 'anthropic':
                return '/v1/messages'
            return '/v1beta/models/' + MODELS[protocol] + ':streamGenerateContent?alt=sse'

        def client_enter(self):
            with self.lock:
                self.active_clients += 1
                self.max_clients = max(self.max_clients, self.active_clients)

        def client_exit(self):
            with self.lock:
                self.active_clients -= 1

        def resource_guard(self):
            self.guard()
            app = self.inspect()[0]
            env = dict(line.split('=', 1) for line in app['Config'].get('Env', []) if '=' in line)
            limit = app.get('HostConfig', {}).get('Memory')
            budget = int(env.get('GEILI_REQUEST_BODY_BUDGET_BYTES', limit // 12 if limit else 0))
            if limit != STAGE_MEMORY or budget != BODY_BUDGET:
                raise RuntimeError('this exact 2x64MiB proof requires Stage 1536MiB / raw budget128MiB')
            root = Path('/proc') / str(self.netns_pid) / 'root/sys/fs/cgroup'
            if not (root / 'memory.current').is_file() or not (root / 'memory.events').is_file():
                raise RuntimeError('readable Stage cgroup v2 accounting required for memory proof')
            return app, root, env

        def prepare(self):
            super().prepare()
            self.snapshot['memory_runner_kind'] = KIND
            b.stage.private_json(self.private / 'snapshot.json', self.snapshot)
            self.initial_app, self.cgroup, self.start_env = self.resource_guard()
            self.events_before = self.memory_events()
            # Do not unknowingly invoke a pre-existing external paid audit.
            audit = self.sql("SELECT json_build_object('key',key,'value',value) FROM settings WHERE key IN ('prompt_audit_config','risk_control_enabled','content_moderation_config');")
            enabled = any(row['key'] == 'risk_control_enabled' and row['value'] == 'true' for row in audit)
            for row in audit:
                value = row['value']
                if enabled and row['key'] != 'risk_control_enabled' and value:
                    try:
                        audit_enabled = json.loads(value).get('enabled', False)
                    except (ValueError, AttributeError):
                        raise RuntimeError('cannot establish disabled Stage external audit configuration')
                    if audit_enabled:
                        raise RuntimeError('Stage external audit must already be disabled; runner does not change settings')

        def setup(self):
            self.owner = self.user('large-memory')
            self.api('/admin/users/' + str(self.owner['id']) + '/balance',
                     {'balance': 1, 'operation': 'set', 'notes': 'Synthetic Stage memory acceptance; no payment'}, self.admin)
            self.keys, self.groups = {}, {}
            for protocol, model in MODELS.items():
                group = self.api('/admin/groups', {'name': 'Memory ' + protocol + ' ' + self.suffix,
                    'platform': protocol, 'rate_multiplier': 0, 'subscription_rate_multiplier': 0,
                    'subscription_enabled': False, 'long_context_pricing_enabled': False,
                    'model_allowlist': {'enabled': True, 'models': [model]},
                    'model_pricing': [{'models': [model], 'billing_mode': 'token',
                                      'input_price': .000001, 'output_price': .000002}]}, self.admin)
                self.groups[protocol] = group
                account = {'name': 'Memory mock ' + protocol + ' ' + self.suffix,
                    'platform': protocol, 'type': 'apikey', 'credentials': {'api_key': 'synthetic-memory-key',
                    'base_url': self.mock_base + '/memory/' + protocol, 'model_mapping': {model: model}},
                    'group_ids': [group['id']], 'concurrency': 10, 'priority': 1}
                if protocol == 'anthropic':
                    account['extra'] = {'anthropic_passthrough': True}
                self.api('/admin/accounts', account, self.admin)
                self.keys[protocol] = self.api('/keys', {'name': 'Memory ' + protocol,
                    'billing_source': 'balance', 'routing_mode': 'single', 'group_id': group['id']}, self.owner['token'])

        def start_mock(self):
            super().start_mock()  # Canonical setns + loopback binding, no host listener.
            fixture = self
            class MemoryMock(http.server.BaseHTTPRequestHandler):
                protocol_version = 'HTTP/1.1'
                def log_message(self, *_):
                    pass

                def do_GET(self):
                    self.send_error(404)

                def do_POST(self):
                    protocol = next((name for name in MODELS if self.path.startswith('/memory/' + name + '/')), None)
                    if protocol is None:
                        self.send_error(404)
                        return
                    remaining = int(self.headers.get('Content-Length', '0'))
                    size, digest = 0, hashlib.sha256()
                    while remaining:
                        chunk = self.rfile.read(min(65536, remaining))
                        if not chunk:
                            break
                        size += len(chunk)
                        remaining -= len(chunk)
                        digest.update(chunk)
                    checksum = digest.hexdigest()
                    with fixture.lock:
                        expected = fixture.expected.get(checksum)
                        if expected is not None:
                            expected['mock_count'] += 1
                            fixture.observations.append({'label': expected['label'], 'protocol': protocol,
                                'bytes': size, 'sha256': checksum, 'at': time.monotonic()})
                        elif len(fixture.unexpected) < 32:
                            fixture.unexpected.append({'protocol': protocol, 'bytes': size, 'sha256': checksum})
                    if expected is None or expected['protocol'] != protocol or expected['size'] != size or remaining:
                        self.send_error(400, 'unregistered synthetic request')
                        return
                    expected['drained'].set()
                    try:
                        self.send_response(200)
                        self.send_header('Content-Type', 'text/event-stream')
                        self.send_header('Connection', 'close')
                        self.end_headers()
                        self.wfile.write(sse(protocol))
                        self.wfile.flush()
                        deadline = time.monotonic() + 120
                        while not expected['release'].wait(.5):
                            if time.monotonic() > deadline:
                                raise TimeoutError('bounded synthetic SSE hold expired')
                            self.wfile.write(b': synthetic memory heartbeat\n\n')
                            self.wfile.flush()
                        self.wfile.write(sse(protocol, terminal=True))
                        self.wfile.flush()
                    except (OSError, TimeoutError):
                        pass
                    finally:
                        expected['closed'].set()
                        self.close_connection = True
            self.server.RequestHandlerClass = MemoryMock

        def memory_events(self):
            return {parts[0]: int(parts[1]) for line in (self.cgroup / 'memory.events').read_text().splitlines()
                    if len(parts := line.split()) == 2}

        def start_sampler(self):
            def sample():
                while not self.sample_stop.is_set():
                    try:
                        current = int((self.cgroup / 'memory.current').read_text())
                        stats = {parts[0]: int(parts[1]) for line in (self.cgroup / 'memory.stat').read_text().splitlines()
                                 if len(parts := line.split()) == 2}
                        self.samples.append({'elapsed': round(time.monotonic() - self.sample_started, 3),
                                             'current': current, 'anon': stats.get('anon'), 'file': stats.get('file')})
                    except (OSError, ValueError) as exc:
                        if len(self.sample_errors) < 8:
                            self.sample_errors.append(type(exc).__name__ + ': ' + str(exc))
                    self.sample_stop.wait(.25)
            self.sample_started = time.monotonic()
            self.sampler = threading.Thread(target=sample, daemon=True)
            self.sampler.start()

        def new_call(self, label, protocol, size=64*MIB, held=True, encoding=None):
            body = exact_body(protocol, self.suffix + '-' + label, size)
            checksum = hashlib.sha256(body).hexdigest()
            entry = {'label': label, 'protocol': protocol, 'size': len(body), 'sha256': checksum,
                     'drained': threading.Event(), 'release': threading.Event(), 'closed': threading.Event(), 'mock_count': 0}
            if not held:
                entry['release'].set()
            with self.lock:
                self.expected[checksum] = entry
            payload = gzip.compress(body, compresslevel=1) if encoding == 'gzip' else body
            call = RawCall(self, label, protocol, payload, encoding).start()
            return call, entry

        def await_held(self, pair):
            call, entry = pair
            self.check(call.label + ' full upstream body drained', entry['drained'].wait(30),
                       {'bytes': entry['size'], 'sha256': entry['sha256'], 'client': call.report()})
            self.check(call.label + ' SSE has first content', call.ready.wait(10) and call.status == 200 and bool(call.output), call.report())

        def finish(self, pair):
            call, entry = pair
            entry['release'].set()
            self.check(call.label + ' completed after release', call.done.wait(15) and call.status == 200 and call.error is None, call.report())
            terminal = b'message_stop' if call.protocol == 'anthropic' else b'"finishReason": "STOP"'
            self.check(call.label + ' terminal event intact', terminal in call.output, call.report())

        def overload_headers_only(self):
            target = urllib.parse.urlsplit(self.base)
            connection = http.client.HTTPConnection(target.hostname, target.port, timeout=10)
            self.early_requests += 1
            self.client_enter()
            self.overload_request_id = str(uuid.uuid4())
            self.overload_request_ids.append(self.overload_request_id)
            try:
                connection.putrequest('POST', self.path('anthropic'))
                for key, value in {'Authorization': 'Bearer ' + self.keys['anthropic']['key'],
                                   'Content-Type': 'application/json', 'Content-Length': str(20*MIB),
                                   'X-Request-ID': self.overload_request_id, 'Connection': 'close'}.items():
                    connection.putheader(key, value)
                connection.endheaders()
                response = connection.getresponse()
                raw = response.read(65536)
                self.check('20MiB header rejected before upload', response.status == 503 and
                           response.getheader('Retry-After') == '3' and b'gateway_memory_capacity_exceeded' in raw,
                           {'status': response.status, 'response': raw.decode(errors='replace')})
            finally:
                connection.close()
                self.client_exit()

        def runtime_evidence(self):
            command = subprocess.run(['docker', 'logs', '--since', self.checked_at, '--tail', '3000', b.stage.APP],
                                     capture_output=True, text=True, timeout=30, check=True)
            result = command.stdout + '\n' + command.stderr
            rows = []
            for line in result.splitlines():
                marker = line.find('runtime.memory')
                if marker < 0:
                    continue
                # Console logs are "<time>\t<level>\t<file>\truntime.memory\t{json}";
                # JSON-encoded logs put the message inside the object instead.
                payload = line[marker + len('runtime.memory'):].lstrip('\t ')
                if not payload.startswith('{'):
                    try:
                        value = json.loads(line)
                    except ValueError:
                        continue
                    if value.get('msg') != 'runtime.memory':
                        continue
                    payload = value
                try:
                    value = json.loads(payload) if isinstance(payload, str) else payload
                except ValueError:
                    continue
                rows.append({key: value[key] for key in ('time', 'ts', 'go_memory_limit_bytes', 'heap_alloc_bytes',
                    'heap_live_bytes', 'heap_idle_bytes', 'heap_free_bytes', 'heap_released_bytes', 'num_gc') if key in value})
            return rows

        def run_checks(self):
            self.start_sampler()
            for cycle in range(self.args.cycles):
                first = [self.new_call('held-' + str(cycle) + '-' + name, name) for name in MODELS]
                for pair in first:
                    self.await_held(pair)
                with self.lock:
                    before = len(self.observations)
                self.overload_headers_only()
                with self.lock:
                    after = len(self.observations)
                self.check('cycle' + str(cycle) + ' full shared budget rejects before any upstream attempt', before == after == 2*(cycle+1))
                time.sleep(self.args.hold_seconds)
                for pair in first:
                    self.finish(pair)
            # A full second pair proves all 128MiB was reclaimed, not just
            # enough capacity for a small health or chat request.
            second = [self.new_call('repeat-' + name, name) for name in MODELS]
            for pair in second:
                self.await_held(pair)
            second[0][0].cancel()
            self.check('client disconnect completed', second[0][0].done.wait(10), second[0][0].report())
            # Strict Anthropic streams intentionally continue draining after a
            # client disconnect to collect the supplier's final billed usage.
            # Retain admission during that drain, then end the mock normally.
            self.overload_headers_only()
            second[0][1]['release'].set()
            self.check('cancelled client upstream drain reached terminal event', second[0][1]['closed'].wait(10))
            deadline = time.monotonic() + 15
            while True:
                settled = self.sql(f"SELECT json_build_object('n',COUNT(*)) FROM usage_logs WHERE user_id={int(self.owner['id'])} AND api_key_id={int(self.keys['anthropic']['id'])};")[0]['n']
                if settled >= self.args.cycles + 1 or time.monotonic() >= deadline:
                    break
                time.sleep(.5)
            self.check('cancelled stream retains final usage accounting', settled >= self.args.cycles + 1)
            replacement = self.new_call('after-cancel', 'anthropic')
            self.await_held(replacement)
            self.finish(replacement)
            self.finish(second[1])
            compressed = self.new_call('gzip-normal', 'anthropic', size=MIB, held=False, encoding='gzip')
            self.await_held(compressed)
            self.finish(compressed)
            uid = int(self.owner['id'])
            completed = len(self.expected)  # Cancelled client's upstream still reached its terminal usage.
            deadline = time.monotonic() + 15
            while True:
                rows = self.sql(f"SELECT json_build_object('pending',COUNT(*) FILTER (WHERE state<>'settled' OR delivered_at IS NULL),'receipts',COUNT(*),'logs',(SELECT COUNT(*) FROM usage_logs WHERE user_id={uid})) FROM usage_settlement_receipts WHERE user_id={uid};")
                if (rows[0]['pending'] == 0 and rows[0]['receipts'] >= completed and rows[0]['logs'] >= completed) or time.monotonic() >= deadline:
                    break
                time.sleep(.5)
            self.check('owned completed usage delivered with no pending receipts', rows[0]['pending'] == 0
                       and rows[0]['receipts'] >= completed and rows[0]['logs'] >= completed, rows[0])
            request_ids = ','.join(b.stage.literal(value) for value in self.overload_request_ids)
            wallet = self.sql(f"SELECT json_build_object('balance',balance::text,'charges',(SELECT COUNT(*) FROM usage_logs WHERE user_id={uid} AND actual_cost<>0),'receipt_charges',(SELECT COUNT(*) FROM usage_settlement_receipts WHERE user_id={uid} AND charged_amount<>0),'key_quota',(SELECT COALESCE(SUM(quota_used),0) FROM api_keys WHERE user_id={uid}),'logs',(SELECT COUNT(*) FROM usage_logs WHERE user_id={uid}),'payments',(SELECT COUNT(*) FROM payment_orders WHERE user_id={uid}),'overload_receipts',(SELECT COUNT(*) FROM usage_settlement_receipts WHERE user_id={uid} AND request_id IN ({request_ids}))) FROM users WHERE id={uid};")[0]
            self.check('mock requests have usage evidence and zero charge/payment/refund',
                       Decimal(wallet['balance']) == Decimal(1) and wallet['charges'] == 0 and wallet['logs'] >= completed
                       and wallet['receipt_charges'] == 0 and Decimal(str(wallet['key_quota'])) == 0
                       and wallet['payments'] == 0 and wallet['overload_receipts'] == 0 and self.refunds == [], wallet)
            self.check('accepted upstream bodies match registered exact hashes',
                       len(self.observations) == len(self.expected) and all(entry['mock_count'] == 1 for entry in self.expected.values()),
                       self.observations)
            self.check('no unexpected large upstream payload', not any(item['bytes'] > 4096 for item in self.unexpected), self.unexpected)
            load_peak = max((item['current'] for item in self.samples), default=0)
            settled_at = time.monotonic()
            deadline = settled_at + 90
            while True:
                settled = int((self.cgroup / 'memory.current').read_text())
                if settled < load_peak or time.monotonic() >= deadline:
                    break
                time.sleep(5)
            self.quiescent = {'peak_bytes': load_peak, 'settled_bytes': settled,
                              'after_seconds': round(time.monotonic() - settled_at, 1)}
            self.check('memory decreases after leases and streams finish', self.quiescent['settled_bytes'] < load_peak, self.quiescent)
            self.check('Stage health still succeeds', self.request('/health')[0] == 200)
            final_app, _, _ = self.resource_guard()
            self.check('Stage identity/start/restarts unchanged',
                       final_app['Id'] == self.initial_app['Id'] and final_app['State']['StartedAt'] == self.initial_app['State']['StartedAt']
                       and final_app['RestartCount'] == self.initial_app['RestartCount'] and not final_app['State'].get('OOMKilled'))
            events = self.memory_events()
            self.check('no Stage OOM or hard-limit event increase', all(events.get(key, 0) == self.events_before.get(key, 0)
                       for key in ('oom', 'oom_kill', 'oom_group_kill', 'max')), {'before': self.events_before, 'after': events})
            self.check('sampled Stage memory preserves10percent headroom', bool(self.samples) and not self.sample_errors
                       and max(item['current'] for item in self.samples) < STAGE_MEMORY*9//10,
                       {'samples': len(self.samples), 'peak_bytes': max((item['current'] for item in self.samples), default=None), 'errors': self.sample_errors})
            self.runtime_rows = self.runtime_evidence()
            deadline = time.monotonic() + 65
            while not self.runtime_rows and time.monotonic() < deadline:
                time.sleep(5)
                self.runtime_rows = self.runtime_evidence()
            self.check('runtime memory telemetry observed', bool(self.runtime_rows))
            if 'GOMEMLIMIT' not in self.start_env:
                self.check('observed Go runtime limit equals75percent', all(row.get('go_memory_limit_bytes') == STAGE_MEMORY*3//4 for row in self.runtime_rows), self.runtime_rows)

        def stop_owned_work(self):
            for entry in self.expected.values():
                entry['release'].set()
            for call in self.clients:
                if not call.done.is_set():
                    call.cancel()
            for call in self.clients:
                call.thread.join(timeout=3)
            self.sample_stop.set()
            if self.sampler:
                self.sampler.join(timeout=3)
            if any(call.thread.is_alive() for call in self.clients):
                raise RuntimeError('synthetic client did not stop during cleanup')

        def execute_memory(self):
            schema = self.verify_schema()
            self.production_identity()
            _, _, env = self.resource_guard()
            if self.args.dry_run:
                print(json.dumps({'dry_run': True, 'kind': KIND, 'revision': self.args.expected_revision,
                    'image': self.args.expected_image, 'memory_bytes': STAGE_MEMORY, 'raw_body_budget_bytes': BODY_BUDGET,
                    'explicit_gomemlimit': 'GOMEMLIMIT' in env, 'schema': schema}))
                return True
            if self.args.restore:
                path = b.stage.secure_file(self.args.restore)
                if not path.is_relative_to(b.stage.BACKUPS) or path.name != 'snapshot.json':
                    raise RuntimeError('restore must use canonical private snapshot')
                self.private, self.snapshot = path.parent, json.loads(path.read_text())
                if (self.snapshot.get('memory_runner_kind') != KIND or self.snapshot.get('revision') != self.args.expected_revision
                    or self.snapshot.get('image') != self.args.expected_image or self.snapshot.get('database') != b.stage.DATABASE
                    or self.snapshot.get('project') != b.stage.PROJECT):
                    raise RuntimeError('memory restoration identity mismatch')
                self.fixture_ids = self.snapshot['fixture_ids']
                if set(self.fixture_ids) != {'users', 'groups', 'accounts', 'plans', 'providers', 'keys'} or any(type(i) is not int or i <= 0 for ids in self.fixture_ids.values() for i in ids):
                    raise RuntimeError('invalid persisted fixture IDs')
                self.login()
                self.restore()
                return True
            error = None
            try:
                self.prepare()
                self.start_mock()
                self.setup()
                self.run_checks()
            except BaseException as exc:
                error = type(exc).__name__ + ': ' + str(exc)
            finally:
                try:
                    self.stop_owned_work()
                except BaseException as exc:
                    error = (error or '') + '; client cleanup: ' + type(exc).__name__ + ': ' + str(exc)
                try:
                    self.restore()
                except BaseException as exc:
                    error = (error or '') + '; cleanup: ' + type(exc).__name__ + ': ' + str(exc)
                finally:
                    if self.server:
                        self.server.shutdown()
                        self.server.server_close()
            result = {'kind': KIND, 'revision': self.args.expected_revision, 'image': self.args.expected_image,
                'runner_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), 'checked_at': self.checked_at,
                'restored': bool(self.snapshot and self.snapshot.get('restored')), 'error': error, 'checks': self.checks,
                'gateway_requests': len(self.clients) + self.early_requests, 'max_gateway_http_concurrency': self.max_clients,
                'clients': [call.report() for call in self.clients], 'mock_observations': self.observations,
                'memory_samples': self.samples, 'sample_errors': self.sample_errors,
                'quiescent_memory': getattr(self, 'quiescent', None), 'cycles': self.args.cycles,
                'runtime_memory': getattr(self, 'runtime_rows', []), 'real_provider_calls': False,
                'payments_executed': False, 'production_ready': False}
            result['passed'] = error is None and result['restored'] and bool(self.checks) and all(check['passed'] for check in self.checks)
            if self.snapshot:
                b.stage.private_json(self.private / 'memory-result.json', result)
                print('RESULT ' + str(self.private / 'memory-result.json'), flush=True)
            return result['passed']
    return MemoryFixture


def main(argv=None):
    early = argparse.ArgumentParser(add_help=False)
    early.add_argument('--ops-bin', required=True)
    known, remaining = early.parse_known_args(argv)
    b = load_ops(known.ops_bin)
    parser = b.candidate_args()
    parser.description = __doc__
    parser.set_defaults(mock_port=19027)
    parser.add_argument('--hold-seconds', type=float, default=65, help='SSE hold,1..65 seconds;default65 captures runtime telemetry')
    parser.add_argument('--cycles', type=int, default=1, help='repeat saturation/release cycles,1..30;default1')
    args = parser.parse_args(remaining)
    b.validate_args(args)
    if not 1 <= args.hold_seconds <= 65:
        raise RuntimeError('hold-seconds must be1..65')
    if not 1 <= args.cycles <= 30:
        raise RuntimeError('cycles must be1..30')
    fixture = fixture_type(b, b.stage.load_source(args.source_root))(args)
    if args.dry_run:
        return 0 if fixture.execute_memory() else 1
    with (b.stage.LAB / '.v2-synthetic.lock').open('a') as lock:
        os.chmod(lock.name, 0o600)
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
        return 0 if fixture.execute_memory() else 1


if __name__ == '__main__':
    raise SystemExit(main())
