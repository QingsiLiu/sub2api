#!/usr/bin/env python3
"""Isolated HTTP acceptance for explicit settlement and plan-owned subscriptions.
All credentials and fixtures live only under deploy/.secrets/.
"""
import importlib.util,json,os,secrets,subprocess,threading,time,argparse
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime
from pathlib import Path

ROOT=Path(__file__).resolve().parents[2]
spec=importlib.util.spec_from_file_location('base_acceptance',Path(__file__).with_name('acceptance.py'))
base=importlib.util.module_from_spec(spec);spec.loader.exec_module(base)
PRIVATE=ROOT/'deploy/.secrets'/('decoupled-acceptance-'+os.getenv('GEILI_DECOUPLED_DB','acceptance_decoupled'))
BASE=os.getenv('GEILI_DECOUPLED_BASE','http://127.0.0.1:18492')
MOCK_PORT=int(os.getenv('GEILI_DECOUPLED_MOCK_PORT','18992'))
PG=base.PROJECT+'-postgres'
DB=os.getenv('GEILI_DECOUPLED_DB','acceptance_decoupled')
assert DB.replace('_','').isalnum(), 'invalid synthetic database name'
PRIVATE.mkdir(parents=True,exist_ok=True,mode=0o700)

def save(name,value):
 p=PRIVATE/name;p.write_text(json.dumps(value,ensure_ascii=False,indent=2));p.chmod(0o600)
def sql(query):
 assert base.run(['docker','inspect','--format','{{index .Config.Labels "geili.acceptance"}}',PG]).strip()=='local'
 output=base.run(['docker','exec','-i',PG,'psql','-XAt','-v','ON_ERROR_STOP=1','-U','acceptance','-d',DB],input=query)
 return [json.loads(line) for line in output.splitlines() if line.startswith('{')]
def request(path,data=None,token=None,method=None,raw=False):return base.request(path,data,token,method,base=BASE,raw=raw)
def api(path,data=None,token=None,method=None):
 status,body=request('/api/v1'+path,data,token,method)
 if status>=400:raise RuntimeError(f'{path} HTTP{status}: {str(body)[:350]}')
 return body.get('data',body)
def admin():
 login={'password':base.local_env()['admin_password']}
 return api('/auth/login',{'email':'admin@subscription-lab.invalid','password':login['password']})['access_token']

class DecoupledMock(base.Mock):
 def do_POST(self):
  if '/bad400/' in self.path or '/partial/' in self.path:
   body=json.loads(self.rfile.read(int(self.headers.get('Content-Length',0))))
   base.MOCK_CALLS.append({'path':self.path,'model':body.get('model')})
   if '/bad400/' in self.path:return self.reply({'error':{'type':'invalid_request_error','message':'synthetic invalid parameters'}},400)
   self.send_response(200);self.send_header('Content-Type','text/event-stream');self.end_headers()
   chunk={'id':'partial-'+secrets.token_hex(4),'object':'chat.completion.chunk','model':body.get('model'),'choices':[{'index':0,'delta':{'content':'partial content'},'finish_reason':None}]}
   self.wfile.write(('data: '+json.dumps(chunk)+'\n\n').encode());self.wfile.flush();return
  super().do_POST()

