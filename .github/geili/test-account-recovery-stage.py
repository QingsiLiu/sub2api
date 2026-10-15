#!/usr/bin/env python3
"""Offline guard/oracle/restoration tests. No Docker, Stage or real provider calls."""
import importlib.util
import json
import io
import threading
import tempfile
import copy
from unittest.mock import patch, Mock
from pathlib import Path
from types import SimpleNamespace
import unittest

spec = importlib.util.spec_from_file_location('account_recovery_stage', Path(__file__).with_name('account-recovery-stage.py'))
s = importlib.util.module_from_spec(spec)
spec.loader.exec_module(s)


def args(**patch):
    values = dict(expected_revision='a' * 40, expected_production_revision='b' * 40,
                  expected_image='ghcr.io/qingsiliu/sub2api@sha256:' + 'c' * 64,
                  expected_production_image='ghcr.io/qingsiliu/sub2api@sha256:' + 'd' * 64,
                  mock_port=19018, replica_port=18519, execute=False, restore=None, dry_run=False)
    values.update(patch)
    return SimpleNamespace(**values)


class GuardTests(unittest.TestCase):
    def test_defaults_readonly_and_execute_is_explicit(self):
        a = args(); s.validate_guard_options(a); self.assertTrue(a.dry_run)
        a = args(execute=True, baseline_image='ghcr.io/qingsiliu/sub2api@sha256:' + 'e' * 64, baseline_revision='f' * 40); s.validate_guard_options(a); self.assertFalse(a.dry_run)
        a = args(restore='/canonical/snapshot.json'); s.validate_guard_options(a); self.assertFalse(a.dry_run)

    def test_rejects_short_revisions_mutable_images_and_colliding_ports(self):
        for patch in ({'expected_revision': 'a' * 7}, {'expected_production_revision': 'main'},
                      {'expected_image': 'ghcr.io/qingsiliu/sub2api:latest'},
                      {'expected_production_image': 'ghcr.io/qingsiliu/sub2api@sha256:' + 'D' * 64},
                      {'replica_port': 19018}, {'mock_port': 8080}, {'replica_port': 80},
                      {'replica_port': True}, {'execute': True, 'dry_run': True},
                      {'execute': True, 'restore': 'snapshot'}, {'execute': True},
                      {'baseline_image': 'ghcr.io/qingsiliu/sub2api:latest', 'baseline_revision': 'a' * 40}):
            with self.subTest(patch=patch), self.assertRaises(RuntimeError):
                s.validate_guard_options(args(**patch))

    def test_rejects_external_or_wildcard_mock_urls(self):
        self.assertEqual(s.mock_account_url('http://127.0.0.1:19018', 'fixture-a'),
                         'http://127.0.0.1:19018/recovery/fixture-a')
        for base in ('http://0.0.0.0:19018', 'https://api.anthropic.com', 'http://localhost:19018',
                     'http://127.0.0.1:19018@evil.invalid', 'http://127.0.0.1:19018/redirect'):
            with self.subTest(base=base), self.assertRaises(RuntimeError): s.mock_account_url(base, 'fixture-a')
        with self.assertRaises(RuntimeError): s.mock_account_url('http://127.0.0.1:19018', '../escape')

    def test_probe_recognition_requires_frozen_request_not_customer_text(self):
        good = {'messages': [{'role': 'user', 'content': 'Say OK.'}], 'max_tokens': 64, 'model': s.MODEL}
        self.assertTrue(s.is_probe(good))
        for patch in ({'max_tokens': 16}, {'messages': [{'role': 'user', 'content': 'fixture_healthy'}]},
                      {'tools': []}, {'thinking': {'type': 'adaptive'}}):
            self.assertFalse(s.is_probe({**good, **patch}))

    def test_new_fixture_declares_only_its_exact_models_independent_of_availability(self):
        writes = []
        cache = SimpleNamespace(fixture_type=lambda _: object,
            b=SimpleNamespace(stage=SimpleNamespace(private_json=lambda *a: None)))
        fixture = s.fixture_type(cache, None)()
        fixture.lock = threading.Lock()
        fixture.recovery_modes = {}
        fixture.admin, fixture.owner = 'synthetic-admin', {'token': 'synthetic-user'}
        fixture.suffix, fixture.mock_base, fixture.private = 'unit', 'http://127.0.0.1:19018', Path('/offline')
        fixture.snapshot = {'recovery_account_names': {}}
        def api(path, payload, token):
            writes.append((path, payload))
            return {'id': len(writes), 'name': payload['name'], 'key': 'synthetic-unit-key'}
        fixture.api = api
        fixture.new_case('manual')
        group_payload = next(payload for path, payload in writes if path == '/admin/groups')
        self.assertEqual(group_payload['model_allowlist'], {'enabled': True, 'models': [s.MODEL, s.OTHER_MODEL]})
        self.assertNotIn('*', group_payload['model_allowlist']['models'])
        self.assertNotIn('undeclared-model', group_payload['model_allowlist']['models'])
        self.assertEqual(group_payload['model_pricing'][0]['models'], group_payload['model_allowlist']['models'])
        writes.clear()
        fixture.new_case('catalog', declare_allowlist=False)
        group_payload = next(payload for path, payload in writes if path == '/admin/groups')
        self.assertNotIn('model_allowlist', group_payload)
        self.assertEqual(group_payload['model_pricing'][0]['models'], [s.MODEL, s.OTHER_MODEL])
        account_payload = next(payload for path, payload in writes if path == '/admin/accounts')
        self.assertEqual(account_payload['credentials']['model_mapping'], {s.MODEL: s.MODEL, s.OTHER_MODEL: s.OTHER_MODEL})
        with self.assertRaises(RuntimeError): fixture.new_case('accidental-optout', declare_allowlist=None)

    def test_balance_latch_cleanup_uses_only_exact_ids_never_prefix_scan(self):
        self.assertEqual(s.balance_latch_keys([29, 13]), ['geili:account:balance_latch:13', 'geili:account:balance_latch:29'])
        with self.assertRaises(RuntimeError): s.balance_latch_keys([True])
        self.assertTrue(all('*' not in key for key in s.balance_latch_keys([13])))

    def test_cleanup_ids_are_exact_no_boolean_or_name_guess(self):
        query = s.cleanup_runtime_sql([29, 13])
        self.assertEqual(query.count('WHERE account_id IN (13,29)'), 3)
        self.assertNotIn('LIKE', query)
        self.assertNotIn('TRUNCATE', query)
        for ids in ([], [True], [0], [-1], ['13'], [13, 13], ['1); DELETE FROM accounts;']):
            with self.subTest(ids=ids), self.assertRaises(RuntimeError): s.cleanup_runtime_sql(ids)


