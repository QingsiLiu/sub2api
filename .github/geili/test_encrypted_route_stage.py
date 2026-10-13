"""Local stdlib-only Stage runner tests. No Docker, SSH, database or paid calls."""
import importlib.util
from contextlib import contextmanager, nullcontext
import json
import re
from pathlib import Path
import tempfile
import types
import unittest
from unittest.mock import Mock, patch

spec = importlib.util.spec_from_file_location('encrypted_route_stage', Path(__file__).with_name('encrypted-route-stage.py'))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


def literal(value):
    return "'" + str(value).replace("'", "''") + "'"


class FakeBase:
    def prepare(self):
        self.snapshot = {'baseline': {}, 'fixture_ids': self.fixture_ids}

    def restore(self):
        self.base_restored = True
        self.snapshot['restored'] = True


class StageRunnerTests(unittest.TestCase):
    def make_fixture(self):
        cache = types.SimpleNamespace(fixture_type=lambda _: FakeBase,
            b=types.SimpleNamespace(stage=types.SimpleNamespace(literal=literal, private_json=Mock(),
                validate_redis_environment=Mock(), REDIS='synthetic-stage-redis')))
        fixture = runner.fixture_type(cache, object())()
        fixture.private = Path(self.enterContext(tempfile.TemporaryDirectory()))
        fixture.fixture_ids = {'users': [12345]}
        fixture.guard = Mock()
        fixture.check = Mock()
        fixture.owner = {'id': 12345}
        fixture.sql = Mock(return_value=[{'n': 0}])
        return fixture, cache

    def fake_lock_process(self):
        return types.SimpleNamespace(stdin=Mock(), stdout=Mock(readline=Mock(return_value='GEILI_SOURCE_LOCK_READY\n')),
            poll=Mock(return_value=None), communicate=Mock(return_value=('', '')), kill=Mock(), returncode=0)

    def test_source_lock_is_bounded_and_committed_even_when_request_fails(self):
        for fail in (False, True):
            proc = self.fake_lock_process()
            stdin = proc.stdin
            with patch.object(runner.subprocess, 'Popen', return_value=proc), patch.object(runner.select, 'select', return_value=([proc.stdout], [], [])), patch.object(runner.os, 'read', return_value=b'GEILI_SOURCE_LOCK_READY\n'):
                if fail:
                    with self.assertRaisesRegex(ValueError, 'request failed'):
                        with runner.bounded_source_lock(['synthetic-psql']):
                            raise ValueError('request failed')
                else:
                    with runner.bounded_source_lock(['synthetic-psql']):
                        pass
            commands = ''.join(c.args[0] for c in stdin.write.call_args_list)
            for text in ("lock_timeout='1s'", "statement_timeout='1s'", "idle_in_transaction_session_timeout='3s'",
                         'LOCK TABLE ONLY openai_encrypted_sources_geili IN ACCESS EXCLUSIVE MODE', 'COMMIT;'):
                self.assertIn(text, commands)
            self.assertNotIn('ALTER', commands)
            proc.communicate.assert_called_once_with(timeout=2)
            proc.kill.assert_not_called()
            stdin.close.assert_called_once()

    def test_source_lock_readiness_and_release_deadlines_fail_closed(self):
        proc = self.fake_lock_process()
        stdin = proc.stdin
        with patch.object(runner.subprocess, 'Popen', return_value=proc), patch.object(runner.select, 'select', return_value=([], [], [])):
            with self.assertRaisesRegex(RuntimeError, 'readiness deadline'):
                with runner.bounded_source_lock(['synthetic-psql']):
                    self.fail('must not issue fault request without lock readiness')
        self.assertIn('COMMIT;', stdin.write.call_args.args[0])
        proc = self.fake_lock_process()
        proc.communicate.side_effect = [runner.subprocess.TimeoutExpired('synthetic', 2), ('', '')]
        with patch.object(runner.subprocess, 'Popen', return_value=proc), patch.object(runner.select, 'select', return_value=([proc.stdout], [], [])), patch.object(runner.os, 'read', return_value=b'GEILI_SOURCE_LOCK_READY\n'):
            with self.assertRaisesRegex(RuntimeError, 'release deadline'):
                with runner.bounded_source_lock(['synthetic-psql']):
                    pass
        proc.kill.assert_called_once()
        self.assertEqual([2, 1], [c.kwargs['timeout'] for c in proc.communicate.call_args_list])

    def configure_fault_fixture(self):
        f, cache = self.make_fixture()
        self.configure_redis_fixture(f)
        cache.b.stage.PG = 'synthetic-owned-pg'
        cache.b.stage.DATABASE = 'synthetic-owned-db'
        f.suffix = 'fault-fixture'
        f.groups = {'A': {'id': 42}}
        f.cross_key = {'id': 345, 'key': 'private-token'}
        f.sql.side_effect = [[{'n': 2, 'owned': 2}], [{'user_id': 12345, 'cipher_digest': 'a' * 64}]]
        f.inference_count = Mock(side_effect=[5, 5, 6])
        f.encrypted_attempts = [{'label': 'A'}]
        f.guard_replica = Mock(side_effect=f.guard)
        f.replica_primary_pid = 123
        f.verified_replica_request = Mock(return_value=(503, {'error': {'code': 'encrypted_session_verification_unavailable', 'private': 'must-not-record'}}, .051))
        f.call_encrypted = Mock(return_value=(200, {}))
        f.audit_usage = Mock(side_effect=[{'n': 1, 'wrong_group': 0, 'multiplier': 1}, {'n': 2, 'wrong_group': 0, 'multiplier': 1}])
        return f, cache

    def test_fault_matrix_requires_actual_hint_then_zero_calls_and_exact_resume_billing(self):
        f, _ = self.configure_fault_fixture()
        with patch.object(runner.subprocess, 'run', return_value=types.SimpleNamespace(returncode=0, stdout='1\n')) as redis, patch.object(runner, 'bounded_source_lock', return_value=nullcontext()) as holder:
            f.verification_fault_check([{'type': 'encrypted_content', 'encrypted_content': 'synthetic'}])
        self.assertIn('EXISTS', redis.call_args.args[0])
        self.assertNotIn('SET', redis.call_args.args[0])
        self.assertEqual(1, redis.call_count)
        self.assertIn('-XqAt', holder.call_args.args[0])
        self.assertEqual([1, 2], [c.args[1] for c in f.audit_usage.call_args_list])
        f.verified_replica_request.assert_called_once_with([{'type': 'encrypted_content', 'encrypted_content': 'synthetic'}], 'verification-fault', 123)
        self.assertEqual(['verification-resumed'], [c.args[2] for c in f.call_encrypted.call_args_list])
        failure = f.check.call_args_list[1]
        self.assertEqual(503, failure.args[2]['status'])
        self.assertEqual('encrypted_session_verification_unavailable', failure.args[2]['error_code'])
        self.assertEqual('dict', failure.args[2]['body_type'])
        self.assertEqual(.051, failure.args[2]['http_elapsed_seconds'])
        self.assertEqual(0, failure.args[2]['inference_delta'])
        self.assertNotIn('must-not-record', str(f.check.call_args_list))
        self.assertEqual(3, f.guard.call_count)

    def test_fault_releases_lock_before_usage_audit_and_distinct_resume(self):
        f, _ = self.configure_fault_fixture()
        events = []
        @contextmanager
        def holder(_):
            events.append('lock')
            try:
                yield
            finally:
                events.append('release')
        f.verified_replica_request.side_effect = lambda *args: (events.append('fault') or (503, {'error': {'code': 'encrypted_session_verification_unavailable'}}, .05))
        def audit(*args):
            events.append('audit-' + str(args[1]))
            return {'n': args[1], 'wrong_group': 0, 'multiplier': 1}
        f.audit_usage.side_effect = audit
        f.call_encrypted.side_effect = lambda *args: (events.append('resume') or (200, {}))
        with patch.object(runner.subprocess, 'run', return_value=types.SimpleNamespace(returncode=0, stdout='1')), patch.object(runner, 'bounded_source_lock', side_effect=holder):
            f.verification_fault_check([])
        self.assertEqual(['lock', 'fault', 'release', 'audit-1', 'resume', 'audit-2'], events)

    def test_verified_replica_transport_uses_frozen_pid_without_in_lock_guard(self):
        f, cache = self.make_fixture()
        f.suffix = 'diagnostic'
        f.args = types.SimpleNamespace(replica_port=18517)
        f.cross_key = {'key': 'private-test-token'}
        f.guard_replica = Mock(side_effect=AssertionError('guard must run before the lock'))
        cache.NETNS_REQUEST = 'synthetic-netns-transport'
        cache.b.stage.command = Mock(return_value=json.dumps([503, 'JSON plus SSE body', .052]))
        response = f.verified_replica_request([], 'verification-fault', 123)
        self.assertEqual(503, response[0])
        self.assertEqual('str', runner.response_diagnostic(response)['body_type'])
        command = cache.b.stage.command.call_args.args[0]
        self.assertEqual(['nsenter', '-t', '123', '-n', 'python3', '-c', 'synthetic-netns-transport'], command)
        payload = json.loads(cache.b.stage.command.call_args.kwargs['input'])
        self.assertEqual('http://127.0.0.1:18517/v1/responses', payload['url'])
        self.assertFalse(payload['data']['stream'])
        self.assertEqual('private-test-token', payload['token'])
        f.guard_replica.assert_not_called()

    def test_response_diagnostics_are_bounded_shape_only(self):
        self.assertIsNone(runner.error_code((503, {'error': 'unstructured private message'})))
        self.assertIsNone(runner.error_code((503, {'error': None})))
        body = {'type': 'error', 'code': 'safe-code', 'message': 'never-output-message',
                'error': {'type': 'api_error', 'code': 'contains secret', 'message': 'never-output-nested'}}
        detail = runner.response_diagnostic((503, body, .052))
        self.assertEqual('error', detail['top_level_type'])
        self.assertEqual('safe-code', detail['top_level_code'])
        self.assertEqual('api_error', detail['error_type'])
        self.assertIsNone(detail['error_code'])
        self.assertNotIn('never-output', json.dumps(detail))
        self.assertNotIn('contains secret', json.dumps(detail))
        detail = runner.response_diagnostic((503, 'raw-json-and-SSE-secret', float('nan')))
        self.assertEqual('str', detail['body_type'])
        self.assertIsNone(detail['error_code'])
        self.assertIsNone(detail['http_elapsed_seconds'])
        self.assertNotIn('raw-json', json.dumps(detail))

    def test_fault_rejects_changed_verified_pid_after_lock_release(self):
        f, _ = self.configure_fault_fixture()
        def guard():
            f.replica_primary_pid += 1
        f.guard_replica.side_effect = guard
        with patch.object(runner.subprocess, 'run', return_value=types.SimpleNamespace(returncode=0, stdout='1')), patch.object(runner, 'bounded_source_lock', return_value=nullcontext()), self.assertRaisesRegex(RuntimeError, 'PID changed'):
            f.verification_fault_check([])
        f.call_encrypted.assert_not_called()

    def test_fault_refuses_unrelated_rows_or_missing_hint_without_retry(self):
        f, _ = self.configure_fault_fixture()
        f.sql.side_effect = [[{'n': 3, 'owned': 2}]]
        with patch.object(runner.subprocess, 'run') as redis, patch.object(runner, 'bounded_source_lock') as holder, self.assertRaisesRegex(RuntimeError, 'unrelated'):
            f.verification_fault_check([])
        redis.assert_not_called()
        holder.assert_not_called()
        f, _ = self.configure_fault_fixture()
        with patch.object(runner.subprocess, 'run', return_value=types.SimpleNamespace(returncode=0, stdout='0')) as redis, patch.object(runner, 'bounded_source_lock') as holder, self.assertRaisesRegex(RuntimeError, 'not ready'):
            f.verification_fault_check([])
        self.assertEqual(1, redis.call_count)
        holder.assert_not_called()
        f.call_encrypted.assert_not_called()

    def test_cold_policy_warmup_uses_both_processes_without_cipher_or_policy_mutation(self):
        f, _ = self.make_fixture()
        f.suffix = 'cold-fixture'
        f.seed_key = {'key': 'synthetic-seed-token', 'id': 101}
        f.cross_key = {'key': 'synthetic-cross-token', 'id': 102}
        f.request = Mock(return_value=(200, {'status': 'completed'}))
        f.replica_request = Mock(return_value=(200, {'status': 'completed'}))
        f.set_policy = Mock()
        f.audit_usage = Mock()
        with patch.object(runner.time, 'sleep') as sleep:
            f.warm_policy_processes()
        sleep.assert_called_once_with(1.1)
        for request in (f.request, f.replica_request):
            request.assert_called_once()
            endpoint, payload, key = request.call_args.args
            self.assertEqual('/v1/responses', endpoint)
            self.assertEqual('synthetic-seed-token', key)
            self.assertFalse(payload['stream'])
            self.assertNotIn('encrypted_content', json.dumps(payload))
            self.assertNotIn('synthetic-encrypted-', json.dumps(payload))
            self.assertNotIn('seed-tools', json.dumps(payload))
            self.assertNotIn('seed-reasoning', json.dumps(payload))
            self.assertNotIn('synthetic-cross-token', json.dumps(payload))
        self.assertNotEqual(f.request.call_args.args[1]['prompt_cache_key'],
                            f.replica_request.call_args.args[1]['prompt_cache_key'])
        f.set_policy.assert_not_called()
        f.sql.assert_not_called()
        f.audit_usage.assert_not_called()
        self.assertTrue(f.check.call_args.args[1])

    def test_cold_policy_warmup_failure_is_recorded_once_and_not_retried(self):
        f, _ = self.make_fixture()
        f.suffix = 'cold-fixture'
        f.seed_key = {'key': 'synthetic-seed-token'}
        f.request = Mock(return_value=(503, {'error': 'synthetic warmup failure'}))
        f.replica_request = Mock(return_value=(200, {}))
        with patch.object(runner.time, 'sleep'), self.assertRaisesRegex(RuntimeError, 'warmup failed'):
            f.warm_policy_processes()
        f.request.assert_called_once()
        f.replica_request.assert_called_once()
        self.assertFalse(f.check.call_args.args[1])

    def test_cold_policy_warmup_precedes_exact_source_seed_calls(self):
        f, _ = self.make_fixture()
        f.suffix = 'cold-fixture'
        f.seed_key = {'key': 'synthetic-seed-token'}
        events = []
        f.start_replica = Mock(side_effect=lambda: events.append('replica-start'))
        f.request = Mock(side_effect=lambda *args: (events.append('primary-warm') or (200, {})))
        f.replica_request = Mock(side_effect=lambda *args: (events.append('secondary-warm') or (200, {})))
        def seed(key, items, label):
            events.append(label)
            self.assertIs(f.seed_key, key)
            return 200, runner.synthetic_response({'input': items}, 'A', f.suffix)
        f.call_encrypted = Mock(side_effect=seed)
        f.sql.side_effect = RuntimeError('stop after both exact source seeds')
        with patch.object(runner.time, 'sleep', side_effect=lambda seconds: events.append(('wait', seconds))), \
                self.assertRaisesRegex(RuntimeError, 'both exact source seeds'):
            f.run_checks()
        self.assertEqual(['replica-start', 'primary-warm', 'secondary-warm', ('wait', 1.1),
                          'seed-tools', 'seed-reasoning'], events)
        self.assertEqual(2, f.call_encrypted.call_count)

    def test_default_off_and_reject_nonobject_malformed_settings(self):
        self.assertEqual('off', runner.settings_mode([]))
        self.assertEqual('off', runner.settings_mode([{'key': runner.SETTING, 'value': '{"mode":"off"}'}]))
        for raw in ('null', '[]', '{}', '{"mode":"typo"}'):
            with self.subTest(raw=raw), self.assertRaises(RuntimeError):
                runner.settings_mode([{'key': runner.SETTING, 'value': raw}])

    def test_exact_setting_restore_only_changes_owned_key(self):
        self.assertEqual("DELETE FROM settings WHERE key='openai_encrypted_route';", runner.restored_setting_sql([], literal))
        row = {'id': 12, 'key': runner.SETTING, 'value': '{"mode":"off","protected_user_ids":[3885]}',
               'updated_at': '2026-10-01T00:00:00Z'}
        query = runner.restored_setting_sql([row, {'key': 'secret', 'value': 'must-not-write'}], literal)
        self.assertIn('WHERE id=12', query)
        self.assertIn("updated_at='2026-10-01T00:00:00Z'", query)
        self.assertNotIn('created_at', query)
        self.assertNotIn('must-not-write', query)

    def test_snapshot_complete_before_rejecting_existing_protection(self):
        f, cache = self.make_fixture()
        f.sql.return_value = [{'key': runner.SETTING, 'value': '{"mode":"protect"}', 'secret': 'private-only'}]
        with self.assertRaises(RuntimeError):
            f.prepare()
        self.assertEqual('private-only', f.snapshot['encrypted_settings_snapshot'][0]['secret'])
        cache.b.stage.private_json.assert_called_once()

    def test_policy_protects_only_owned_synthetic_user(self):
        f, _ = self.make_fixture()
        with patch.object(runner.time, 'sleep') as wait:
            f.set_policy('protect')
        query = f.sql.call_args.args[0]
        self.assertIn('12345', query)
        self.assertNotIn('3885', query)
        self.assertIn('ON CONFLICT(key)', query)
        wait.assert_called_once_with(6)
        f.owner['id'] = 3885
        with self.assertRaises(RuntimeError):
            f.set_policy('protect')

    def test_settings_failure_still_runs_replica_and_base_cleanup(self):
        f, _ = self.make_fixture()
        f.snapshot = {'encrypted_settings_snapshot': []}
        f.sql.side_effect = RuntimeError('synthetic restoration failure')
        with self.assertRaises(RuntimeError):
            f.restore()
        self.assertTrue(f.base_restored)
        self.assertFalse(f.snapshot['restored'])

    def test_cleanup_metadata_exact_user_ids_and_settings_restored(self):
        f, _ = self.make_fixture()
        f.snapshot = {'encrypted_settings_snapshot': []}
        f.sql.side_effect = [[], [], [], [], [{'n': 0}]]
        with patch.object(runner.time, 'sleep'):
            f.restore()
        queries = [c.args[0] for c in f.sql.call_args_list]
        self.assertIn('DELETE FROM openai_encrypted_sources_geili WHERE user_id IN (12345);', queries)
        self.assertTrue(f.snapshot['encrypted_settings_restored'])
        self.assertTrue(f.base_restored)

    def test_default_main_readonly_exits_before_lease_or_fixture_mutations(self):
        import argparse
        parser = argparse.ArgumentParser()
        parser.add_argument('--replica-port', type=int, default=18517)
        parser.add_argument('--mock-port', type=int, default=18515)
        def validate(args):
            args.dry_run = True
            args.source_root = '/synthetic-source'
        cache = types.SimpleNamespace(valid_replica_port=lambda *_: True,
            b=types.SimpleNamespace(candidate_args=lambda: argparse.ArgumentParser(),
                validate_args=validate, stage=types.SimpleNamespace(load_source=lambda _: object())))
        fixture = Mock()
        fixture.execute_billing.return_value = True
        with patch.object(runner, 'load_ops', return_value=cache), patch.object(runner, 'fixture_type', return_value=lambda _: fixture), patch('sys.argv', ['encrypted-route-stage.py', '--ops-bin', '/fake-ops']):
            # Main adds replica-port itself; mock-port normally comes from canonical args.
            cache.valid_replica_port = lambda *_: True
            original_validate = cache.b.validate_args
            def with_mock_port(args):
                original_validate(args)
                args.mock_port = 18515
            cache.b.validate_args = with_mock_port
            self.assertEqual(0, runner.main())
        fixture.execute_billing.assert_called_once()
        fixture.prepare.assert_not_called()
        fixture.setup.assert_not_called()
        fixture.set_policy.assert_not_called()

    def test_settings_sql_columns_match_repository_schema(self):
        schema = (Path(__file__).resolve().parents[2] / 'backend/migrations/005_schema_parity.sql').read_text()
        table = re.search(r'CREATE TABLE IF NOT EXISTS settings\s*\((.*?)\);', schema, re.S).group(1)
        columns = set(re.findall(r'^\s*(\w+)\s+(?:BIGSERIAL|VARCHAR|TEXT|TIMESTAMPTZ)', table, re.M))
        self.assertEqual({'id', 'key', 'value', 'updated_at'}, columns)
        f, _ = self.make_fixture()
        with patch.object(runner.time, 'sleep'):
            f.set_policy('protect')
        query = f.sql.call_args.args[0]
        written = set(re.search(r'INSERT INTO settings\((.*?)\)', query).group(1).split(','))
        self.assertTrue(written <= columns)
        self.assertNotIn('created_at', query)
        row = {'id': 123, 'key': runner.SETTING, 'value': '{"mode":"off"}', 'updated_at': '2026-10-01T00:00:00Z'}
        self.assertNotIn('created_at', runner.restored_setting_sql([row], literal))

    def test_restoration_detects_unexpected_setting_drift_and_still_cleans_metadata(self):
        f, _ = self.make_fixture()
        f.snapshot = {'encrypted_settings_snapshot': []}
        f.sql.side_effect = [[], [{'id': 42, 'key': 'unexpected', 'value': 'changed', 'updated_at': 'now'}], [], [], [{'n': 0}]]
        def check(_, passed, *args):
            if not passed:
                raise AssertionError('unexpected settings drift')
        f.check.side_effect = check
        with patch.object(runner.time, 'sleep'), self.assertRaises(RuntimeError):
            f.restore()
        self.assertFalse(f.snapshot['encrypted_settings_restored'])
        self.assertTrue(f.base_restored)
        self.assertTrue(any('DELETE FROM openai_encrypted_sources_geili' in c.args[0] for c in f.sql.call_args_list))

    def perf_batches(self, latency_off=.010, latency_protect=.011, elapsed_off=1, elapsed_protect=1):
        return [{'mode': mode, 'round': i, 'latencies_seconds': [latency_off if mode == 'off' else latency_protect] * 300,
                 'elapsed_seconds': elapsed_off if mode == 'off' else elapsed_protect, 'failed_responses': 0}
                for i in range(2) for mode in ('off', 'protect')]

    def test_performance_aggregation_raw_samples_and_thresholds(self):
        summary = runner.performance_summary(self.perf_batches())
        self.assertTrue(summary['passed'])
        self.assertEqual(600, summary['modes']['off']['samples'])
        self.assertAlmostEqual(1, summary['p95_delta_ms'])
        self.assertAlmostEqual(300, summary['modes']['protect']['throughput_per_second'])
        self.assertFalse(runner.performance_summary(self.perf_batches(latency_protect=.021))['passed'])
        self.assertFalse(runner.performance_summary(self.perf_batches(elapsed_protect=1.03))['passed'])
        batches = self.perf_batches()
        batches[0]['failed_responses'] = 1
        self.assertFalse(runner.performance_summary(batches)['passed'])

    def test_performance_sampler_uses_primary_four_interleaved_batches_and_separate_key(self):
        f, cache = self.make_fixture()
        f.args = types.SimpleNamespace(performance_samples=300, expected_revision='synthetic-revision', expected_image='sha256:synthetic')
        f.groups = {'A': {'id': 42}}
        f.suffix = 'synthetic-perf'
        key = {'id': 345, 'key': 'synthetic-token-never-recorded'}
        f.new_key = Mock(return_value=key)
        f.set_policy = Mock()
        f.request = Mock(return_value=[200, {'status': 'completed', 'output': [{'text': 'acceptance ok A'}]}, {}])
        f.replica_request = Mock(side_effect=AssertionError('performance must not use netns subprocess'))
        with patch.object(runner, 'performance_summary', return_value={'passed': True}):
            f.sample_performance()
        f.new_key.assert_called_once_with('performance-only', [42])
        self.assertEqual(['off', 'protect', 'protect', 'off'], [c.args[0] for c in f.set_policy.call_args_list])
        self.assertEqual(1280, f.request.call_count)
        f.replica_request.assert_not_called()
        self.assertEqual([300] * 4, [len(b['latencies_seconds']) for b in f.performance['batches']])
        self.assertTrue(all(b['concurrency'] == 4 and b['warmup_requests'] == 20 for b in f.performance['batches']))
        saved = cache.b.stage.private_json.call_args.args[1]
        self.assertEqual('synthetic-revision', saved['revision'])
        self.assertNotIn('synthetic-token-never-recorded', json.dumps(saved))

    def test_performance_empty_zero_and_invalid_samples_fail_closed(self):
        self.assertFalse(runner.performance_summary([])['passed'])
        self.assertFalse(runner.performance_summary(self.perf_batches(elapsed_off=0))['passed'])
        self.assertFalse(runner.performance_summary(self.perf_batches(latency_off=0))['passed'])
        self.assertFalse(runner.performance_summary(self.perf_batches(latency_protect=float('nan')))['passed'])
        batches = self.perf_batches()
        batches[0]['latencies_seconds'].pop()
        self.assertFalse(runner.performance_summary(batches)['passed'])

    def test_source_cache_exact_prefix_and_user_ownership(self):
        digest = 'a' * 64
        rows = [{'user_id': 12345, 'cipher_digest': digest}]
        self.assertEqual(['openai_encrypted_route_geili:v2:12345:' + digest], runner.source_cache_keys(rows * 2, [12345]))
        for bad in [{'user_id': 3885, 'cipher_digest': digest}, {'user_id': 12345, 'cipher_digest': '*'},
                    {'user_id': 12345, 'cipher_digest': 'A' * 64}]:
            with self.assertRaises(RuntimeError):
                runner.source_cache_keys([bad], [12345])

    def configure_redis_fixture(self, f):
        f.inspect = Mock(return_value=[{'Config': {'Env': ['REDIS_PASSWORD=synthetic-private-password', 'REDIS_DB=0']}}])

    def test_source_cache_manifest_precedes_deletes_and_auth_not_in_arguments(self):
        f, cache = self.make_fixture()
        self.configure_redis_fixture(f)
        rows = [{'user_id': 12345, 'cipher_digest': 'a' * 64}]
        f.snapshot = {}
        f.sql.return_value = rows
        def command(args, **kwargs):
            self.assertTrue(f.cache_manifest_ready)
            self.assertEqual(rows, f.snapshot['encrypted_redis_sources'])
            self.assertNotIn('synthetic-private-password', ' '.join(args))
            self.assertEqual('synthetic-private-password', kwargs['env']['REDISCLI_AUTH'])
            self.assertIn('openai_encrypted_route_geili:v2:12345:' + 'a' * 64, args)
            self.assertEqual('0', args[args.index('-n') + 1])
            return types.SimpleNamespace(returncode=0, stdout='1' if 'DEL' in args else '0')
        with patch.object(runner.subprocess, 'run', side_effect=command) as calls:
            f.cleanup_source_cache()
        self.assertEqual(2, calls.call_count)
        self.assertTrue(f.snapshot['encrypted_redis_restored'])

    def test_source_cache_absent_or_empty_auth_removes_inherited_secret(self):
        for env in (['REDIS_DB=0'], ['REDIS_PASSWORD=', 'REDIS_DB=0']):
            with self.subTest(env=env):
                f, _ = self.make_fixture()
                f.inspect = Mock(return_value=[{'Config': {'Env': env}}])
                f.snapshot = {}
                f.sql.return_value = [{'user_id': 12345, 'cipher_digest': 'a' * 64}]
                def command(args, **kwargs):
                    self.assertNotIn('-e', args)
                    self.assertNotIn('REDISCLI_AUTH', args)
                    self.assertNotIn('REDISCLI_AUTH', kwargs['env'])
                    self.assertEqual('0', args[args.index('-n') + 1])
                    return types.SimpleNamespace(returncode=0, stdout='0')
                with patch.dict(runner.os.environ, {'REDISCLI_AUTH': 'inherited-secret'}), patch.object(runner.subprocess, 'run', side_effect=command) as calls:
                    f.cleanup_source_cache()
                self.assertEqual(2, calls.call_count)
                self.assertTrue(f.snapshot['encrypted_redis_restored'])

    def test_source_cache_invalid_auth_fails_before_redis_command(self):
        for password in ('bad\nsecret', 'bad\rsecret', 'bad\0secret'):
            with self.subTest(password=password):
                f, _ = self.make_fixture()
                f.inspect = Mock(return_value=[{'Config': {'Env': ['REDIS_PASSWORD=' + password]}}])
                f.snapshot = {}
                f.sql.return_value = [{'user_id': 12345, 'cipher_digest': 'a' * 64}]
                with patch.object(runner.subprocess, 'run') as calls, self.assertRaises(RuntimeError) as failure:
                    f.cleanup_source_cache()
                calls.assert_not_called()
                self.assertNotIn(password, str(failure.exception))

    def test_source_cache_resume_manifest_after_sql_rows_deleted(self):
        f, _ = self.make_fixture()
        self.configure_redis_fixture(f)
        f.snapshot = {'encrypted_redis_sources': [{'user_id': 12345, 'cipher_digest': 'b' * 64}]}
        f.sql.return_value = []
        with patch.object(runner.subprocess, 'run', return_value=types.SimpleNamespace(returncode=0, stdout='0')) as call:
            f.cleanup_source_cache()
        self.assertIn('openai_encrypted_route_geili:v2:12345:' + 'b' * 64, call.call_args.args[0])

    def test_source_cache_command_failure_still_cleans_sql_and_base(self):
        f, _ = self.make_fixture()
        self.configure_redis_fixture(f)
        f.snapshot = {'encrypted_settings_snapshot': []}
        f.sql.side_effect = [[], [], [{'user_id': 12345, 'cipher_digest': 'c' * 64}], [], [{'n': 0}]]
        with patch.object(runner.time, 'sleep'), patch.object(runner.subprocess, 'run', side_effect=RuntimeError('private-command-detail')), self.assertRaises(RuntimeError) as failure:
            f.restore()
        self.assertNotIn('private-command-detail', str(failure.exception))
        self.assertTrue(f.base_restored)
        self.assertFalse(f.snapshot['encrypted_redis_restored'])
        self.assertTrue(any('DELETE FROM openai_encrypted_sources_geili' in c.args[0] for c in f.sql.call_args_list))

    def test_source_cache_refuses_production_redis(self):
        f, cache = self.make_fixture()
        self.configure_redis_fixture(f)
        f.snapshot = {}
        f.sql.return_value = [{'user_id': 12345, 'cipher_digest': 'd' * 64}]
        cache.b.stage.REDIS = 'sub2api-redis'
        with patch.object(runner.subprocess, 'run') as call, self.assertRaises(RuntimeError):
            f.cleanup_source_cache()
        call.assert_not_called()

    def test_synthetic_seed_and_recovery_response_have_stable_shape(self):
        tools = runner.synthetic_response({'input': 'seed-tools'}, 'A', 'fixture')
        self.assertEqual('function_call_output', tools['output'][0]['type'])
        reasoning = runner.synthetic_response({'input': 'seed-reasoning'}, 'A', 'fixture')
        self.assertEqual('retained readable summary', reasoning['output'][0]['summary'][0]['text'])
        recovered = runner.synthetic_response({'input': [{'role': 'user', 'content': 'continue'}]}, 'B', 'fixture')
        self.assertEqual('completed', recovered['status'])
        self.assertIn('acceptance ok B', json.dumps(recovered))


if __name__ == '__main__':
    unittest.main()
