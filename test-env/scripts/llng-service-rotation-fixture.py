# TEST_CASES: LLNG-READY-T-005
"""Native LDAP/database credential rotation in the disposable finance stack.

Dedicated bind account, database and role avoid changing shared credentials.
Loopback SCRAM is enforced for this role before checking retired passwords.
Private backup and stdin carry secrets; stdout contains only booleans/counts.
"""
import json
import os
import pathlib
import re
import secrets
import subprocess
import sys
import time

root = pathlib.Path('/home/whl/anas-llng-ready-test-20261023')
backup = root / 'service-rotation-backup'
docker = ['docker', '-H', 'unix:///run/anas-llng-test-1023.sock']
main, watch, pg, samba = ['anas_llng_test_1023_' + n for n in ['llng', 'llng_dirwatch', 'postgres', 'samba']]
database = role = 'llng_rotation_20261023'
bind_dn = 'CN=llngrotation,CN=Users,DC=llngtest,DC=invalid'
sections = ['globalStorageOptions', 'persistentStorageOptions', 'oidcStorageOptions',
            'samlStorageOptions', 'casStorageOptions', 'notificationStorageOptions']
action = sys.argv[1]
assert action in {'prepare', 'resume', 'activate', 'rotate-ldap', 'apply-ldap', 'rotate-db', 'apply-db', 'probe', 'rollback'}
os.umask(0o077)
infos = {}
for target in [main, watch, pg, samba]:
    infos[target] = json.loads(subprocess.check_output(docker + ['inspect', target]))[0]
    expected = 'llng-oidc-20261023' if target in [pg, samba] else 'llng-ready-20261023'
    assert infos[target]['Config']['Labels']['anas.test.run'] == expected
    assert any(m['Source'].startswith(str(root / 'data') + '/') for m in infos[target]['Mounts'])
assert any(m['Source'] == str(root / 'data/llng/conf') for m in infos[main]['Mounts'])


def run(args, data=None):
    result = subprocess.run(docker + args, input=data, capture_output=True)
    if result.returncode:
        if backup.exists():
            with (backup / 'private-errors.log').open('ab') as output:
                output.write(result.stdout + result.stderr)
        raise RuntimeError('isolated service rotation operation failed; private evidence retained')
    return result.stdout


def sql(query):
    return run(['exec', '-i', pg, 'psql', '-U', 'postgres', '-v', 'ON_ERROR_STOP=1', '-At'], query.encode()).decode().strip()


def latest():
    path = max((root / 'data/llng/conf').glob('lmConf-*.json'), key=lambda p: int(p.stem.split('-')[1]))
    return json.loads(path.read_text())


def configure(patch):
    # Native configuration API, restricted to fields owned by this exercise.
    program = r'''
use strict; use warnings; use JSON; use Lemonldap::NG::Common::Conf;
local $/; my $patch=decode_json(<STDIN>);
my $store=Lemonldap::NG::Common::Conf->new({type=>'File',dirName=>'/var/lib/lemonldap-ng/conf'});
my $conf=$store->getConf({noCache=>1,raw=>1}); die "configuration unavailable\n" unless $conf;
for my $key (keys %$patch) {
    die "unexpected rotation field\n" unless $key =~ /^(?:managerDn|managerPassword|(?:global|persistent|oidc|saml|cas|notification)StorageOptions)$/;
    $conf->{$key}=$patch->{$key};
}
$conf->{cfgAuthor}='ANAS isolated service rotation'; $conf->{cfgDate}=time;
$conf->{cfgLog}='native service credential acceptance';
die "configuration save failed\n" unless $store->saveConf($conf)>0;
'''
    run(['stop', watch])
    try:
        run(['exec', '-i', '-u', '33:33', main, 'perl', '-e', program], json.dumps(patch).encode())
        run(['exec', main, '/usr/share/lemonldap-ng/bin/lemonldap-ng-cli', '--user=www-data', '--group=www-data', 'update-cache'])
        run(['exec', main, 'curl', '-fsS', 'http://127.0.0.1:8089/reload'])
    finally:
        run(['start', watch])


