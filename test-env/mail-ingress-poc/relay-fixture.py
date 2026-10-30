#!/usr/bin/env python3
"""Isolated Postfix + filesystem source on the user-approved osa host."""
from pathlib import Path
import json
import os
import subprocess
import sys

ROOT=Path('/home/ubuntu/anas-mail-relay-test-20261025')
SOCKET='unix:///run/anas-mail-cloud-1025.sock'
# Docker 20 uses the config digest after import; the source daemon reports the
# manifest digest. The private image-baseline records identical rootfs layers.
IMAGE='sha256:4b35cb5d547b3cb7e2de20ce5c0687f4f4bd3694665277dda319246be115b527'
NAME='anas_mail_cloud_1025_relay'
os.environ['DOCKER_HOST']=SOCKET
def run(*args,data=None):return subprocess.check_output(args,input=data,stderr=subprocess.PIPE).decode()
assert run('docker','info','--format','{{.DockerRootDir}}').strip()==str(ROOT/'docker')
mode=sys.argv[1]
def guard():
    rows=run('docker','ps','-aq','--filter','label=anas.mail-relay-poc=true').split()
    if not rows:return []
    rows=json.loads(run('docker','inspect',*rows))
    assert len(rows)==1 and rows[0]['Name']=='/'+NAME
    assert rows[0]['Config']['Labels']['anas.mail-relay-workspace']==str(ROOT)
    for mount in rows[0]['Mounts']:
        assert mount['Type']=='bind' and Path(mount['Source']).resolve().is_relative_to(ROOT)
    return rows
def write(path,value):
    path.write_text(value);path.chmod(0o600)
if mode=='prepare':
    assert not guard()
    f=ROOT/'fixture';f.mkdir(mode=0o700,exist_ok=True)
    cfg=json.loads((ROOT/'relay-runtime.json').read_text())
    assert cfg['root']=='/var/lib/anas-relay' and set(cfg['routes'])=={'box-a@relay.ingress.invalid','box-b@relay.ingress.invalid'}
    write(f/'relay.json',json.dumps(cfg))
    main='''compatibility_level = 3.6
myhostname = relay.ingress.invalid
myorigin = relay.ingress.invalid
mydestination = localhost
inet_interfaces = all
inet_protocols = ipv4
relay_domains = relay.ingress.invalid
relay_recipient_maps = regexp:/fixture/recipients
transport_maps = static:anaspoc:
anaspoc_destination_recipient_limit = 1
smtpd_relay_restrictions = reject_unauth_destination
smtpd_recipient_restrictions = reject_unlisted_recipient
smtpd_tls_security_level = none
smtputf8_enable = no
message_size_limit = 65536
enable_long_queue_ids = yes
maillog_file = /dev/stdout
mail_owner = postfix
setgid_group = postdrop
queue_directory = /var/spool/postfix
data_directory = /var/lib/postfix
'''
    master='''smtp inet n - n - - smtpd
pickup unix n - n 60 1 pickup
cleanup unix n - n - 0 cleanup
qmgr unix n - n 300 1 qmgr
rewrite unix - - n - - trivial-rewrite
bounce unix - - n - 0 bounce
defer unix - - n - 0 bounce
trace unix - - n - 0 bounce
verify unix - - n - 1 verify
flush unix n - n 1000? 0 flush
proxymap unix - - n - - proxymap
proxywrite unix - - n - 1 proxymap
showq unix n - n - - showq
postlog unix-dgram n - n - 1 postlogd
anvil unix - - n - 1 anvil
error unix - - n - - error
retry unix - - n - - error
discard unix - - n - - discard
anaspoc unix - n n - 1 pipe
  flags=R user=nobody argv=/fixture/cloud-relay /fixture/relay.json accept ${recipient} ${sender} ${queue_id}
'''
    write(f/'main.cf',main);write(f/'master.cf',master)
    write(f/'recipients','/^box-[ab]@relay\\.ingress\\.invalid$/ OK\n')
    for name in ['main.cf','master.cf','recipients']:(f/name).chmod(0o644)
    for name in ['postfix-queue','postfix-data','spool']:(ROOT/'data'/name).mkdir(mode=0o700,parents=True,exist_ok=True)
    for name in ['mail','state','logs','config','tls']:(ROOT/'unused'/name).mkdir(mode=0o700,parents=True,exist_ok=True)
    f.joinpath('cloud-relay').chmod(0o755)
    run('sudo','-n','chown','-R','65534:65534',str(f),str(ROOT/'data/spool'))
    run('sudo','-n','chown','0:0',str(f/'main.cf'),str(f/'master.cf'),str(f/'recipients'))
    print(json.dumps({'prepared':True}))
