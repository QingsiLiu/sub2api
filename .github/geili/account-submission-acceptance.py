#!/usr/bin/env python3
"""Synthetic HTTP checks shared by local and candidate-bound Stage adapters.
Never submits a real supplier key or contacts official providers.
"""
import concurrent.futures
import hashlib
import json
from pathlib import Path
import time


def literal(value):
    return "'" + str(value).replace("'", "''") + "'"


def verify(f):
    calls = []
    f.intake_canaries = []  # Memory-only: used to inspect logs without disclosing credentials/tokens.

    def reserve(number=1):
        nonlocal calls
        now = time.monotonic()
        calls = [at for at in calls if now - at < 61]
        if len(calls) + number > 28:
            delay = max(0, 61 - (now - calls[0]))
            print('Waiting for the public submission rate window', flush=True)
            while delay > 0:
                interval = min(delay, 30)
                time.sleep(interval)
                delay -= interval
            calls = []
        calls.extend([time.monotonic()] * number)

    def public(action, body, secondary=False):
        reserve()
        caller = f.replica_request if secondary else f.request
        return caller('/api/v1/account-submissions/' + action, body)

    def make(platform, group=None, name='key'):
        config = {'name': 'Intake ' + name + ' ' + platform + ' ' + f.suffix,
                  'platform': platform, 'group_ids': [] if group is None else [group],
                  'proxy_id': None, 'concurrency': 2, 'priority': 50, 'rate_multiplier': 0}
        result = f.api('/admin/accounts/submission-invites', config, f.admin)
        f.remember_invite(result['invite']['id'])
        f.intake_canaries.append(result['token'])
        return result, config

    def account(invite):
        rows = f.sql('SELECT json_build_object(\'account_id\',account_id,\'submitted_at\',submitted_at,\'hash\',token_hash) FROM account_submission_invites_geili WHERE id=' + str(invite['invite']['id']) + ';')
        return rows[0]

    def no_secret(value, secrets):
        rendered = json.dumps(value, ensure_ascii=False)
        return all(secret not in rendered for secret in secrets)

    user = f.user('key-intake')
    f.api('/admin/users/' + str(user['id']) + '/balance', {'balance': 5, 'operation': 'set', 'notes': 'Synthetic key intake acceptance'}, f.admin)
    anonymous = f.request('/api/v1/admin/accounts/submission-invites')
    f.check('anonymous callers cannot create/list invitations', anonymous[0] == 401)
    f.check('public submission SPA loads without login', f.request('/submit-key')[0] == 200)

    for platform in ('anthropic', 'openai'):
        model = 'claude-sonnet-4-6' if platform == 'anthropic' else 'gpt-4.1-mini'
        group = f.api('/admin/groups', {'name': 'Intake ' + platform + ' ' + f.suffix,
            'platform': platform, 'rate_multiplier': 1, 'subscription_rate_multiplier': 1,
            'long_context_pricing_enabled': False,
            'model_pricing': [{'models': [model], 'billing_mode': 'token', 'input_price': .000001, 'output_price': .000002}]}, f.admin)
        invite, config = make(platform, group['id'])
        token = invite['token']
        secret = 'synthetic-key-intake-' + platform + '-' + f.suffix
        f.intake_canaries.append(secret)
        stored = account(invite)
        f.check(platform + ' stores only SHA256 invitation hash', stored['hash'] == hashlib.sha256(token.encode()).hexdigest() and stored['hash'] != token)
        inspected = public('inspect', {'token': token})
        f.check(platform + ' invitation inspection is public and minimal', inspected[0] == 200 and set(inspected[1]['data']) == {'platform', 'expires_at', 'status'} and no_secret(inspected[1], [token, secret, config['name']]))
        bad = public('submit', {'token': token, 'api_key': secret, 'base_url': f.mock_base})
        f.check(platform + ' external caller cannot set upstream URL', bad[0] == 400 and account(invite)['account_id'] is None and no_secret(bad[1], [secret, token]))
        bad = public('submit', {'token': token, 'api_key': ''})
        f.check(platform + ' empty key does not consume invitation', bad[0] == 400 and account(invite)['account_id'] is None)
        bad = public('submit', {'token': token, 'api_key': 'synthetic-oversized-' + 'x' * 9000})
        f.check(platform + ' oversized request does not consume invitation', bad[0] == 400 and account(invite)['account_id'] is None and no_secret(bad[1], [token, 'synthetic-oversized-']))
        reserve(6)
        def submit(index):
            caller = f.replica_request if getattr(f, 'has_replica', False) and index % 2 else f.request
            return caller('/api/v1/account-submissions/submit', {'token': token, 'api_key': secret})
        with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
            submitted = list(pool.map(submit, range(6)))
        label = 'two-instance concurrent submissions' if getattr(f, 'has_replica', False) else 'concurrent submissions'
        f.check(platform + ' ' + label + ' succeed once', all(row[0] == 200 for row in submitted))
        record = account(invite)
        ident = record['account_id']
        f.remember_account(ident)
        row = f.sql('SELECT json_build_object(\'n\',(SELECT count(*) FROM accounts WHERE name=' + literal(config['name']) + '),\'status\',status,\'schedulable\',schedulable,\'rate\',rate_multiplier,\'base_url\',credentials->>\'base_url\',\'outbox\',(SELECT count(*) FROM scheduler_outbox WHERE account_id=accounts.id),\'groups\',(SELECT count(*) FROM account_groups WHERE account_id=accounts.id)) FROM accounts WHERE id=' + str(ident) + ';')[0]
        official = 'https://api.anthropic.com' if platform == 'anthropic' else 'https://api.openai.com'
        f.check(platform + ' commits one paused account with official URL and exact group', row['n'] == 1 and row['status'] == 'inactive' and row['schedulable'] is False and row['rate'] == 0 and row['base_url'] == official and row['groups'] == 1)
        detail = f.api('/admin/accounts/' + str(ident), token=f.admin)
        f.check(platform + ' details hide the key and report its presence', no_secret(detail, [secret, token]) and detail.get('credentials_status', {}).get('has_api_key') is True)
        exported = f.api('/admin/accounts/data?ids=' + str(ident) + '&include_proxies=false', token=f.admin)
        f.check(platform + ' console export skips externally submitted account', exported.get('skipped_external') == 1 and not exported['accounts'] and no_secret(exported, [secret, token]))
        clone = f.request('/api/v1/admin/accounts/' + str(ident) + '/duplicate', {}, f.admin)
        f.check(platform + ' copy cannot bypass credential protection', clone[0] == 400 and no_secret(clone[1], [secret, token]))
        changed = public('submit', {'token': token, 'api_key': 'synthetic-replacement'})
        same = f.sql('SELECT json_build_object(\'same\',credentials->>\'api_key\'=' + literal(secret) + ') FROM accounts WHERE id=' + str(ident) + ';')[0]['same']
        f.check(platform + ' retry confirms success without replacing the key', changed[0] == 200 and same)
        leaked = f.sql('SELECT json_build_object(\'n\',count(*)) FROM audit_logs WHERE request_body LIKE ' + literal('%' + secret + '%') + ' OR request_body LIKE ' + literal('%' + token + '%') + ';')[0]['n']
        f.check(platform + ' audit records contain neither submitted key nor invitation token', leaked == 0)
        # Only this synthetic fixture is redirected by an administrator to the loopback mock.
        f.api('/admin/accounts/' + str(ident), {'credentials': {'base_url': f.mock_base + '/upstream', 'model_mapping': {model: model}}, 'extra': {}, 'status': 'active'}, f.admin, 'PUT')
        f.api('/admin/accounts/' + str(ident) + '/schedulable', {'schedulable': True}, f.admin)
        key = f.api('/keys', {'name': 'Intake test', 'billing_source': 'balance', 'routing_mode': 'single', 'group_id': group['id']}, user['token'])
        path = '/v1/messages' if platform == 'anthropic' else '/v1/chat/completions'
        response = f.request(path, {'model': model, 'messages': [{'role': 'user', 'content': 'Synthetic intake acceptance'}], 'max_tokens': 16}, key['key'])
        f.check(platform + ' administrator activation permits mocked gateway inference', response[0] == 200)
        deadline = time.monotonic() + 15
        while True:
            usage = f.sql('SELECT json_build_object(\'n\',count(*),\'account\',min(account_id),\'cost\',min(actual_cost)) FROM usage_logs WHERE api_key_id=' + str(key['id']) + ';')[0]
            if usage['n'] or time.monotonic() >= deadline: break
            time.sleep(.1)
        f.check(platform + ' inference settles once under existing billing rules', usage['n'] == 1 and usage['account'] == ident and usage['cost'] > 0)
        detail = f.api('/admin/accounts/' + str(ident), token=f.admin)
        f.check(platform + ' metadata edit preserves stored credential presence', detail.get('credentials_status', {}).get('has_api_key') is True)
        exported = f.api('/admin/accounts/data?ids=' + str(ident) + '&include_proxies=false', token=f.admin)
        f.check(platform + ' editing Extra cannot remove export protection', exported.get('skipped_external') == 1 and not exported['accounts'])
        f.api('/admin/accounts/' + str(ident), {'status': 'inactive'}, f.admin, 'PUT')

    for kind in ('revoked', 'expired', 'unknown'):
        invite, _ = make('openai', name=kind)
        token = invite['token']
        if kind == 'revoked': f.api('/admin/accounts/submission-invites/' + str(invite['invite']['id']) + '/revoke', {}, f.admin)
        if kind == 'expired': f.sql("UPDATE account_submission_invites_geili SET expires_at=NOW()-INTERVAL '1 second' WHERE id=" + str(invite['invite']['id']) + ';')
        if kind == 'unknown': token = 'a' * 43
        denied = public('submit', {'token': token, 'api_key': 'synthetic-not-stored'})
        f.check(kind + ' invitation cannot create an account', denied[0] in (400, 410) and account(invite)['account_id'] is None and no_secret(denied[1], ['synthetic-not-stored', token]))
    listed = f.api('/admin/accounts/submission-invites', token=f.admin)
    f.check('administrator can inspect invitation status without original tokens', bool(listed['items']) and all('token' not in item and 'token_hash' not in item for item in listed['items']))
    backup = f.request('/api/v1/admin/backups/synthetic-fixture/download-url', token=f.admin)
    f.check('raw database backup download cannot bypass credential protection', backup[0] == 403 and backup[1].get('reason') == 'SUBMISSION_BACKUP_PROTECTED')
    f.check('submitted accounts remain absent from all-account exports', f.api('/admin/accounts/data?search=' + f.suffix + '&include_proxies=false', token=f.admin).get('skipped_external') == 2)
    return len(f.checks)


