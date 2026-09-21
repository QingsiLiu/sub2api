#!/usr/bin/env python3
"""Isolated two-instance and Redis outage V2 acceptance; no production resources.
Uses the local geili.acceptance=local PostgreSQL; creates a new acceptance_* DB,
its own labelled Redis and two server processes; stops only resources it created.
"""
import argparse
from datetime import datetime, timezone
import importlib.util
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import time

ROOT=Path(__file__).resolve().parents[2]
spec=importlib.util.spec_from_file_location('v2',Path(__file__).with_name('subscription-v2-acceptance.py'));v2=importlib.util.module_from_spec(spec);spec.loader.exec_module(v2)


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary',default=str(ROOT/'backend/bin/subscription-v2-server'))
    args=parser.parse_args()
    suffix=datetime.now(timezone.utc).strftime('%Y%m%d%H%M%S')
    db='acceptance_v2_resilience_'+suffix
    private=ROOT/'deploy/.secrets/subscription-v2-comprehensive'/db
    private.mkdir(parents=True,mode=0o700)
    pg=v2.BASE_HARNESS.PROJECT+'-postgres'
    label=subprocess.check_output(['docker','inspect','--format','{{index .Config.Labels "geili.acceptance"}}',pg],text=True).strip();assert label=='local'
    subprocess.run(['docker','exec',pg,'createdb','-U','acceptance',db],check=True)
    redis='geili-v2-resilience-'+suffix
    with socket.socket() as available:
        available.bind(('127.0.0.1',0));redis_port=available.getsockname()[1]
    subprocess.run(['docker','run','-d','--name',redis,'--label','geili.acceptance=local','-p',f'127.0.0.1:{redis_port}:6379','redis:8.4-alpine'],check=True,stdout=subprocess.DEVNULL)
    apps=[];logs=[];fixture=None
    cfg=v2.BASE_HARNESS.local_env();totp=secrets.token_hex(32)
    try:
        port=int(subprocess.check_output(['docker','port',redis,'6379/tcp'],text=True).strip().rsplit(':',1)[1])
        for appport in [18514,18515]:
            runtime=private/str(appport);runtime.mkdir(mode=0o700)
            env={**os.environ,'AUTO_SETUP':'true','SERVER_HOST':'127.0.0.1','SERVER_PORT':str(appport),'DATA_DIR':str(runtime),'DATABASE_HOST':'127.0.0.1','DATABASE_PORT':str(v2.BASE_HARNESS.PG_PORT),'DATABASE_USER':'acceptance','DATABASE_DBNAME':db,'DATABASE_PASSWORD':cfg['password'],'DATABASE_SSLMODE':'disable','REDIS_HOST':'127.0.0.1','REDIS_PORT':str(port),'REDIS_DB':'0','REDIS_DIAL_TIMEOUT_SECONDS':'1','REDIS_READ_TIMEOUT_SECONDS':'1','REDIS_WRITE_TIMEOUT_SECONDS':'1','ADMIN_EMAIL':'admin@subscription-lab.invalid','ADMIN_PASSWORD':cfg['admin_password'],'JWT_SECRET':cfg['password']*2,'TOTP_ENCRYPTION_KEY':totp,'TZ':'Asia/Shanghai'}
            log=open(runtime/'app.log','w',opener=lambda p,f:os.open(p,f,0o600));logs.append(log)
            app=subprocess.Popen([str(Path(args.binary).resolve())],cwd=runtime,env=env,stdout=log,stderr=subprocess.STDOUT);apps.append(app)
            for _ in range(150):
                if app.poll() is not None:raise RuntimeError('isolated app exited, inspect private log')
                try:
                    if v2.BASE_HARNESS.request('/health',base='http://127.0.0.1:'+str(appport))[0]==200:break
                except Exception:pass
                time.sleep(.2)
            else:raise RuntimeError('isolated app health timeout')
        fixture=v2.Fixture(argparse.Namespace(base='http://127.0.0.1:18514',database=db,project=v2.BASE_HARNESS.PROJECT,mock_port=19014))
        fixture.sql("INSERT INTO settings(key,value) SELECT 'admin_compliance_acknowledgement:'||id,'{\"version\":\"v2026.06.10\",\"user_agent\":\"isolated synthetic fixture\"}' FROM users WHERE email='admin@subscription-lab.invalid' ON CONFLICT DO NOTHING;")
        fixture.start_mock();fixture.setup()
        user=fixture.user('resilience')
        quote=fixture.quote(user,'month45','purchase',units=1)
        fixture.base='http://127.0.0.1:18515'
        order=fixture.pay(user,quote)
        sub=fixture.subscription(user);sid=sub['id'];key=fixture.key(user,sid)
        fixture.check('quote from first instance accepted by second instance',sub['contract']['quantity']==1)
        fixture.check('second instance success before outage',fixture.call(key)[0]==200);fixture.await_usage(user,sid,'0.0012')
        fixture.base='http://127.0.0.1:18514';fixture.check('first instance sees settled ledger',fixture.subscription(user,sid)['daily_usage_usd']==.0012)
        fixture.pay(user,fixture.quote(user,'month45','stack',units=1))
        fixture.base='http://127.0.0.1:18515';sub=fixture.subscription(user,sid);fixture.check('other instance observes stack without clearing usage',sub['quota_summary']['daily_limit_usd']==90 and sub['daily_usage_usd']==.0012)
        subprocess.run(['docker','stop',redis],check=True,stdout=subprocess.DEVNULL)
        started=time.monotonic();status,body,_=fixture.call(key)
        fixture.check('Redis outage returns bounded success or explicit unavailable',status in (200,500,503) and time.monotonic()-started<40,{'status':status,'message':v2.safe_detail(body)})
        rows=fixture.sql(f"SELECT json_build_object('usage',(SELECT COALESCE(SUM(used_usd),0) FROM subscription_daily_usage WHERE subscription_id={sid}),'alloc',(SELECT COALESCE(SUM(a.cost_usd),0) FROM subscription_usage_allocations a JOIN subscription_requests r ON r.request_key=a.request_key WHERE r.subscription_id={sid}))")
        expected=.0024 if status==200 else .0012
        fixture.check('outage does not lose or duplicate debit',abs(rows[0]['usage']-expected)<1e-9 and abs(rows[0]['alloc']-expected)<1e-9,rows[0])
        subprocess.run(['docker','start',redis],check=True,stdout=subprocess.DEVNULL)
        for _ in range(40):
            if subprocess.run(['docker','exec',redis,'redis-cli','ping'],capture_output=True).returncode==0:break
            time.sleep(.1)
        fixture.check('cache restart with empty memory preserves effective quota',abs(fixture.subscription(user,sid)['daily_usage_usd']-expected)<1e-9)
        assert int(subprocess.check_output(['docker','port',redis,'6379/tcp'],text=True).strip().rsplit(':',1)[1])==port
        deadline=time.monotonic()+35
        statuses=[]
        while True:
            recovered,_,_=fixture.call(key);statuses.append(recovered)
            if recovered==200 or time.monotonic()>=deadline:break
            time.sleep(.25)
        fixture.check('request after cache recovery succeeds within35s',recovered==200,{'statuses':statuses});fixture.await_usage(user,sid,str(expected+.0012))
        fixture.base='http://127.0.0.1:18514';fixture.api('/admin/subscriptions/'+str(sid)+'/reset-quota',{'daily':True},fixture.admin)
        fixture.base='http://127.0.0.1:18515';fixture.check('quota reset propagates across instances',fixture.subscription(user,sid)['daily_usage_usd']==0)
        fixture.base='http://127.0.0.1:18514';fixture.api('/admin/subscriptions/'+str(sid)+'/revoke',{},fixture.admin)
        fixture.base='http://127.0.0.1:18515';fixture.check('revocation blocks other instance without balance fallback',fixture.call(key)[0]==403)
        fixture.base='http://127.0.0.1:18514';fixture.api('/admin/subscriptions/'+str(sid)+'/restore',{},fixture.admin)
        fixture.base='http://127.0.0.1:18515';fixture.check('restoration keeps identity and restores service',fixture.subscription(user,sid)['id']==sid and fixture.call(key)[0]==200)
        print('RESILIENCE_REPORT '+str(fixture.private/'report.json'),flush=True)
    finally:
        if fixture and fixture.server:fixture.server.shutdown();fixture.server.server_close()
        for app in apps:app.terminate()
        for app in apps:
            try:app.wait(timeout=20)
            except subprocess.TimeoutExpired:app.kill();app.wait()
        for log in logs:log.close()
        subprocess.run(['docker','stop',redis],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        print('Owned app processes and Redis stopped; database/evidence retained.',flush=True)


if __name__=='__main__':main()