class BenchmarkResourceTests(unittest.TestCase):
    def primary(self, **patch):
        return {'Id': 'primary-id', 'State': {'Pid': 55, 'Running': True}, 'HostConfig': {
            'NanoCpus': 1000000000, 'Memory': 1610612736, 'MemorySwap': 3221225472,
            'CpuQuota': 0, 'CpuPeriod': 0, 'CpusetCpus': '', 'PidsLimit': None, **patch}}

    def test_one_cpu_and_one_point_five_gib_copy_exact_bytes(self):
        identity = s.benchmark_resource_identity(self.primary())
        self.assertEqual(s.benchmark_resource_args(identity),
            ['--cpus', '1', '--memory', '1610612736', '--memory-swap', '3221225472'])
        self.assertEqual(identity['PidsLimit'], None)
        self.assertEqual(identity['NanoCpus'], 1000000000)
        fractional = s.benchmark_resource_identity(self.primary(NanoCpus=1000000001))
        self.assertEqual(s.benchmark_resource_args(fractional)[:2], ['--cpus', '1.000000001'])

    def test_cpu_quota_affinity_pid_and_soft_limits_copy_without_nano_cpus(self):
        identity = s.benchmark_resource_identity(self.primary(NanoCpus=0, CpuQuota=50000,
            CpuPeriod=100000, CpuShares=512, CpusetCpus='0-1,3', CpusetMems='0', PidsLimit=256,
            MemorySwap=-1, MemoryReservation=536870912, MemorySwappiness=0,
            OomKillDisable=True, OomScoreAdj=100, Ulimits=[{'Name': 'nofile', 'Soft': 4096, 'Hard': 8192}]))
        flags = s.benchmark_resource_args(identity)
        self.assertNotIn('--cpus', flags)
        for flag, value in (('--cpu-quota', '50000'), ('--cpu-period', '100000'), ('--cpu-shares', '512'),
                            ('--cpuset-cpus', '0-1,3'), ('--cpuset-mems', '0'), ('--pids-limit', '256'),
                            ('--memory-swap', '-1'), ('--memory-reservation', '536870912'),
                            ('--memory-swappiness', '0'), ('--oom-score-adj', '100'), ('--ulimit', 'nofile=4096:8192')):
            self.assertEqual(flags[flags.index(flag) + 1], value)
        self.assertIn('--oom-kill-disable', flags)

    def test_unlimited_defaults_remain_unlimited(self):
        identity = s.benchmark_resource_identity({'HostConfig': {}})
        self.assertEqual(s.benchmark_resource_args(identity), [])
        s.guard_benchmark_resources({'HostConfig': {}}, {'HostConfig': {'OomKillDisable': None, 'Ulimits': []}}, identity)
        limits = s.benchmark_resource_identity({'HostConfig': {'CpuQuota': -1, 'PidsLimit': -1}})
        self.assertEqual(s.benchmark_resource_args(limits), ['--cpu-quota', '-1', '--pids-limit', '-1'])

    def test_invalid_or_uncopyable_controls_are_rejected_before_creation(self):
        invalid = [dict(NanoCpus=True), dict(CpuQuota=100000), dict(CpuPeriod=100000),
                   dict(NanoCpus=0, CpuPeriod=999), dict(Memory=-1), dict(MemorySwap=0),
                   dict(MemorySwap=1024), dict(Memory=0), dict(MemoryReservation=3221225472),
                   dict(CpusetCpus='0;--privileged'), dict(CpusetCpus='3-1'), dict(PidsLimit=True),
                   dict(MemorySwappiness=101), dict(CpuRealtimeRuntime=1000), dict(BlkioWeight=500),
                   dict(CgroupParent='/limited'), dict(DeviceRequests=[{'Count': -1}]),
                   dict(Ulimits=[{'Name': 'nofile', 'Soft': 8192, 'Hard': 4096}])]
        for patch in invalid:
            with self.subTest(patch=patch), self.assertRaises(RuntimeError):
                s.benchmark_resource_identity(self.primary(**patch))

    def test_drift_on_either_container_invalidates_the_comparison(self):
        original = self.primary()
        identity = s.benchmark_resource_identity(original)
        s.guard_benchmark_resources(original, copy.deepcopy(original), identity)
        for side in ('primary', 'baseline'):
            for patch in (dict(NanoCpus=2000000000), dict(MemorySwap=-1), dict(PidsLimit=128), dict(CpusetCpus='1')):
                primary, baseline = copy.deepcopy(original), copy.deepcopy(original)
                (primary if side == 'primary' else baseline)['HostConfig'].update(patch)
                with self.subTest(side=side, patch=patch), self.assertRaises(RuntimeError):
                    s.guard_benchmark_resources(primary, baseline, identity)
        with self.assertRaises(RuntimeError): s.guard_benchmark_resources(original, original, None)

    def test_start_baseline_passes_resources_and_guards_captured_identity(self):
        primary = self.primary()
        baseline = copy.deepcopy(primary)
        baseline['Id'] = 'baseline-id'
        baseline['HostConfig']['NetworkMode'] = 'container:primary-id'
        commands, saved = [], []
        def command(argv, **options):
            commands.append(argv)
            if argv[:3] == ['docker', 'image', 'inspect']:
                return json.dumps([{'Config': {'Labels': {'org.opencontainers.image.revision': 'b' * 40}}}])
            if argv[:2] == ['docker', 'inspect']: return json.dumps([baseline])
            if argv[:2] == ['docker', 'run']: return 'baseline-id'
            if argv[:3] == ['docker', 'volume', 'create']: return 'owned-volume'
            if argv[0] == 'nsenter': return '[200,{}]'
            raise AssertionError(argv)
        stage = SimpleNamespace(PROJECT='owned-stage', command=command,
            private_json=lambda path, payload: saved.append(copy.deepcopy(payload)))
        cache = SimpleNamespace(fixture_type=lambda _: object, b=SimpleNamespace(stage=stage),
            assert_replica_port_available=Mock(), replica_env=lambda *a: {'SERVER_PORT': '18520'},
            container_owned=lambda *a: True, NETNS_REQUEST='offline-only')
        fixture = s.fixture_type(cache, None)()
        fixture.guard, fixture.inspect = Mock(), lambda: [primary]
        fixture.args = SimpleNamespace(baseline_image='immutable-image', baseline_revision='b' * 40, baseline_port=18520)
        fixture.suffix, fixture.snapshot, fixture.expected_identity = 'unit', {}, ('primary-id',)
        fixture.wait_until = lambda name, predicate, seconds: self.assertTrue(predicate())
        with tempfile.TemporaryDirectory() as directory, patch.object(s.subprocess, 'run', return_value=SimpleNamespace(returncode=1)):
            fixture.private = Path(directory)
            fixture.start_baseline()
        argv = next(argv for argv in commands if argv[:2] == ['docker', 'run'])
        self.assertEqual(argv[-7:], ['--cpus', '1', '--memory', '1610612736', '--memory-swap', '3221225472', 'immutable-image'])
        record = fixture.snapshot['recovery_baseline']
        self.assertEqual(record['resource_identity'], s.benchmark_resource_identity(primary))
        self.assertEqual(record['primary_id'], 'primary-id')
        self.assertEqual(fixture.guard_baseline(), 55)
        primary['HostConfig']['NanoCpus'] = 2000000000
        with self.assertRaises(RuntimeError): fixture.guard_baseline()
        self.assertTrue(saved)

    def test_batch_end_drift_preserves_completed_samples_and_refuses_acceptance(self):
        saved = []
        cache = SimpleNamespace(fixture_type=lambda _: object, b=SimpleNamespace(stage=SimpleNamespace(
            private_json=lambda path, payload: saved.append(copy.deepcopy(payload)))))
        fixture = s.fixture_type(cache, None)()
        fixture.start_baseline = lambda: None
        fixture.new_case = lambda label: {'key': {'key': 'synthetic-token'}}
        fixture.snapshot = {'recovery_baseline': {'resource_identity': s.benchmark_resource_identity(self.primary())}}
        fixture.private = Path('/offline-private')
        fixture.args = SimpleNamespace(baseline_image='old-image', baseline_revision='b' * 40,
            expected_image='new-image', expected_revision='a' * 40, baseline_port=18520)
        fixture.guard_baseline = Mock(side_effect=[55, RuntimeError('resource drift')])
        fixture.check = Mock()
        batch = {'elapsed_seconds': 10, 'latencies_seconds': [.01] * 1000,
            'response_statuses': [200] * 1000, 'failed_responses': 0, 'warmup_failed': 0}
        with patch.object(s, 'benchmark_client', return_value=batch) as client:
            with self.assertRaisesRegex(RuntimeError, 'resource drift'): fixture.performance_checks()
        self.assertEqual(client.call_count, 1)
        self.assertEqual(len(saved[-1]['batches'][0]['latencies_seconds']), 1000)
        self.assertIn('not accepted', saved[-1]['batch_progress'][0]['state'])
        self.assertNotIn('summary', saved[-1])
        fixture.check.assert_not_called()


