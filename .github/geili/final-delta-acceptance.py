#!/usr/bin/env python3
"""Local-only HTTP regression of the .2 -> .7 non-subscription changes.

Run after subscription-v2-comprehensive.py on an isolated acceptance_* database
and dedicated Redis. Only synthetic providers and accounts are used. Reports and
optional browser credentials stay under ignored deploy/.secrets/.
"""
import argparse
import importlib.util
import json
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('v2', Path(__file__).with_name('subscription-v2-acceptance.py'))
v2 = importlib.util.module_from_spec(spec)
spec.loader.exec_module(v2)


class DeltaFixture(v2.Fixture):
    def request(self, path, data=None, token=None, method=None):
        for attempt in range(5):
            result = super().request(path, data, token, method)
            if path != '/api/v1/auth/login' or result[0] != 429 or attempt == 4:
                return result
            print('WAIT synthetic login rate-limit window (20s)', flush=True)
            time.sleep(20)

    def balance(self, uid):
        return self.sql(f"SELECT json_build_object('balance',balance) FROM users WHERE id={int(uid)}")[0]['balance']


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', default='http://127.0.0.1:18494')
    parser.add_argument('--database', required=True)
    parser.add_argument('--project', default='geili-acceptance-local')
    parser.add_argument('--mock-port', type=int, default=19013)
    parser.add_argument('--keep-browser-fixture', action='store_true')
    args = parser.parse_args()
    f = DeltaFixture(args)
    f.start_mock()
    try:
        f.setup()
        verify(f)
        print('DELTA_REPORT ' + str(f.private / 'report.json'), flush=True)
        if args.keep_browser_fixture:
            print('Synthetic UI fixture ready; mock retained until interrupted.', flush=True)
            while True:
                time.sleep(1)
    finally:
        f.server.shutdown()
        f.server.server_close()


