#!/usr/bin/env python3
"""Bounded synthetic acceptance harness. Secrets stay in deploy/.secrets/."""
import argparse
import http.server
import json
import os
from pathlib import Path
import secrets
import subprocess
import threading
import time
import urllib.error
import urllib.request
import urllib.parse
import concurrent.futures

ROOT = Path(__file__).resolve().parents[2]
PRIVATE = ROOT / 'deploy/.secrets/acceptance'
PROJECT = 'geili-acceptance-local'
BASE = 'http://127.0.0.1:18489'
MOCK_CALLS = []

def run(args, **kw):
    return subprocess.run(args, check=True, capture_output=True, text=True, **kw).stdout

def request(path, data=None, token=None, method=None, base=BASE, raw=False):
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    req = urllib.request.Request(base + path, data=None if data is None else json.dumps(data).encode(), headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=180) as response:
            body = response.read().decode()
            return response.status, body if raw else json.loads(body)
    except urllib.error.HTTPError as err:
        body = err.read().decode()
        try: body = json.loads(body)
        except ValueError: pass
        return err.code, body

def api(path, data=None, token=None, method=None):
    status, body = request('/api/v1' + path, data, token, method)
    if status >= 400:
        raise RuntimeError(f'{method or ("POST" if data is not None else "GET")} {path}: HTTP {status}: {str(body)[:250]}')
    return body.get('data', body)

class Mock(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args): pass
    def do_GET(self):
        if self.path == '/__calls': return self.reply(MOCK_CALLS)
        self.reply({'object':'list','data':[{'id':'gpt-4.1-mini','object':'model'}]})
    def reply(self, obj, status=200):
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(json.dumps(obj).encode())
    def do_POST(self):
        body=json.loads(self.rfile.read(int(self.headers.get('Content-Length', 0))))
        model=body.get('model', '')
        MOCK_CALLS.append({'path':self.path, 'model':model})
        if '/down/' in self.path:
            return self.reply({'error':{'type':'server_error','message':'synthetic upstream failure'}},503)
        rid='mock-' + secrets.token_hex(6)
        if urllib.parse.urlsplit(self.path).path.endswith('/messages'):
            result={'id':rid,'type':'message','role':'assistant','model':model,'content':[{'type':'text','text':'acceptance ok'}], 'stop_reason':'end_turn','usage':{'input_tokens':1000,'output_tokens':100}}
        elif urllib.parse.urlsplit(self.path).path.endswith('/responses'):
            result={'id':rid,'object':'response','created_at':int(time.time()),'status':'completed','model':model,'output':[{'id':'msg-'+rid,'type':'message','role':'assistant','status':'completed','content':[{'type':'output_text','text':'acceptance ok','annotations':[]}]}], 'usage':{'input_tokens':1000,'output_tokens':100,'total_tokens':1100}}
        else:
            result={'id':rid,'object':'chat.completion','created':int(time.time()),'model':model,'choices':[{'index':0,'message':{'role':'assistant','content':'acceptance ok'},'finish_reason':'stop'}], 'usage':{'prompt_tokens':1000,'completion_tokens':100,'total_tokens':1100}}
        if body.get('stream'):
            self.send_response(200); self.send_header('Content-Type','text/event-stream'); self.end_headers()
            def event(name, data):
                payload=(('event: '+name+'\n') if name else '')+'data: '+json.dumps(data)+'\n\n'
                self.wfile.write(payload.encode()); self.wfile.flush()
            if urllib.parse.urlsplit(self.path).path.endswith('/messages'):
                event('message_start', {'type':'message_start','message':{**result,'content':[],'stop_reason':None,'usage':{'input_tokens':1000,'output_tokens':0}}})
                event('content_block_start',{'type':'content_block_start','index':0,'content_block':{'type':'text','text':''}})
                event('content_block_delta',{'type':'content_block_delta','index':0,'delta':{'type':'text_delta','text':'acceptance ok'}})
                event('content_block_stop',{'type':'content_block_stop','index':0})
                event('message_delta',{'type':'message_delta','delta':{'stop_reason':'end_turn'},'usage':{'output_tokens':100}})
                event('message_stop',{'type':'message_stop'})
            elif urllib.parse.urlsplit(self.path).path.endswith('/responses'):
                event('response.created',{'type':'response.created','response':{**result,'status':'in_progress','output':[],'usage':None}})
                event('response.output_text.delta',{'type':'response.output_text.delta','output_index':0,'content_index':0,'item_id':'msg-'+rid,'delta':'acceptance ok'})
                event('response.completed',{'type':'response.completed','response':result})
            else:
                event('',{**result,'object':'chat.completion.chunk','choices':[{'index':0,'delta':{'role':'assistant','content':'acceptance ok'},'finish_reason':None}],'usage':None})
                event('',{**result,'object':'chat.completion.chunk','choices':[{'index':0,'delta':{},'finish_reason':'stop'}]})
                self.wfile.write(b'data: [DONE]\n\n')
            return
        self.reply(result)