def serve():
 cfg=json.loads((ROOT/'deploy/.secrets/acceptance/local.json').read_text())
 login={'password':base.local_env()['admin_password']}
 # This is a fresh, isolated synthetic database; never clone production data.
 assert base.run(['docker','inspect','--format','{{index .Config.Labels "geili.acceptance"}}',PG]).strip()=='local'
 present=base.run(['docker','exec',PG,'psql','-XAt','-U','acceptance','-d','acceptance','-c',f"SELECT 1 FROM pg_database WHERE datname='{DB}'"]).strip()
 if present!='1':base.run(['docker','exec',PG,'createdb','-U','acceptance',DB])
 mock=base.http.server.ThreadingHTTPServer(('127.0.0.1',MOCK_PORT),DecoupledMock)
 threading.Thread(target=mock.serve_forever,daemon=True).start()
 runtime=PRIVATE/('runtime-'+DB);runtime.mkdir(exist_ok=True,mode=0o700)
 env={**os.environ,'AUTO_SETUP':'true','SERVER_HOST':'127.0.0.1','SERVER_PORT':str(base.urllib.parse.urlparse(BASE).port),'DATA_DIR':str(runtime),'DATABASE_HOST':'127.0.0.1','DATABASE_PORT':str(base.PG_PORT),'DATABASE_USER':'acceptance','DATABASE_DBNAME':DB,'DATABASE_PASSWORD':cfg['password'],'DATABASE_SSLMODE':'disable','REDIS_HOST':'127.0.0.1','REDIS_PORT':str(base.REDIS_PORT),'REDIS_DB':'5','ADMIN_EMAIL':'admin@subscription-lab.invalid','ADMIN_PASSWORD':login['password'],'JWT_SECRET':cfg['password']*2,'TOTP_ENCRYPTION_KEY':secrets.token_hex(32),'TZ':'Asia/Shanghai'}
 app=subprocess.Popen([str(ROOT/'backend/bin/decoupled-server')],cwd=runtime,env=env,stdout=open(PRIVATE/'server.log','a'),stderr=subprocess.STDOUT)
 (PRIVATE/'app.pid').write_text(str(app.pid))
 for _ in range(150):
  try:
   if request('/health')[0]==200:break
  except Exception:pass
  if app.poll() is not None:raise RuntimeError('Local decoupled server failed; inspect private server.log')
  time.sleep(.2)
 # Seed meaningful old records for compatibility checks (migration sequencing is covered by PostgreSQL integration tests). Idempotent on restart.
 sql("""
 INSERT INTO groups(name,platform,subscription_type,rate_multiplier,daily_limit_usd,status)
 SELECT 'decoupling-legacy-fixture','openai','subscription',0.7,10,'active'
 WHERE NOT EXISTS(SELECT 1 FROM groups WHERE name='decoupling-legacy-fixture');
 INSERT INTO users(email,password_hash,role,status,balance,concurrency)
 SELECT 'decoupling-legacy@example.invalid',password_hash,'user','active',1,5 FROM users
 WHERE email='admin@subscription-lab.invalid' AND NOT EXISTS(SELECT 1 FROM users WHERE email='decoupling-legacy@example.invalid');
 INSERT INTO subscription_plans(group_id,name,description,price,validity_days,validity_unit,features,product_name,for_sale,sort_order)
 SELECT id,'Legacy quota product','migration fixture',9,30,'day','','',false,0 FROM groups WHERE name='decoupling-legacy-fixture'
 AND NOT EXISTS(SELECT 1 FROM subscription_plans WHERE name='Legacy quota product');
 INSERT INTO user_subscriptions(user_id,group_id,starts_at,expires_at,status,daily_usage_usd,weekly_usage_usd,monthly_usage_usd)
 SELECT u.id,g.id,NOW(),NOW()+INTERVAL '30 days','active',0.125,0.125,0.125 FROM users u CROSS JOIN groups g
 WHERE u.email='decoupling-legacy@example.invalid' AND g.name='decoupling-legacy-fixture'
 AND NOT EXISTS(SELECT 1 FROM user_subscriptions us WHERE us.user_id=u.id AND us.group_id=g.id);
 INSERT INTO api_keys(user_id,group_id,name,key,status,billing_source)
 SELECT u.id,g.id,'Legacy unchanged','sk-decoupling-legacy-fixture','active','balance' FROM users u CROSS JOIN groups g
 WHERE u.email='decoupling-legacy@example.invalid' AND g.name='decoupling-legacy-fixture'
 AND NOT EXISTS(SELECT 1 FROM api_keys WHERE key='sk-decoupling-legacy-fixture');
 """)
 old=sql("SELECT json_build_object('id',us.id,'expires_at',us.expires_at,'starts_at',us.starts_at,'daily_usage',us.daily_usage_usd) FROM user_subscriptions us JOIN users u ON u.id=us.user_id WHERE u.email='decoupling-legacy@example.invalid';")
 if not (PRIVATE/'pre-migration.json').exists():save('pre-migration.json',old)
 sql("INSERT INTO settings(key,value) SELECT 'admin_compliance_acknowledgement:'||id,'{\"version\":\"v2026.06.10\",\"user_agent\":\"isolated synthetic test fixture\"}' FROM users WHERE email='admin@subscription-lab.invalid' ON CONFLICT(key) DO NOTHING;")
 print('Decoupled acceptance application and mock ready on '+BASE,flush=True)
 try:app.wait()
 finally:mock.shutdown();app.terminate()