class PerformanceTests(unittest.TestCase):
    def test_batch_client_uses1200seconds_and_does_not_retry_failed_batch(self):
        with patch.object(s, 'private_client', return_value='{"failed_responses":0}') as client:
            self.assertEqual(s.benchmark_client(['synthetic-client'], {'model': s.MODEL}), {'failed_responses': 0})
        client.assert_called_once_with(['synthetic-client'], {'model': s.MODEL}, 1200)
        with patch.object(s, 'private_client', side_effect=RuntimeError('timeout')) as client:
            with self.assertRaises(RuntimeError): s.benchmark_client(['synthetic-client'], {})
        self.assertEqual(client.call_count, 1)

    def batches(self):
        return [{'mode': mode, 'elapsed_seconds': 10, 'latencies_seconds': [.01] * 1000,
                 'response_statuses': [200] * 1000, 'failed_responses': 0, 'warmup_failed': 0}
                for mode in ('baseline', 'candidate', 'candidate', 'baseline')]

    def test_embedded_clients_compile_and_measure_all_samples_without_network(self):
        compile(s.STREAM_REQUEST, '<stream-client>', 'exec')
        body = s.anthropic_sse(s.MODEL, 'healthy')
        class Response:
            status = 200
            def __init__(self): self.stream = io.BytesIO(body)
            def __enter__(self): return self
            def __exit__(self, *args): pass
            def readline(self, limit): return self.stream.readline(limit)
            def read(self, limit): return self.stream.read(limit)
        opener = SimpleNamespace(open=Mock(side_effect=lambda *a, **kw: Response()))
        output = io.StringIO()
        payload = {'url': 'http://127.0.0.1:18520/v1/messages', 'token': 'synthetic-unit-token', 'model': s.MODEL}
        with tempfile.TemporaryDirectory() as directory:
            payload['journal'] = str(Path(directory) / 'batch.jsonl')
            with patch('sys.stdin', io.StringIO(json.dumps(payload))), patch('sys.stdout', output), patch('urllib.request.build_opener', return_value=opener):
                exec(compile(s.BENCH_REQUEST, '<benchmark-client>', 'exec'), {})
            journal = Path(payload['journal'])
            rows = [json.loads(line) for line in journal.read_text().splitlines()]
            self.assertEqual(journal.stat().st_mode & 0o777, 0o600)
            self.assertEqual(len(rows), 1020)
            self.assertEqual({r['index'] for r in rows if r['phase'] == 'measured'}, set(range(1000)))
            self.assertEqual(sum(r['phase'] == 'warmup' for r in rows), 20)
            self.assertTrue(all(r['status'] == 200 and r['ok'] for r in rows))
            self.assertTrue(all(0 <= r['first_byte_seconds'] <= r['terminal_seconds'] <= r['latency'] for r in rows))
            self.assertTrue(all(r['tail_seconds'] >= 0 for r in rows))
            self.assertNotIn('synthetic-unit-token', journal.read_text())
        result = json.loads(output.getvalue())
        self.assertEqual(opener.open.call_count, 1020)
        self.assertEqual(len(result['latencies_seconds']), 1000)
        self.assertEqual(result['response_statuses'], [200] * 1000)
        self.assertEqual(result['failed_responses'], 0)
        self.assertEqual(result['warmup_failed'], 0)

    def test_benchmark_keeps_reading_errors_after_terminal_event(self):
        body = s.anthropic_sse(s.MODEL, 'healthy') + b'event: error\ndata: {"type":"error","error":{"type":"api_error"}}\n\n'
        class Response:
            status = 200
            def __init__(self): self.stream = io.BytesIO(body)
            def __enter__(self): return self
            def __exit__(self, *args): pass
            def readline(self, limit): return self.stream.readline(limit)
            def read(self, limit): return self.stream.read(limit)
        opener = SimpleNamespace(open=Mock(side_effect=lambda *a, **kw: Response()))
        output = io.StringIO()
        payload = {'url':'http://127.0.0.1:18520/v1/messages','token':'synthetic-only','model':s.MODEL}
        with patch('sys.stdin', io.StringIO(json.dumps(payload))), patch('sys.stdout',output), patch('urllib.request.build_opener',return_value=opener):
            exec(compile(s.BENCH_REQUEST,'<benchmark-client>','exec'),{})
        result = json.loads(output.getvalue())
        self.assertEqual(result['failed_responses'],1000)
        self.assertEqual(result['warmup_failed'],20)

    def test_exact_four_batches_prove_old_image_comparison(self):
        summary = s.performance_summary(self.batches())
        self.assertTrue(summary['passed'])
        self.assertEqual(summary['modes']['baseline']['samples'], 2000)
        self.assertEqual(summary['throughput_ratio'], 1)

    def test_failures_outliers_missing_samples_and_slowdown_cannot_be_hidden(self):
        cases = []
        rows = self.batches(); rows[1]['response_statuses'][0] = 503; cases.append(rows)
        rows = self.batches(); rows[1]['latencies_seconds'] = [.03] * 1000; cases.append(rows)
        rows = self.batches(); rows[1]['elapsed_seconds'] = 15; cases.append(rows)
        rows = self.batches(); rows[0]['latencies_seconds'].pop(); cases.append(rows)
        rows = self.batches(); rows[0]['warmup_failed'] = 1; cases.append(rows)
        rows = self.batches(); rows[0]['latencies_seconds'][0] = float('nan'); cases.append(rows)
        for rows in cases: self.assertFalse(s.performance_summary(rows)['passed'])


