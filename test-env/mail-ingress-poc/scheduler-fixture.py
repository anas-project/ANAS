#!/usr/bin/env python3
"""Manage only the explicitly named finance notification experiment."""
from pathlib import Path
import hashlib
import hmac
import json
import os
import signal
import socket
import ssl
import subprocess
import sys
import time
import urllib.request
import urllib.error

ROOT=Path('/home/whl/anas-mail-ingress-test-20261025')
os.environ['DOCKER_HOST']='unix:///run/anas-mail-test-1024.sock'
assert subprocess.check_output(['docker','info','--format','{{.DockerRootDir}}'],text=True).strip()=='/home/whl/anas-mail-go-test-20261024/docker'
subprocess.run(['python3',str(ROOT/'consumer_server.py'),'inventory'],check=True,stdout=subprocess.DEVNULL)
mode=sys.argv[1]
out=ROOT/'evidence-scheduler';out.mkdir(mode=0o700,exist_ok=True)
def write(path,value):
    path.write_text(json.dumps(value,indent=2));path.chmod(0o600)
def psql(sql):
    args=['docker','compose','-f',str(ROOT/'compose.json'),'exec','-T','postgres','sh','-c','export PGPASSWORD="$POSTGRES_PASSWORD"; exec psql "$@"','psql','-h','127.0.0.1','-U','ingress','-d','ingress','-v','ON_ERROR_STOP=1','-At','-c',sql]
    return subprocess.check_output(args,text=True)
def process():
    m=json.loads((ROOT/'scheduler-process.json').read_text());p=Path('/proc')/str(m['pid'])
    if not p.exists() or p.joinpath('stat').read_text().split()[2]=='Z':return None
    assert p.joinpath('stat').read_text().split()[21]==m['start_ticks']
    assert str(ROOT/'consumer-scheduler').encode() in p.joinpath('cmdline').read_bytes().split(b'\0')
    return m['pid']
if mode=='configure':
    subprocess.run(['python3',str(ROOT/'consumer_server.py'),'configure'],check=True,stdout=subprocess.DEVNULL)
    cfg=json.loads((ROOT/'consumer.json').read_text())
    n=json.loads((ROOT/'notification-runtime.json').read_text())
    cfg.update(notify_secret=n['secret'],notify_cert=str(ROOT/'data/tls/notify-cert.pem'),notify_key=str(ROOT/'data/tls/notify-key.pem'))
    write(ROOT/'consumer-scheduler.json',cfg)
    print(json.dumps({'configured':True}))
elif mode=='start':
    if (ROOT/'scheduler-process.json').exists():assert process() is None,'scheduler already running'
    with socket.socket() as probe:probe.bind(('127.0.0.1',28443))
    env=dict(os.environ,MAIL_POC_SERVE='1',HTTPS_PROXY='socks5h://127.0.0.1:10829')
    p=subprocess.Popen([str(ROOT/'consumer-scheduler'),str(ROOT/'consumer-scheduler.json')],env=env,stdin=subprocess.DEVNULL,stdout=open(out/'scheduler.log','ab'),stderr=subprocess.STDOUT,start_new_session=True)
    time.sleep(.2)
    write(ROOT/'scheduler-process.json',{'pid':p.pid,'start_ticks':Path(f'/proc/{p.pid}/stat').read_text().split()[21]})
    cfg=json.loads((ROOT/'consumer-scheduler.json').read_text())
    for _ in range(50):
        assert p.poll() is None,'scheduler exited before readiness'
        try:
            urllib.request.urlopen('https://127.0.0.1:28443/notify',context=ssl.create_default_context(cafile=cfg['notify_cert']),timeout=1)
        except urllib.error.HTTPError as e:
            if e.code==404:break
        except OSError:pass
        time.sleep(.1)
    else:raise RuntimeError('scheduler HTTPS did not become ready')
    print(json.dumps({'started':True}))
elif mode in ['stop','pause','resume']:
    pid=process();assert pid is not None
    os.killpg(pid,{'stop':signal.SIGTERM,'pause':signal.SIGSTOP,'resume':signal.SIGCONT}[mode])
    if mode=='stop':
        for _ in range(80):
            if process() is None:break
            time.sleep(.1)
        assert process() is None,'scheduler failed to stop'
    print(json.dumps({mode:True}))
elif mode=='snapshot':
    result={}
    for name,sql in {
        'events':'SELECT seq,source,id,target,received_at,processed_at FROM ingress_notification ORDER BY seq',
        'polls':'SELECT seq,source,reason,started_at,finished_at,result FROM ingress_poll ORDER BY seq',
        'ledger':'SELECT source,id,target,state,cloud_acked FROM ingress_delivery ORDER BY id'
    }.items():result[name]=json.loads(psql("SELECT COALESCE(json_agg(t),'[]'::json) FROM ("+sql+") t"))
    write(out/'scheduler-snapshot.json',result);print(json.dumps(result))
elif mode=='notify':
    cfg=json.loads((ROOT/'consumer-scheduler.json').read_text())
    request=json.load(sys.stdin)
    body=json.dumps({'source':cfg['source'],'id':request['id'],'target':request.get('target','box-a')},separators=(',',':')).encode()
    stamp=str(int(time.time()*1000)+request.get('time_offset_ms',0))
    signature=hmac.new(cfg['notify_secret'].encode(),stamp.encode()+b'.'+body,hashlib.sha256).hexdigest()
    if request.get('invalid'):signature='00'*32
    req=urllib.request.Request('https://127.0.0.1:28443/notify',data=body,headers={'Content-Type':'application/json','X-Mail-Time':stamp,'X-Mail-Signature':signature})
    started=time.monotonic()
    try:
        with urllib.request.urlopen(req,context=ssl.create_default_context(cafile=cfg['notify_cert']),timeout=8) as response:
            result={'status':response.status,'body':json.load(response)}
    except urllib.error.HTTPError as e:result={'status':e.code}
    result['seconds']=time.monotonic()-started;print(json.dumps(result))
elif mode in ['mail-stop','mail-start','postgres-stop','postgres-start']:
    service,action=mode.split('-')
    subprocess.run(['docker','compose','-f',str(ROOT/'compose.json'),action,service],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
    if action=='start':
        for _ in range(45):
            try:
                if service=='postgres':psql('SELECT 1')
                else:subprocess.run(['docker','compose','-f',str(ROOT/'compose.json'),'exec','-T','mail','doveadm','user','box-a@ingress.invalid'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
                break
            except subprocess.CalledProcessError:time.sleep(1)
        else:raise RuntimeError('fixture service did not become ready')
    print(json.dumps({'service':service,'action':action}))
else:raise SystemExit('unknown scheduler action')