def bootstrap():
 token=admin();stamp=str(int(time.time()));groups={}
 for name,model,rate in [('gpt','gpt-4.1-mini',1.1),('pro','gpt-4.1-mini',1.3),('cn','neutral-custom-national',.8),('down','gpt-4.1-mini',4),('disabled','gpt-4.1-mini',1)]:
  g=api('/admin/groups',{'name':'decoupled-'+name+'-'+stamp,'platform':'openai','rate_multiplier':.2,'subscription_rate_multiplier':rate,'subscription_enabled':name!='disabled','long_context_pricing_enabled':False,'model_pricing':[{'models':[model],'billing_mode':'token','input_price':.000001,'output_price':.000002}]},token)
  account=api('/admin/accounts',{'name':'decoupled-'+name+'-'+stamp,'platform':'openai','type':'apikey','credentials':{'api_key':'synthetic-test-key','base_url':f'http://127.0.0.1:{MOCK_PORT}/'+name,'model_mapping':{model:model}},'group_ids':[g['id']],'concurrency':5,'priority':1},token)
  groups[name]={'id':g['id'],'account_id':account['id'],'model':model,'rate':rate}
 plan=api('/admin/payment/plans',{'name':'Independent quota '+stamp,'description':'synthetic fixture','price':10,'daily_limit_usd':1,'weekly_limit_usd':5,'monthly_limit_usd':20,'validity_days':30,'validity_unit':'day','for_sale':True},token)
 password=secrets.token_urlsafe(24)
 user=api('/admin/users',{'email':'decoupled-'+stamp+'@example.invalid','password':password,'balance':5,'concurrency':8},token)
 sub=api('/admin/subscriptions/assign',{'user_id':user['id'],'plan_id':plan['id']},token)
 utoken=api('/auth/login',{'email':user['email'],'password':password})['access_token']
 single=api('/keys',{'name':'Explicit subscription single','billing_source':'subscription','routing_mode':'single','subscription_id':sub['id'],'group_id':groups['gpt']['id']},utoken)
 multi=api('/keys',{'name':'Explicit subscription multi','billing_source':'subscription','routing_mode':'composite','subscription_id':sub['id'],'group_ids':[groups['gpt']['id'],groups['cn']['id']]},utoken)
 balance=api('/keys',{'name':'Explicit balance','billing_source':'balance','routing_mode':'single','group_id':groups['gpt']['id']},utoken)
 save('state.json',{'admin':token,'user_token':utoken,'user':user,'password':password,'plan':plan,'subscription':sub,'groups':groups,'single':single,'multi':multi,'balance':balance})
 print(json.dumps({'user_id':user['id'],'plan_id':plan['id'],'subscription_id':sub['id'],'key_ids':[single['id'],multi['id'],balance['id']]},ensure_ascii=False),flush=True)

