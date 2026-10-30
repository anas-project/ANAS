# TEST_CASES: LLNG-READY-T-005
"""Rotate a disposable Nextcloud OIDC secret through native interfaces.

The two endpoints are changed separately to exercise the mismatch boundary.
This does not implement or claim an ANAS workspace credential-rotation command.
Secrets stay in a private backup and stdin; output contains only status/error codes.
"""
import http.client
import json
import os
import pathlib
import secrets
import subprocess
import sys
import urllib.parse

root = pathlib.Path('/home/whl/anas-llng-ready-test-20261023')
docker = ['docker', '-H', 'unix:///run/anas-llng-test-1023.sock']
main = 'anas_llng_test_1023_llng'
cloud = 'anas_llng_test_1023_nextcloud'
backup = root / 'client-rotation-backup'
action = sys.argv[1]
assert action in {'prepare', 'provider', 'consumer', 'probe', 'rollback'}
os.umask(0o077)
for name in [main, cloud]:
    info = json.loads(subprocess.check_output(docker + ['inspect', name]))[0]
    assert info['Config']['Labels']['anas.test.run'] == 'llng-ready-20261023'
    if name == main:
        assert any(m['Source'] == str(root / 'data/llng/conf') for m in info['Mounts'])

if action == 'prepare':
    conf_file = max((root / 'data/llng/conf').glob('lmConf-*.json'), key=lambda p: int(p.stem.split('-')[1]))
    conf = json.loads(conf_file.read_text())
    rp = conf['oidcRPMetaDataOptions']['nextcloud']
    assert rp['oidcRPMetaDataOptionsClientID'] == 'nextcloud'
    original = rp['oidcRPMetaDataOptionsClientSecret']
    assert original == json.loads((root / 'secrets.json').read_text())['nextcloud_oidc']
    backup.mkdir(mode=0o700)
    (backup / 'secrets.json').write_text(json.dumps({'original': original, 'replacement': secrets.token_urlsafe(48)}))

values = json.loads((backup / 'secrets.json').read_text())

def provider(secret):
    program = r'''
use strict; use warnings; use JSON; use Lemonldap::NG::Common::Conf;
local $/; my $input=decode_json(<STDIN>);
my $store=Lemonldap::NG::Common::Conf->new({type=>'File',dirName=>'/var/lib/lemonldap-ng/conf'});
die "configuration unavailable\n" unless $store;
my $conf=$store->getConf({noCache=>1,raw=>1}); die "configuration unavailable\n" unless $conf;
my $rp=$conf->{oidcRPMetaDataOptions}->{nextcloud};
die "unexpected client\n" unless $rp->{oidcRPMetaDataOptionsClientID} eq 'nextcloud';
$rp->{oidcRPMetaDataOptionsClientSecret}=$input->{secret};
$conf->{cfgAuthor}='ANAS isolated client rotation'; $conf->{cfgDate}=time;
$conf->{cfgLog}='native client secret acceptance';
die "configuration save failed\n" unless $store->saveConf($conf)>0;
'''
    result = subprocess.run(docker + ['exec', '-i', '-u', '33:33', main, 'perl', '-e', program],
                            input=json.dumps({'secret': secret}), capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError('isolated provider credential update failed; backup retained')
    for args in [
        ['exec', main, '/usr/share/lemonldap-ng/bin/lemonldap-ng-cli', '--user=www-data', '--group=www-data', 'update-cache'],
        ['exec', main, 'curl', '-fsS', 'http://127.0.0.1:8089/reload'],
    ]:
        if subprocess.run(docker + args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
            raise RuntimeError('isolated provider credential reload failed; backup retained')

def consumer(secret):
    path = '/tmp/anas-client-rotation-' + secrets.token_hex(12)
    try:
        write = subprocess.run(docker + ['exec', '-i', '-u', '33:33', cloud, 'php', '-r',
            'umask(0077); $f=fopen($argv[1], "x"); if (!$f) exit(1); '
            'fwrite($f, stream_get_contents(STDIN)); fclose($f);', path],
            input=secret, capture_output=True, text=True)
        if write.returncode:
            raise RuntimeError('isolated private credential projection failed')
        result = subprocess.run(docker + ['exec', '-u', '33:33', cloud, 'php', '/var/www/html/occ',
            # 8.11's update-option detection omits clientsecret-file. Repeating
            # the existing client ID selects the update path without changing it.
            'user_oidc:provider', '--quiet', '--clientid=nextcloud', '--clientsecret-file=' + path, 'anas'], capture_output=True)
        if result.returncode:
            raise RuntimeError('isolated consumer credential update failed; backup retained')
    finally:
        subprocess.run(docker + ['exec', '-u', '33:33', cloud, 'rm', '-f', path],
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True)

if action == 'provider':
    provider(values['replacement'])
elif action == 'consumer':
    consumer(values['replacement'])
elif action == 'rollback':
    provider(values['original'])
    consumer(values['original'])

if action == 'probe':
    result = {}
    for name, secret in values.items():
        # Only the isolated internal HTTP leg is used; no code or token is minted.
        conn = http.client.HTTPConnection('10.253.26.2', 80, timeout=20)
        body = urllib.parse.urlencode({'client_id': 'nextcloud', 'client_secret': secret,
            'grant_type': 'authorization_code', 'code': 'deliberately-invalid-rotation-probe',
            'redirect_uri': 'https://cloud.llngtest.invalid:18443/apps/user_oidc/code'})
        conn.request('POST', '/oauth2/token', body, {'Host': 'auth.llngtest.invalid',
            'Content-Type': 'application/x-www-form-urlencoded'})
        response = conn.getresponse()
        data = json.loads(response.read()); conn.close()
        error = data.get('error')
        assert error in {'invalid_client', 'invalid_grant'} and 'access_token' not in data
        result[name] = {'status': response.status, 'error': error}
    print(json.dumps(result))
else:
    print(json.dumps({'action': action, 'client_id': 'nextcloud'}))
