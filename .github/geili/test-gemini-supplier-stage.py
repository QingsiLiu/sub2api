#!/usr/bin/env python3
"""Offline runner guard, fixture, oracle and exact restoration checks."""
import importlib.util
import json
from pathlib import Path
from types import SimpleNamespace
import unittest

spec = importlib.util.spec_from_file_location('gemini_supplier_stage', Path(__file__).with_name('gemini-supplier-stage.py'))
s = importlib.util.module_from_spec(spec)
spec.loader.exec_module(s)


def args(**patch):
    value = dict(expected_revision='a' * 40, expected_production_revision='b' * 40,
        expected_image='ghcr.io/qingsiliu/sub2api@sha256:' + 'c' * 64,
        expected_production_image='ghcr.io/qingsiliu/sub2api@sha256:' + 'd' * 64,
        mock_port=19018, replica_port=18519, dry_run=False, execute=False, restore=None)
    return SimpleNamespace(**(value | patch))


class GuardTests(unittest.TestCase):
    def test_default_is_readonly_and_execute_restore_are_exclusive(self):
        a = args(); s.validate_options(a); self.assertTrue(a.dry_run)
        a = args(execute=True); s.validate_options(a); self.assertFalse(a.dry_run)
        a = args(restore='/private/snapshot.json'); s.validate_options(a); self.assertFalse(a.dry_run)
        for patch in ({'dry_run': True, 'execute': True}, {'restore': 'x', 'execute': True},
                      {'dry_run': True, 'restore': 'x'}):
            with self.subTest(patch=patch), self.assertRaises(RuntimeError): s.validate_options(args(**patch))

    def test_rejects_mutable_identity_and_bad_ports(self):
        for patch in ({'expected_revision': 'main'}, {'expected_production_revision': 'a' * 7},
                      {'expected_image': 'ghcr.io/qingsiliu/sub2api:latest'}, {'expected_production_image': 'ghcr.io/qingsiliu/sub2api:latest'},
                      {'replica_port': 19018}, {'mock_port': 8080}, {'replica_port': True}, {'mock_port': 0}):
            with self.subTest(patch=patch), self.assertRaises(RuntimeError): s.validate_options(args(**patch))

    def test_only_exact_owned_loopback_routes(self):
        self.assertEqual(s.mock_url('http://127.0.0.1:19018', 'unit-a'), 'http://127.0.0.1:19018/gemini-supplier/unit-a')
        for base in ('https://api.google.com', 'http://0.0.0.0:19018', 'http://localhost:19018', 'http://127.0.0.1:19018/path'):
            with self.subTest(base=base), self.assertRaises(RuntimeError): s.mock_url(base, 'unit')
        with self.assertRaises(RuntimeError): s.mock_url('http://127.0.0.1:19018', '../escape')
        self.assertEqual(s.route('/gemini-supplier/unit-a/v1/messages?beta=true').group(1), 'unit-a')
        self.assertIsNone(s.route('/upstream/real'))
        self.assertEqual(s.upstream_kind('v1/messages'), 'messages')
        self.assertEqual(s.upstream_kind('v1beta/models/' + s.MODEL + ':streamGenerateContent'), 'native')
        self.assertIsNone(s.upstream_kind('v1/responses'))

    def test_cleanup_rejects_guessed_duplicate_boolean_ids(self):
        self.assertEqual(s.exact_ids([23, 11]), '11,23')
        for values in ([], [True], [0], ['1'], [1, 1]):
            with self.subTest(values=values), self.assertRaises(RuntimeError): s.exact_ids(values)


class OracleTests(unittest.TestCase):
    def test_exact_modes_detect_same_account_retries_and_exhaustion(self):
        self.assertEqual(s.expected_modes('same-group'), ['balance', 'capacity', 'healthy'])
        self.assertEqual(s.expected_modes('cross-group'), ['balance', 'capacity', 'healthy'])
        self.assertEqual(s.expected_modes('exhausted'), ['balance', 'capacity'])
        self.assertEqual(s.expected_modes('ordinary'), ['ordinary'])

    def test_payloads_all_four_protocols_stream_and_nonstream(self):
        for protocol in s.PROTOCOLS:
            for stream in (False, True):
                path, body = s.payload(protocol, 'synthetic-unit', stream)
                self.assertIn('synthetic-unit', json.dumps(body))
                if protocol == 'native':
                    self.assertIn('streamGenerateContent' if stream else ':generateContent', path)
                    self.assertEqual(body['generationConfig']['maxOutputTokens'], 16)
                else:
                    self.assertEqual(body['stream'], stream)
                    self.assertEqual(body['model'], s.MODEL)

    def test_stream_needs_semantic_text_and_correct_terminal(self):
        for protocol, terminal in [('responses', 'response.completed'), ('messages', 'message_stop'), ('chat/completions', '[DONE]')]:
            text = 'data: ' + json.dumps({'text': s.TEXT}) + '\n\n'
            done = 'data: ' + ('[DONE]' if terminal == '[DONE]' else json.dumps({'type': terminal})) + '\n\n'
            self.assertTrue(s.complete(protocol, [200, text + done], True))
            self.assertFalse(s.complete(protocol, [200, text], True))
            self.assertFalse(s.complete(protocol, [200, text + done + done], True))
            self.assertFalse(s.complete(protocol, [503, text + done], True))
            failed = 'data: ' + json.dumps({'type': 'response.failed', 'error': {'message': 'upstream'}}) + '\n\n'
            self.assertFalse(s.complete(protocol, [200, text + failed + done], True))
        native = {'candidates': [{'content': {'parts': [{'text': s.TEXT}]}, 'finishReason': 'STOP'}]}
        self.assertTrue(s.complete('native', [200, 'data: ' + json.dumps(native) + '\n\n'], True))

    def test_nonstream_requires_completed_not_merely_http200(self):
        rows = {'responses': {'status': 'completed', 'text': s.TEXT},
                'messages': {'stop_reason': 'end_turn', 'text': s.TEXT},
                'chat/completions': {'choices': [{'finish_reason': 'stop', 'message': {'content': s.TEXT}}]},
                'native': {'candidates': [{'finishReason': 'STOP', 'content': {'parts': [{'text': s.TEXT}]}}]}}
        for protocol, body in rows.items():
            self.assertTrue(s.complete(protocol, [200, body], False))
            self.assertFalse(s.complete(protocol, [200, {'text': s.TEXT}], False))
            self.assertFalse(s.complete(protocol, [200, dict(body, error={'message': 'failed'})], False))


