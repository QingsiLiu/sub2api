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
