#!/usr/bin/env python3
"""Synthetic HTTP purchase/webhook/refund acceptance; requires local decoupled serve.
No real provider credentials or funds. Output contains only fixture IDs and verdicts.
"""
import hashlib, importlib.util, json, secrets, threading, time, urllib.parse
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

spec=importlib.util.spec_from_file_location('decoupled',Path(__file__).with_name('decoupled-acceptance.py'))
d=importlib.util.module_from_spec(spec);spec.loader.exec_module(d)
assert d.BASE.startswith('http://127.0.0.1:'), 'local synthetic target only'
assert d.base.run(['docker','inspect','--format','{{index .Config.Labels "geili.acceptance"}}',d.PG]).strip()=='local'
PRIVATE=d.ROOT/'deploy/.secrets/entitlement-acceptance';PRIVATE.mkdir(parents=True,exist_ok=True,mode=0o700)
checks=[];merchant=secrets.token_hex(24);orders={};refund_calls=[]
def sign(params):
 return hashlib.md5(('&'.join(k+'='+str(params[k]) for k in sorted(params) if k not in ('sign','sign_type') and params[k]!='')+merchant).encode()).hexdigest()
class Gateway(d.base.http.server.BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def do_POST(self):
  values=urllib.parse.parse_qs(self.rfile.read(int(self.headers.get('Content-Length',0))).decode());v={k:x[0] for k,x in values.items()}
  if self.path.startswith('/mapi.php'):
   assert v.get('sign')==sign(v),'application generated incorrect provider signature'
   key=v['out_trade_no'];orders[key]=v
   response={'code':1,'trade_no':'fixture-'+key,'payurl':'http://127.0.0.1:19003/pay/'+key,'qrcode':'synthetic-'+key}
  elif self.path.startswith('/api.php?act=refund'):
   assert v.get('key')==merchant
   refund_calls.append(v['out_trade_no']);response={'code':1,'msg':'ok'}
  else:response={'code':0,'msg':'unsupported synthetic endpoint'}
  self.send_response(200);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(json.dumps(response).encode())
 def do_GET(self):
  query=urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query);key=query.get('out_trade_no',[''])[0];v=orders.get(key,{})
  self.send_response(200);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(json.dumps({'code':1,'status':1,'money':v.get('money','0'),'out_trade_no':key,'trade_no':'fixture-'+key}).encode())
def check(name,ok,detail=None):
 checks.append({'name':name,'passed':bool(ok),'detail':detail})
 path=PRIVATE/'report.json';path.write_text(json.dumps(checks,ensure_ascii=False,indent=2));path.chmod(0o600)
 print(('PASS ' if ok else 'FAIL ')+name,flush=True)
 if not ok:raise AssertionError(name+': '+str(detail)[:250])
