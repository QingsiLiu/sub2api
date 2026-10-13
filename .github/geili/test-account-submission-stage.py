#!/usr/bin/env python3
"""Offline checks for exact-ID Stage restoration; never opens a network or database."""
import importlib.util
from pathlib import Path
from types import SimpleNamespace
import unittest

spec = importlib.util.spec_from_file_location('intake_stage', Path(__file__).with_name('account-submission-stage.py'))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class BaseFixture:
    def __init__(self):
        self.snapshot = {'intake_invite_ids': [7], 'fixture_suffix': 'owned', 'fixture_ids': {'accounts': []}}
        self.fixture_ids = self.snapshot['fixture_ids']
        self.private = Path('/not-written')
        self.queries = []
        self.rows = [{'id': 7, 'name': 'Intake openai owned', 'account_id': 11}]
        self.super_restored = False

    def sql(self, query):
        self.queries.append(query)
        if query.startswith("SELECT json_build_object('proxy_id'"):return []
        return self.rows if query.startswith('SELECT') else []

    def restore(self):
        self.super_restored = True


class StageRestorationTests(unittest.TestCase):
    def setUp(self):
        self.writes = []
        cache = SimpleNamespace(fixture_type=lambda _: BaseFixture,
            b=SimpleNamespace(stage=SimpleNamespace(private_json=lambda path, value: self.writes.append(path))))
        self.fixture = runner.fixture_type(cache, None, None)()

    def test_restores_only_recorded_owned_invites_and_recovers_account_id(self):
        self.fixture.restore()
        self.assertEqual(self.fixture.fixture_ids['accounts'], [11])
        self.assertTrue(self.fixture.snapshot['intake_invites_removed'])
        self.assertTrue(self.fixture.super_restored)
        self.assertEqual(self.fixture.queries[-1], 'DELETE FROM account_submission_invites_geili WHERE id IN (7);')

    def test_expands_exact_batch_members_before_connection_and_provenance_cleanup(self):
        self.fixture.rows.append({'id': 8, 'name': 'Intake openai owned', 'account_id': 12})
        self.fixture.restore()
        self.assertEqual(self.fixture.fixture_ids['accounts'], [11, 12])
        self.assertIn('parent_invite_id IN (7)', self.fixture.queries[0])
        self.assertIn('WHERE invite_id IN (7,8)', self.fixture.queries[-2])
        self.assertEqual(self.fixture.queries[-1], 'DELETE FROM account_submission_invites_geili WHERE id IN (7,8);')

    def test_foreign_or_invalid_batch_member_refuses_all_database_mutation(self):
        for row in ({'id':8, 'name':'Foreign owner', 'account_id':12}, {'id':True, 'name':'Intake owned', 'account_id':12}):
            with self.subTest(row=row):
                self.fixture.rows = [row]
                with self.assertRaises(RuntimeError): self.fixture.restore()
                self.assertFalse(any(q.startswith(('UPDATE', 'DELETE')) for q in self.fixture.queries))

    def test_detaches_owned_account_before_proxy_and_provenance_cleanup(self):
        original=self.fixture.sql
        def sql(query):
            if query.startswith("SELECT json_build_object('proxy_id'"):
                self.fixture.queries.append(query)
                return [{'proxy_id':13,'name':'[external] Proxy owned'}]
            if query.startswith("SELECT json_build_object('id',id) FROM proxies"):
                self.fixture.queries.append(query)
                return []
            return original(query)
        self.fixture.sql=sql
        self.fixture.restore()
        queries=self.fixture.queries
        detach=next(q for q in queries if q.startswith('UPDATE accounts SET proxy_id=NULL'))
        self.assertIn('account_submission_invites_geili WHERE id IN (7)',detach)
        self.assertIn('account_submission_connections_geili WHERE invite_id IN (7)',detach)
        provenance=next(q for q in queries if q.startswith('DELETE FROM account_submission_connections_geili'))
        self.assertLess(queries.index(detach),queries.index(provenance))
        self.assertTrue(self.fixture.snapshot['intake_proxies_removed'])
        self.assertEqual(self.fixture.snapshot['intake_proxy_ids'],[13])

    def test_foreign_proxy_refuses_cleanup_before_account_mutation(self):
        original=self.fixture.sql
        def sql(query):
            if query.startswith("SELECT json_build_object('proxy_id'"):
                self.fixture.queries.append(query)
                return [{'proxy_id':13,'name':'[external] Another owner'}]
            return original(query)
        self.fixture.sql=sql
        with self.assertRaisesRegex(RuntimeError,'non-owned external proxy'):
            self.fixture.restore()
        self.assertFalse(any(q.startswith(('UPDATE','DELETE')) for q in self.fixture.queries))

    def test_refuses_nonowned_invite_before_deleting_or_altering_any_account(self):
        self.fixture.rows[0]['name'] = 'Ordinary user invitation'
        with self.assertRaisesRegex(RuntimeError, 'non-owned'):
            self.fixture.restore()
        self.assertFalse(self.fixture.super_restored)
        self.assertEqual(self.fixture.fixture_ids['accounts'], [])
        self.assertEqual(len(self.fixture.queries), 1)

    def test_invalid_or_injected_ids_never_reach_sql(self):
        for ident in ('7); DELETE FROM accounts;', -1, True, 0):
            with self.subTest(ident=ident):
                self.fixture.snapshot['intake_invite_ids'] = [ident]
                with self.assertRaisesRegex(RuntimeError, 'invalid'):
                    self.fixture.restore()
                self.assertEqual(self.fixture.queries, [])

    def test_recovery_is_idempotent_and_does_not_duplicate_accounts(self):
        self.fixture.remember_account(11)
        self.fixture.remember_account(11)
        self.fixture.restore()
        self.assertEqual(self.fixture.fixture_ids['accounts'], [11])
        self.fixture.rows = []
        self.fixture.restore()
        self.assertEqual(self.fixture.fixture_ids['accounts'], [11])
        self.assertTrue(self.fixture.super_restored)

    def test_failure_before_any_invite_creation_preserves_existing_invites(self):
        self.fixture.snapshot['intake_invite_ids'] = []
        self.fixture.restore()
        self.assertEqual(self.fixture.queries, [])
        self.assertTrue(self.fixture.super_restored)


if __name__ == '__main__':
    unittest.main()
