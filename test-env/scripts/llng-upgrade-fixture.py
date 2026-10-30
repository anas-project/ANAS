# TEST_CASES: LLNG-READY-T-008
"""Actual 2.23.2 Module baseline -> current candidate on separate fixture data.

Only the disposable daemon is addressed. The existing candidate containers and
their restored database are stopped and retained, then restarted by restore.
"""
import json
import os
import pathlib
import subprocess
import sys
import time

root = pathlib.Path('/home/whl/anas-llng-ready-test-20261023')
work = root / 'upgrade-baseline'
docker = ['docker', '-H', 'unix:///run/anas-llng-test-1023.sock']
name = 'anas_llng_test_1023_upgrade'
main = 'anas_llng_test_1023_llng'
watch = 'anas_llng_test_1023_llng_dirwatch'
database = 'llng_upgrade_v2_20261023'
action = sys.argv[1]
assert action in {'start-old', 'promote', 'restore', 'inventory'}
os.umask(0o077)
for target in [main, watch]:
    info = json.loads(subprocess.check_output(docker + ['inspect', target]))[0]
    assert info['Config']['Labels']['anas.test.run'] == 'llng-ready-20261023'
    assert any(m['Source'] == str(root / 'data/llng/conf') for m in info['Mounts'])

def run(args, **kwargs):
    result = subprocess.run(docker + args, capture_output=True, **kwargs)
    if result.returncode:
        raise RuntimeError('isolated upgrade operation failed; private baseline retained')
    return result.stdout