def run():
 a=d.admin();suffix=secrets.token_hex(5)
 for provider in d.api('/admin/payment/providers',token=a):
  if provider['name'].startswith('Synthetic entitlement '):d.api('/admin/payment/providers/'+str(provider['id']),{'enabled':False},a,'PUT')
 d.api('/admin/payment/providers',{'provider_key':'easypay','name':'Synthetic entitlement '+suffix,'config':{'pid':'fixture-merchant','pkey':merchant,'apiBase':'http://127.0.0.1:19003','notifyUrl':d.BASE+'/api/v1/payment/webhook/easypay','returnUrl':d.BASE+'/payment/result','currency':'USD'},'supported_types':['alipay'],'enabled':True,'payment_mode':'qrcode','refund_enabled':True,'allow_user_refund':True},a)
 d.api('/admin/payment/config',{'enabled':True,'min_amount':0.01,'max_amount':1000,'max_pending_orders':20,'enabled_payment_types':['alipay'],'cancel_rate_limit_enabled':False},a,'PUT')
 plan=d.api('/admin/payment/plans',{'name':'Purchase fixture '+suffix,'price':10,'daily_limit_usd':45,'weekly_limit_usd':315,'monthly_limit_usd':1350,'validity_days':30,'validity_unit':'day','for_sale':True},a)
 password=secrets.token_urlsafe(24);user=d.api('/admin/users',{'email':'rights-'+suffix+'@example.invalid','password':password,'balance':0,'concurrency':10},a)
 token=d.api('/auth/login',{'email':user['email'],'password':password})['access_token']
 def sub():return next(x for x in d.api('/subscriptions',token=token) if x['plan_id']==plan['id'])
 def create(mode,n):return d.api('/payment/orders',{'plan_id':plan['id'],'order_type':'subscription','payment_type':'alipay','subscription_mode':mode,'subscription_quantity':n},token)
 def notify(o,bad=False):
  p={'pid':'fixture-merchant','out_trade_no':o['out_trade_no'],'trade_no':'fixture-'+o['out_trade_no'],'trade_status':'TRADE_SUCCESS','money':str(o['pay_amount']),'type':'alipay'};p['sign']=sign(p);p['sign_type']='MD5'
  if bad:p['sign']='invalid'
  return d.request('/api/v1/payment/webhook/easypay?'+urllib.parse.urlencode(p),raw=True)
 quote=d.api('/payment/subscription-quote',{'plan_id':plan['id'],'subscription_mode':'stack','subscription_quantity':1},token)
 check('quote includes amount and next expiry',quote['order_amount']==10 and bool(quote['projected']['next_expiry_at']))
 first=create('stack',1)
 d.api('/admin/payment/plans/'+str(plan['id']),{'daily_limit_usd':90},a,'PUT')
 check('invalid callback signature is rejected',notify(first,True)[0]==400)
 check('signed payment callback accepted',notify(first)[0]==200)
 before=sub();sid=before['id'];check('downstream grant honors order-time quota snapshot',before['quota_summary']['daily_limit_usd']==45 and len(before['entitlements'])==1)
 check('purchase dates and group IDs are valid',all(not x['created_at'].startswith('0001-') for x in before['entitlements']) and 0 not in before.get('entitled_group_ids',[]))
 with ThreadPoolExecutor(max_workers=5) as pool:replies=list(pool.map(lambda _:notify(first)[0],range(5)))
 check('duplicate concurrent callbacks do not duplicate entitlement',all(c==200 for c in replies) and len(sub()['entitlements'])==1)
 renew=create('renew',1);check('renew callback accepted',notify(renew)[0]==200)
 extended=sub();check('renew keeps quota and extends original lot',extended['quota_summary']['daily_limit_usd']==45 and len(extended['entitlements'])==1 and extended['expires_at']>before['expires_at'])
 result=d.api('/admin/payment/orders/'+str(renew['order_id'])+'/refund',{'amount':10,'reason':'synthetic renewal refund','deduct_balance':True},a)
 check('real HTTP refund callback path preserves original purchase',result['success'] and sub()['expires_at']==before['expires_at'] and sub()['quota_summary']['active_lot_count']==1)
 stacked=create('stack',2);check('quantity determines payment amount',stacked['amount']==20)
 check('stack callback accepted',notify(stacked)[0]==200)
 after=sub();check('independent quota snapshots aggregate correctly',after['quota_summary']['daily_limit_usd']==225 and after['quota_summary']['active_lot_count']==3)
 response=d.api('/admin/payment/orders/'+str(stacked['order_id']),token=a)
 check('admin order exposes mode and quantity',response['order']['subscription_mode']=='stack' and response['order']['subscription_quantity']==2)
 status,_=d.request('/api/v1/payment/subscription-quote',{'plan_id':plan['id'],'subscription_mode':'renew','subscription_quantity':4},token)
 check('renewing more units than owned is rejected',status==409)
 status,_=d.request('/api/v1/payment/subscription-quote',{'plan_id':plan['id'],'subscription_mode':'stack','subscription_quantity':-2},token)
 check('negative quote quantity is rejected',status==400)
 status,_=d.request('/api/v1/admin/payment/orders/'+str(stacked['order_id'])+'/refund',{'amount':5,'reason':'partial synthetic','force':True,'deduct_balance':True},a)
 check('partial automatic refund cannot bypass safety with force',status==409 and sub()['quota_summary']['active_lot_count']==3)
 result=d.api('/admin/payment/orders/'+str(stacked['order_id'])+'/refund',{'amount':20,'reason':'synthetic stack refund','deduct_balance':True},a)
 check('stack refund revokes only its two units',result['success'] and sub()['quota_summary']['daily_limit_usd']==45 and sub()['quota_summary']['active_lot_count']==1)
 check('only intended refunds reached mock provider',len(refund_calls)==2)
 state=json.loads((d.PRIVATE/'state.json').read_text());group=state['groups']['gpt']
 key=d.api('/keys',{'name':'rights fixture','billing_source':'subscription','subscription_id':sid,'group_id':group['id']},token)
 status,_=d.request('/v1/chat/completions',{'model':group['model'],'messages':[{'role':'user','content':'synthetic'}],'max_tokens':16},key['key'])
 check('surviving original quota remains usable',status==200)
 for _ in range(100):
  if any(e['lifetime_usage_usd']>0 for e in sub()['entitlements']):break
  time.sleep(.05)
 status,body=d.request('/api/v1/admin/payment/orders/'+str(first['order_id'])+'/refund',{'amount':10,'reason':'used synthetic','force':True,'deduct_balance':True},a)
 check('used lot requires manual review even with force',status==409 and len(refund_calls)==2)
 check('history contains purchase renewal and refund operations',len(sub().get('entitlement_operations',[]))>=5)
 print('Entitlement synthetic HTTP acceptance: '+str(len(checks))+' checks passed',flush=True)
if __name__=='__main__':
 server=d.base.http.server.ThreadingHTTPServer(('127.0.0.1',19003),Gateway);threading.Thread(target=server.serve_forever,daemon=True).start()
 try:run()
 finally:server.shutdown();server.server_close()
