#!/usr/bin/env python3
"""Isolated HTTP media acceptance; secrets remain under deploy/.secrets.

serve owns separate PG/Redis and a mock S3 store. verify defaults to synthetic
AUAPI. Live mode requires the explicit isolated AUAPI fixture configuration, uses
one journaled POST per spec, never retries paid creates, and keeps task IDs for
poll-only recovery. This script does not modify production.
"""
import argparse
import base64
import hashlib
import http.server
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import time
import urllib.parse

ROOT=Path(__file__).resolve().parents[2]
spec=importlib.util.spec_from_file_location('media_harness',ROOT/'.github/geili/acceptance.py')
h=importlib.util.module_from_spec(spec);spec.loader.exec_module(h)
h.PROJECT='geili-auapi-media-local';h.PRIVATE=ROOT/'deploy/.secrets/auapi-media-http'
h.PG_PORT=15449;h.REDIS_PORT=16399;h.MOCK_PORT=18999;h.BASE='http://127.0.0.1:18499'
h.request.__defaults__=(None,None,None,h.BASE,False)
MOCK='http://127.0.0.1:'+str(h.MOCK_PORT)
PNG=base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLbtAAAAABJRU5ErkJggg==')
MP4=b'\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom'+b'fixture-media'
PRICES={'gpt-image-2.5':{'1k:low':.007675},'MiniMax-H3':{'768p:no_video_input:audio_true':.04},'wan3.0-video':{'720p:no_video_input:audio_false':.0614},'seedance-2.5':{'720p:no_video_input:audio_false':.2842}}
TASKS={};POSTS=[]
class MediaMock(h.Mock):
    def do_PUT(self):
        data=self.rfile.read(int(self.headers.get('Content-Length',0)))
        dest=h.PRIVATE/'objects'/hashlib.sha256(self.path.encode()).hexdigest()
        dest.parent.mkdir(exist_ok=True,mode=0o700);dest.write_bytes(data)
        self.send_response(200);self.send_header('ETag','"fixture"');self.end_headers()
    def do_GET(self):
        parsed=urllib.parse.urlsplit(self.path)
        if parsed.path.startswith('/storage/'):
            key='/media/'+parsed.path[len('/storage/'):]
            dest=h.PRIVATE/'objects'/hashlib.sha256(key.encode()).hexdigest()
            if not dest.exists():return self.reply({'error':'missing'},404)
            data=dest.read_bytes();self.send_response(200);self.end_headers();self.wfile.write(data);return
        if parsed.path=='/__media_calls':return self.reply(POSTS)
        if parsed.path.startswith('/v1/tasks/'):
            ident=parsed.path.split('/')[3];task=TASKS.get(ident)
            if not task:return self.reply({'error':'missing'},404)
            if parsed.path.endswith('/content'):
                data=MP4 if task['kind']=='video' else PNG
                self.send_response(200);self.send_header('Content-Type','video/mp4' if task['kind']=='video' else 'image/png');self.end_headers();self.wfile.write(data);return
            return self.reply({'id':ident,'status':'succeeded','amount_usd':task['amount'],'outputs':[{'index':0,'mimeType':'video/mp4' if task['kind']=='video' else 'image/png'}]})
        return super().do_GET()
    def do_POST(self):
        if self.path not in ('/v1/pricing/estimate','/v1/images/tasks','/v1/videos/tasks'):return super().do_POST()
        body=json.loads(self.rfile.read(int(self.headers.get('Content-Length',0))))
        kind=body.get('kind','image');p=body['parameters'];model=body['model']
        spec=p['resolution']+(':no_video_input:audio_'+str(p['generate_audio']).lower() if kind=='video' else ':'+p['quality'])
        price=PRICES[model][spec]*(p['duration'] if kind=='video' else p['n'])
        if self.path.endswith('/estimate'):return self.reply({'amount_usd':str(price)})
        if body['input']['prompt']=='fault_402':return self.reply({'error':'synthetic funds'},402)
        ident='fixture_'+hashlib.sha256(self.headers.get('Idempotency-Key','').encode()).hexdigest()[:24]
        if ident not in TASKS:
            POSTS.append({'id':ident,'model':model,'kind':kind});TASKS[ident]={'kind':kind,'amount':str(price)}
        if body['input']['prompt']=='fault_unknown':return self.reply({'error':'synthetic uncertain acceptance'},500)
        return self.reply({'id':ident,'status':'queued','amount_usd':str(price)})

