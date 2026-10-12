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
            b=types.SimpleNamespace(stage=types.SimpleNamespace(literal=literal, private_json=Mock())))
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
        f.sql.side_effect = [[], [], [], [{'n': 0}]]
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
        f.sql.side_effect = [[], [{'id': 42, 'key': 'unexpected', 'value': 'changed', 'updated_at': 'now'}], [], [{'n': 0}]]
        def check(_, passed, *args):
            if not passed:
                raise AssertionError('unexpected settings drift')
        f.check.side_effect = check
        with patch.object(runner.time, 'sleep'), self.assertRaises(RuntimeError):
            f.restore()
        self.assertFalse(f.snapshot['encrypted_settings_restored'])
        self.assertTrue(f.base_restored)
        self.assertTrue(any('DELETE FROM openai_encrypted_sources_geili' in c.args[0] for c in f.sql.call_args_list))

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
