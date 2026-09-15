#!/usr/bin/env python3
"""Provision bounded, expiring downstream API credentials for staging checks.

Only dedicated acceptance users/keys are written. Supplier credentials and
production configuration are never exported or changed. Output omits secrets.
"""
import argparse
import json
from pathlib import Path
import secrets
import ssl
import urllib.request
import urllib.error
import certifi

GROUPS = {'stable':4, 'pro':27, 'claude':46, 'grok':88, 'cn':87}

def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--admin-key-file',required=True)
    parser.add_argument('--state',required=True)
    args=parser.parse_args()
    output=Path(args.state)
    state=json.loads(output.read_text()) if output.exists() else {'email':'stage-acceptance-20260916@example.invalid','password':secrets.token_urlsafe(28),'keys':{}}
    def save():
        output.parent.mkdir(parents=True,exist_ok=True,mode=0o700)
        with open(output,'w',opener=lambda p,f:__import__('os').open(p,f,0o600)) as f:json.dump(state,f)
        output.chmod(0o600)
    admin=Path(args.admin_key_file).read_text().strip()
    tls=ssl.create_default_context(cafile=certifi.where())
    def api(path,data=None,token=None,admin_auth=False):
        headers={'Content-Type':'application/json','User-Agent':'Geili-Acceptance/1.0'}
        if admin_auth:headers['x-api-key']=admin
        if token:headers['Authorization']='Bearer '+token
        req=urllib.request.Request('https://sub.geiliapi.com/api/v1'+path,data=None if data is None else json.dumps(data).encode(),headers=headers)
        try:
            with urllib.request.urlopen(req,context=tls,timeout=30) as response:body=json.load(response)
        except urllib.error.HTTPError as e:
            raise RuntimeError(f'{path}: HTTP {e.code}') from None
        return body.get('data',body)
    save()
    if 'user_id' not in state:
        found=api('/admin/users?search='+state['email'],admin_auth=True)
        users=found.get('items',[]) if isinstance(found,dict) else found
        matches=[u for u in users if u.get('email')==state['email']]
        if matches:user=matches[0]
        else:user=api('/admin/users',{'email':state['email'],'password':state['password'],'username':'Staging API acceptance','notes':'Dedicated acceptance fixture; 1 USD total synthetic balance, expiring downstream keys. No supplier secrets copied.','balance':1,'concurrency':2,'rpm_limit':10,'restrict_public_groups':True,'allowed_groups':list(GROUPS.values())},admin_auth=True)
        state['user_id']=user['id'];save()
    login=api('/auth/login',{'email':state['email'],'password':state['password']})
    token=login['access_token']
    for label,gid in GROUPS.items():
        if label in state['keys']:continue
        key=api('/keys',{'name':'stage-acceptance-'+label,'group_id':gid,'quota':.2,'expires_in_days':1},token=token)
        state['keys'][label]={'id':key['id'],'key':key['key'],'group_id':gid,'expires_at':key['expires_at'],'quota':key['quota']};save()
    print(json.dumps({'user_id':state['user_id'],'total_balance_budget':1,'keys':[{k:v[k] for k in ['id','group_id','quota','expires_at']} for v in state['keys'].values()]},ensure_ascii=False))

if __name__=='__main__':main()