def ready(target):
    for _ in range(90):
        if subprocess.run(docker + ['exec', target, 'test', '-f', '/run/llng-configured'],
                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
            return
        time.sleep(2)
    raise RuntimeError('isolated upgrade readiness timed out; inspect private container logs')

def inventory():
    result = {}
    for table in ['sessions', 'psessions', 'oidcsessions', 'samlsessions']:
        query = "SELECT count(*), coalesce(md5(string_agg(id || a_session::text, '' ORDER BY id)), '') FROM " + table
        row = run(['exec', 'anas_llng_test_1023_postgres', 'psql', '-U', 'postgres', '-d', database, '-Atc', query]).decode().strip()
        count, digest = row.split('|')
        result[table] = {'count': int(count), 'content_digest': digest}
    return result

def start(image):
    run(['run', '-d', '--name', name, '--network', 'host', '--memory', '512m',
         '--label', 'anas.test.run=llng-ready-20261023',
         '--add-host', 'dc.llngtest.invalid:10.253.26.2', '--add-host', 'auth.llngtest.invalid:10.253.26.2',
         '--add-host', 'cloud.llngtest.invalid:10.253.26.2', '--add-host', 'portal.llngtest.invalid:10.253.26.2',
         '-e', 'PERL_LWP_SSL_CA_FILE=/certs/trust-bundle.crt',
         '--tmpfs', '/etc/lemonldap-ng', '--tmpfs', '/etc/nginx/sites-enabled',
         '--tmpfs', '/var/lib/lemonldap-ng/psessions', '--tmpfs', '/var/lib/lemonldap-ng/sessions',
         '-v', str(work / 'conf') + ':/var/lib/lemonldap-ng/conf', '-v', str(root / 'certs') + ':/certs:ro',
         '-v', str(root) + ':/fixture:ro',
         '-v', str(root / 'application-list.json') + ':/opt/anas/application-list.json:ro',
         '-v', str(root / 'account-policies.json') + ':/opt/anas/account-policies.json:ro',
         '--entrypoint', '/usr/bin/perl', image, '/fixture/upgrade-baseline/launch.pl'])
    ready(name)
    version = run(['exec', name, 'perl', '-MLemonldap::NG::Portal', '-e', 'print $Lemonldap::NG::Portal::VERSION']).decode()
    image_id = run(['inspect', '--format', '{{.Image}}', name]).decode().strip()
    return {'version': version, 'image': image_id}

if action == 'start-old':
    work.mkdir(mode=0o700)
    (work / 'conf').mkdir(mode=0o755)
    # Historical group parsing expects single-word names such as APP_<module>.
    # Give this upgrade-only client an isolated group instead of changing the
    # normal candidate's existing groups or silently bypassing admission.
    group_program = r'''
import json, pathlib, uuid, ldb
from samba.auth import system_session
from samba.param import LoadParm
from samba.samdb import SamDB
lp=LoadParm();lp.load('/var/lib/samba/etc/smb.conf')
db=SamDB(url='/var/lib/samba/private/sam.ldb',session_info=system_session(),lp=lp)
assert str(db.domain_dn()).lower()=='dc=llngtest,dc=invalid'
name='APP_llng_upgrade'; marker='ANAS LLNG upgrade 20261023'
rows=db.search(base=db.domain_dn(),expression='(sAMAccountName='+name+')',attrs=['description','objectGUID','member'])
if not rows:
    db.newgroup(name,groupou='OU=Groups')
    rows=db.search(base=db.domain_dn(),expression='(sAMAccountName='+name+')',attrs=['objectGUID','member'])
else:
    assert str(rows[0].get('description',[''])[0])==marker
row=rows[0];raw=bytes(row['objectGUID'][0]); change=ldb.Message();change.dn=row.dn
for key,value in {'description':marker,'anasIdentityAnchor':str(uuid.UUID(bytes_le=raw)),'mS-DS-ConsistencyGuid':raw}.items():
    change[key]=ldb.MessageElement(value,ldb.FLAG_MOD_REPLACE,key)
db.modify(change)
user=db.search(base=db.domain_dn(),expression='(sAMAccountName=portalnormal)',attrs=['description'])[0]
assert str(user.get('description',[''])[0])=='ANAS LLNG readiness 20261023'
if str(user.dn).lower() not in [str(v).lower() for v in row.get('member',[])]:
    db.add_remove_group_members(name,['portalnormal'],add_members_operation=True)
'''
    run(['exec', 'anas_llng_test_1023_samba', 'python3', '-c', group_program])
    env = json.loads((root / 'llng-env.json').read_text())
    env.update(LLNG_DB_NAME=database, LOGLEVEL='warn', LLNG_LOG_LEVEL='warn', NPROC='2', LLNG_WORKERS='2')
    env.update(OIDC_RP_APPS='vikunja', OIDC_RP__VIKUNJA__ALLOW_GROUPS='APP_llng_upgrade',
               OIDC_RP__VIKUNJA__RULE='(defined($anasIdentityAnchor) and $anasIdentityAnchor ne "") and inGroup("APP_llng_upgrade")',
               LLNG_REAUTH_CLIENT_IDS='', LLNG_ACCOUNT_CLIENT_IDS='', LLNG_REAUTH_PARENT_CLIENTS='{}')
    (work / 'env.json').write_text(json.dumps(env))
    (work / 'launch.pl').write_text("use strict;use warnings;use JSON;open my $f,'<','/fixture/upgrade-baseline/env.json' or die $!;local $/;my $e=decode_json(<$f>);@ENV{keys %$e}=values %$e;exec '/usr/local/bin/anas-entrypoint.sh';")
    count = run(['exec', 'anas_llng_test_1023_postgres', 'psql', '-U', 'postgres', '-Atc',
        "SELECT count(*) FROM pg_database WHERE datname='" + database + "'"]).decode().strip()
    assert count == '0', 'upgrade target must be a new database'
    run(['stop', watch, main])
    print(json.dumps(start('anas-llng-old-baseline:20261023')))
elif action == 'promote':
    assert work.is_dir()
    before = inventory()
    (work / 'old-inventory.json').write_text(json.dumps(before))
    run(['stop', name])
    # Keep the exact old config snapshot before the new entrypoint updates it.
    import shutil
    shutil.copytree(work / 'conf', work / 'old-conf')
    run(['rm', name])
    result = start('anas-llng-passkey-test:20261023')
    result['tables_preserved'] = inventory() == before
    assert result['tables_preserved'], 'startup must not discard existing native state'
    print(json.dumps(result))
elif action == 'inventory':
    print(json.dumps(inventory()))
else:
    # Exact fixture name; never remove a production or normal candidate container.
    exists = subprocess.run(docker + ['inspect', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0
    if exists:
        info = json.loads(run(['inspect', name]))[0]
        assert info['Config']['Labels']['anas.test.run'] == 'llng-ready-20261023'
        logs = subprocess.run(docker + ['logs', name], capture_output=True)
        (work / 'container-private.log').write_bytes(logs.stdout + logs.stderr)
        run(['rm', '-f', name])
    run(['start', main]); ready(main)
    run(['start', watch])
    print(json.dumps({'candidate_restored': True, 'old_baseline_retained': True}))
