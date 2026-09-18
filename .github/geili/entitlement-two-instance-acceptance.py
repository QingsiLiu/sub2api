#!/usr/bin/env python3
"""Run a second local app against the synthetic database to verify shared rights."""
import importlib.util,json,os,secrets,subprocess,time
from pathlib import Path
spec=importlib.util.spec_from_file_location('decoupled',Path(__file__).with_name('decoupled-acceptance.py'));d=importlib.util.module_from_spec(spec);spec.loader.exec_module(d)
assert d.BASE.startswith('http://127.0.0.1:')
SECOND='http://127.0.0.1:18509'
private=d.PRIVATE/'second';private.mkdir(parents=True,exist_ok=True,mode=0o700)
cfg=d.base.local_env();env={**os.environ,'AUTO_SETUP':'true','SERVER_HOST':'127.0.0.1','SERVER_PORT':'18509','DATA_DIR':str(private),'DATABASE_HOST':'127.0.0.1','DATABASE_PORT':str(d.base.PG_PORT),'DATABASE_USER':'acceptance','DATABASE_DBNAME':d.DB,'DATABASE_PASSWORD':cfg['password'],'DATABASE_SSLMODE':'disable','REDIS_HOST':'127.0.0.1','REDIS_PORT':str(d.base.REDIS_PORT),'REDIS_DB':'5','ADMIN_EMAIL':'admin@subscription-lab.invalid','ADMIN_PASSWORD':cfg['admin_password'],'JWT_SECRET':cfg['password']*2,'TOTP_ENCRYPTION_KEY':secrets.token_hex(32),'TZ':'Asia/Shanghai'}
checks=[]
def check(name,value):
 checks.append({'name':name,'passed':bool(value)});print(('PASS ' if value else 'FAIL ')+name,flush=True)
 if not value:raise AssertionError(name)
app=subprocess.Popen([str(d.ROOT/'backend/bin/decoupled-server')],cwd=private,env=env,stdout=open(private/'server.log','a'),stderr=subprocess.STDOUT)
try:
 for _ in range(100):
  try:
   if d.base.request('/health',base=SECOND)[0]==200:break
  except Exception:pass
  if app.poll() is not None:raise RuntimeError('second application failed')
  time.sleep(.1)
 state=json.loads((d.PRIVATE/'state.json').read_text());sid=state['subscription']['id'];key=state['single'];admin=d.admin();group=state['groups']['gpt']
 payload={'model':group['model'],'messages':[{'role':'user','content':'synthetic two-instance test'}],'max_tokens':16}
 def call(base):return d.base.request('/v1/chat/completions',payload,key['key'],base=base)[0]
 check('second application uses shared valid entitlement',call(SECOND)==200)
 d.api('/admin/subscriptions/'+str(sid)+'/revoke',{},admin)
 check('first instance rejects revoked rights',call(d.BASE)==403)
 check('second instance rejects revoked rights immediately',call(SECOND)==403)
 d.api('/admin/subscriptions/'+str(sid)+'/restore',{},admin)
 check('second instance sees restored rights',call(SECOND)==200)
 for _ in range(100):
  rows=d.sql(f"SELECT json_build_object('pending',count(*)) FROM subscription_requests WHERE subscription_id={sid} AND status='admitted';")
  if rows[0]['pending']==0:break
  time.sleep(.05)
 d.api('/admin/subscriptions/'+str(sid)+'/reset-quota',{'daily':True,'weekly':True,'monthly':True},admin)
 status,body=d.base.request('/api/v1/subscriptions',token=state['user_token'],base=SECOND)
 target=next(s for s in body['data'] if s['id']==sid)
 check('second instance reports reset aggregate and lot usage',status==200 and target['quota_summary']['daily_usage_usd']==0 and all(l['daily_usage_usd']==0 for l in target['entitlements'] if l['status']=='active'))
 for _ in range(100):
  n=d.sql(f"SELECT json_build_object('n',count(*)) FROM subscription_cache_outbox WHERE subscription_id={sid};")[0]['n']
  if n==0:break
  time.sleep(.05)
 check('durable invalidation outbox drained',n==0)
 path=d.PRIVATE/'two-instance-report.json';path.write_text(json.dumps(checks,indent=2));path.chmod(0o600)
finally:
 app.terminate()
 try:app.wait(timeout=20)
 except subprocess.TimeoutExpired:app.kill();app.wait()