class OracleTests(unittest.TestCase):
    def test_catalog_matrix_exercises_inactive_messages_and_error_responses(self):
        fixture = s.fixture_type(SimpleNamespace(fixture_type=lambda _: object), None)()
        cases, calls, checks = [], [], []
        def new_case(label, **options):
            self.assertEqual(options, {'declare_allowlist': False})
            case = {'label': label, 'account': {'id': len(cases) + 1}}
            cases.append(case)
            return case
        fixture.new_case, fixture.admin = new_case, 'synthetic-admin'
        fixture.request = Mock(return_value=[200, {}])
        fixture.counts = lambda label: {'inference': int(label in calls), 'probes': int(label in calls)}
        fixture.account_state = lambda case: {'status': 'active' if case['label'] in calls else case['label'][8:],
                                               'runtime_state': {'state': 'enabled'}}
        def call(case, protocol):
            calls.append(case['label'])
            if protocol == 'messages': return [200, s.anthropic_sse(s.MODEL, 'healthy').decode()]
            self.assertEqual(protocol, 'responses')
            return [200, 'event: response.completed\ndata: {"type":"response.completed"}\n\n']
        fixture.call = call
        fixture.wait_usage = lambda case, n: {'n': n, 'wrong_group': 0, 'cost': .01}
        fixture.check = lambda name, passed, detail: checks.append((name, passed, detail))
        fixture.recoverable_catalog_checks()
        self.assertEqual(calls, ['catalog-inactive', 'catalog-error'])
        self.assertEqual([c.args[1] for c in fixture.request.call_args_list], [{'status': 'inactive'}, {'status': 'error'}])
        self.assertTrue(all(c.args[3] == 'PUT' for c in fixture.request.call_args_list))
        self.assertEqual(len(checks), 6)
        self.assertTrue(all(passed for _, passed, _ in checks))

    def test_response_detail_reports_status_and_terminals_without_payload(self):
        response = [403, {'error': {'message': 'private-message', 'code': 'MODEL_NOT_ALLOWED'}}]
        self.assertEqual(s.response_detail(response), {'status': 403, 'event_types': []})
        stream = [200, 'event: error\ndata: {"type":"error","error":{"message":"private-message"}}\n\n']
        self.assertEqual(s.response_detail(stream), {'status': 200, 'event_types': ['error']})
        self.assertNotIn('private-message', json.dumps(s.response_detail(stream)))

    def mock_fixture(self):
        class BaseHandler:
            def do_POST(self): self.fallback = True
        class BaseFixture:
            def start_mock(self): self.server = SimpleNamespace(RequestHandlerClass=BaseHandler)
        fixture = s.fixture_type(SimpleNamespace(fixture_type=lambda _: BaseFixture), None)()
        fixture.lock = threading.Lock()
        fixture.start_mock()
        return fixture

    def post_mock(self, fixture, path, payload):
        handler = fixture.server.RequestHandlerClass.__new__(fixture.server.RequestHandlerClass)
        raw = json.dumps(payload).encode()
        handler.path = path
        handler.rfile, handler.wfile = io.BytesIO(raw), io.BytesIO()
        handler.headers = {'Content-Length': str(len(raw))}
        handler.send_response, handler.send_header, handler.end_headers, handler.reply = Mock(), Mock(), Mock(), Mock()
        handler.do_POST()
        return handler

    def test_anthropic_beta_query_routes_inference_and_probe_to_owned_mock(self):
        fixture = self.mock_fixture()
        for target in ('/recovery/tool-messages/v1/messages?beta=true',
                       '/recovery/tool-messages/messages?beta=true&unused=1'):
            handler = self.post_mock(fixture, target, {'model': s.MODEL, 'stream': True,
                'max_tokens': 16, 'messages': [{'role': 'user', 'content': 'fixture_empty_delta'}]})
            self.assertFalse(getattr(handler, 'fallback', False))
            handler.send_response.assert_called_once_with(200)
            events = s.sse_events(handler.wfile.getvalue().decode())
            self.assertEqual(next(e['delta']['partial_json'] for e in events if e['type'] == 'content_block_delta'), '')
        handler = self.post_mock(fixture, '/recovery/tool-messages/v1/messages?beta=true',
            {'model': s.MODEL, 'max_tokens': 64, 'messages': [{'role': 'user', 'content': 'Say OK.'}]})
        self.assertFalse(getattr(handler, 'fallback', False))
        self.assertEqual(handler.reply.call_args.args[1], 200)
        self.assertEqual([e['probe'] for e in fixture.recovery_events], [False, False, True])
        self.assertEqual(fixture.recovery_unmatched_paths, {})

    def test_unmatched_routes_preserve_bounded_path_evidence_without_query_values(self):
        fixture = self.mock_fixture()
        for _ in range(2):
            handler = self.post_mock(fixture, '/recovery/tool-messages/v1/wrong?api_key=synthetic-private-value', {})
            self.assertTrue(handler.fallback)
        self.assertEqual(fixture.recovery_unmatched_paths, {'/recovery/tool-messages/v1/wrong': 2})
        for i in range(100): self.post_mock(fixture, '/wrong/' + str(i), {})
        self.assertLessEqual(len(fixture.recovery_unmatched_paths), 65)
        self.assertNotIn('synthetic-private-value', json.dumps(fixture.recovery_unmatched_paths))
        self.assertEqual(fixture.recovery_events, [])

    def test_empty_delta_fixture_is_valid_and_bad_fixture_is_actually_bad(self):
        events = s.sse_events(s.anthropic_sse(s.MODEL, 'empty_delta').decode())
        block = next(e['content_block'] for e in events if e['type'] == 'content_block_start')
        delta = next(e['delta']['partial_json'] for e in events if e['type'] == 'content_block_delta')
        self.assertEqual(block['input'], {})
        self.assertEqual(delta, '')
        bad = s.sse_events(s.anthropic_sse(s.MODEL, 'bad_tool').decode())
        delta = next(e['delta']['partial_json'] for e in bad if e['type'] == 'content_block_delta')
        with self.assertRaises(json.JSONDecodeError): json.loads(delta)

    def test_completion_requires_protocol_terminal_and_no_error(self):
        body = s.anthropic_sse(s.MODEL, 'healthy').decode()
        self.assertTrue(s.completed('messages', [200, body]))
        self.assertFalse(s.completed('messages', [502, body]))
        self.assertFalse(s.completed('responses', [200, body]))
        self.assertFalse(s.completed('chat/completions', [200, 'data: [DONE]\n\n']))
        for protocol in s.PROTOCOLS:
            self.assertFalse(s.completed(protocol, [200, body + 'event: error\ndata: {"type":"error","error":{"message":"failed"}}\n\n']))
        self.assertTrue(s.completed('responses', [200, 'event: response.completed\ndata: {"type":"response.completed"}\n\n']))
        self.assertTrue(s.completed('chat/completions', [200, 'data: {"choices":[{"finish_reason":"tool_calls"}]}\n\ndata: [DONE]\n\n']))

    def test_external_cloudflare_is_unverified_but_mock_matrix_has_no_missing_gate(self):
        self.assertIn('actual Cloudflare edge timeout', s.EXTERNAL_UNVERIFIED)
        self.assertEqual(s.REMAINING, [])

    def test_30second_foreground_deadline_is_separate_from_45second_heartbeat(self):
        failed = [503, {'error': {'message': 'unavailable'}}, {'elapsed': 30.2, 'heartbeats': []}]
        self.assertTrue(s.foreground_wait_ok(failed))
        self.assertFalse(s.foreground_wait_ok([503, '', {'elapsed': 2, 'heartbeats': []}]))
        self.assertFalse(s.foreground_wait_ok([503, '', {'elapsed': 44, 'heartbeats': [40]}]))
        body = s.anthropic_sse(s.MODEL, 'healthy').decode()
        delayed = [200, body, {'elapsed': 55.2, 'heartbeats': [45.1]}]
        self.assertTrue(s.heartbeat_wait_ok(delayed))
        self.assertFalse(s.foreground_wait_ok(delayed))
        for timing in ({'elapsed': 55, 'heartbeats': []}, {'elapsed': 55, 'heartbeats': [54]},
                       {'elapsed': 2, 'heartbeats': [1]}):
            self.assertFalse(s.heartbeat_wait_ok([200, body, timing]))


