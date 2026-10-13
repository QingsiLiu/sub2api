"""Synthetic batch intake: scoped HTTP lifecycle, no supplier calls or activation."""
import json


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