def sql(query):
    out=h.run(['docker','exec',h.PROJECT+'-postgres','psql','-X','-qAt','-U','acceptance','-d','acceptance','-c',query])
    return [json.loads(x) for x in out.splitlines() if x.startswith(('{','['))]

def verify(live=None):
    h.PRIVATE.mkdir(mode=0o700,parents=True,exist_ok=True)
    config=h.local_env();admin=h.api('/auth/login',{'email':'admin@acceptance.invalid','password':config['admin_password']})['access_token']
    statepath=h.PRIVATE/('live-state.json' if live else 'mock-state.json')
    if statepath.exists():state=json.loads(statepath.read_text())
    else:
        group=h.api('/admin/groups',{'name':'AUAPI isolated media','platform':'openai','rate_multiplier':1,'subscription_rate_multiplier':1,'allow_image_generation':True},admin)
        credentials={'image_provider':'auapi','base_url':live['origin'] if live else MOCK,'api_key':live['api_key'] if live else 'synthetic-fixture','model_mapping':{x:x for x in PRICES},'auapi_media_prices':json.dumps(PRICES)}
        account=h.api('/admin/accounts',{'name':'AUAPI isolated media','platform':'openai','type':'apikey','credentials':credentials,'group_ids':[group['id']],'concurrency':3,'priority':1,'rate_multiplier':1},admin)
        owner=h.api('/admin/users',{'email':('live' if live else 'mock')+'-media@example.invalid','password':config['admin_password'],'balance':15,'concurrency':5},admin)
        usertoken=h.api('/auth/login',{'email':owner['email'],'password':config['admin_password']})['access_token']
        key=h.api('/keys',{'name':'one-key-all-media','group_id':group['id'],'billing_source':'balance'},usertoken)
        state={'group':group['id'],'account':account['id'],'user':owner['id'],'key':key}
        statepath.write_text(json.dumps(state));statepath.chmod(0o600)
    cards=[{'models':[name],'billing_mode':'image' if name=='gpt-image-2.5' else 'video','per_request_price':next(iter(specs.values()))} for name,specs in PRICES.items()]
    h.api('/admin/groups/'+str(state['group']),{'model_pricing':cards},admin,'PUT')
    report={'production_modified':False,'real_provider':bool(live),'source_revision':h.run(['git','rev-parse','HEAD'],cwd=ROOT).strip(),'source_dirty':bool(h.run(['git','status','--porcelain'],cwd=ROOT).strip()),'binary_sha256':hashlib.sha256((ROOT/'backend/bin/auapi-media-acceptance').read_bytes()).hexdigest(),'checks':[],'tasks':[]}
    def check(label,ok):
        report['checks'].append({'name':label,'passed':bool(ok)});print(('PASS ' if ok else 'FAIL ')+label,flush=True)
        (h.PRIVATE/'latest-report.json').write_text(json.dumps(report,indent=2))
        if not ok:raise AssertionError(label)
    jobs=[('gpt-image-2.5',{'resolution':'1k','quality':'low'},.007675),('wan3.0-video',{'resolution':'720p','duration':5,'generate_audio':False,'aspect_ratio':'16:9'},.307),('MiniMax-H3',{'resolution':'768p','duration':5,'generate_audio':True},.2),('seedance-2.5',{'resolution':'720p','duration':4,'generate_audio':False,'aspect_ratio':'16:9'},1.1368)]
    for model,params,price in jobs:
        payload={'model':model,'prompt':'A small boat crossing a calm lake.',**params}
        isvideo=model!='gpt-image-2.5';path='/v1/videos/generations' if isvideo else '/v1/images/generations/async'
        journal=h.PRIVATE/('live-' if live else 'mock-');journal=journal.with_name(journal.name+model+'.json')
        before=sql(f"SELECT json_build_object('balance',balance,'frozen',frozen_balance) FROM users WHERE id={state['user']}")[0]
        if journal.exists():
            accepted=json.loads(journal.read_text())
            if not accepted.get('task_id'):raise ValueError('previous submit uncertain; never retry paid create')
            ident=accepted['task_id']
        else:
            journal.write_text(json.dumps({'state':'submit_started','request':payload}));journal.chmod(0o600)
            code,body=h.request(path,payload,state['key']['key'])
            ident=body.get('task_id') or body.get('id') if isinstance(body,dict) else None
            journal.write_text(json.dumps({'state':'accepted' if ident else 'uncertain','http_status':code,'response':body,'task_id':ident,'before':before}))
            check(model+' accepted once',code==202 and bool(ident))
        endpoint='/v1/videos/' if isvideo else '/v1/images/tasks/'
        started=time.monotonic();last={}
        while time.monotonic()-started<900:
            code,last=h.request(endpoint+ident,token=state['key']['key'])
            if code==200 and last.get('status') in ('completed','failed'):break
            time.sleep(3)
        check(model+' terminal success',code==200 and last.get('status')=='completed')
        rows=sql(f"SELECT json_build_object('count',COUNT(*),'amount',COALESCE(SUM(actual_cost),0),'image_count',COALESCE(SUM(image_count),0),'video_count',COALESCE(SUM(video_count),0)) FROM usage_logs WHERE api_key_id={state['key']['id']} AND model='{model}'")
        for _ in range(60):
            if rows[0]['count']>=1:break
            time.sleep(.5)
            rows=sql(f"SELECT json_build_object('count',COUNT(*),'amount',COALESCE(SUM(actual_cost),0),'image_count',COALESCE(SUM(image_count),0),'video_count',COALESCE(SUM(video_count),0)) FROM usage_logs WHERE api_key_id={state['key']['id']} AND model='{model}'")
        check(model+' one exact settlement',rows[0]['count']==1 and abs(float(rows[0]['amount'])-price)<1e-8)
        check(model+' media usage type',rows[0]['video_count']==int(isvideo) and rows[0]['image_count']==int(not isvideo))
        owned=sql(f"SELECT json_build_object('frozen',frozen_balance) FROM users WHERE id={state['user']}")[0]
        check(model+' hold fully captured',float(owned['frozen'])==0)
        check(model+' result URL',bool(last.get('video_url') if isvideo else last.get('image_url')))
        report['tasks'].append({'model':model,'task_id':ident,'retail_usd':price,'result':last})
    report['passed']=True;(h.PRIVATE/'latest-report.json').write_text(json.dumps(report,indent=2));print('Media HTTP acceptance passed',flush=True)

if __name__=='__main__':
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('action',choices=['serve','verify']);p.add_argument('--live-config',type=Path);args=p.parse_args()
    if args.action=='serve':
        h.Mock=MediaMock
        os.environ.update(AUAPI_IMAGE_ENABLED='true',IMAGE_STORAGE_ENABLED='true',IMAGE_STORAGE_ENDPOINT=MOCK,IMAGE_STORAGE_BUCKET='media',IMAGE_STORAGE_ACCESS_KEY_ID='fixture',IMAGE_STORAGE_SECRET_ACCESS_KEY='fixture',IMAGE_STORAGE_FORCE_PATH_STYLE='true',IMAGE_STORAGE_PUBLIC_BASE_URL=MOCK+'/storage',IMAGE_STORAGE_MAX_DOWNLOAD_BYTES='134217728')
        dest=ROOT/'backend/bin/acceptance-server'
        if dest.exists() or dest.is_symlink():raise ValueError('acceptance binary already exists; inspect')
        dest.symlink_to('auapi-media-acceptance')
        try:h.serve()
        finally:dest.unlink(missing_ok=True)
    else:verify(json.loads(args.live_config.read_text()) if args.live_config else None)
