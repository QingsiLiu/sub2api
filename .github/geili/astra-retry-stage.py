#!/usr/bin/env python3
"""Candidate-bound relay text retry checks on Stage, using owned mocks only."""
import argparse
from datetime import datetime, timedelta, timezone
import fcntl
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import signal
import time

MODEL = 'gpt-6-astra'


def load_ops(path):
    spec = importlib.util.spec_from_file_location('astra_nonpayment', Path(path) / 'accept-nonpayment-release-stage.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def fixture_type(ops, module):
    class Retry(ops.fixture_type(module)):
        def start_mock(self):
            original = module.BASE_HARNESS.Mock
            fixture = self
            self.retry_modes = {}
            self.retry_calls = {}
            self.retry_account_ids = {}
            self.probe_calls = 0

            class Mock(original):
                def do_POST(self):
                    raw = self.rfile.read(int(self.headers.get('Content-Length', 0)))
                    parts = self.path.split('/')
                    if len(parts) > 4 and parts[1:3] == ['upstream', 'astra-retry']:
                        label = parts[3]
                        payload = json.loads(raw)
                        if (payload.get('stream') is False and payload.get('max_output_tokens') == 512
                                and payload.get('tool_choice') == 'required'
                                and any(t.get('name') == 'probe_ping' for t in payload.get('tools', []))):
                            with fixture.lock:
                                fixture.probe_calls += 1
                            return self.reply(dict(id='synthetic-probe', object='response', status='completed',
                                model=payload['model'], output=[dict(type='function_call', id='probe', call_id='probe',
                                name='probe_ping', arguments='{"ok":true}', status='completed')],
                                usage=dict(input_tokens=1, output_tokens=1, total_tokens=2)))
                        with fixture.lock:
                            rows = fixture.retry_calls.setdefault(label, [])
                            rows.append(dict(account=parts[4], at=time.monotonic()))
                            attempt = len(rows)
                        status = fixture.retry_modes[label]
                        if status and attempt <= 2:
                            kind = 'model_not_found' if status in (401, 404) else 'server_error'
                            return self.reply(dict(error=dict(type=kind, code=kind,
                                message='model not found' if status in (401, 404) else 'synthetic relay temporarily unavailable')), status)
                    saved = self.rfile
                    try:
                        self.rfile = io.BytesIO(raw)
                        return super().do_POST()
                    finally:
                        self.rfile = saved

            module.BASE_HARNESS.Mock = Mock
            try:
                super().start_mock()
            finally:
                module.BASE_HARNESS.Mock = original

        def scenario(self, label, status, legacy=False):
            self.retry_modes[label] = status
            group = self.api('/admin/groups', dict(name='Astra retry '+label+' '+self.suffix,
                platform='openai', rate_multiplier=1, allow_messages_dispatch=True,
                model_pricing=[dict(models=[MODEL], billing_mode='token', input_price=.000001, output_price=.000002)]), self.admin)
            self.retry_account_ids[label] = []
            for index in range(2):
                extra = dict(openai_responses_mode='force_responses', openai_responses_supported=True)
                if legacy:
                    now = datetime.now(timezone.utc)
                    extra['model_rate_limits'] = {MODEL: dict(reason='upstream_404_model_not_found',
                        rate_limited_at=(now-timedelta(seconds=30)).isoformat(),
                        rate_limit_reset_at=(now+timedelta(minutes=30)).isoformat())}
                account = self.api('/admin/accounts', dict(name='Astra retry '+label+' '+str(index)+' '+self.suffix,
                    platform='openai', type='apikey', concurrency=4, priority=index+1,
                    credentials=dict(api_key='synthetic-astra-retry', pool_mode=False, base_url=self.mock_base+'/upstream/astra-retry/'+label+'/'+str(index),
                        model_mapping={MODEL: MODEL}), extra=extra, group_ids=[group['id']]), self.admin)
                self.retry_account_ids[label].append(int(account['id']))
            user = next(c['user'] for c in self.cases if c['kind'] == 'balance')
            return self.api('/keys', dict(name='Astra retry '+label, billing_source='balance', group_id=group['id']), user['token'])

        def run_checks(self):
            completed = 0
            for path in ('responses', 'chat/completions', 'messages'):
                for stream in (False, True):
                    for status in (502, 429, 404, 401):
                        label = path.replace('/', '-')+'-'+str(stream)+'-'+str(status)
                        key = self.scenario(label, status)
                        payload = dict(model=MODEL, stream=stream, max_tokens=32)
                        if path == 'responses':
                            payload['input'] = 'synthetic Astra retry fixture'
                        else:
                            payload['messages'] = [dict(role='user', content='synthetic Astra retry fixture')]
                        code, body, _ = self.request('/v1/'+path, payload, key['key'])
                        terminal = ('response.completed' if path == 'responses' else 'message_stop' if path == 'messages' else '[DONE]')
                        success = code == 200 and (terminal in body if isinstance(body, str) else isinstance(body, dict) and not body.get('error'))
                        self.check(label+' completes one client request', success)
                        calls = self.retry_calls.get(label, [])
                        self.check(label+' retries a spent pool', len(calls) == 3 and calls[0]['account'] != calls[1]['account'])
                        completed += 1
            label = 'legacy-model-cooldown'
            key = self.scenario(label, 0, legacy=True)
            ids = ','.join(str(i) for i in self.retry_account_ids[label])
            row = self.sql("SELECT json_build_object('n',count(*),'active',bool_and((extra->'model_rate_limits'->'gpt-6-astra'->>'rate_limit_reset_at')::timestamptz>NOW()+interval '20 minutes')) FROM accounts WHERE id IN ("+ids+");")[0]
            self.check('legacy fixture retains two active 30 minute records', row['n'] == 2 and row['active'] is True)
            began = time.monotonic()
            code, body, _ = self.request('/v1/responses', dict(model=MODEL, stream=False, input='synthetic legacy cooldown'), key['key'])
            self.check('legacy 30 minute model pause no longer blocks text', code == 200 and time.monotonic()-began < 10 and len(self.retry_calls.get(label, [])) == 1)
            completed += 1
            deadline = time.monotonic()+90
            while time.monotonic() < deadline:
                rows = self.fixture_receipts()
                if len(rows) == completed and all(ops.b.receipt_evidence_ok(row) for row in rows):
                    break
                time.sleep(.5)
            self.check('failed attempts do not create extra settlements; each successful call settles once',
                len(rows) == completed and all(ops.b.receipt_evidence_ok(row) for row in rows))
            self.check('production and existing payment configuration unchanged', self.protected_state_unchanged())
    return Retry


def main(argv=None):
    early = argparse.ArgumentParser(add_help=False)
    early.add_argument('--ops-bin', required=True)
    known, remaining = early.parse_known_args(argv)
    ops = load_ops(known.ops_bin)
    args = ops.b.candidate_args().parse_args(remaining)
    ops.b.validate_args(args)
    fixture = fixture_type(ops, ops.b.stage.load_source(args.source_root))(args)
    with (ops.b.stage.LAB/'.v2-synthetic.lock').open('a') as lock:
        os.chmod(lock.name, 0o600)
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
        passed = fixture.execute_billing()
        if fixture.snapshot:
            path = fixture.private/'billing-result.json'
            report = json.loads(path.read_text())
            report.update(astra_retry_runner_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                synthetic_only=True, real_payment=False, real_provider=False, synthetic_requests=25, max_http_concurrency=1)
            ops.b.stage.private_json(path, report)
    return 0 if passed else 1


if __name__ == '__main__':
    raise SystemExit(main())