def local_main():
    import importlib.util
    import os
    import secrets
    spec = importlib.util.spec_from_file_location('intake_local', Path(__file__).with_name('acceptance.py'))
    h = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(h)
    if h.PROJECT != 'geili-key-intake-local':
        raise RuntimeError('local runner requires its dedicated geili-key-intake-local database')
    label = h.run(['docker', 'inspect', '--format', '{{index .Config.Labels "geili.acceptance"}}', h.PROJECT + '-postgres']).strip()
    if label != 'local': raise RuntimeError('not an owned synthetic database')

    class Local:
        has_replica = False
        def __init__(self):
            self.suffix = secrets.token_hex(5)
            self.mock_base = 'http://127.0.0.1:' + str(h.MOCK_PORT)
            self.checks = []
            self.admin = h.api('/auth/login', {'email': 'admin@acceptance.invalid', 'password': h.local_env()['admin_password']})['access_token']
        @staticmethod
        def request(path, data=None, token=None, method=None):
            status, raw = h.request(path, data, token, method, raw=True)
            if isinstance(raw, str):
                try: raw = json.loads(raw)
                except ValueError: pass
            return status, raw

        api = staticmethod(h.api)
        sql = staticmethod(h.sql)
        def check(self, name, condition, detail=None):
            self.checks.append({'name': name, 'passed': bool(condition)})
            print(('PASS ' if condition else 'FAIL ') + name, flush=True)
            report = h.PRIVATE / 'intake-report.json'
            report.write_text(json.dumps({'checks': self.checks, 'synthetic_only': True}, indent=2))
            report.chmod(0o600)
            if not condition: raise AssertionError(name)
        def remember_invite(self, ident): pass
        def remember_account(self, ident): pass
        def user(self, label):
            password = secrets.token_urlsafe(24)
            user = h.api('/admin/users', {'email': label + '-' + self.suffix + '@example.invalid', 'password': password, 'balance': 0, 'concurrency': 10}, self.admin)
            user['token'] = h.api('/auth/login', {'email': user['email'], 'password': password})['access_token']
            return user
    fixture = Local()
    verify(fixture)
    print('Synthetic local HTTP checks passed: ' + str(len(fixture.checks)), flush=True)


if __name__ == '__main__':
    local_main()
