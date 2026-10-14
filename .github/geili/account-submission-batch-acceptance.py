"""Synthetic batch intake and per-member usage, exclusively loopback model calls."""
import json
import math
import time
import uuid


def verify(f, public, make):
    for platform in ('anthropic', 'openai'):
        invite, config = make(platform, name='batch')
        token = invite['token']
        keys = ['synthetic-batch-' + str(n) + '-' + platform + '-' + f.suffix for n in range(3)]
        f.intake_canaries.extend(keys)
        proxies = [
            {'name': 'Batch proxy A ' + platform + ' ' + f.suffix, 'protocol': 'http', 'host': '8.8.8.8', 'port': 8080, 'username': 'holder-a', 'password': 'synthetic-proxy-a-' + f.suffix},
            {'name': 'Batch proxy B ' + platform + ' ' + f.suffix, 'protocol': 'socks5', 'host': '1.1.1.1', 'port': 1080, 'username': 'holder-b', 'password': 'synthetic-proxy-b-' + f.suffix},
        ]
        f.intake_canaries.extend(proxy['password'] for proxy in proxies)
        body = {'token': token, 'items': [
            {'api_key': keys[0], 'connection': {'mode': 'custom', 'proxy': proxies[0]}},
            {'api_key': keys[1], 'connection': {'mode': 'custom', 'proxy': proxies[1]}},
            {'api_key': keys[2], 'connection': {'mode': 'direct'}},
        ]}
        rejected = public('submit/batch', {'token': token, 'api_keys': [keys[0], keys[0]]})
        f.check(platform + ' duplicate batch keys reject without consuming invite', rejected[0] == 400 and public('inspect', {'token': token})[1]['data']['status'] == 'pending')
        result = public('submit/batch', body)
        f.check(platform + ' batch saves atomically', result[0] == 200)
        ident = invite['invite']['id']
        records = f.sql("SELECT json_build_object('id',i.id,'account_id',a.id,'status',a.status,'schedulable',a.schedulable,'base_url',a.credentials->>'base_url','key_ok',a.credentials->>'api_key' LIKE 'synthetic-batch-%') FROM account_submission_invites_geili i JOIN accounts a ON a.id=i.account_id WHERE i.id=" + str(ident) + ' OR i.parent_invite_id=' + str(ident) + ' ORDER BY i.id;')
        for row in records:
            f.remember_account(row['account_id'])
        official = 'https://api.anthropic.com' if platform == 'anthropic' else 'https://api.openai.com'
        f.check(platform + ' all batch members remain paused with official URLs', len(records) == 3 and all(row['status'] == 'inactive' and row['schedulable'] is False and row['base_url'] == official and row['key_ok'] for row in records))
        usage = public('usage', {'token': token})
        data = usage[1]['data']
        f.check(platform + ' batch exposes three zero-usage summaries and total without secrets', usage[0] == 200 and len(data['accounts']) == 3 and data['summary']['requests'] == 0 and all(row['summary']['requests'] == 0 for row in data['accounts']) and all(secret not in json.dumps(data) for secret in keys + [token]))
        listing = f.api('/admin/accounts/submission-invites?page=1&page_size=100', token=f.admin)
        root = next(row for row in listing['items'] if row['id'] == ident)
        f.check(platform + ' history shows one invitation with three accounts', root['account_count'] == 3 and all(row['id'] not in [record['id'] for record in records[1:]] for row in listing['items']))
        ids = ','.join(str(row['account_id']) for row in records)
        exported = f.api('/admin/accounts/data?ids=' + ids + '&include_proxies=false', token=f.admin)
        f.check(platform + ' all batch credentials retain export protection', exported.get('skipped_external') == 3 and not exported['accounts'])
        states = [public('connection', {'token': token, 'account_ref': member['account_ref']})[1]['data'] for member in data['accounts']]
        f.check(platform + ' each key binds its own proxy with passwords hidden', [state['mode'] for state in states] == ['custom', 'custom', 'direct'] and states[0]['proxy']['host'] == '8.8.8.8' and states[1]['proxy']['host'] == '1.1.1.1' and all(proxy['password'] not in json.dumps(states) for proxy in proxies))
        ref = data['accounts'][1]['account_ref']
        f.check(platform + ' opaque member selector reads its own connection', public('connection', {'token': token, 'account_ref': ref})[0] == 200)
        saved = public('connection/update', {'token': token, 'account_ref': ref, 'version': 0,
            'request_id': str(uuid.uuid4()), 'connection': {'mode': 'direct'}})
        versions = [public('connection', {'token': token, 'account_ref': member['account_ref']})[1]['data']['version'] for member in data['accounts']]
        f.check(platform + ' member connection write changes only the selected member', saved[0] == 200 and versions == [0, 1, 0])
        # Each member gets its own isolated synthetic group, so every recorded
        # request can be attributed without relying on scheduler randomness.
        user = f.user('batch-' + platform)
        f.api('/admin/users/' + str(user['id']) + '/balance', {'balance': 5, 'operation': 'set', 'notes': 'Synthetic batch usage acceptance'}, f.admin)
        model = 'claude-sonnet-4-6' if platform == 'anthropic' else 'gpt-4.1-mini'
        expected = []
        for index, record in enumerate(records):
            group = f.api('/admin/groups', {'name': 'Batch ' + platform + str(index) + ' ' + f.suffix,
                'platform': platform, 'rate_multiplier': 1, 'subscription_rate_multiplier': 1,
                'long_context_pricing_enabled': False, 'model_pricing': [{'models': [model],
                    'billing_mode': 'token', 'input_price': .000001, 'output_price': .000002}]}, f.admin)
            account = record['account_id']
            f.api('/admin/accounts/' + str(account), {'credentials': {'base_url': f.mock_base + '/upstream',
                'model_mapping': {model: model}}, 'group_ids': [group['id']], 'proxy_id': 0, 'extra': {}, 'status': 'active'}, f.admin, 'PUT')
            f.api('/admin/accounts/' + str(account) + '/schedulable', {'schedulable': True}, f.admin)
            key = f.api('/keys', {'name': 'Batch usage ' + str(index), 'billing_source': 'balance',
                'routing_mode': 'single', 'group_id': group['id']}, user['token'])
            for _ in range(index + 1):
                response = f.request('/v1/messages' if platform == 'anthropic' else '/v1/chat/completions',
                    {'model': model, 'messages': [{'role': 'user', 'content': 'Synthetic batch usage'}], 'max_tokens': 16}, key['key'])
                f.check(platform + ' member ' + str(index) + ' loopback inference succeeds ' + str(_), response[0] == 200)
            deadline = time.monotonic() + 15
            while True:
                row = f.sql("SELECT json_build_object('requests',count(*),'reference_cost',coalesce(sum(total_cost),0)," +
                    "'input_tokens',coalesce(sum(input_tokens),0),'output_tokens',coalesce(sum(output_tokens),0)," +
                    "'cache_creation_tokens',coalesce(sum(cache_creation_tokens),0),'cache_read_tokens',coalesce(sum(cache_read_tokens),0)) FROM usage_logs WHERE account_id=" + str(account) + ';')[0]
                if row['requests'] == index + 1 or time.monotonic() >= deadline:
                    break
                time.sleep(.1)
            f.check(platform + ' member ' + str(index) + ' settles each request once with positive reference cost despite zero account multiplier', row['requests'] == index + 1 and row['reference_cost'] > 0)
            expected.append(row)
            f.api('/admin/accounts/' + str(account), {'status': 'inactive'}, f.admin, 'PUT')
        current = public('usage', {'token': token}, secondary=bool(getattr(f, 'has_replica', False)))
        measured = current[1]['data']
        def same(actual, wanted):
            return all(math.isclose(actual[k], value, rel_tol=1e-9, abs_tol=1e-15) if k == 'reference_cost' else actual[k] == value for k, value in wanted.items())
        f.check(platform + ' batch keeps independently attributed 1/2/3 request member summaries', current[0] == 200 and len(measured['accounts']) == 3 and all(same(member['summary'], row) for member, row in zip(measured['accounts'], expected)))
        total = {k: sum(row[k] for row in expected) for k in expected[0]}
        f.check(platform + ' batch total equals all member reference costs and token dimensions', same(measured['summary'], total) and sum(row['requests'] for row in measured['daily']) == 6 and sum(row['requests'] for row in measured['models']) == 6)
        f.check(platform + ' nonzero batch projection hides credentials and capability', all(secret not in json.dumps(measured) for secret in keys + [token]))
        replay = public('submit/batch', {'token': token, 'api_keys': ['synthetic-replacement']}, secondary=bool(getattr(f, 'has_replica', False)))
        count = f.sql("SELECT json_build_object('n',count(*)) FROM account_submission_invites_geili WHERE id=" + str(ident) + ' OR parent_invite_id=' + str(ident) + ';')[0]['n']
        f.check(platform + ' batch retry across instances never creates or replaces members', replay[0] == 200 and count == 3)
        other, _ = make(platform, name='batch-foreign')
        other_key = 'synthetic-other-' + platform + '-' + f.suffix
        f.intake_canaries.append(other_key)
        public('submit/batch', {'token': other['token'], 'api_keys': [other_key]})
        f.check(platform + ' another invitation cannot address this member', public('connection', {'token': other['token'], 'account_ref': ref})[0] == 410)
        f.api('/admin/accounts/submission-invites/' + str(ident) + '/revoke-view', {}, f.admin)
        f.check(platform + ' revocation removes total and all member access', public('usage', {'token': token})[0] == 410 and public('connection', {'token': token, 'account_ref': ref})[0] == 410)


        # One complete advanced template, with different credentials and public
        # connection drafts per member; no supplier inference is needed here.
        advanced, _ = make(platform, name='advanced-batch')
        advanced_token = advanced['token']
        advanced_keys = ['synthetic-advanced-' + str(n) + '-' + f.suffix for n in range(2)]
        f.intake_canaries.extend(advanced_keys)
        payload = {'name': 'Advanced batch ' + platform + ' ' + f.suffix, 'notes': 'Shared advanced notes',
            'platform': platform, 'type': 'apikey', 'concurrency': 17, 'priority': 7, 'rate_multiplier': 0,
            'load_factor': 19, 'auto_pause_on_expired': False, 'extra': {'quota_daily_limit': 7},
            'credentials': {'base_url': official, 'api_key': advanced_keys[0], 'model_mapping': {'public': 'upstream'}}}
        form = {'token': advanced_token, 'action': 'create-batch', 'payload': payload,
            'items': [{'api_key': key, 'connection': {'mode': 'custom', 'proxy': proxies[n]}} for n, key in enumerate(advanced_keys)]}
        f.check(platform + ' advanced form saves multiple keys in one call', public('form', form)[0] == 200)
        advanced_id = advanced['invite']['id']
        saved = f.sql("SELECT json_build_object('account_id',a.id,'key',a.credentials->>'api_key','base_url',a.credentials->>'base_url', 'mapping',a.credentials->'model_mapping','notes',a.notes,'concurrency',a.concurrency,'priority',a.priority,'multiplier',a.rate_multiplier,'load_factor',a.load_factor,'auto_pause',a.auto_pause_on_expired,'daily_quota',a.extra->'quota_daily_limit','status',a.status,'enabled',a.schedulable,'host',p.host) FROM account_submission_invites_geili i JOIN accounts a ON a.id=i.account_id JOIN proxies p ON p.id=a.proxy_id WHERE i.id=" + str(advanced_id) + ' OR i.parent_invite_id=' + str(advanced_id) + ' ORDER BY i.id;')
        for row in saved:
            f.remember_account(row['account_id'])
        f.check(platform + ' advanced batch retains every shared setting and its own key proxy', len(saved) == 2 and all(row['key'] == advanced_keys[n] and row['host'] == proxies[n]['host'] and row['base_url'] == official and row['mapping'] == {'public': 'upstream'} and row['notes'] == 'Shared advanced notes' and row['concurrency'] == 17 and row['priority'] == 7 and row['multiplier'] == 0 and row['load_factor'] == 19 and row['auto_pause'] is False and row['daily_quota'] == 7 and row['status'] == 'inactive' and row['enabled'] is False for n, row in enumerate(saved)))
        f.check(platform + ' advanced form replay across instances does not replace any key', public('form', form, secondary=bool(getattr(f, 'has_replica', False)))[0] == 200)
        aggregate = public('usage', {'token': advanced_token})
        f.check(platform + ' advanced batch exposes both usage rows without secrets', aggregate[0] == 200 and len(aggregate[1]['data']['accounts']) == 2 and all(secret not in json.dumps(aggregate[1]) for secret in advanced_keys + [advanced_token]))