def ldap_account(secret, create=False):
    program = r'''
import json, sys, ldb
from samba.auth import system_session
from samba.param import LoadParm
from samba.samdb import SamDB
lp=LoadParm();lp.load('/var/lib/samba/etc/smb.conf')
db=SamDB(url='/var/lib/samba/private/sam.ldb',session_info=system_session(),lp=lp)
assert str(db.domain_dn()).lower()=='dc=llngtest,dc=invalid'
data=json.load(sys.stdin); marker='ANAS LLNG service rotation 20261023'
rows=db.search(base=db.domain_dn(),expression='(sAMAccountName=llngrotation)',attrs=['description','userAccountControl'])
if data['create']:
    assert not rows, 'dedicated bind identity must be new'
    db.newuser('llngrotation',data['secret'],description=marker)
    rows=db.search(base=db.domain_dn(),expression='(sAMAccountName=llngrotation)',attrs=['description','userAccountControl'])
assert len(rows)==1 and str(rows[0]['description'][0])==marker
db.setpassword('(sAMAccountName=llngrotation)',data['secret'])
change=ldb.Message();change.dn=rows[0].dn
change['userAccountControl']=ldb.MessageElement(str(int(str(rows[0]['userAccountControl'][0]))|65536),ldb.FLAG_MOD_REPLACE,'userAccountControl')
db.modify(change)
'''
    run(['exec', '-i', samba, 'python3', '-c', program], json.dumps({'secret': secret, 'create': create}).encode())


def database_password(secret):
    assert re.fullmatch('[A-Za-z0-9!_-]+', secret)
    sql("SET password_encryption='scram-sha-256'; ALTER ROLE " + role + " PASSWORD '" + secret + "';")


def old_password_grace(minutes=None):
    # Keep the exact original file; modify only the disposable DC and reload
    # natively. A one-minute window tests real expiry without an hour-long wait.
    path = '/var/lib/samba/etc/smb.conf'
    saved = backup / 'smb.conf.original'
    if not saved.exists():
        saved.write_bytes(run(['exec', samba, 'cat', path]))
    content = saved.read_text()
    if minutes is not None:
        assert minutes in {0, 1}
        content = re.sub(r'(?im)^\s*old password allowed period\s*=.*\n?', '', content)
        content, count = re.subn(r'(?im)^\s*\[global\]\s*$', '[global]\n\told password allowed period = ' + str(minutes), content, count=1)
        assert count == 1
    run(['exec', '-i', samba, 'python3', '-c',
         'import pathlib,sys; pathlib.Path(sys.argv[1]).write_bytes(sys.stdin.buffer.read())', path], content.encode())
    # The 4.23.6 fixture kept accepting the old password beyond the shortened
    # window after reload-config. Restart the disposable DC to apply it, then
    # verify a fresh certificate-checked LDAPS connection before continuing.
    run(['restart', samba])
    for _ in range(30):
        ready = subprocess.run(docker + ['exec', main, 'perl', '-MNet::LDAP', '-e',
            "my $l=Net::LDAP->new('ldaps://dc.llngtest.invalid:636',timeout=>2,verify=>'require',cafile=>'/certs/trust-bundle.crt'); exit($l ? 0 : 1);"], capture_output=True)
        if ready.returncode == 0:
            break
        time.sleep(1)
    else:
        raise RuntimeError('isolated directory did not become ready after policy restart')
    if minutes is not None:
        assert run(['exec', samba, 'testparm', '-s', '--parameter-name=old password allowed period', path]).decode().strip() == str(minutes)


if action == 'prepare':
    backup.mkdir(mode=0o700)
    conf = latest()
    original = {k: conf[k] for k in sections + ['managerDn', 'managerPassword']}
    (backup / 'original.json').write_text(json.dumps(original))
    values = {name: 'R8!' + secrets.token_urlsafe(40) for name in ['ldap_old', 'ldap_new', 'db_old', 'db_new']}
    (backup / 'secrets.json').write_text(json.dumps(values))
    assert sql("SELECT count(*) FROM pg_database WHERE datname='" + database + "'") == '0'
    assert sql("SELECT count(*) FROM pg_roles WHERE rolname='" + role + "'") == '0'
    source = re.search(r'database=([^;]+)', conf['globalStorageOptions']['DataSource']).group(1)
    assert source == 'llng_restore_20261023'
    sql('CREATE ROLE ' + role + ' LOGIN; CREATE DATABASE ' + database + ' OWNER ' + role + ';')
    database_password(values['db_old'])
    dump = run(['exec', pg, 'pg_dump', '-U', 'postgres', '--no-owner', '--no-privileges', '-Fc', source])
    (backup / 'database.dump').write_bytes(dump)
    run(['exec', '-i', pg, 'pg_restore', '-U', 'postgres', '--role=' + role, '--no-owner', '--no-privileges',
         '--exit-on-error', '-d', database], dump)
    hba_inside = sql('SHOW hba_file;')
    mounts = [m for m in infos[pg]['Mounts'] if hba_inside.startswith(m['Destination'].rstrip('/') + '/')]
    assert len(mounts) == 1
    hba = pathlib.Path(mounts[0]['Source']) / pathlib.PurePosixPath(hba_inside).relative_to(mounts[0]['Destination'])
    assert hba.resolve().is_relative_to(root / 'data/postgres')
    prefix = ('# ANAS isolated LLNG credential rotation\n'
              f'host {database} {role} 127.0.0.1/32 scram-sha-256\n'
              f'host {database} {role} ::1/128 scram-sha-256\n')
    content = hba.read_text(); assert prefix not in content
    (backup / 'pg_hba.original').write_text(content)
    hba.write_text(prefix + content)  # Preserve owner/inode of the active file.
    assert sql('SELECT pg_reload_conf();') == 't'
    assert sql('SELECT count(*) FROM pg_hba_file_rules WHERE error IS NOT NULL;') == '0'
    ldap_account(values['ldap_old'], create=True)
    (backup / 'prepared').touch()