def verify():
 s=json.loads((PRIVATE/'state.json').read_text());checks=[];sid=s['subscription']['id'];uid=s['user']['id'];groups=s['groups'];token=s['admin'];user=s['user_token']
 def check(name,ok,detail=None):
  checks.append({'name':name,'passed':bool(ok),'detail':detail});save('report.json',checks);print(('PASS ' if ok else 'FAIL ')+name,flush=True)
  if not ok:raise AssertionError(name+': '+str(detail)[:600])
 def snap():return sql(f"SELECT json_build_object('used',daily_usage_usd,'balance',(SELECT balance FROM users WHERE id={uid})) FROM user_subscriptions WHERE id={sid};")[0]
 def call(key,label,model,rate,stream=False):
  before=snap();count=sql(f"SELECT json_build_object('n',count(*)) FROM usage_logs WHERE api_key_id={key['id']};")[0]['n']
  status,body=request('/v1/chat/completions',{'model':model,'messages':[{'role':'user','content':'Reply OK'}],'max_tokens':16,'stream':stream},key['key'],raw=stream)
  check(label+' HTTP200',status==200,str(body)[:250])
  if stream:check(label+' stream content','acceptance ok' in body and '[DONE]' in body)
  for _ in range(100):
   rows=sql(f"SELECT row_to_json(x) FROM (SELECT id,subscription_id,group_id,total_cost,actual_cost,rate_multiplier,route_billing_snapshot FROM usage_logs WHERE api_key_id={key['id']} ORDER BY id DESC LIMIT 1) x;")
   newCount=sql(f"SELECT json_build_object('n',count(*)) FROM usage_logs WHERE api_key_id={key['id']};")[0]['n']
   if newCount>count:break
   time.sleep(.1)
  check(label+' exactly one record',newCount==count+1,newCount)
  row=rows[0];check(label+' multiplier',abs(row['actual_cost']-.0012*rate)<1e-9,row)
  after=snap()
  if key['billing_source']=='subscription':check(label+' shared quota owner',row['subscription_id']==sid and abs(after['used']-before['used']-row['actual_cost'])<1e-9 and after['balance']==before['balance'],row)
  else:check(label+' balance only',row['subscription_id'] is None and after['used']==before['used'] and abs(before['balance']-after['balance']-row['actual_cost'])<1e-9,row)
  return row
 api('/keys/'+str(s['multi']['id']),{'group_ids':[groups['gpt']['id'],groups['cn']['id']]},user,'PUT')
 check('plan has no group',s['plan'].get('group_id') is None,s['plan'].get('group_id'))
 check('subscription is owned by plan',s['subscription'].get('plan_id')==s['plan']['id'] and not s['subscription'].get('group_id'),s['subscription'])
 call(s['single'],'single group subscription',groups['gpt']['model'],1.1)
 call(s['balance'],'same group balance',groups['gpt']['model'],.2)
 call(s['multi'],'GPT multi group',groups['gpt']['model'],1.1)
 row=call(s['multi'],'custom national same OpenAI protocol',groups['cn']['model'],.8)
 check('custom model selects national pool',row['group_id']==groups['cn']['id'],row)
 api('/keys/'+str(s['multi']['id']),{'group_ids':[groups['down']['id'],groups['pro']['id']]},user,'PUT')
 row=call(s['multi'],'503 cross-group fallback',groups['gpt']['model'],1.3)
 check('fallback final pool and snapshot',row['group_id']==groups['pro']['id'] and len(row['route_billing_snapshot']['attempts'])==2,row)
 call(s['multi'],'503 streaming fallback',groups['gpt']['model'],1.3,True)
 verify_edges(s,check,call,snap)
 print(f'Decoupled acceptance: {len(checks)} checks passed',flush=True)

