# TEST_CASES: LLNG-READY-T-005
"""Exercise native LLNG key overlap and rollback only in the disposable finance stack.

Retirement here deliberately tests rejection before all historical test tokens expire.
Production retirement must wait for token lifetime and the agreed rollback window.
Private material is passed on stdin and retained only in the private fixture backup.
"""
import json
import os
import pathlib
import subprocess
import sys

root = pathlib.Path('/home/whl/anas-llng-ready-test-20261023')
docker = ['docker', '-H', 'unix:///run/anas-llng-test-1023.sock']
container = 'anas_llng_test_1023_llng'
backup = root / 'rotation-backup'
key_name = 'anas-rotation-20261023'
key_id = 'llng-test-rotated-20261023'
action = sys.argv[1]
assert action in {'prepare', 'promote', 'retire', 'rollback', 'refresh-consumer-cache'}
os.umask(0o077)
info = json.loads(subprocess.check_output(docker + ['inspect', container]))[0]
assert info['Config']['Labels']['anas.test.run'] == 'llng-ready-20261023'
assert any(m['Source'] == str(root / 'data/llng/conf') for m in info['Mounts'])
assert (root / 'data/llng/conf/.anas-ready').exists()

if action == 'refresh-consumer-cache':
    # user_oidc 8.11 caches JWKS for one hour and rejects an unknown kid before
    # reaching its refresh retry. Publish first, invalidate only this provider's
    # timestamp, then perform a real login before promoting the new signer.
    cloud = 'anas_llng_test_1023_nextcloud'
    cloud_info = json.loads(subprocess.check_output(docker + ['inspect', cloud]))[0]
    assert cloud_info['Config']['Labels']['anas.test.run'] == 'llng-ready-20261023'
    result = subprocess.run(docker + ['exec', '-u', '33:33', cloud, 'php', '/var/www/html/occ',
        'config:app:delete', 'user_oidc', 'provider-1-jwksCacheTimestamp'], capture_output=True)
    if result.returncode:
        raise RuntimeError('isolated Nextcloud native JWKS cache invalidation failed')
    print(json.dumps({'action': action, 'provider_id': 1}))
    sys.exit(0)

if action == 'prepare':
    backup.mkdir(mode=0o700)
    files = list((root / 'data/llng/conf').glob('lmConf-*.json'))
    path = max(files, key=lambda p: int(p.stem.split('-')[1]))
    conf = json.loads(path.read_text())
    original = {k: conf.get(k) for k in ['keys', 'oidcServiceSignatureKey']}
    (backup / 'original.json').write_text(json.dumps(original))
    for args in [
        ['openssl', 'genpkey', '-algorithm', 'RSA', '-pkeyopt', 'rsa_keygen_bits:2048', '-out', str(backup / 'new.key')],
        ['openssl', 'pkey', '-in', str(backup / 'new.key'), '-pubout', '-out', str(backup / 'new.pub')],
    ]:
        if subprocess.run(args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
            raise RuntimeError('isolated signing key generation failed')
else:
    original = json.loads((backup / 'original.json').read_text())

old_list = original['oidcServiceSignatureKey'] or 'default-oidc-sig,old-oidc-sig,new-oidc-sig'
if action == 'rollback':
    patch = original
else:
    keys = dict(original['keys'] or {})
    assert key_name not in keys
    keys[key_name] = {'keyPrivate': (backup / 'new.key').read_text(),
                      'keyPublic': (backup / 'new.pub').read_text(), 'keyId': key_id}
    order = old_list + ',' + key_name if action == 'prepare' else key_name + ',' + old_list
    if action == 'retire':
        order = key_name
    patch = {'keys': keys, 'oidcServiceSignatureKey': order}

program = r'''
use strict; use warnings; use JSON; use Lemonldap::NG::Common::Conf;
local $/; my $patch=decode_json(<STDIN>);
my $store=Lemonldap::NG::Common::Conf->new({type=>'File',dirName=>'/var/lib/lemonldap-ng/conf'});
die "configuration unavailable\n" unless $store;
my $conf=$store->getConf({noCache=>1,raw=>1}); die "configuration unavailable\n" unless $conf;
for my $key (keys %$patch) {
    die "unexpected rotation field\n" unless $key eq 'keys' || $key eq 'oidcServiceSignatureKey';
    if (defined $patch->{$key}) { $conf->{$key}=$patch->{$key}; } else { delete $conf->{$key}; }
}
$conf->{cfgAuthor}='ANAS isolated rotation acceptance'; $conf->{cfgDate}=time;
$conf->{cfgLog}='native key overlap acceptance';
my $version=$store->saveConf($conf); die "configuration save failed\n" unless $version>0;
print encode_json({configuration_version=>$version});
'''
result = subprocess.run(docker + ['exec', '-i', '-u', '33:33', container, 'perl', '-e', program],
                        input=json.dumps(patch), capture_output=True, text=True)
if result.returncode:
    raise RuntimeError('native rotation configuration failed; private backup retained')
version = json.loads(result.stdout)['configuration_version']
for args in [
    ['exec', container, '/usr/share/lemonldap-ng/bin/lemonldap-ng-cli', '--user=www-data', '--group=www-data', 'update-cache'],
    ['exec', container, 'curl', '-fsS', 'http://127.0.0.1:8089/reload'],
]:
    if subprocess.run(docker + args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
        raise RuntimeError('native rotation reload failed; private backup retained')
print(json.dumps({'action': action, 'configuration_version': version, 'new_key_id': key_id}))