class RestorationTests(unittest.TestCase):
    def fixture(self, name='Recovery A suffix', kind='account-recovery-stage-v1', keys=None):
        class Base:
            def restore(self): self.log.append('base_restore')
        writes = []
        cache = SimpleNamespace(fixture_type=lambda _: Base,
                 b=SimpleNamespace(stage=SimpleNamespace(private_json=lambda *a: writes.append(a))))
        cls = s.fixture_type(cache, None)
        f = cls()
        f.stage = cache.b.stage
        f.log = []
        f.snapshot = {'recovery_runner_kind': kind, 'recovery_account_names': {'13': 'Recovery A suffix'}}
        f.fixture_ids = {'accounts': [13], 'users': [77], 'keys': [31] if keys is None else keys}
        f.private = Path('/offline-private')
        f.guard = lambda: f.log.append('guard')
        f.cleanup_replica = lambda: f.log.append('replica_removed')
        f.cleanup_baseline = lambda: f.log.append('baseline_removed')
        f.cleanup_balance_latches = lambda ids: f.log.append(('exact_latches', ids))
        def sql(query):
            f.log.append(query)
            if 'SELECT json_build_object(\'id\'' in query: return [{'id': 13, 'name': name}]
            if 'SELECT json_build_object(\'n\'' in query: return [{'n': 0}]
            return []
        f.sql = sql
        return f

    def test_stops_keys_and_invalidates_grants_before_runtime_cleanup(self):
        f = self.fixture(); f.restore()
        updates = [i for i, q in enumerate(f.log) if isinstance(q, str) and q.startswith('UPDATE')]
        delete = next(i for i, q in enumerate(f.log) if isinstance(q, str) and 'DELETE FROM account_runtime_geili' in q)
        self.assertTrue(updates and max(updates) < delete)
        self.assertLess(f.log.index('replica_removed'), delete)
        self.assertEqual(f.log[-1], 'base_restore')
        self.assertTrue(f.snapshot['recovery_runtime_restored'])

    def test_redis_latch_cleanup_never_scans_or_exposes_password(self):
        f = self.fixture()
        f.stage.REDIS = 'owned-stage-redis'
        f.stage.validate_redis_environment = lambda env: self.assertEqual(env['REDIS_DB'], '0')
        f.inspect = lambda: [{'Config': {'Env': ['REDIS_HOST=redis', 'REDIS_PORT=6379', 'REDIS_DB=0', 'REDIS_PASSWORD=synthetic-private-secret']}}, {}]
        outputs = [SimpleNamespace(returncode=0, stdout=str(n)) for n in (1, 1, 0, 0)]
        with patch.object(s.subprocess, 'run', side_effect=outputs) as command:
            # Use the real cleanup implementation, not the generic restore stub.
            s.fixture_type(SimpleNamespace(fixture_type=lambda _: object, b=SimpleNamespace(stage=f.stage)), None).cleanup_balance_latches(f, [13])
        for call in command.call_args_list:
            argv = call.args[0]
            self.assertIn('geili:account:balance_latch:13', argv)
            self.assertNotIn('*', ' '.join(argv))
            self.assertNotIn('synthetic-private-secret', ' '.join(argv))
            self.assertNotIn('SCAN', argv)
            self.assertEqual(argv[argv.index('-n') + 1], '0')
        self.assertTrue(f.snapshot['recovery_balance_latches_restored'])
        self.assertNotIn('synthetic-private-secret', json.dumps(f.snapshot))

    def test_partial_setup_with_no_key_still_restores_owned_account(self):
        f = self.fixture(keys=[]); f.restore()
        self.assertTrue(f.snapshot['recovery_runtime_restored'])
        self.assertFalse(any(isinstance(q, str) and 'UPDATE api_keys' in q for q in f.log))

    def test_wrong_account_name_or_other_runner_blocks_all_mutations(self):
        for f in (self.fixture(name='unrelated user account'), self.fixture(kind='other-runner')):
            with self.assertRaises(RuntimeError): f.restore()
            self.assertFalse(any(isinstance(q, str) and q.startswith(('UPDATE', 'BEGIN;')) for q in f.log))


if __name__ == '__main__': unittest.main()