def local_env():
    PRIVATE.mkdir(parents=True, exist_ok=True, mode=0o700)
    path=PRIVATE/'local.json'
    if path.exists(): return json.loads(path.read_text())
    cfg={'password': secrets.token_urlsafe(24), 'admin_password': secrets.token_urlsafe(24)}
    path.write_text(json.dumps(cfg)); path.chmod(0o600)
    return cfg

def serve():
    cfg=local_env()
    pg_env=PRIVATE/'postgres.env'
    pg_env.write_text('POSTGRES_USER=acceptance\nPOSTGRES_DB=acceptance\nPOSTGRES_PASSWORD='+cfg['password']+'\n');pg_env.chmod(0o600)
    for name, args in [
        ('postgres', ['--env-file', str(pg_env), '-p','127.0.0.1:15439:5432','postgres:18-alpine']),
        ('redis', ['-p','127.0.0.1:16389:6379','redis:8-alpine'])]:
        cname=PROJECT+'-'+name
        if subprocess.run(['docker','inspect',cname],capture_output=True).returncode:
            run(['docker','run','-d','--name',cname,'--label','geili.acceptance=local']+args)
        else: run(['docker','start',cname])
    for _ in range(50):
        if subprocess.run(['docker','exec',PROJECT+'-postgres','pg_isready','-U','acceptance'],capture_output=True).returncode==0: break
        time.sleep(.2)
    mock=http.server.ThreadingHTTPServer(('127.0.0.1',18989), Mock)
    threading.Thread(target=mock.serve_forever,daemon=True).start()
    runtime=PRIVATE/'runtime';runtime.mkdir(exist_ok=True,mode=0o700)
    env={**os.environ, 'AUTO_SETUP':'true','SERVER_HOST':'127.0.0.1','SERVER_PORT':'18489','DATA_DIR':str(runtime),'DATABASE_HOST':'127.0.0.1','DATABASE_PORT':'15439','DATABASE_USER':'acceptance','DATABASE_DBNAME':'acceptance','DATABASE_PASSWORD':cfg['password'],'DATABASE_SSLMODE':'disable','REDIS_HOST':'127.0.0.1','REDIS_PORT':'16389','ADMIN_EMAIL':'admin@acceptance.invalid','ADMIN_PASSWORD':cfg['admin_password'],'JWT_SECRET':cfg['password']*2,'TOTP_ENCRYPTION_KEY':secrets.token_hex(32),'TZ':'Asia/Shanghai'}
    logfile=open(PRIVATE/'server.log','a')
    app=subprocess.Popen([str(ROOT/'backend/bin/acceptance-server')],cwd=runtime,env=env,stdout=logfile,stderr=subprocess.STDOUT)
    (PRIVATE/'app.pid').write_text(str(app.pid))
    print('Local acceptance server starting on '+BASE,flush=True)
    for _ in range(100):
        try:
            if request('/health')[0]==200:break
        except Exception:pass
        if app.poll() is not None:raise RuntimeError('Acceptance server failed; inspect private server.log')
        time.sleep(.2)
    # Isolated test fixture; no legal agreement is submitted on behalf of a user.
    label=run(['docker','inspect','--format','{{index .Config.Labels "geili.acceptance"}}',PROJECT+'-postgres']).strip()
    if label!='local':raise RuntimeError('Unexpected acceptance database container')
    run(['docker','exec','-i',PROJECT+'-postgres','psql','-X','-v','ON_ERROR_STOP=1','-U','acceptance','-d','acceptance'],input="INSERT INTO settings (key,value) SELECT 'admin_compliance_acknowledgement:' || id, '{\"version\":\"v2026.06.10\",\"user_agent\":\"isolated synthetic test fixture\"}' FROM users WHERE email='admin@acceptance.invalid' ON CONFLICT(key) DO NOTHING;")

    try: app.wait()
    finally: mock.shutdown();app.terminate()