def verify(f):
    def group(label, panel, **extra):
        return f.api('/admin/groups', {'name': 'Final ' + label + ' ' + f.suffix, 'platform': 'openai',
            'usage_panel': panel, 'rate_multiplier': 1, 'subscription_rate_multiplier': 1,
            'subscription_enabled': True, 'model_allowlist': {'enabled': True, 'models': ['final-unpriced']}, **extra}, f.admin)

    old = group('GPT old', 'gpt')
    new = group('GPT new', 'gpt')
    cl = group('Claude', 'claude')
    cn = group('National', 'national')
    no_panel = group('No panel', '')
    exclusive = group('Exclusive', 'gpt', is_exclusive=True)
    user = f.user('final-browser')
    other = f.user('other-owner')
    f.api('/admin/users/' + str(user['id']) + '/balance', {'balance': 5, 'operation': 'set', 'notes': 'synthetic acceptance'}, f.admin)
    f.pay(user, f.quote(user, 'month45', 'purchase', units=2))
    sid = f.subscription(user)['id']
    key = f.api('/keys', {'name': 'Final composite balance', 'billing_source': 'balance', 'routing_mode': 'composite',
        'group_ids': [cl['id'], old['id'], cn['id']]}, user['token'])
    subkey = f.api('/keys', {'name': 'Final composite subscription', 'billing_source': 'subscription', 'subscription_id': sid,
        'routing_mode': 'composite', 'group_ids': [old['id'], cl['id']]}, user['token'])
    for target in (key, subkey):
        path = '/keys/' + str(target['id'])
        before = f.api(path, token=user['token'])
        f.api(path, {'group_id': new['id']}, user['token'], 'PUT')
        expected = [new['id']] + [i for i in before['group_ids'] if i != old['id']]
        read = f.api(path, token=user['token'])
        label = target['billing_source']
        f.check(label + ' panel replacement persists', read['group_ids'] == expected and not read.get('group_id'))
        f.check(label + ' preserves settlement owner', read['billing_source'] == before['billing_source'] and read.get('subscription_id') == before.get('subscription_id'))
        f.check(label + ' ordered group DTO', [x['id'] for x in read.get('groups', [])] == expected)
    for label, payload, token in (
        ('other owner', {'group_id': old['id']}, other['token']),
        ('exclusive', {'group_id': exclusive['id']}, user['token']),
        ('no panel', {'group_id': no_panel['id']}, user['token']),
        ('ambiguous', {'group_id': old['id'], 'group_ids': [old['id']]}, user['token']),
        ('duplicates', {'group_ids': [new['id'], new['id']]}, user['token']),
        ('empty', {'group_ids': []}, user['token']), ('negative', {'group_id': -1}, user['token']),
    ):
        path = '/keys/' + str(key['id'])
        before = f.api(path, token=user['token'])
        status, _, _ = f.request('/api/v1' + path, payload, token, 'PUT')
        after = f.api(path, token=user['token'])
        f.check(label + ' update rejected without mutation', 400 <= status < 500 and after['group_ids'] == before['group_ids'])
    for stream in (False, True):
        status, body, _ = f.request('/v1/responses', {'model': 'final-unpriced', 'input': 'synthetic', 'stream': stream}, key['key'])
        f.check('unpriced request rejected ' + str(stream), status == 400 and body.get('error', {}).get('code') == 'MODEL_PRICE_NOT_CONFIGURED')
    rows = []
    for _ in range(50):
        rows = f.sql(f"SELECT row_to_json(x) FROM (SELECT model,requested_model,request_type,group_id,account_id,upstream_endpoint,upstream_model,requested_group_ids FROM ops_error_logs WHERE api_key_id={key['id']} ORDER BY id) x")
        if len(rows) == 2:
            break
        time.sleep(.1)
    f.check('unpriced logs preserve intent', len(rows) == 2 and [x['request_type'] for x in rows] == [1, 2] and all(x['model'] == 'final-unpriced' and x['requested_group_ids'] == [new['id'], cl['id'], cn['id']] for x in rows))
    f.check('unpriced logs do not invent upstream dispatch', len(rows) == 2 and all(not x['upstream_endpoint'] and not x['upstream_model'] and x['account_id'] is None and x['group_id'] is None for x in rows))
    count = f.sql(f"SELECT json_build_object('n',count(*)) FROM usage_logs WHERE api_key_id={key['id']}")[0]['n']
    f.check('rejection does not charge', count == 0 and f.balance(user['id']) == 5)
    saved = f.api('/admin/settings', token=f.admin)
    try:
        f.check('anonymous admin denied', f.request('/api/v1/admin/settings')[0] == 401)
        f.check('user catalog write denied', f.request('/api/v1/admin/settings', {'openai_sync_model_ids': ['gpt-5.5']}, user['token'], 'PUT')[0] == 403)
        for label, value in [('empty', []), ('null', None), ('unknown', ['unknown-fixture']), ('duplicate', ['gpt-5.5', ' gpt-5.5 ']), ('wildcard', ['gpt-*']), ('type', 'gpt-5.5')]:
            status, _, _ = f.request('/api/v1/admin/settings', {'openai_sync_model_ids': value, 'site_name': 'MUST NOT WRITE'}, f.admin, 'PUT')
            read = f.api('/admin/settings', token=f.admin)
            f.check('invalid catalog atomic ' + label, status == 400 and read['site_name'] == saved['site_name'] and read['openai_sync_model_ids'] == saved['openai_sync_model_ids'])
        f.api('/admin/settings', {'openai_sync_model_ids': [' gpt-reserve ', 'gpt-5.5']}, f.admin, 'PUT')
        f.check('catalog trim and order', f.api('/admin/settings', token=f.admin)['openai_sync_model_ids'] == ['gpt-reserve', 'gpt-5.5'])
        f.api('/admin/settings', {'site_name': saved['site_name']}, f.admin, 'PUT')
        f.check('partial settings preserve catalog', f.api('/admin/settings', token=f.admin)['openai_sync_model_ids'] == ['gpt-reserve', 'gpt-5.5'])
    finally:
        f.api('/admin/settings', {'openai_sync_model_ids': saved['openai_sync_model_ids'], 'site_name': saved['site_name']}, f.admin, 'PUT')
    verify_zero_rates(f, user)
    credentials = ROOT / 'deploy/.secrets/final-acceptance-20260922/browser.json'
    credentials.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    credentials.write_text(json.dumps({'email': user['email'], 'password': user['fixture_password'], 'user_id': user['id'],
        'key_id': key['id'], 'subscription_key_id': subkey['id'], 'subscription_id': sid, 'plans': f.plans}))
    credentials.chmod(0o600)


