#!/usr/bin/env python3
"""Run uncached local Subscription V2 gates and preserve exact-source evidence.

python3 .github/geili/subscription-v2-validation.py [backend|frontend|all]
Docker must be running; CI=true makes missing database integration fail.
Logs may contain synthetic authentication data and stay under deploy/.secrets.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
PRIVATE = ROOT / 'deploy/.secrets/subscription-v2-comprehensive'


def source_identity():
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    paths = subprocess.check_output(['git', 'ls-files', '-c', '-o', '--exclude-standard', '--', 'backend', 'frontend', '.github/geili', '.github/workflows'], cwd=ROOT, text=True).splitlines()
    digest = hashlib.sha256()
    for name in sorted(set(paths)):
        path = ROOT / name
        if not path.is_file():
            continue
        digest.update(name.encode() + b'\0' + hashlib.sha256(path.read_bytes()).digest())
    return {'revision': revision, 'source_sha256': digest.hexdigest()}


def go_summary(path):
    counts = {'passed': 0, 'failed': 0, 'skipped': 0, 'package_failed': 0}
    skipped = []
    for line in path.read_text(errors='replace').splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        action = event.get('Action')
        if event.get('Test') and action in ('pass', 'fail', 'skip'):
            counts[{'pass': 'passed', 'fail': 'failed', 'skip': 'skipped'}[action]] += 1
            if action == 'skip':
                skipped.append(event.get('Package', '') + '/' + event['Test'])
        elif not event.get('Test') and action == 'fail':
            counts['package_failed'] += 1
    return {**counts, 'skipped_tests': skipped}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('scope', choices=['backend', 'frontend', 'all'], nargs='?', default='all')
    args = parser.parse_args()
    PRIVATE.mkdir(parents=True, exist_ok=True, mode=0o700)
    run = PRIVATE / (datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ') + '-' + args.scope)
    run.mkdir(mode=0o700)
    identity = source_identity()
    backend = [('go_default', ['go', 'test', '-json', '-count=1', './...']), ('go_unit', ['go', 'test', '-json', '-tags=unit', '-count=1', './...']), ('go_integration', ['go', 'test', '-json', '-tags=integration', '-count=1', './...']), ('subscription_payment_postgres', ['go', 'test', '-json', '-race', '-tags=unit,integration', '-count=1', './internal/service', '-run', 'TestSubscriptionV2Postgres']), ('subscription_race', ['go', 'test', '-json', '-race', '-tags=unit', '-count=1', './internal/geili/subscription', './internal/service', './internal/repository', './internal/server/middleware', '-run', 'Lot|Entitlement|Subscription|Contract|UpstreamDispatch']), ('go_utc', ['go', 'test', '-json', '-tags=unit', '-count=1', './internal/geili/subscription', './internal/service', './internal/repository', './internal/server/middleware', '-run', 'Lot|Entitlement|Subscription|Contract|UpstreamDispatch'])]
    frontend = [('frontend_full', ['pnpm', 'test:run']), ('frontend_typecheck', ['pnpm', 'typecheck']), ('frontend_build', ['pnpm', 'build'])]
    jobs = ([('backend', *item) for item in backend] if args.scope in ('backend', 'all') else []) + ([('frontend', *item) for item in frontend] if args.scope in ('frontend', 'all') else [])
    if args.scope in ('backend', 'all'):
        subprocess.run(['docker', 'info', '--format', '{{.ServerVersion}}'], check=True, stdout=subprocess.DEVNULL)
    evidence = {'identity': identity, 'started_at': datetime.now(timezone.utc).isoformat(), 'gates': [], 'passed': False}
    for directory, name, argv in jobs:
        path = run / (name + '.log')
        env = {**os.environ, 'CI': 'true'}
        if name == 'go_utc':
            env['TZ'] = 'UTC'
        started = datetime.now(timezone.utc)
        print('RUN ' + name, flush=True)
        with open(path, 'w', opener=lambda p, flags: os.open(p, flags, 0o600)) as log:
            result = subprocess.run(argv, cwd=ROOT / directory, env=env, stdout=log, stderr=subprocess.STDOUT)
        item = {'name': name, 'exit_code': result.returncode, 'seconds': (datetime.now(timezone.utc) - started).total_seconds(), 'log_sha256': hashlib.sha256(path.read_bytes()).hexdigest()}
        if directory == 'backend':
            item.update(go_summary(path))
        evidence['gates'].append(item)
        output = run / 'report.json'
        output.write_text(json.dumps(evidence, indent=2) + '\n')
        output.chmod(0o600)
        print(('PASS ' if result.returncode == 0 else 'FAIL ') + name, flush=True)
    evidence['completed_at'] = datetime.now(timezone.utc).isoformat()
    evidence['final_identity'] = source_identity()
    evidence['source_unchanged'] = evidence['final_identity'] == identity
    evidence['passed'] = all(x['exit_code'] == 0 for x in evidence['gates']) and evidence['source_unchanged']
    (run / 'report.json').write_text(json.dumps(evidence, indent=2) + '\n')
    print('REPORT ' + str(run / 'report.json'), flush=True)
    return 0 if evidence['passed'] else 1


if __name__ == '__main__':
    sys.exit(main())