elif mode=='start':
    assert not guard()
    f=ROOT/'fixture'
    args=['docker','run','-d','--name',NAME,'--network','host','--dns','1.1.1.1','--memory','128m','--cpus','0.5',
          '--restart','no','--label','anas.mail-relay-poc=true','--label','anas.mail-relay-workspace='+str(ROOT)]
    mounts=[(str(f),'/fixture',True),(str(f/'main.cf'),'/etc/postfix/main.cf',True),(str(f/'master.cf'),'/etc/postfix/master.cf',True),
            (str(ROOT/'data/spool'),'/var/lib/anas-relay',False),(str(ROOT/'data/postfix-queue'),'/var/spool/postfix',False),(str(ROOT/'data/postfix-data'),'/var/lib/postfix',False)]
    image=json.loads(run('docker','image','inspect',IMAGE))[0]
    covered={m[1] for m in mounts}
    for i,target in enumerate((image['Config'].get('Volumes') or {})):
        if target not in covered:
            path=ROOT/'unused'/str(i);path.mkdir(exist_ok=True)
            mounts.append((str(path),target,False))
    for source,target,readonly in mounts:args+=['--mount',f'type=bind,source={source},target={target}'+(',readonly' if readonly else '')]
    args+=['--entrypoint','/bin/sh',IMAGE,'-c',
           'set -e; chown root:root /var/spool/postfix; chmod 0755 /var/spool/postfix; chown postfix:postfix /var/lib/postfix; postfix check; runuser -u nobody -- /fixture/cloud-relay /fixture/relay.json serve & exec postfix start-fg']
    run(*args);rows=guard();assert len(rows)==1
    print(json.dumps({'started':True,'container':rows[0]['Id'],'image':rows[0]['Image']}))
elif mode=='snapshot':
    rows=guard();assert len(rows)==1
    p=subprocess.run(['sudo','-n','find',str(ROOT/'data/spool/mail'),'-name','metadata.json'],text=True,capture_output=True)
    messages=[]
    if p.returncode==0:
        for name in p.stdout.splitlines():
            data=json.loads(run('sudo','-n','cat',name));data['delivered']=subprocess.run(['sudo','-n','test','-f',str(Path(name).with_name('ack.json'))]).returncode==0
            messages.append(data)
    queue=run('docker','exec',NAME,'postqueue','-j')
    print(json.dumps({'running':rows[0]['State']['Running'],'messages':messages,'queue':queue}))
elif mode=='restart':
    assert len(guard())==1
    run('docker','restart',NAME)
    print(json.dumps({'restarted':True,'data_preserved':True}))
elif mode in ['block-spool','unblock-spool','flush','fault-after-publish']:
    assert len(guard())==1
    if mode=='flush':run('docker','exec',NAME,'postqueue','-f')
    elif mode=='fault-after-publish':run('docker','exec','--user','nobody',NAME,'touch','/var/lib/anas-relay/fault-after-publish')
    else:run('sudo','-n','chmod','500' if mode=='block-spool' else '700',str(ROOT/'data/spool/staging'))
    print(json.dumps({mode:True}))
elif mode=='cleanup':
    rows=guard()
    if rows:
        evidence=ROOT/'evidence';evidence.mkdir(mode=0o700,exist_ok=True)
        p=subprocess.run(['docker','logs',NAME],text=True,stdout=subprocess.PIPE,stderr=subprocess.STDOUT)
        write(evidence/'postfix.log',p.stdout)
        run('docker','rm','-f',NAME)
    assert not guard();print(json.dumps({'removed':len(rows),'data_preserved':True}))
else:raise SystemExit('unknown relay fixture mode')
