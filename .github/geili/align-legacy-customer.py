#!/usr/bin/env python3
"""Preview/backup or apply one explicitly reviewed legacy expiry alignment.

Credentials are read from SUB2API_ADMIN_API_KEY / SUB2API_JWT, never argv.
Preview writes a mode0600 backup under deploy/.secrets/legacy-alignment.
Apply requires that exact preview file; changed/expired rights fail closed.
No account identifiers are hardcoded; no automatic bulk alignment exists.
"""
import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import urllib.error
import urllib.parse
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[2]
PRIVATE = ROOT / 'deploy/.secrets/legacy-alignment'

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', required=True)
    parser.add_argument('--subscription-id', required=True, type=int)
    parser.add_argument('--user-id', type=int)
    parser.add_argument('--entitlement-ids', nargs='+', type=int)
    parser.add_argument('--reason')
    parser.add_argument('--apply-preview', type=Path)
    args = parser.parse_args()
    base = args.base.rstrip('/')
    url = urllib.parse.urlsplit(base)
    if url.username or url.password or url.query or url.fragment or (url.scheme != 'https' and not (url.scheme == 'http' and url.hostname in ('localhost','127.0.0.1'))):
        parser.error('Use an HTTPS API origin or explicit localhost')
    headers = {'Content-Type': 'application/json'}
    key = os.environ.get('SUB2API_ADMIN_API_KEY')
    jwt = os.environ.get('SUB2API_JWT')
    if key:
        headers['x-api-key'] = key
    elif jwt:
        headers['Authorization'] = 'Bearer ' + jwt
    else:
        parser.error('Set an administrator credential in the environment')
    if args.subscription_id <= 0:
        parser.error('Positive subscription ID required')
    if args.apply_preview:
        path = args.apply_preview.resolve(strict=True)
        if not path.is_relative_to(PRIVATE.resolve()) or path.stat().st_mode & 0o077:
            parser.error('Preview must be a private mode0600 file under deploy/.secrets/legacy-alignment')
        saved = json.loads(path.read_text())
        if saved['base'] != base or saved['subscription_id'] != args.subscription_id:
            parser.error('Preview origin/subscription mismatch')
        request = {**saved['request'], 'apply': True, 'expected_snapshot': saved['result']['snapshot']}
    else:
        if not args.user_id or not args.entitlement_ids or len(args.entitlement_ids)<2 or not args.reason:
            parser.error('Preview requires owner, at least two entitlement IDs and reason')
        request = {'user_id':args.user_id, 'entitlement_ids':args.entitlement_ids,
                   'reason':args.reason, 'idempotency_key':str(uuid.uuid4()), 'apply':False}
    req = urllib.request.Request(base + '/api/v1/admin/subscriptions/' + str(args.subscription_id) + '/align-legacy', data=json.dumps(request).encode(), headers=headers)
    try:
        with urllib.request.build_opener(NoRedirect).open(req, timeout=30) as response:
            result = json.load(response)['data']
    except urllib.error.HTTPError as exc:
        raise SystemExit('Alignment rejected: HTTP '+str(exc.code)+'; inspect authenticated admin response, do not alter the saved preview') from None
    if not args.apply_preview:
        PRIVATE.mkdir(mode=0o700, parents=True, exist_ok=True)
        path = PRIVATE / (str(args.subscription_id)+'-'+datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ')+'-'+request['idempotency_key']+'.json')
        with os.fdopen(os.open(path, os.O_CREAT|os.O_EXCL|os.O_WRONLY, 0o600),'w') as out:
            json.dump({'base':base,'subscription_id':args.subscription_id,'request':request,'result':result},out,ensure_ascii=False,indent=2)
        print('Preview backup: '+str(path))
    else:
        output = path.with_suffix('.result.json')
        with os.fdopen(os.open(output,os.O_CREAT|os.O_TRUNC|os.O_WRONLY,0o600),'w') as out:
            json.dump(result,out,ensure_ascii=False,indent=2)
    print(json.dumps(result,ensure_ascii=False,indent=2))

if __name__ == '__main__':
    main()
