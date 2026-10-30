#!/usr/bin/env python3
"""Generate a private, isolated native-software mail experiment (no production writes)."""
# TEST_CASES: MAILPOC-T-001, MAILPOC-T-002, MAILPOC-T-003
import argparse
import json
from pathlib import Path
import secrets
import subprocess


def write(root, name, content, mode=0o600):
    path = root / name
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content)
    path.chmod(mode)


def prepare(root, project):
    if not root.is_absolute() or 'mail' not in root.name or 'test' not in str(root):
        raise SystemExit('An explicit absolute mail test workspace is required')
    if (root / 'compose.json').exists():
        raise SystemExit('Refusing to replace an existing experiment')
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    secret = {k: secrets.token_urlsafe(24) for k in ('admin', 'alice', 'bob', 'client')}
    secret['des_key'] = secrets.token_hex(12)
    write(root, 'data/secrets.json', json.dumps(secret))
    write(root, 'data/tls/openssl.cnf', '''[req]
distinguished_name=dn
x509_extensions=ext
prompt=no
[dn]
CN=ANAS Mail PoC
[ext]
basicConstraints=critical,CA:TRUE
keyUsage=critical,digitalSignature,keyEncipherment,keyCertSign
subjectAltName=DNS:auth.mailtest.invalid,DNS:webmail.mailtest.invalid,DNS:mail.mailtest.invalid,DNS:dc.mailtest.invalid
''')
    subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '7',
                    '-config', str(root/'data/tls/openssl.cnf'), '-keyout', str(root/'data/tls/key.pem'),
                    '-out', str(root/'data/tls/cert.pem')], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    (root/'data/tls/key.pem').chmod(0o600)
    (root/'data/tls/cert.pem').chmod(0o644)
    for name in ('samba', 'samba-etc', 'llng/conf', 'llng/sessions', 'llng/psessions',
                 'dms/mail', 'dms/state', 'dms/logs', 'dms/config', 'roundcube/db', 'roundcube/temp'):
        (root/'data'/name).mkdir(parents=True, exist_ok=True)
    # Native file provisioning isolates the OAuth/Webmail experiment from a new
    # directory synchronization implementation. It is not an automatic AD provisioner.
    write(root, 'data/dms/config/postfix-accounts.cf', '\n'.join(
        f'{u}@mailtest.invalid|{{PLAIN}}{secrets.token_urlsafe(48)}|userdb_mail_path=/var/mail/box-{u} userdb_home=/var/mail/box-{u}/home'
        for u in ('alice', 'bob'))+'\n')
    write(root, 'data/dms/config/postfix-virtual.cf',
          'shopping@mailtest.invalid alice@mailtest.invalid\nwork@mailtest.invalid alice@mailtest.invalid\n')
    write(root, 'data/dms/bindings.json', json.dumps({'note':'test provisioning only; not a production synchronizer',
          'alice': {'mailbox_id':'box-alice'}, 'bob': {'mailbox_id':'box-bob'}}))
    write(root, 'data/dms/config/dovecot.cf', '''ssl_client_ca_file = /certs/cert.pem
auth_mechanisms = oauthbearer xoauth2
''')
    # No public MX or Internet delivery: all recipients belong to this test domain.
    write(root, 'data/dms/config/postfix-main.cf', '''smtpd_recipient_restrictions = reject_unauth_destination
smtpd_relay_restrictions = permit_sasl_authenticated, reject_unauth_destination
''')
    write(root, 'fixture/samba-start.sh', '''#!/bin/bash
set -euo pipefail
if [ ! -f /var/lib/samba/.mail-poc-ready ]; then
  test ! -f /var/lib/samba/private/sam.ldb || { echo 'Incomplete test provisioning; preserve evidence and use an empty workspace' >&2; exit 1; }
  password=$(python3 -c 'import json; print(json.load(open("/input/secrets.json"))["admin"])')
  samba-tool domain provision --realm=MAILTEST.INVALID --domain=MAILTEST --server-role=dc --dns-backend=SAMBA_INTERNAL --adminpass="$password" --use-rfc2307 --option='posix:eadb=/var/lib/samba/eadb.tdb' >/var/lib/samba/provision.log 2>&1
  unset password
  SAMBA_DC_BASE_DN=DC=mailtest,DC=invalid bash /usr/local/bin/install-identity-schema.sh
  python3 /fixture/samba-users.py
  cat >>/etc/samba/smb.conf <<'CONF'
[global]
  tls enabled = yes
  tls keyfile = /var/lib/samba/private/mail-poc-key.pem
  tls certfile = /certs/cert.pem
  tls cafile = /certs/cert.pem
CONF
  touch /var/lib/samba/.mail-poc-ready
fi
mkdir -p /var/log/samba
cp /certs/key.pem /var/lib/samba/private/mail-poc-key.pem
chown root:root /var/lib/samba/private/mail-poc-key.pem
chmod 600 /var/lib/samba/private/mail-poc-key.pem
exec samba -i --no-process-group
''', 0o755)
    write(root, 'fixture/samba-users.py', '''import json, uuid
from samba.samdb import SamDB
from samba.auth import system_session
from samba.param import LoadParm
import ldb
lp=LoadParm(); lp.load('/etc/samba/smb.conf')
db=SamDB(url='/var/lib/samba/private/sam.ldb', session_info=system_session(), lp=lp)
secrets=json.load(open('/input/secrets.json'))
bindings={}
for user in ('alice','bob'):
    db.newuser(user,secrets[user],givenname=user.title(),surname='PoC',mailaddress=user+'@mailtest.invalid')
    row=db.search(base='DC=mailtest,DC=invalid',expression='(sAMAccountName='+user+')',attrs=['objectGUID'])[0]
    anchor=str(uuid.UUID(bytes_le=bytes(row['objectGUID'][0])))
    msg=ldb.Message(); msg.dn=row.dn
    msg['anasIdentityAnchor']=ldb.MessageElement(anchor,ldb.FLAG_MOD_REPLACE,'anasIdentityAnchor')
    msg['mS-DS-ConsistencyGuid']=ldb.MessageElement(bytes(row['objectGUID'][0]),ldb.FLAG_MOD_REPLACE,'mS-DS-ConsistencyGuid')
    if user=='alice':
        msg['proxyAddresses']=ldb.MessageElement(['smtp:shopping@mailtest.invalid','smtp:work@mailtest.invalid'],ldb.FLAG_MOD_REPLACE,'proxyAddresses')
    db.modify(msg)
    bindings[user]={'anchor':anchor,'mailbox_id':'box-'+user}
json.dump(bindings,open('/var/lib/samba/test-bindings.json','w'))
''', 0o644)
    cfg = {
        'domain':'mailtest.invalid','portal':'https://auth.mailtest.invalid:18883/',
        'authentication':'AD','userDB':'AD','passwordDB':'AD','registerDB':'Null',
        'ldapServer':'ldaps://dc.mailtest.invalid:636','ldapVerify':'require','ldapCAFile':'/certs/cert.pem',
        'managerDn':'CN=Administrator,CN=Users,DC=mailtest,DC=invalid','managerPassword':secret['admin'],
        'ldapBase':'CN=Users,DC=mailtest,DC=invalid','ldapVersion':3,
        'AuthLDAPFilter':'(&(objectClass=user)(sAMAccountName=$user))',
        'ldapExportedVars':{'uid':'sAMAccountName','mail':'mail','cn':'cn','anasIdentityAnchor':'anasIdentityAnchor'},
        'https':1,'securedCookie':1,'cookieName':'mailpoc_sso','portalDisplayRegister':0,
        'portalDisplayResetPassword':0,'portalDisplayChangePassword':0,
        'issuerDBOpenIDConnectActivation':1,'issuerDBOpenIDConnectPath':'^/oauth2/',
        'issuerDBOpenIDConnectRule':1,'oidcServiceAllowAuthorizationCodeFlow':1,
        'oidcRPMetaDataOptions':{'roundcube':{
            'oidcRPMetaDataOptionsClientID':'mail-poc-roundcube','oidcRPMetaDataOptionsClientSecret':secret['client'],
            'oidcRPMetaDataOptionsRedirectUris':'https://webmail.mailtest.invalid:18883/index.php/login/oauth',
            'oidcRPMetaDataOptionsUserIDAttr':'anasIdentityAnchor','oidcRPMetaDataOptionsIDTokenSignAlg':'RS256',
            'oidcRPMetaDataOptionsIDTokenForceClaims':1,'oidcRPMetaDataOptionsBypassConsent':1,
            'oidcRPMetaDataOptionsRequirePKCE':1}},
        'oidcRPMetaDataExportedVars':{'roundcube':{'email':'mail','name':'cn','preferred_username':'uid'}},
        'applicationList':{'1mail':{'type':'category','catname':'Mail PoC','mail':{'type':'application','options':{
            'name':'Roundcube','description':'Mail experiment','uri':'https://webmail.mailtest.invalid:18883/',
            'display':'auto','logo':'mail.png'}}}},
        'reloadUrls':{},'portalMainLogo':'','portalSkin':'bootstrap','language':'en',
    }
    private_key = subprocess.check_output(['openssl', 'genrsa', '2048'], stderr=subprocess.DEVNULL)
    public_key = subprocess.check_output(['openssl', 'rsa', '-pubout'], input=private_key, stderr=subprocess.DEVNULL)
    cfg.update(oidcServicePrivateKeySig=private_key.decode(), oidcServicePublicKeySig=public_key.decode(), oidcServiceKeyIdSig='mail-poc-key')
    write(root, 'data/llng/custom.json', json.dumps(cfg))
    write(root, 'fixture/llng-start.sh', '''#!/bin/sh
set -eu
/usr/local/bin/anas-llng-restore-runtime
if [ ! -f /var/lib/lemonldap-ng/conf/lmConf-1.json ]; then
  cp -a /var/lib/lemonldap-ng/conf-orig/. /var/lib/lemonldap-ng/conf/
fi
chown -R www-data:www-data /var/lib/lemonldap-ng/conf /var/lib/lemonldap-ng/sessions /var/lib/lemonldap-ng/psessions
cp /certs/cert.pem /usr/local/share/ca-certificates/mail-poc.crt
update-ca-certificates >/dev/null
perl -MJSON::PP -MYAML -0777 -e 'print YAML::Dump(decode_json(<>))' /input/custom.json > /tmp/llng_config_custom.yaml
exec /bin/sh /docker-entrypoint.sh
''', 0o755)
    rc = {
        'oauth_provider':'generic','oauth_provider_name':'ANAS Mail PoC',
        'oauth_client_id':'mail-poc-roundcube','oauth_client_secret':secret['client'],
        'oauth_auth_uri':'https://auth.mailtest.invalid:18883/oauth2/authorize',
        'oauth_token_uri':'https://auth.mailtest.invalid:18883/oauth2/token',
        'oauth_identity_uri':'https://auth.mailtest.invalid:18883/oauth2/userinfo',
        'oauth_issuer':'https://auth.mailtest.invalid:18883/',
        'oauth_jwks_uri':'https://auth.mailtest.invalid:18883/oauth2/jwks',
        'oauth_scope':'openid email profile','oauth_identity_fields':['email'],
        'oauth_login_redirect':True,'oauth_verify_peer':'/certs/cert.pem','oauth_pkce':'S256',
        'use_https':True,'des_key':secret['des_key'], 'product_name':'ANAS Mail PoC',
        'identities_level':0,'enable_installer':False,'spellcheck_engine':'',
        'imap_conn_options':{'ssl':{'verify_peer':True,'verify_peer_name':True,'cafile':'/certs/cert.pem'}},
        'smtp_conn_options':{'ssl':{'verify_peer':True,'verify_peer_name':True,'cafile':'/certs/cert.pem'}},
    }
    # JSON carries only generated ASCII secrets and static values; encode as base64
    # rather than attempting PHP string escaping for executable configuration.
    import base64
    encoded=base64.b64encode(json.dumps(rc).encode()).decode()
    write(root, 'data/roundcube/config/zz-poc.php', "<?php\n$config = array_replace($config, json_decode(base64_decode('"+encoded+"'), true));\n", 0o644)
    write(root, 'fixture/nginx.conf', '''events {}
http {
  access_log off;
  server {
    listen 18883 ssl;
    server_name auth.mailtest.invalid;
    ssl_certificate /certs/cert.pem;
    ssl_certificate_key /certs/key.pem;
    location / { proxy_pass http://iam:80; proxy_set_header Host $http_host; proxy_set_header X-Forwarded-Proto https; }
  }
  server {
    listen 18883 ssl;
    server_name webmail.mailtest.invalid;
    ssl_certificate /certs/cert.pem;
    ssl_certificate_key /certs/key.pem;
    client_max_body_size 10m;
    location / { proxy_pass http://webmail:80; proxy_set_header Host $http_host; proxy_set_header X-Forwarded-Proto https; }
  }
}
''', 0o644)
    common={'restart':'no','labels':{'anas.mail-poc':'true'},'networks':['mailpoc']}
    def svc(image, **kwargs):
        return {**common,'image':image,**kwargs}
    services={
        'directory':svc('ghcr.io/anas-project/anas-samba-dc:4.23.6-r13',hostname='dc',mem_limit='256m',
            entrypoint=['/bin/bash','/fixture/samba-start.sh'],
            volumes=['./data/samba:/var/lib/samba','./data/samba-etc:/etc/samba','./data/tls:/certs:ro','./data/secrets.json:/input/secrets.json:ro','./fixture:/fixture:ro'],
            networks={'mailpoc':{'aliases':['dc.mailtest.invalid']}}),
        'iam':svc('anas-llng-passkey-test:20261023',mem_limit='320m',entrypoint=['/bin/sh','/fixture/llng-start.sh'],
            environment={'SSODOMAIN':'mailtest.invalid','PORTAL_HOSTNAME':'auth.mailtest.invalid','MANAGER_HOSTNAME':'manager.mailtest.invalid','NPROC':'1','PORT':'80','IPV4_ONLY':'true'},
            volumes=['./data/llng/conf:/var/lib/lemonldap-ng/conf','./data/llng/sessions:/var/lib/lemonldap-ng/sessions','./data/llng/psessions:/var/lib/lemonldap-ng/psessions','./data/llng/custom.json:/input/custom.json:ro','./data/tls:/certs:ro','./fixture:/fixture:ro'],
            tmpfs=['/etc/lemonldap-ng','/etc/nginx/sites-enabled']),
        'mail':svc('ghcr.io/docker-mailserver/docker-mailserver:16.0.1',hostname='mail.mailtest.invalid',mem_limit='512m',
            environment={'ENABLE_CLAMAV':'0','ENABLE_AMAVIS':'0','ENABLE_SPAMASSASSIN':'0','ENABLE_FAIL2BAN':'0','ENABLE_RSPAMD':'0','ENABLE_OPENDKIM':'0','ENABLE_OPENDMARC':'0','ENABLE_POLICYD_SPF':'0','ENABLE_QUOTAS':'0','ENABLE_UPDATE_CHECK':'0','PERMIT_DOCKER':'none','SPOOF_PROTECTION':'1','SSL_TYPE':'manual','SSL_CERT_PATH':'/certs/cert.pem','SSL_KEY_PATH':'/certs/key.pem','ENABLE_OAUTH2':'1','OAUTH2_INTROSPECTION_URL':'https://auth.mailtest.invalid:18883/oauth2/userinfo','POSTMASTER_ADDRESS':'alice@mailtest.invalid','LOG_LEVEL':'info'},
            volumes=['./data/dms/mail:/var/mail','./data/dms/state:/var/mail-state','./data/dms/logs:/var/log/mail','./data/dms/config:/tmp/docker-mailserver','./data/tls:/certs:ro'],
            ports=['19993:993','15887:587'],networks={'mailpoc':{'aliases':['mail.mailtest.invalid']}}),
        'webmail':svc('roundcube/roundcubemail:1.7.5-apache',mem_limit='256m',
            environment={'ROUNDCUBEMAIL_DB_TYPE':'sqlite','ROUNDCUBEMAIL_DEFAULT_HOST':'ssl://mail.mailtest.invalid','ROUNDCUBEMAIL_DEFAULT_PORT':'993','ROUNDCUBEMAIL_SMTP_SERVER':'tls://mail.mailtest.invalid','ROUNDCUBEMAIL_SMTP_PORT':'587','ROUNDCUBEMAIL_PLUGINS':'archive,zipdownload','ROUNDCUBEMAIL_UPLOAD_MAX_FILESIZE':'10M'},
            volumes=['./data/roundcube/db:/var/roundcube/db','./data/roundcube/config:/var/roundcube/config:ro','./data/roundcube/temp:/tmp/roundcube-temp','./data/tls:/certs:ro']),
        'proxy':svc('nginx:alpine',mem_limit='48m',
            volumes=['./fixture/nginx.conf:/etc/nginx/nginx.conf:ro','./data/tls:/certs:ro'],ports=['18883:18883'],
            networks={'mailpoc':{'aliases':['auth.mailtest.invalid','webmail.mailtest.invalid']}})
    }
    # Docker internal networks do not publish host ports on the tested engine.
    # The runner must block NEW outbound forwarding in the dedicated namespace.
    compose={'name':project,'services':services,'networks':{'mailpoc':{'name':project+'_mailpoc'}}}
    write(root,'compose.json',json.dumps(compose,indent=2))
    print(json.dumps({'workspace':str(root),'project':project,'services':list(services),'credentials_printed':False}))


if __name__ == '__main__':
    parser=argparse.ArgumentParser()
    parser.add_argument('--workspace',type=Path,required=True)
    parser.add_argument('--project',default='anas_mail_test_1023')
    args=parser.parse_args()
    prepare(args.workspace,args.project)
