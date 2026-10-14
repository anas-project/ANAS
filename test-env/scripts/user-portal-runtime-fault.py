#!/usr/bin/env python3
# TEST_CASES: PORTAL-T-003
"""Owned-container fault controls for the real portal acceptance suite."""
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from urllib.parse import urlsplit,urlunsplit

root=Path(sys.argv[1]);action=sys.argv[2]
if not re.fullmatch(r'/home/whl/anas-user-portal-e2e-test-[A-Za-z0-9-]+',str(root)):
    raise SystemExit('isolated fixture required')
run=json.loads((root/'run.json').read_text());prefix=run['prefix'];assert re.fullmatch(r'anas_pu[0-9a-f]{8}_',prefix)
socket='unix:///run/'+root.name+'.sock';assert run['socket']==socket.removeprefix('unix://')
private=root/'private';assert private.resolve()==private and private.stat().st_mode&0o077==0
name=prefix+'user_portal';samba=prefix+'samba_dc'
def docker(*args):return subprocess.check_output(['docker','-H',socket,*args],stderr=subprocess.PIPE)
def inspect():
 d=json.loads(docker('inspect',name))[0];assert d['Name']=='/'+name;return d
original=private/'portal-runtime-original.json'
if not original.exists():original.write_text(json.dumps(inspect()));original.chmod(0o600)
base=json.loads(original.read_text())
def recreate(proxy):
 env=dict(x.split('=',1) for x in base['Config']['Env'])
 if proxy:
  u=urlsplit(env['USER_PORTAL_LDAP_URL']);env['USER_PORTAL_LDAP_URL']=urlunsplit((u.scheme,u.hostname+':1636',u.path,u.query,u.fragment))
 envfile=private/'portal-fault.env';envfile.write_text('\n'.join(k+'='+v for k,v in env.items())+'\n');envfile.chmod(0o600)
 args=['run','-d','--name',name,'--hostname',base['Config']['Hostname'],'--read-only','--cap-drop','ALL','--cap-add','CHOWN','--cap-add','SETUID','--cap-add','SETGID','--security-opt','no-new-privileges:true','--env-file',str(envfile)]
 networks=list(base['NetworkSettings']['Networks']);assert len(networks)==1 and networks[0].startswith(prefix);args+=['--network',networks[0]]
 for dns in base['HostConfig']['Dns']:args+=['--dns',dns]
 for m in base['Mounts']:
  assert root in Path(m['Source']).parents
  args+=['--volume',m['Source']+':'+m['Destination']+('' if m['RW'] else ':ro')]
 for k,v in base['Config']['Labels'].items():args+=['--label',k+'='+v]
 args+=['--entrypoint','/usr/local/bin/anas-user-portal',base['Image']]
 docker('rm','-f',name);docker(*args);envfile.unlink()
if action=='proxy-start':
 script=root/'source/test-env/scripts/user-portal-ldap-reply-proxy.py'
 docker('cp',str(script),samba+':/tmp/anas-portal-reply-proxy.py')
 docker('exec','-d','-e','PORTAL_TEST_FIXTURE='+root.name,samba,'python3','/tmp/anas-portal-reply-proxy.py')
elif action=='proxy-enable':recreate(True)
elif action=='proxy-disable':recreate(False)
elif action=='proxy-arm':docker('exec',samba,'touch','/tmp/anas-portal-drop-next-reply')
elif action=='proxy-result':
 result=json.loads(docker('exec',samba,'cat','/tmp/anas-portal-proxy-result.json'));print(json.dumps(result));sys.exit(0)
elif action=='restart':docker('restart',name)
elif action=='audit-arm':
 ready=private/'audit-fault-ready'
 if ready.exists():ready.unlink()
 out=(private/'audit-fault-stderr').open('ab')
 subprocess.Popen(['python3',str(root/'source/test-env/scripts/user-portal-audit-fault.py'),str(root)],stdout=out,stderr=out,start_new_session=True)
 import time
 for _ in range(50):
  if ready.exists():break
  time.sleep(.1)
 assert ready.exists()
elif action=='audit-restore':
 docker('stop',name)
 p=root/'workspace/data/user_portal/audit/audit.jsonl';saved=p.with_name('audit-held.jsonl');assert saved.is_file() and not saved.is_symlink()
 p.rename(p.with_name('audit-replacement.jsonl'));saved.rename(p);docker('start',name)
elif action=='audit-intent-failure':
 p=root/'workspace/data/user_portal/audit/audit.jsonl';assert p.stat().st_nlink==1;os.link(p,p.with_name('audit-extra-link.jsonl'))
elif action=='audit-unlink-failure':
 p=root/'workspace/data/user_portal/audit/audit-extra-link.jsonl';assert p.stat().st_nlink==2;p.unlink();docker('restart',name)
elif action=='audit-summary':
 p=root/'workspace/data/user_portal/audit/audit.jsonl';records=[json.loads(line) for line in p.read_text().splitlines()];print(json.dumps({'records':len(records),'pending':[r['id'] for r in records if r['phase']=='intent' and not any(x['id']==r['id'] and x['phase']=='result' for x in records)]}));sys.exit(0)
elif action=='audit-contract':
 p=root/'workspace/data/user_portal/audit/audit.jsonl';raw=p.read_text();records=[json.loads(line) for line in raw.splitlines()]
 fixture=json.loads((private/'ldap.json').read_text());secrets=[fixture[k] for k in ['old','new','wrong']]+[v['password'] for v in fixture['services'].values()]
 checks=[{'requirements':['PORTAL-R-025'],'name':'durable audit contains no fixture passwords or tokens','pass':all(s not in raw for s in secrets) and re.search(r'eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+',raw) is None},
 {'requirements':['PORTAL-R-032'],'name':'every durable record has request context','pass':bool(records) and all(v.get('id') and v.get('time') and v.get('action') and re.fullmatch(r'[a-f0-9]{64}',v.get('config_digest','')) for v in records)},
 {'requirements':['PORTAL-R-032'],'name':'write intents bind stable actor and target','pass':all(re.fullmatch(r'[a-f0-9-]{36}',v['actor']) and re.fullmatch(r'[a-f0-9-]{36}',v['target']) for v in records if v['phase']=='intent')},
 {'requirements':['PORTAL-R-032'],'name':'reset and member differences are durable','pass':any(v['action']=='password.reset' and v.get('force_change') is True and v.get('rule') for v in records) and any(v['action'].startswith('groups.member.') and isinstance(v.get('expected_direct'),bool) and v.get('group') and v.get('rule') for v in records)},
 {'requirements':['PORTAL-R-034'],'name':'rejected and failed operations are durable','pass':any(v.get('directory')=='denied' for v in records) and any(v.get('directory')=='rejected' for v in records)},
 {'requirements':['PORTAL-R-035','PORTAL-R-036'],'name':'saved results have an earlier intent and pending history survives','pass':all(any(i['id']==v['id'] and i['phase']=='intent' for i in records[:n]) for n,v in enumerate(records) if v.get('directory')=='saved') and any(v['phase']=='intent' and not any(x['id']==v['id'] and x['phase']=='result' for x in records) for v in records)}]
 result={'stage':'complete' if all(v['pass'] for v in checks) else 'failed','checks':checks};(root/'evidence/audit-contract.json').write_text(json.dumps(result));print(json.dumps(result));sys.exit(0 if result['stage']=='complete' else 1)
else:raise SystemExit('unknown fault action')
print(json.dumps({'action':action,'applied':True}))
