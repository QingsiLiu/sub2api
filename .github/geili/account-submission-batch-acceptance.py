"""Synthetic batch intake and per-member usage, exclusively loopback model calls."""
import json
import time
import uuid


def verify(f, public, make):
    for platform in ('anthropic', 'openai'):
        invite, config = make(platform, name='batch')
        token = invite['token']
        keys = ['synthetic-batch-' + str(n) + '-' + platform + '-' + f.suffix for n in range(3)]
        f.intake_canaries.extend(keys)
        body = {'token': token, 'api_keys': keys}
        rejected = public('submit/batch', {**body, 'api_keys': [keys[0], keys[0]]})
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
                'model_mapping': {model: model}}, 'group_ids': [group['id']], 'extra': {}, 'status': 'active'}, f.admin, 'PUT')
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
            return all(abs(actual[k] - value) < 1e-8 if k == 'reference_cost' else actual[k] == value for k, value in wanted.items())
        f.check(platform + ' batch keeps independently attributed 1/2/3 request member summaries', current[0] == 200 and len(measured['accounts']) == 3 and all(same(member['summary'], row) for member, row in zip(measured['accounts'], expected)))
        total = {k: sum(row[k] for row in expected) for k in expected[0]}
        f.check(platform + ' batch total equals all member reference costs and token dimensions', same(measured['summary'], total) and sum(row['requests'] for row in measured['daily']) == 6 and sum(row['requests'] for row in measured['models']) == 6)
        f.check(platform + ' nonzero batch projection hides credentials and capability', all(secret not in json.dumps(measured) for secret in keys + [token]))
        replay = public('submit/batch', {**body, 'api_keys': ['synthetic-replacement']}, secondary=bool(getattr(f, 'has_replica', False)))
        count = f.sql("SELECT json_build_object('n',count(*)) FROM account_submission_invites_geili WHERE id=" + str(ident) + ' OR parent_invite_id=' + str(ident) + ';')[0]['n']
        f.check(platform + ' batch retry across instances never creates or replaces members', replay[0] == 200 and count == 3)
        other, _ = make(platform, name='batch-foreign')
        other_key = 'synthetic-other-' + platform + '-' + f.suffix
        f.intake_canaries.append(other_key)
        public('submit/batch', {'token': other['token'], 'api_keys': [other_key]})
        f.check(platform + ' another invitation cannot address this member', public('connection', {'token': other['token'], 'account_ref': ref})[0] == 410)
        f.api('/admin/accounts/submission-invites/' + str(ident) + '/revoke-view', {}, f.admin)
        f.check(platform + ' revocation removes total and all member access', public('usage', {'token': token})[0] == 410 and public('connection', {'token': token, 'account_ref': ref})[0] == 410)
