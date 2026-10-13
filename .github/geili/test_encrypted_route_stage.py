"""Local stdlib-only Stage runner tests. No Docker, SSH, database or paid calls."""
import importlib.util
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