def bootstrap():
    cfg=local_env()
    token=api('/auth/login',{'email':'admin@acceptance.invalid','password':cfg['admin_password']})['access_token']
    stamp=str(int(time.time()))
    groups={}
    for name,provider,model,rate in [('stable','openai','gpt-4.1-mini',1),('pro','openai','gpt-4.1-mini',3),('claude','anthropic','claude-sonnet-4-6',2),('grok','grok','grok-4.3',4),('deepseek','deepseek','deepseek-chat',.5)]:
        g=api('/admin/groups',{'name':'acceptance-'+name+'-'+stamp,'platform':provider,'rate_multiplier':.2,'subscription_rate_multiplier':rate,'model_pricing':[{'models':[model],'billing_mode':'token','input_price':0.000001,'output_price':0.000002,'cache_read_price':0.0000001}], 'long_context_pricing_enabled':False},token)
        groups[name]={'id':g['id'],'model':model,'platform':provider,'rate':rate}
        api('/admin/accounts',{'name':'acceptance-'+name+'-'+stamp,'platform':provider,'type':'apikey','credentials':{'api_key':'synthetic-test-key','base_url':'http://127.0.0.1:18989/'+name,'model_mapping':{model:model}},'group_ids':[g['id']],'concurrency':5,'priority':1,'rate_multiplier':1},token)
    facade=api('/admin/groups',{'name':'acceptance-all-'+stamp,'platform':'composite','subscription_type':'subscription','rate_multiplier':1,'subscription_rate_multiplier':1,'daily_limit_usd':10},token)
    for name,g in groups.items():
        api('/admin/groups/'+str(facade['id'])+'/composite-routes',{'public_model':g['model'],'match_type':'exact','target_platform':g['platform'],'target_group_id':g['id'],'profile_key':name,'upstream_model':g['model'],'enabled':True,'priority':20 if name=='pro' else 10},token)
    user=api('/admin/users',{'email':'acceptance-'+stamp+'@example.invalid','password':cfg['admin_password'],'balance':5,'concurrency':5},token)
    sub=api('/admin/subscriptions/assign',{'user_id':user['id'],'group_id':facade['id'],'validity_days':30},token)
    utoken=api('/auth/login',{'email':user['email'],'password':cfg['admin_password']})['access_token']
    key=api('/keys',{'name':'acceptance-universal','group_id':facade['id'],'subscription_id':sub['id'],'route_preferences':{'openai':'stable','anthropic':'claude','grok':'grok','deepseek':'deepseek'}},utoken)
    state={'admin_token':token,'user_token':utoken,'key':key,'sub':sub,'groups':groups,'facade':facade,'user':user}
    f=PRIVATE/'state.json';f.write_text(json.dumps(state));f.chmod(0o600)
    print('Synthetic users, groups, routes and subscription key created.',flush=True)
    for name,g in groups.items():
        if name=='pro': continue
        path='/v1/messages' if name=='claude' else '/v1/chat/completions'
        status,body=request(path,{'model':g['model'],'messages':[{'role':'user','content':'Reply OK'}],'max_tokens':16},key['key'])
        print(name, status, str(body)[:180],flush=True)

def sql(query):
    output=run(['docker','exec','-i',PROJECT+'-postgres','psql','-X','-At','-v','ON_ERROR_STOP=1','-U','acceptance','-d','acceptance'],input=query)
    return [json.loads(line) for line in output.splitlines() if line.startswith('{')]

