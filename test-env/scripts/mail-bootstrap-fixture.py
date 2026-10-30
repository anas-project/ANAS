#!/usr/bin/python3
"""Offline bootstrap + actual company reads in the mailschema.invalid fixture."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import uuid

import ldb
from samba.dcerpc import security
from samba.ndr import ndr_unpack


def load(path, name):
    spec=importlib.util.spec_from_file_location(name,path)
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
    return module


ROOT=Path('/candidate')
legacy=load(ROOT/'test-env/scripts/mail-directory-schema-fixture.py','legacy_mail_fixture')
BOOT=ROOT/'modules/samba_dc/samba_dc/root/usr/local/bin/bootstrap-mail-directory.py'
bootstrap=load(BOOT,'bootstrap_fixture')
BASE=legacy.BASE
assert BASE=='DC=mailschema,DC=invalid'
GROUPS='OU=Groups,'+BASE
IDS=Path('/evidence/fixture-ids.json')
CONFIG=Path('/evidence/company-live.json')


def anchor(db,dn):
    row=db.search(base=dn,scope=ldb.SCOPE_BASE,attrs=['objectGUID'])[0]
    guid=bytes(row['objectGUID'][0]);value=str(uuid.UUID(bytes_le=guid))
    legacy.modify(db,dn,{'mS-DS-ConsistencyGuid':guid,'anasIdentityAnchor':value})
    return value


def seed():
    db=legacy.local();secret=json.loads(legacy.SECRETS.read_text())
    for dn in (legacy.PEOPLE,legacy.SERVICES,GROUPS):db.add({'dn':dn,'objectClass':'organizationalUnit'})
    ids={}
    for name in ('alice','bob','reader','primary','outsider'):
        service=name in ('reader','primary')
        db.newuser(name,secret[name],userou='OU=Service Accounts' if service else 'OU=People')
        dn='CN='+name+','+(legacy.SERVICES if service else legacy.PEOPLE)
        ids[name]=anchor(db,dn)
        if not service:legacy.modify(db,dn,{'mail':name+'@mailschema.invalid'})
    legacy.modify(db,'CN=outsider,'+legacy.PEOPLE,{'userAccountControl':'514'})
    legacy.modify(db,'CN=alice,'+legacy.PEOPLE,{'displayName':'Alice Public','telephoneNumber':'12345'})
    for name in ('Finance','Parent','PrimaryTeam'):
        db.newgroup(name,groupou='OU=Groups');ids[name]=anchor(db,'CN='+name+','+GROUPS)
    legacy.modify(db,'CN=Finance,'+GROUPS,{'member':['CN=alice,'+legacy.PEOPLE]})
    legacy.modify(db,'CN=Parent,'+GROUPS,{'member':['CN=Finance,'+GROUPS]})
    legacy.modify(db,'CN=PrimaryTeam,'+GROUPS,{'member':['CN=bob,'+legacy.PEOPLE]})
    group=db.search(base='CN=PrimaryTeam,'+GROUPS,scope=ldb.SCOPE_BASE,attrs=['objectSid'])[0]
    rid=str(ndr_unpack(security.dom_sid,bytes(group['objectSid'][0]))).rsplit('-',1)[1]
    legacy.modify(db,'CN=bob,'+legacy.PEOPLE,{'primaryGroupID':rid})
    # A real stopped-directory snapshot is captured before any mail bootstrap.
    archive=Path('/evidence/samba-before.tar')
    with tarfile.open(archive,'w') as target:target.add('/var/lib/samba',arcname='samba')
    archive.chmod(0o600)
    snapshot=hashlib.sha256(archive.read_bytes()).hexdigest()
    os.environ.update(SAMBA_DC_BASE_DN=BASE,SAMBA_DC_BASE_USERS_DN=legacy.PEOPLE,SAMBA_DC_DOMAIN_ACTION='provision',SAMBA_DC_MAIL_DIRECTORY_ENABLED='true',SAMBA_DC_MAIL_INITIAL_SOURCE_DOMAIN='mailschema.invalid',SAMBA_DC_MAIL_INITIAL_SNAPSHOT_ID=snapshot)
    for role,prefix in {'reader':'SAMBA_DC_LDAP_BIND','primary':'SAMBA_DC_ANCHOR_BIND','portal':'SAMBA_DC_MAIL_CREATOR_BIND','address':'SAMBA_DC_MAIL_ADDRESS_BIND'}.items():
        name={'portal':'svc_mail_create','address':'svc_mail_address'}.get(role,role)
        os.environ[prefix+'_DN']='CN='+name+','+legacy.SERVICES
        os.environ[prefix+'_PASSWORD']=secret[role]
    cfg,passwords=bootstrap.configuration(os.environ)
    bootstrap.EVIDENCE=Path('/evidence/mail-bootstrap')
    assert bootstrap.execute(cfg,bootstrap.Native(cfg,passwords))
    before=bootstrap.private_json(bootstrap.MARKER)
    assert not bootstrap.execute(cfg,bootstrap.Native(cfg,passwords))
    assert bootstrap.private_json(bootstrap.MARKER)==before
    print(json.dumps({'check':'actual_offline_bootstrap_repeat','passed':True,'snapshot_sha256':snapshot}),flush=True)
    db=legacy.local()
    # Private legacy values must never be queried by the public directory.
    legacy.modify(db,'CN=alice,'+legacy.PEOPLE,{'proxyAddresses':['smtp:private-alias@mailschema.invalid','smtp:independent-private@mailschema.invalid']})
    cfg={'Directory':{'URL':'ldaps://127.0.0.1:636','BindDN':'CN=reader,'+legacy.SERVICES,'BindPassword':secret['reader'],'BaseDN':BASE,'UsersDN':legacy.PEOPLE,'GroupsDN':GROUPS,'MailDomain':'mailschema.invalid','AllowGroups':[{'DN':'CN=Parent,'+GROUPS,'Anchor':ids['Parent']},{'DN':'CN=PrimaryTeam,'+GROUPS,'Anchor':ids['PrimaryTeam']}]},'CAPEM':Path('/var/lib/samba/private/tls/ca.pem').read_text(),'Alice':ids['alice'],'Bob':ids['bob'],'Finance':ids['Finance'],'Parent':ids['Parent'],'Primary':ids['PrimaryTeam'],'Updated':False}
    CONFIG.write_text(json.dumps(cfg));CONFIG.chmod(0o600)
    IDS.write_text(json.dumps(ids));IDS.chmod(0o600)


def change():
    db=legacy.connection('Administrator')
    legacy.modify(db,'CN=alice,'+legacy.PEOPLE,{'displayName':'Alice Updated'})
    legacy.modify(db,'CN=bob,'+legacy.PEOPLE,{'userAccountControl':'514'})
    db.rename('CN=Parent,'+GROUPS,'CN=Parent Renamed,'+GROUPS)
    cfg=json.loads(CONFIG.read_text());cfg['Updated']=True;CONFIG.write_text(json.dumps(cfg))
    print(json.dumps({'check':'actual_profile_disable_group_rename','passed':True}),flush=True)


if __name__=='__main__':
    if sys.argv[1]=='provision':legacy.provision()
    elif sys.argv[1]=='seed':seed()
    elif sys.argv[1]=='audit-config':
        with Path('/etc/samba/smb.conf').open('a') as stream:stream.write('\n[global]\nlog level = 1 dsdb_json_audit:5@/evidence/dsdb.json\nlogging = file\n')
    elif sys.argv[1]=='change':change()
    else:raise SystemExit(2)
