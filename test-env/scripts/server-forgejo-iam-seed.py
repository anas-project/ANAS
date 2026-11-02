#!/usr/bin/env python3
"""Seed the owned custom AD group after Samba readiness, before IAM starts.

Run inside the dedicated acceptance namespace with casdoor or llng as argument.
This creates test directory data; the production Hook never creates claim groups.
"""
import subprocess,json,time,os,sys
from pathlib import Path
r=Path('/home/whl/anas-fj-iam-20261030'); provider=sys.argv[1]
assert provider in ['casdoor','llng']
assert os.path.samestat(os.stat('/run/netns/anas-fj-iam-1030'),os.stat('/proc/self/ns/net'))
b=['docker','-H',os.environ.get('DOCKER_HOST','unix:///run/anas-fj-iam-1030-docker.sock')]
assert subprocess.check_output(b+['info','--format','{{.DockerRootDir}}']).decode().strip()==str(r/'docker')
prefix='fjic_' if provider=='casdoor' else 'fjil_'; workspace=r/(provider+'-work')
deadline=time.monotonic()+700
while True:
 p=subprocess.run(b+['inspect',prefix+'samba_dc'],capture_output=True)
 if p.returncode==0:
  v=json.loads(p.stdout)[0]
  assert v['Config']['Labels']['com.docker.compose.project.working_dir'].startswith(str(workspace/'.anas/deployments')+'/')
  if v['State'].get('Health',{}).get('Status')=='healthy':break
 assert time.monotonic()<deadline,'Samba did not become ready for owned group fixture'
 time.sleep(5)
code=r'''
import os,ldb
from samba.credentials import Credentials,DONT_USE_KERBEROS
from samba.param import LoadParm
from samba.samdb import SamDB
lp=LoadParm();lp.load('/etc/samba/smb.conf')
c=Credentials();c.guess(lp);c.set_username(os.environ['SAMBA_DC_ADMIN_NAME']);c.set_password(os.environ['SAMBA_DC_ADMIN_PASSWORD']);c.set_kerberos_state(DONT_USE_KERBEROS)
db=SamDB(url='ldap://127.0.0.1',lp=lp,credentials=c)
domain=str(db.domain_dn());assert domain.lower()=='dc=fj-iam,dc=test'
base=os.environ['SAMBA_DC_BASE_GROUPS_DN'];assert base.lower().endswith(','+domain.lower())
rows=db.search(base=domain,expression='(sAMAccountName=forgejo-reviewers)',attrs=['description'])
marker='ANAS Forgejo IAM acceptance 20261030'
if not rows:
 db.newgroup('forgejo-reviewers',groupou=base[:-(len(domain)+1)])
 rows=db.search(base=domain,expression='(sAMAccountName=forgejo-reviewers)',attrs=[])
 msg=ldb.Message();msg.dn=rows[0].dn;msg['description']=ldb.MessageElement(marker,ldb.FLAG_MOD_REPLACE,'description');db.modify(msg)
else:assert len(rows)==1 and str(rows[0].get('description',[''])[0])==marker
print('owned custom directory group is ready')
'''
subprocess.run(b+['exec','-i',prefix+'samba_dc','python3','-c',code],check=True)