class FixtureTests(unittest.TestCase):
    def fixture(self):
        stage = SimpleNamespace(private_json=lambda *a: None)
        fixture = s.fixture_type(SimpleNamespace(fixture_type=lambda _: object, b=SimpleNamespace(stage=stage)), None)()
        fixture.suffix, fixture.private, fixture.mock_base = 'unit', Path('/offline'), 'http://127.0.0.1:19018'
        fixture.admin, fixture.owner = 'synthetic-admin', {'id': 90, 'token': 'synthetic-owner'}
        fixture.snapshot = {'gemini_supplier_accounts': {}, 'gemini_supplier_runner': s.KIND}
        fixture.modes, fixture.writes = {}, []
        def api(path, data, token):
            fixture.writes.append((path, data))
            return {'id': len(fixture.writes), 'name': data['name'], 'key': 'synthetic-key'}
        fixture.api = api
        return fixture

    def test_fixture_uses_only_gemini_mock_pool_retries_and_valid_balance_key(self):
        f = self.fixture(); key, accounts = f.new_case('unit', 'cross-group')
        groups = [body for path, body in f.writes if path == '/admin/groups']
        self.assertEqual(len(groups), 2)
        self.assertTrue(all(g['platform'] == 'gemini' and g['model_allowlist']['models'] == [s.MODEL] for g in groups))
        account_bodies = [body for path, body in f.writes if path == '/admin/accounts']
        self.assertEqual(len(account_bodies), 3)
        self.assertEqual([len(a['group_ids']) for a in account_bodies], [2, 2, 1])
        self.assertTrue(all(a['credentials']['pool_mode_retry_count'] == 5 for a in account_bodies))
        self.assertTrue(all(a['credentials']['base_url'].startswith('http://127.0.0.1:19018/gemini-supplier/') for a in account_bodies))
        key_body = f.writes[-1][1]
        self.assertEqual(key_body['billing_source'], 'balance')
        self.assertEqual(key_body['routing_mode'], 'composite')
        self.assertFalse(any('/payment/' in path for path, _ in f.writes))
        self.assertEqual(len(f.snapshot['gemini_supplier_accounts']), 3)

    def test_ordinary_error_has_unused_healthy_backup(self):
        f = self.fixture(); f.new_case('unit', 'ordinary')
        self.assertEqual(list(f.modes.values()), ['ordinary', 'healthy'])

    def test_restore_refuses_another_runner_or_account_name(self):
        for runner, name in [('other-runner', 'owned'), (s.KIND, 'not-owned')]:
            f = self.fixture(); f.fixture_ids = {'accounts': [7]}
            f.snapshot.update(gemini_supplier_runner=runner, gemini_supplier_accounts={'7': 'owned'})
            f.guard = lambda: None
            f.sql = lambda query: [{'id': 7, 'name': name}]
            with self.assertRaises(RuntimeError): f.restore()

    def test_restore_disables_exact_owned_ids_drains_then_calls_canonical_restore(self):
        calls = []
        class Base:
            def restore(self):
                calls.append('canonical-restore')
                return True
        cache = SimpleNamespace(fixture_type=lambda _: Base,
            b=SimpleNamespace(stage=SimpleNamespace(private_json=lambda *a: None)))
        f = s.fixture_type(cache, None)()
        f.private, f.guard = Path('/offline'), lambda: None
        f.fixture_ids = {'accounts': [7], 'keys': [11], 'users': [13]}
        f.snapshot = {'gemini_supplier_accounts': {'7': 'owned'}, 'gemini_supplier_runner': s.KIND}
        def sql(query):
            calls.append(query)
            if "'name',name" in query:
                return [{'id': 7, 'name': 'owned'}]
            if "'n',COUNT(*)" in query:
                return [{'n': 0}]
            return []
        f.sql = sql
        f.cleanup_replica = lambda: calls.append('cleanup-replica')
        self.assertTrue(f.restore())
        self.assertTrue(f.snapshot['gemini_supplier_runtime_restored'])
        self.assertEqual(calls[-1], 'canonical-restore')
        self.assertTrue(any('id IN (11) AND user_id IN (13)' in query for query in calls))
        delete = next(query for query in calls if query.startswith('BEGIN;'))
        self.assertEqual(delete.count('account_id IN (7)'), 3)
        self.assertNotIn('LIKE', delete)
        self.assertLess(calls.index('cleanup-replica'), calls.index(delete))


if __name__ == '__main__':
    unittest.main()