values = json.loads((backup / 'secrets.json').read_text())
original = json.loads((backup / 'original.json').read_text())
if action == 'resume':
    assert (backup / 'prepared').exists()
    current = latest()
    assert all(current[k] == v for k, v in original.items()), 'resume requires the original shared configuration'
    # Explicitly rearm only the dedicated identities after a failed attempt.
    old_password_grace(0)
    ldap_account(values['ldap_old'])
    database_password(values['db_old'])
elif action == 'activate':
    assert (backup / 'prepared').exists()
    patch = json.loads(json.dumps(original))
    patch.update(managerDn=bind_dn, managerPassword=values['ldap_old'])
    for section in sections:
        options = patch[section]
        for key in ['DataSource', 'dbiChain']:
            if key in options:
                options[key] = re.sub(r'database=[^;]+', 'database=' + database, options[key])
        for key in ['UserName', 'dbiUser']:
            if key in options: options[key] = role
        for key in ['Password', 'dbiPassword']:
            if key in options: options[key] = values['db_old']
    configure(patch)
elif action == 'rotate-ldap':
    old_password_grace(1)
    ldap_account(values['ldap_new'])
elif action == 'apply-ldap':
    configure({'managerPassword': values['ldap_new']})
elif action == 'rotate-db':
    database_password(values['db_new'])
elif action == 'apply-db':
    conf = latest(); patch = {k: conf[k] for k in sections}
    for options in patch.values():
        for key in ['Password', 'dbiPassword']:
            if key in options: options[key] = values['db_new']
    configure(patch)
elif action == 'rollback':
    # Original shared account/role were never changed. Restore native config;
    # dedicated roles, SCRAM rules and private backup remain for inspection.
    try:
        configure(original)
    finally:
        if (backup / 'smb.conf.original').exists():
            old_password_grace()
elif action == 'probe':
    program = r'''
use strict; use warnings; use JSON; use DBI; use Net::LDAP;
local $/; my $v=decode_json(<STDIN>); my %result;
for my $name (qw(db_old db_new)) {
    my $dbh=DBI->connect('dbi:Pg:dbname=llng_rotation_20261023;host=127.0.0.1;port=5432','llng_rotation_20261023',$v->{$name},{RaiseError=>0,PrintError=>0});
    $result{$name}=$dbh ? JSON::true : JSON::false;
    $dbh->disconnect if $dbh;
}
for my $name (qw(ldap_old ldap_new)) {
    my $ldap=Net::LDAP->new('ldaps://dc.llngtest.invalid:636',verify=>'require',cafile=>'/certs/trust-bundle.crt') or die "directory unavailable\n";
    my $response=$ldap->bind('CN=llngrotation,CN=Users,DC=llngtest,DC=invalid',password=>$v->{$name});
    $result{$name}=$response->code==0 ? JSON::true : JSON::false;
    $ldap->unbind;
}
print encode_json(\%result);
'''
    result = json.loads(run(['exec', '-i', main, 'perl', '-e', program], json.dumps(values).encode()))
    result['oidc_rows'] = int(
        run(['exec', pg, 'psql', '-U', 'postgres', '-d', database, '-Atc', 'SELECT count(*) FROM oidcsessions;']).decode().strip())
    print(json.dumps(result))
if action != 'probe':
    print(json.dumps({'action': action, 'dedicated_database': database, 'shared_credentials_unchanged': True}))
