"""Synthetic-only external connection lifecycle. Does not connect to any supplier/proxy."""
import json
import time
import uuid

def verify(f, public, make, account):
    writes=[]
    def write(token, version, connection, request_id=None, secondary=False):
        now=time.monotonic()
        writes[:]=[at for at in writes if now-at<61]
        if len(writes)>=9:
            delay=max(0,61-(now-writes[0]))
            print('Waiting for the external connection write rate window',flush=True)
            while delay>0:
                interval=min(delay,30);time.sleep(interval);delay-=interval
            writes.clear()
        writes.append(time.monotonic())
        return public('connection/update', {'token':token,'version':version,'request_id':request_id or str(uuid.uuid4()),'connection':connection},secondary)
    for platform in ('anthropic','openai'):
        invite,config=make(platform,name='connection');token=invite['token']
        key='synthetic-connection-key-'+f.suffix;password='fake-proxy-password-'+f.suffix
        f.intake_canaries.extend([key,password])
        base={'token':token,'api_key':key,'name':'Holder '+platform+' '+f.suffix,'concurrency':37}
        rejected=public('submit',{**base,'connection':{'mode':'custom','proxy':{'name':'local','protocol':'http','host':'127.0.0.1','port':8080,'username':'','password':''}}})
        f.check(platform+' external private proxy rejected without consuming invite',rejected[0]==400 and public('inspect',{'token':token})[1]['data']['status']=='pending')
        proxy={'name':'connection '+platform+' '+f.suffix,'protocol':'http','host':'8.8.8.8','port':8080,'username':'holder','password':password}
        result=public('submit',{**base,'connection':{'mode':'custom','proxy':proxy}})
        f.check(platform+' external shared-form submission succeeds',result[0]==200)
        ident=account(invite)['account_id'];f.remember_account(ident)
        stored=f.sql("SELECT json_build_object('name',name,'concurrency',concurrency,'priority',priority,'multiplier',rate_multiplier,'status',status,'schedulable',schedulable) FROM accounts WHERE id="+str(ident)+';')[0]
        f.check(platform+' holder controls name and concurrency but not priority billing or activation',stored=={'name':base['name'],'concurrency':37,'priority':50,'multiplier':0,'status':'inactive','schedulable':False})
        state=public('connection',{'token':token})[1]['data']
        f.check(platform+' connection snapshot hides password and internal IDs',state['mode']=='custom' and state['has_password'] and not any(k in state for k in ('account_id','proxy_id','group_ids')) and password not in json.dumps(state))
        masked=f.api('/admin/proxies?page=1&page_size=100&search='+f.suffix,token=f.admin)
        f.check(platform+' administrative proxy responses mask holder password',password not in json.dumps(masked))
        exported=f.api('/admin/proxies/data?search='+f.suffix,token=f.admin)
        f.check(platform+' external proxy export skips secret with count',exported.get('skipped_external_proxies',0)>=1 and password not in json.dumps(exported))
        before_count=f.sql("SELECT json_build_object('count',count(*)) FROM accounts WHERE name="+"'"+base['name']+"';")[0]['count']
        f.check(platform+' duplicate submission never replaces key or configuration',public('submit',{**base,'api_key':'replacement-test-key','concurrency':1})[0]==200 and before_count==1)
        proxy['password']='';request_id=str(uuid.uuid4())
        saved=write(token,0,{'mode':'custom','proxy':proxy},request_id)
        f.check(platform+' blank proxy password preserves existing secret',saved[0]==200 and saved[1]['data']['version']==1 and saved[1]['data']['has_password'])
        replay=write(token,0,{'mode':'custom','proxy':proxy},request_id,secondary=bool(getattr(f,'has_replica',False)))
        f.check(platform+' lost response replay is idempotent across instances',replay[0]==200 and replay[1]['data']['version']==1)
        stale=write(token,0,{'mode':'direct'})
        f.check(platform+' stale connection version cannot overwrite a newer save',stale[0]==409)
        proxy['clear_password']=True
        cleared=write(token,1,{'mode':'custom','proxy':proxy})
        f.check(platform+' explicit password clearing works',cleared[0]==200 and not cleared[1]['data']['has_password'])
        fresh=public('connection',{'token':token},secondary=bool(getattr(f,'has_replica',False)))[1]
        f.check(platform+' both instances observe the same current proxy',fresh['data']==cleared[1]['data'])
        direct=write(token,2,{'mode':'direct'})
        f.check(platform+' direct selection cleans replaced unshared proxy',direct[0]==200 and f.sql("SELECT json_build_object('count',count(*)) FROM proxies WHERE name='[external] "+proxy['name']+"';")[0]['count']==0)
        f.sql('UPDATE account_submission_invites_geili SET expires_at=now()-interval \'1 day\' WHERE id='+str(invite['invite']['id'])+';')
        f.check(platform+' submitted expired original link keeps connection management',public('connection',{'token':token})[0]==200)
        f.api('/admin/accounts/submission-invites/'+str(invite['invite']['id'])+'/revoke-view',{},f.admin)
        f.check(platform+' revoked access blocks both connection reads and writes',public('connection',{'token':token})[0]==410 and write(token,3,{'mode':'direct'})[0]==410)
        replacement=f.api('/admin/accounts/submission-invites/'+str(invite['invite']['id'])+'/reset-view',{},f.admin)['token'];f.intake_canaries.append(replacement)
        f.check(platform+' reset rejects old link and enables new connection capability',public('connection',{'token':token})[0]==410 and public('connection',{'token':replacement})[0]==200)
