#!/usr/bin/env python3
"""Candidate-bound, synthetic-only Stage intake acceptance. Never deploys.
Uses canonical ops identity/configuration guards, the exclusive Stage lease,
owned same-digest second instance, and exact-ID fixture restoration.
"""
import argparse
import fcntl
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import signal


def load(path, name):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def fixture_type(cache, source, checks):
    class IntakeFixture(cache.fixture_type(source)):
        has_replica = True

        def remember_invite(self, ident):
            self.snapshot.setdefault('intake_invite_ids', []).append(int(ident))
            cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)

        def remember_account(self, ident):
            if type(ident) is not int or ident <= 0:
                raise RuntimeError('invalid submitted fixture account ID')
            if ident not in self.fixture_ids['accounts']:
                self.fixture_ids['accounts'].append(ident)
                cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)

        def setup(self):
            self.start_replica()

        def run_checks(self):
            checks.verify(self)
            # Credentials are synthetic and only searched in logs, never printed or returned.
            secrets = ['synthetic-key-intake-' + platform + '-' + self.suffix for platform in ('anthropic', 'openai')]
            logs = cache.b.stage.command(['docker', 'logs', cache.b.stage.APP])
            self.check('primary access/error logs contain no submitted keys', all(secret not in logs for secret in secrets))
            record = self.snapshot['cache_replica']
            logs = cache.b.stage.command(['docker', 'logs', record['name']])
            self.check('replica logs contain no submitted keys', all(secret not in logs for secret in secrets))

        def restore(self):
            if self.snapshot:
                ids = self.snapshot.get('intake_invite_ids', [])
                if any(type(ident) is not int or ident <= 0 for ident in ids):
                    raise RuntimeError('invalid exact invitation fixture IDs')
                if ids:
                    selected = ','.join(str(ident) for ident in ids)
                    rows = self.sql("SELECT json_build_object('id',id,'name',config->>'name','account_id',account_id) FROM account_submission_invites_geili WHERE id IN (" + selected + ');')
                    for row in rows:
                        if not row['name'].endswith(' ' + self.snapshot['fixture_suffix']):
                            raise RuntimeError('refuse cleanup of non-owned invitation')
                        if row['account_id']:
                            self.remember_account(row['account_id'])
                    # Only synthetic invite provenance is removed. Production/user records are never touched.
                    self.sql('DELETE FROM account_submission_invites_geili WHERE id IN (' + selected + ');')
                    self.snapshot['intake_invites_removed'] = True
                    cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)
            return super().restore()
    return IntakeFixture


def main():
    early = argparse.ArgumentParser(add_help=False)
    early.add_argument('--ops-bin', required=True)
    known, remaining = early.parse_known_args()
    cache = load(Path(known.ops_bin).resolve(strict=True) / 'accept-two-instance-cache-stage.py', 'intake_cache_stage')
    checks = load(Path(__file__).with_name('account-submission-acceptance.py'), 'intake_checks')
    parser = cache.b.candidate_args()
    parser.add_argument('--replica-port', type=int, default=18517)
    args = parser.parse_args(remaining)
    cache.b.validate_args(args)
    if not cache.valid_replica_port(args.replica_port, args.mock_port):
        raise RuntimeError('invalid replica port')
    fixture = fixture_type(cache, cache.b.stage.load_source(args.source_root), checks)(args)
    if args.dry_run:
        return 0 if fixture.execute_billing() else 1
    with (cache.b.stage.LAB / '.v2-synthetic.lock').open('a') as lock:
        os.chmod(lock.name, 0o600)
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
        passed = fixture.execute_billing()
    if fixture.snapshot:
        result = {'revision': args.expected_revision, 'image': args.expected_image,
            'kind': 'account-submission-stage', 'passed': passed,
            'checks': len(fixture.checks), 'restored': fixture.snapshot.get('restored', False),
            'replica_removed': fixture.snapshot.get('cache_replica', {}).get('removed', False),
            'invites_removed': fixture.snapshot.get('intake_invites_removed', False),
            'runner_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
            'checks_sha256': hashlib.sha256(Path(__file__).with_name('account-submission-acceptance.py').read_bytes()).hexdigest(),
            'synthetic_only': True, 'production_ready': False}
        cache.b.stage.private_json(fixture.private / 'intake-result.json', result)
        print('RESULT ' + str(fixture.private / 'intake-result.json'), flush=True)
    return 0 if passed else 1


if __name__ == '__main__':
    raise SystemExit(main())