def verify():
    state=json.loads((PRIVATE/'state.json').read_text())
    key=state['key'];groups=state['groups'];sid=state['sub']['id'];uid=state['user']['id'];fid=state['facade']['id']
    admin=state['admin_token'];user=state['user_token'];checks=[]
    def check(name, condition, detail=None):
        checks.append({'name':name,'passed':bool(condition),'detail':detail})
        print(('PASS ' if condition else 'FAIL ')+name,flush=True)
        (PRIVATE/'report.json').write_text(json.dumps(checks,indent=2))
        if not condition: raise AssertionError(name+': '+str(detail))
    def snapshot():
        return sql(f"SELECT json_build_object('used',daily_usage_usd,'balance',(SELECT balance FROM users WHERE id={uid})) FROM user_subscriptions WHERE id={sid};")[0]
    def lastlog(kid):
        return sql(f"SELECT row_to_json(x) FROM (SELECT id,model,group_id,subscription_id,total_cost,actual_cost,rate_multiplier,billing_type,route_billing_snapshot FROM usage_logs WHERE api_key_id={kid} ORDER BY id DESC LIMIT 1) x;")[0]
    def call(name, stream=False, credential=None, path=None):
        g=groups[name];path=path or ('/v1/messages' if name=='claude' else '/v1/chat/completions')
        before=sql('SELECT json_build_object(\'id\',COALESCE(MAX(id),0)) FROM usage_logs;')[0]['id']
        payload={'model':g['model'],'messages':[{'role':'user','content':'Reply OK'}],'max_tokens':16,'stream':stream}
        if path=='/v1/responses': payload={'model':g['model'],'input':'Reply OK','max_output_tokens':16,'stream':stream}
        status,body=request(path,payload,credential or key['key'],raw=stream)
        check(f'{name} {path} {"SSE" if stream else "JSON"} HTTP200',status==200,str(body)[:200])
        if stream: check(name+' stream content', 'acceptance ok' in body,body[:200])
        elif name=='claude': check('Anthropic response shape',body.get('type')=='message',body)
        for _ in range(50):
            row=sql('SELECT json_build_object(\'id\',COALESCE(MAX(id),0)) FROM usage_logs;')[0]
            if row['id']>before: break
            time.sleep(.1)
        return lastlog(key['id'])
    start=snapshot()
    for name in ['stable','claude','grok','deepseek']:
        row=call(name)
        check(name+' target group',row['group_id']==groups[name]['id'],row)
        check(name+' subscription multiplier',abs(row['actual_cost']-.0012*groups[name]['rate'])<1e-9,row)
        check(name+' same subscription',row['subscription_id']==sid,row)
        trace=row['route_billing_snapshot']
        check(name+' route billing snapshot',trace and trace['subscription_id']==sid and trace['target_group_id']==groups[name]['id'] and trace['route_id']>0 and trace['resolved_platform']==groups[name]['platform'] and abs(trace['raw_cost']*trace['effective_multiplier']-trace['actual_cost'])<1e-9,trace)
    expected=.0012*sum(groups[n]['rate'] for n in ['stable','claude','grok','deepseek'])
    after=snapshot();check('shared quota equals all providers',abs(after['used']-start['used']-expected)<1e-9,after)
    check('subscription leaves balance unchanged',after['balance']==start['balance'],after)
    balanceKey=api('/keys',{'name':'acceptance-balance','group_id':groups['stable']['id']},user)
    call('stable',credential=balanceKey['key'])
    row=lastlog(balanceKey['id']);check('balance multiplier independent after subscription cache',abs(row['actual_cost']-.00024)<1e-9 and row['subscription_id'] is None,row)
    prefs=key['route_preferences'].copy();prefs['openai']='pro'
    api('/keys/'+str(key['id']),{'route_preferences':prefs},user,'PUT')
    row=call('pro');check('console preference selects Pro pool and rate',row['group_id']==groups['pro']['id'] and abs(row['actual_cost']-.0036)<1e-9,row)
    routes=api('/admin/groups/'+str(fid)+'/composite-routes',token=admin)
    pro=next(r for r in routes if r['profile_key']=='pro')
    updated={**pro,'target_group_id':groups['stable']['id']}
    api('/admin/groups/'+str(fid)+'/composite-routes/'+str(pro['id']),updated,admin,'PUT')
    row=call('pro');check('route target edit persists',row['group_id']==groups['stable']['id'],row)
    api('/admin/groups/'+str(fid)+'/composite-routes/'+str(pro['id']),{**updated,'enabled':False},admin,'PUT')
    status,_=request('/v1/chat/completions',{'model':groups['stable']['model'],'messages':[{'role':'user','content':'OK'}]},key['key'])
    check('disabled selected profile fails closed',status==400,status)
    api('/admin/groups/'+str(fid)+'/composite-routes/'+str(pro['id']),pro,admin,'PUT')
    prefs['openai']='stable';api('/keys/'+str(key['id']),{'route_preferences':prefs},user,'PUT')
    for name in ['stable','claude','grok','deepseek']:
        row=call(name,True);check(name+' SSE billing',abs(row['actual_cost']-.0012*groups[name]['rate'])<1e-9,row)
    row=call('stable',True,path='/v1/responses');check('Responses SSE billing',abs(row['actual_cost']-.0012)<1e-9,row)
    api('/admin/groups/'+str(groups['stable']['id']),{'subscription_rate_multiplier':0},admin,'PUT')
    row=call('stable');check('explicit zero subscription multiplier',row['actual_cost']==0 and row['total_cost']>0,row)
    api('/admin/groups/'+str(groups['stable']['id']),{'subscription_rate_multiplier':1},admin,'PUT')
    api('/admin/groups/'+str(fid)+'/composite-routes',{'public_model':'mystery-unpriced-acceptance','match_type':'exact','target_platform':'openai','target_group_id':groups['stable']['id'],'upstream_model':'mystery-unpriced-acceptance','enabled':True},admin)
    status,_=request('/v1/chat/completions',{'model':'mystery-unpriced-acceptance','messages':[{'role':'user','content':'OK'}]},key['key'])
    check('unpriced model rejected before forwarding',status==400,status)
    status,_=request('/v1/chat/completions',{'model':'missing-model-acceptance','messages':[{'role':'user','content':'OK'}]},key['key'])
    check('unknown model rejected',status==400,status)
    before=snapshot()
    api('/admin/groups/'+str(fid),{'daily_limit_usd':before['used']-.00000001},admin,'PUT')
    status,_=request('/v1/chat/completions',{'model':groups['stable']['model'],'messages':[{'role':'user','content':'OK'}]},key['key'])
    check('exhausted shared quota blocks calls',status==429 or status==403,status)
    check('exhaustion does not charge balance',snapshot()==before,snapshot())
    api('/admin/groups/'+str(fid),{'daily_limit_usd':10},admin,'PUT')
    # Authentication rechecks the bound subscription rather than stale facade caches.
    sql(f"UPDATE user_subscriptions SET expires_at=NOW()-interval '1 second' WHERE id={sid};")
    status,_=request('/v1/chat/completions',{'model':groups['stable']['model'],'messages':[{'role':'user','content':'OK'}]},key['key'])
    check('expired pinned subscription denied',status==403,status)
    sql(f"UPDATE user_subscriptions SET expires_at=NOW()+interval '1 day' WHERE id={sid};")
    options=api('/groups/'+str(fid)+'/subscription-routes',token=user)
    check('user route dropdown exposes provider prices',len([x for x in options if x['profile_key']])==5,options)
    before=snapshot()
    payload={'model':groups['stable']['model'],'messages':[{'role':'user','content':'Reply OK'}],'max_tokens':16}
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
        results=list(pool.map(lambda _:request('/v1/chat/completions',payload,key['key'])[0],range(8)))
    check('eight concurrent calls succeed',results==[200]*8,results)
    for _ in range(100):
        after=snapshot()
        if abs(after['used']-before['used']-.0012*8)<1e-9:break
        time.sleep(.1)
    check('concurrent settlement has no lost or duplicate charges',abs(after['used']-before['used']-.0012*8)<1e-9,after)
    owner=api('/admin/groups',{'name':'acceptance-owner-'+str(int(time.time())),'platform':'openai','subscription_type':'subscription','rate_multiplier':.2,'subscription_rate_multiplier':1.7,'daily_limit_usd':10,'model_pricing':[{'models':['gpt-4.1-mini'],'input_price':.000001,'output_price':.000002}]},admin)
    sub2=api('/admin/subscriptions/assign',{'user_id':uid,'group_id':owner['id'],'validity_days':30},admin)
    sql(f"INSERT INTO user_subscription_groups (user_subscription_id,group_id) VALUES ({sub2['id']},{fid}) ON CONFLICT DO NOTHING;")
    status,_=request('/api/v1/keys',{'name':'ambiguous','group_id':fid},user)
    check('multiple subscriptions require explicit selection',status==400,status)
    secondKey=api('/keys',{'name':'second-plan','group_id':fid,'subscription_id':sub2['id']},user)
    before=snapshot();call('stable',credential=secondKey['key']);row=lastlog(secondKey['id'])
    check('selected second subscription owns the charge',row['subscription_id']==sub2['id'] and snapshot()==before,row)
    api('/admin/accounts',{'name':'legacy-pool-'+str(owner['id']),'platform':'openai','type':'apikey','credentials':{'api_key':'synthetic-test-key','base_url':'http://127.0.0.1:18989/legacy','model_mapping':{'gpt-4.1-mini':'gpt-4.1-mini'}},'group_ids':[owner['id']],'concurrency':5,'priority':1},admin)
    legacy=api('/keys',{'name':'legacy-single-provider','group_id':owner['id']},user)
    call('stable',credential=legacy['key']);row=lastlog(legacy['id'])
    check('legacy subscription key retains its quota owner and independent rate',row['subscription_id']==sub2['id'] and abs(row['actual_cost']-.0012*1.7)<1e-9,row)
    visible=api('/groups/available',token=user)
    check('new manual assignments grant unified facade visibility',any(g['name']=='全模型订阅' for g in visible))
    outsider=api('/admin/users',{'email':'outsider-'+str(owner['id'])+'@example.invalid','password':local_env()['admin_password'],'balance':0,'concurrency':1},admin)
    outsideToken=api('/auth/login',{'email':outsider['email'],'password':local_env()['admin_password']})['access_token']
    status,_=request('/api/v1/keys',{'name':'forged','group_id':fid,'subscription_id':sid},outsideToken)
    check('another user cannot bind someone else subscription',status>=400,status)
    sql(f"UPDATE user_subscriptions SET starts_at=NOW()+interval '1 day' WHERE id={sid};")
    status,_=request('/v1/chat/completions',payload,key['key'])
    check('future pinned subscription denied',status==403,status)
    sql(f"UPDATE user_subscriptions SET starts_at=NOW()-interval '1 day' WHERE id={sid};")
    # Force a fresh pool, with a failing first account and a working second one.
    retryGroup=api('/admin/groups',{'name':'retry-pool-'+str(owner['id']),'platform':'openai','rate_multiplier':.2,'subscription_rate_multiplier':1,'model_pricing':[{'models':['gpt-4.1-mini'],'input_price':.000001,'output_price':.000002}]},admin)
    for label,priority in [('down',1),('retry-ok',2)]:
        api('/admin/accounts',{'name':label+'-'+str(owner['id']),'platform':'openai','type':'apikey','credentials':{'api_key':'synthetic-test-key','base_url':'http://127.0.0.1:18989/'+label,'model_mapping':{'gpt-4.1-mini':'gpt-4.1-mini'}},'group_ids':[retryGroup['id']],'concurrency':5,'priority':priority},admin)
    stable=next(r for r in routes if r['profile_key']=='stable')
    api('/admin/groups/'+str(fid)+'/composite-routes/'+str(stable['id']),{**stable,'target_group_id':retryGroup['id']},admin,'PUT')
    before=snapshot();countBefore=len(request('/__calls',base='http://127.0.0.1:18989')[1])
    row=call('stable')
    calls=request('/__calls',base='http://127.0.0.1:18989')[1][countBefore:]
    check('upstream 503 switches account within selected pool',any('/down/' in x['path'] for x in calls) and any('/retry-ok/' in x['path'] for x in calls),calls)
    check('failover settles only successful request once',row['group_id']==retryGroup['id'] and abs(snapshot()['used']-before['used']-.0012)<1e-9,row)
    api('/admin/groups/'+str(fid)+'/composite-routes/'+str(stable['id']),stable,admin,'PUT')
    check('custom visual overlay removed',not (ROOT/'frontend/src/geili').exists())
    print(f'Acceptance complete: {len(checks)} checks passed',flush=True)

if __name__=='__main__':
    parser=argparse.ArgumentParser();parser.add_argument('command',choices=['serve','bootstrap','verify']);args=parser.parse_args()
    {'serve':serve,'bootstrap':bootstrap,'verify':verify}[args.command]()