def verify_edges(s,check,call,snap):
 token=s['admin'];ut=s['user_token'];sid=s['subscription']['id'];uid=s['user']['id'];pid=s['plan']['id'];groups=s['groups'];gid=groups['gpt']['id'];model=groups['gpt']['model'];multi=s['multi'];single=s['single']
 payload={'model':model,'messages':[{'role':'user','content':'OK'}],'max_tokens':16}
 def subrow():return sql(f"SELECT row_to_json(t) FROM (SELECT id,plan_id,starts_at,expires_at,status,daily_usage_usd,weekly_usage_usd,monthly_usage_usd,daily_window_start,weekly_window_start,monthly_window_start FROM user_subscriptions WHERE id={sid}) t;")[0]
 def count():return sql(f"SELECT json_build_object('n',count(*)) FROM usage_logs WHERE user_id={uid};")[0]['n']
 def reject(name,path,data,auth=ut,method=None):
  status,body=request(path,data,auth,method);check(name,400<=status<500,{'status':status,'error':body.get('message') if isinstance(body,dict) else str(body)[:100]})
 def new_key(data):return api('/keys',{'name':'boundary fixture',**data},ut)
 def mockcalls():return base.request('/__calls',base=f'http://127.0.0.1:{MOCK_PORT}')[1]
 api('/keys/'+str(multi['id']),{'group_ids':[gid,groups['cn']['id']]},ut,'PUT')
 status,body=request('/v1/models',token=multi['key']);check('model union includes neutral national name',status==200 and {model,groups['cn']['model']}<={r['id'] for r in body['data']})
 status,body=request('/v1/models?client_version=1.0',token=multi['key']);check('Codex manifest shape',status==200 and 'models' in body)
 reject('missing model rejected','/v1/chat/completions',{'messages':[]},multi['key'])
 reject('unknown model rejected','/v1/chat/completions',{**payload,'model':'unknown-explicit-model'},multi['key'])
 reject('subscription-disabled group rejected','/api/v1/keys',{'name':'denied','billing_source':'subscription','group_id':groups['disabled']['id'],'subscription_id':sid})
 reject('balance cannot bind subscription','/api/v1/keys',{'name':'denied','billing_source':'balance','group_id':gid,'subscription_id':sid})
 reject('routing fields mutually exclusive','/api/v1/keys',{'name':'denied','billing_source':'balance','routing_mode':'composite','group_id':gid,'group_ids':[gid]})
 reject('duplicate groups rejected','/api/v1/keys',{'name':'denied','billing_source':'balance','routing_mode':'composite','group_ids':[gid,gid]})
 foreign=sql(f"SELECT json_build_object('id',id) FROM user_subscriptions WHERE user_id!={uid} LIMIT 1;")[0]['id']
 reject('other user subscription rejected','/api/v1/keys',{'name':'denied','billing_source':'subscription','group_id':gid,'subscription_id':foreign})
 private=api('/admin/groups',{'name':'decoupled-private-'+str(time.time_ns()),'platform':'openai','rate_multiplier':1,'subscription_enabled':True,'is_exclusive':True},token)
 reject('private unassigned group rejected','/api/v1/keys',{'name':'denied','billing_source':'subscription','group_id':private['id'],'subscription_id':sid})
 before=subrow();api('/admin/payment/plans/'+str(pid),{'daily_limit_usd':.5},token,'PUT');after=subrow()
 check('plan quota edit preserves usage and term',before==after)
 detail=api('/subscriptions',token=ut)
 check('existing purchased quota snapshot survives plan edits',any(x['id']==sid and x['quota_summary']['daily_limit_usd']==1 for x in detail))
 sql(f"UPDATE user_subscription_entitlements SET daily_usage_usd=daily_limit_usd WHERE user_subscription_id={sid} AND status='active'; UPDATE user_subscriptions SET daily_usage_usd=1 WHERE id={sid};");before=snap();n=count()
 reject('exhausted subscription rejects without balance fallback','/v1/chat/completions',payload,multi['key'])
 check('rejection leaves usage and balance intact',snap()==before and count()==n)
 api('/admin/payment/plans/'+str(pid),{'daily_limit_usd':1},token,'PUT');api('/admin/subscriptions/'+str(sid)+'/reset-quota',{'daily':True},token)
 api('/admin/groups/'+str(gid),{'subscription_rate_multiplier':0},token,'PUT');call(single,'zero subscription multiplier',model,0)
 api('/admin/groups/'+str(gid),{'subscription_rate_multiplier':1.1,'subscription_enabled':False},token,'PUT')
 reject('existing key sees subscription group disabled','/v1/chat/completions',payload,single['key'])
 api('/admin/groups/'+str(gid),{'subscription_enabled':True},token,'PUT')
 plan2=api('/admin/payment/plans',{'name':'Second independent '+str(time.time_ns()),'price':11,'daily_limit_usd':1,'validity_days':30,'validity_unit':'day'},token)
 sub2=api('/admin/subscriptions/assign',{'user_id':uid,'plan_id':plan2['id']},token)
 check('different plans have independent pools',sub2['id']!=sid)
 reject('multiple subscriptions require explicit choice','/api/v1/keys',{'name':'ambiguous','billing_source':'subscription','group_id':gid})
 k2=new_key({'billing_source':'subscription','group_id':gid,'subscription_id':sub2['id']});before=snap()
 status,_=request('/v1/chat/completions',payload,k2['key']);check('second quota pool request succeeds',status==200)
 for _ in range(50):
  used=sql(f"SELECT json_build_object('n',daily_usage_usd) FROM user_subscriptions WHERE id={sub2['id']};")[0]['n']
  if used>0:break
  time.sleep(.1)
 check('second plan usage isolated',used>0 and snap()==before)
 def redeem_for(planid,label):
  codes=api('/admin/redeem-codes/generate',{'type':'subscription','count':1,'plan_id':planid,'validity_days':7},token)
  check(label+' code references plan only',codes[0].get('plan_id')==planid and not codes[0].get('group_id'))
  result=api('/redeem',{'code':codes[0]['code']},ut)
  return codes[0],result
 before=subrow();code,_=redeem_for(pid,'same plan renewal');after=subrow()
 delta=(datetime.fromisoformat(after['expires_at'])-datetime.fromisoformat(before['expires_at'])).total_seconds()
 check('same plan renewal keeps ID usage and periods',after['id']==before['id'] and delta==7*86400 and all(after[x]==before[x] for x in ['starts_at','daily_usage_usd','weekly_usage_usd','monthly_usage_usd','daily_window_start','weekly_window_start','monthly_window_start']))
 reject('repeat redemption rejected','/api/v1/redeem',{'code':code['code']});check('repeat code does not extend twice',subrow()==after)
 # A paid synthetic order exercises the real fulfillment transaction without a payment provider.
 before=subrow();order_tag='decoupled-'+secrets.token_hex(12)
 order=sql(f"INSERT INTO payment_orders(user_id,user_email,user_name,amount,pay_amount,recharge_code,out_trade_no,payment_type,payment_trade_no,order_type,plan_id,subscription_days,status,paid_at,expires_at,client_ip,src_host) SELECT id,email,'Acceptance',1,1,'{order_tag}','{order_tag}','test','{order_tag}','subscription',{pid},3,'PAID',NOW(),NOW()+INTERVAL '1 hour','127.0.0.1','isolated.invalid' FROM users WHERE id={uid} RETURNING json_build_object('id',id);")[0]['id']
 api('/admin/payment/orders/'+str(order)+'/retry',{},token)
 after=subrow();check('paid plan order extends existing pool once',(datetime.fromisoformat(after['expires_at'])-datetime.fromisoformat(before['expires_at'])).total_seconds()==3*86400 and after['daily_usage_usd']==before['daily_usage_usd'])
 # Simulate delivery recovery after fulfillment committed; durable audit prevents double extension.
 sql(f"UPDATE payment_orders SET status='RECHARGING',updated_at=NOW()-INTERVAL '1 hour' WHERE id={order};")
 api('/admin/payment/orders/'+str(order)+'/retry',{},token)
 check('replayed paid order does not extend twice',subrow()==after)
 audit=sql(f"SELECT json_build_object('n',count(*)) FROM payment_audit_logs WHERE order_id='{order}' AND action='SUBSCRIPTION_ASSIGNED';")[0]['n']
 check('payment has one durable assignment audit',audit==1)
 # Concurrent requests produce one charge per successful response in the same pool.
 before=snap();n=count()
 with ThreadPoolExecutor(max_workers=4) as executor:
  responses=list(executor.map(lambda _:request('/v1/chat/completions',payload,single['key'])[0],range(4)))
 for _ in range(100):
  if count()>=n+4:break
  time.sleep(.1)
 check('concurrent successful requests each settle once',responses==[200]*4 and count()==n+4 and abs(snap()['used']-before['used']-4*.0012*1.1)<1e-9)
 # Deliberately expire only this synthetic pool; renewal must keep its ID.
 sql(f"UPDATE user_subscription_entitlements SET expires_at=NOW()-INTERVAL '1 day',status='expired' WHERE user_subscription_id={sid}; UPDATE user_subscriptions SET expires_at=NOW()-INTERVAL '1 day',status='expired' WHERE id={sid};")
 reject('expired subscription rejected','/v1/chat/completions',payload,single['key'])
 api('/keys/'+str(single['id']),{'status':'inactive'},ut,'PUT');api('/keys/'+str(single['id']),{'status':'active'},ut,'PUT')
 check('expired subscription key can still be disabled',True)
 redeem_for(pid,'expired renewal');after=subrow();check('expired renewal starts new period without new pool',after['id']==sid and after['status']=='active' and after['daily_usage_usd']==0 and after['weekly_usage_usd']==0 and after['monthly_usage_usd']==0)
 # New models must have both capability declarations and a price.
 unpriced='neutral-unpriced-'+str(time.time_ns())
 g=api('/admin/groups',{'name':'unpriced fixture '+str(time.time_ns()),'platform':'openai','rate_multiplier':1,'subscription_enabled':True},token)
 api('/admin/accounts',{'name':'unpriced fixture '+str(time.time_ns()),'platform':'openai','type':'apikey','credentials':{'api_key':'synthetic-test-key','base_url':f'http://127.0.0.1:{MOCK_PORT}/unpriced','model_mapping':{unpriced:unpriced}},'group_ids':[g['id']],'concurrency':5,'priority':1},token)
 k=new_key({'billing_source':'subscription','subscription_id':sid,'group_id':g['id']})
 reject('declared but unpriced model rejected','/v1/chat/completions',{**payload,'model':unpriced},k['key'])
 # No replay on client errors or once streamed content has been delivered.
 for name in ['bad400','partial']:
  g=api('/admin/groups',{'name':name+' fixture '+str(time.time_ns()),'platform':'openai','rate_multiplier':1,'subscription_enabled':True,'model_pricing':[{'models':[model],'billing_mode':'token','input_price':.000001,'output_price':.000002}]},token)
  api('/admin/accounts',{'name':name+' fixture '+str(time.time_ns()),'platform':'openai','type':'apikey','credentials':{'api_key':'synthetic-test-key','base_url':f'http://127.0.0.1:{MOCK_PORT}/'+name,'model_mapping':{model:model}},'group_ids':[g['id']],'concurrency':5,'priority':1},token)
  api('/keys/'+str(multi['id']),{'group_ids':[g['id'],groups['pro']['id']]},ut,'PUT')
  n=len(mockcalls());status,body=request('/v1/chat/completions',{**payload,'stream':name=='partial'},multi['key'],raw=name=='partial');seen=mockcalls()[n:]
  check(name+' does not cross-group replay',bool(seen) and all('/pro/' not in x['path'] for x in seen),{'status':status,'attempts':seen})
  if name=='partial':check('partial SSE content reaches client','partial content' in body)
 api('/keys/'+str(multi['id']),{'group_ids':[gid,groups['cn']['id']]},ut,'PUT')
 # Snapshots and filters must expose actual settlement ownership.
 result=api('/usage?subscription_id='+str(sid)+'&page_size=100',token=ut)
 check('subscription-filtered usage contains only its quota pool',result['total']>0 and all(x['subscription_id']==sid and x['billing_source']=='subscription' for x in result['items']))
 check('usage identifies target group and plan',all(x.get('target_group_name') and x.get('subscription_name') for x in result['items']))
 # Migration replay is tested via ApplyMigrations in the PostgreSQL suite.
 # Never execute an old migration file directly against a live upgraded schema.
 legacy=sql("SELECT json_build_object('id',us.id,'expires_at',us.expires_at,'starts_at',us.starts_at,'daily_usage',us.daily_usage_usd) FROM user_subscriptions us JOIN users u ON u.id=us.user_id WHERE u.email='decoupling-legacy@example.invalid';")
 check('unrelated legacy subscription identity term and usage preserved',legacy==json.loads((PRIVATE/'pre-migration.json').read_text()))
 api('/admin/payment/plans/'+str(plan2['id']),token=token,method='DELETE')
 check('referenced plan archived with quota intact',sql(f"SELECT json_build_object('archived',archived_at IS NOT NULL,'quota',daily_limit_usd) FROM subscription_plans WHERE id={plan2['id']};")[0]=={'archived':True,'quota':1})

if __name__=='__main__':
 p=argparse.ArgumentParser();p.add_argument('command',choices=['serve','bootstrap','verify']);a=p.parse_args();globals()[a.command]()