def verify_zero_rates(f, user):
    group = f.api('/admin/groups', {'name': 'Free synthetic ' + f.suffix, 'platform': 'openai', 'rate_multiplier': 0,
        'subscription_rate_multiplier': 1, 'subscription_enabled': True,
        'model_pricing': [{'models': [v2.MODEL], 'billing_mode': 'token', 'input_price': .000001, 'output_price': .000002}]}, f.admin)
    f.check('create zero balance rate retains separate subscription rate', group['rate_multiplier'] == 0 and group['subscription_rate_multiplier'] == 1)
    f.api('/admin/accounts', {'name': 'Free mock ' + f.suffix, 'platform': 'openai', 'type': 'apikey',
        'credentials': {'api_key': 'synthetic-test-key', 'base_url': f.mock_base + '/upstream', 'model_mapping': {v2.MODEL: v2.MODEL}},
        'group_ids': [group['id']], 'concurrency': 5, 'priority': 1}, f.admin)
    key = f.api('/keys', {'name': 'Free balance fixture', 'billing_source': 'balance', 'routing_mode': 'single', 'group_id': group['id']}, user['token'])
    before_balance = f.balance(user['id'])
    before_sub = f.subscription(user)['daily_usage_usd']
    f.check('zero balance rate request succeeds', f.call(key)[0] == 200)
    rows = []
    for _ in range(50):
        rows = f.sql(f"SELECT row_to_json(x) FROM (SELECT total_cost,actual_cost,rate_multiplier FROM usage_logs WHERE api_key_id={key['id']}) x")
        if rows:
            break
        time.sleep(.1)
    f.check('zero rate records raw cost with no balance or subscription debit', len(rows) == 1 and rows[0]['total_cost'] > 0 and rows[0]['actual_cost'] == 0 and rows[0]['rate_multiplier'] == 0 and f.balance(user['id']) == before_balance and f.subscription(user)['daily_usage_usd'] == before_sub)
    for rate in (1, 0):
        updated = f.api('/admin/groups/' + str(group['id']), {'rate_multiplier': rate}, f.admin, 'PUT')
        f.check('update balance multiplier ' + str(rate), updated['rate_multiplier'] == rate and updated['subscription_rate_multiplier'] == 1)
    f.api('/admin/groups/' + str(group['id']) + '/rate-multipliers', {'entries': [{'user_id': user['id'], 'rate_multiplier': 0}]}, f.admin, 'PUT')
    f.check('batch user override accepts zero', True)
    f.api('/admin/users/' + str(user['id']), {'group_rates': {str(group['id']): 0}}, f.admin, 'PUT')
    updated = f.api('/admin/users/' + str(user['id']), token=f.admin)
    f.check('user override accepts zero', updated.get('group_rates', {}).get(str(group['id'])) == 0)
    for rate in (-1, -.001):
        status, _, _ = f.request('/api/v1/admin/groups/' + str(group['id']), {'rate_multiplier': rate}, f.admin, 'PUT')
        f.check('negative multiplier rejected as client error', status == 400)
    f.check('invalid multiplier preserves saved zero', f.api('/admin/groups/' + str(group['id']), token=f.admin)['rate_multiplier'] == 0)


if __name__ == '__main__':
    main()
