#!/usr/bin/env python3
"""Extend the existing native fixture, never a production workspace."""
# TEST_CASES: MAILPOC-T-004, MAILPOC-T-005, MAILPOC-T-006
import base64
import importlib.util
import json
from pathlib import Path
import secrets
import subprocess
import sys

MAILGO_PLUGIN = r'''<?php
// TEST_CASES: MAILPOC-T-004
// Thin Roundcube 1.7.5 experiment adapter, not a production plugin.
class mailgo extends rcube_plugin
{
    private $credential;
    public function init()
    {
        // Compose/autocomplete requests do not necessarily list address sources.
        rcmail::get_instance()->config->set('autocomplete_addressbooks',['sql','mailgo_company']);
        $this->add_hook('addressbooks_list', [$this, 'address_sources']);
        $this->add_hook('addressbook_get', [$this, 'addressbook_get']);
        $this->add_hook('template_object_username', [$this, 'display_username']);
        foreach (['startup','authenticate','storage_init','smtp_connect','user_create','login_after'] as $hook) {
            $this->add_hook($hook, [$this, $hook]);
        }
    }
    public function address_sources($args)
    {
        if (empty($_SESSION['mailgo'])) return $args;
        $sources=$this->api('addressbooks', ['session_ref'=>$_SESSION['mailgo']['session_ref']]);
        foreach ($sources['sources'] as $source) $args['sources']['mailgo_'.$source['id']]=$source+['id'=>'mailgo_'.$source['id']];
        foreach ($sources['sources'] as $source) $args['sources']['mailgo_'.$source['id']]['id']='mailgo_'.$source['id'];
        $rc=rcmail::get_instance();
        $rc->config->set('autocomplete_addressbooks',array_merge(['sql'],array_keys($args['sources'])));
        return $args;
    }
    public function addressbook_get($args)
    {
        if ($args['id']==='mailgo_company') $args['instance']=new mailgo_directory_backend($this);
        return $args;
    }
    public function display_username($args)
    {
        if (!empty($_SESSION['mailgo']['address'])) { $args['content'] = htmlspecialchars($_SESSION['mailgo']['address'], ENT_QUOTES, 'UTF-8'); }
        return $args;
    }
    public function api($path, $data)
    {
        $rc = rcmail::get_instance();
        $c = curl_init('https://manage.mailtest.invalid:18884/internal/' . $path);
        curl_setopt_array($c, [CURLOPT_POST => true, CURLOPT_POSTFIELDS => http_build_query($data),
            CURLOPT_RETURNTRANSFER => true, CURLOPT_TIMEOUT => 5, CURLOPT_CAINFO => '/certs/cert.pem',
            CURLOPT_USERPWD => 'roundcube:' . $rc->config->get('mailgo_secret')]);
        $raw = curl_exec($c);
        $status = curl_getinfo($c, CURLINFO_HTTP_CODE);
        curl_close($c);
        if ($status !== 200) { http_response_code(403); exit('Mail session unavailable; return to mail management.'); }
        $result = json_decode($raw, true);
        if (!is_array($result)) { http_response_code(503); exit('Invalid credential response'); }
        return $result;
    }
    private function save($c)
    {
        $rc = rcmail::get_instance();
        $c['smtp_token'] = $rc->encrypt($c['smtp_token']);
        $_SESSION['password'] = $rc->encrypt('Bearer ' . $c['access_token']);
        $c['refresh_token'] = $rc->encrypt($c['refresh_token']);
        unset($c['access_token']);
        $_SESSION['mailgo'] = $c;
    }
    public function startup($args)
    {
        $rc = rcmail::get_instance();
        if (($_GET['_action'] ?? '') === 'plugin.mailgo') {
            $box = $_GET['box'] ?? '';
            if (!preg_match('/^box-0[123]$/D', $box)) { http_response_code(403); exit; }
            // One active mailbox per RC browser session. A new handoff destroys
            // the old session so stale compose requests cannot silently switch.
            $rc->kill_session();
            $state = bin2hex(random_bytes(32));
            $_SESSION['mailgo_state'] = $state;
            header('Location: https://manage.mailtest.invalid:18884/handoff?box=' . rawurlencode($box) . '&state=' . $state);
            exit;
        }
        if (isset($_GET['mailgo_code'])) {
            $state = $_GET['mailgo_state'] ?? '';
            if (empty($_SESSION['mailgo_state']) || !hash_equals($_SESSION['mailgo_state'], $state)) { http_response_code(403); exit('Handoff state rejected'); }
            unset($_SESSION['mailgo_state']);
            $this->credential = $this->api('redeem', ['code' => $_GET['mailgo_code'], 'state' => $state]);
            $args['action'] = 'login';
            $args['task'] = 'login';
        } elseif (!empty($_SESSION['mailgo']) && !empty($_SESSION['user_id'])) {
            $this->api('session', ['session_ref'=>$_SESSION['mailgo']['session_ref']]);
            if ($_SESSION['mailgo']['expires_at'] < time() + 30) {
                $c = $this->api('refresh', ['refresh_token' => $rc->decrypt($_SESSION['mailgo']['refresh_token'])]);
                if ($c['username'] !== $_SESSION['username']) { http_response_code(403); exit('Mailbox mismatch'); }
                $this->save($c);
                $this->sync_identities();
            }
        }
        return $args;
    }
    public function authenticate($args)
    {
        if (!$this->credential) { $args['valid'] = false; return $args; }
        $args['user'] = $this->credential['username'];
        $args['pass'] = 'Bearer ' . $this->credential['access_token'];
        $args['host'] = 'ssl://mail.mailtest.invalid:993';
        $args['valid'] = true;
        $args['cookiecheck'] = false;
        $this->save($this->credential);
        return $args;
    }
    public function storage_init($args) { $args['auth_type'] = 'XOAUTH2'; return $args; }
    public function smtp_connect($args)
    {
        $rc = rcmail::get_instance();
        $args['smtp_auth_type'] = 'XOAUTH2';
        $args['smtp_user'] = $_SESSION['mailgo']['smtp_username'] ?? '';
        $args['smtp_pass'] = 'Bearer ' . $rc->decrypt($_SESSION['mailgo']['smtp_token'] ?? '');
        return $args;
    }
    public function user_create($args)
    {
        $args['email'] = $this->credential['address'];
        $args['name'] = $this->credential['address'];
        return $args;
    }
    private function sync_identities()
    {
        $rc = rcmail::get_instance();
        $c = $_SESSION['mailgo'];
        $old = $rc->user->list_identities();
        $known = array_column($old, 'email');
        foreach ($c['addresses'] as $address) {
            if (!in_array($address, $known, true)) { $rc->user->insert_identity(['email' => $address, 'name' => $address]); }
        }
        foreach ($rc->user->list_identities() as $identity) {
            if (!in_array($identity['email'], $c['addresses'], true)) { $rc->user->delete_identity($identity['identity_id']); }
            elseif ($identity['email'] === $c['address']) { $rc->user->set_default($identity['identity_id']); }
        }
    }
    public function login_after($args)
    {
        $this->save($this->credential);
        $this->sync_identities();
        return $args;
    }
}
'''

SENDER_ADAPTER = r'''-- TEST_CASES: MAILPOC-T-004
-- Transport adapter only. All address ownership decisions are made by Go.
local http = require 'rspamd_http'
local ucl = require 'ucl'
local secret_file = assert(io.open('/tmp/docker-mailserver/rspamd-key', 'r'))
local auth = secret_file:read('*a'):gsub('%s+', '')
secret_file:close()
rspamd_config:register_symbol({
  name = 'MAILGO_SENDER', type = 'prefilter', priority = 20,
  callback = function(task)
    local login = task:get_user()
    if not login or login == '' then return end -- ordinary incoming SMTP
    local function addresses(kind)
      local result = {}
      for _, item in ipairs(task:get_from(kind) or {}) do table.insert(result, item.addr or '') end
      return result
    end
    local queued = http.request({task = task, url = 'http://manager:8080/internal/sender',
      method = 'POST', timeout = 3, mime_type = 'application/json',
      headers = {Authorization = 'Basic ' .. auth},
      body = ucl.to_format({login=login, envelope=addresses('smtp'), from=addresses('mime'),
        from_count=#(task:get_header_full('From') or {})}, 'json-compact'),
      callback = function(err, code)
        if err or (code ~= 204 and code ~= 403) then
          task:set_pre_result('soft reject', 'Mail authorization unavailable', 'mailgo')
        elseif code == 403 then
          task:set_pre_result('reject', 'Sender address not authorized for this mailbox', 'mailgo')
        else
          -- This isolated fixture tests authorization, not spam classification.
          task:set_pre_result('accept', 'Mail Go sender allowed', 'mailgo')
        end
      end})
    if not queued then task:set_pre_result('soft reject', 'Mail authorization unavailable', 'mailgo') end
  end
})
'''

root = Path(sys.argv[1]).resolve()
assert root.name in ('mail-test-go','mail-test-directory','mail-test-recovery') and root.parent.name.startswith('anas-mail-go-test-')
source = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('base', source.parent / 'scripts/mail-webmail-poc-prepare.py')
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)
base.prepare(root, 'anas_mail_test_go_recovery_1024' if root.name=='mail-test-recovery' else 'anas_mail_test_go_1024')
write = lambda name, text, mode=0o600: base.write(root, name, text, mode)
secret = json.loads((root/'data/secrets.json').read_text())
secret.update({k: secrets.token_urlsafe(32) for k in ('postgres','roundcube','dovecot','rspamd','kick')})
write('data/secrets.json', json.dumps(secret))
ssl = (root/'data/tls/openssl.cnf').read_text().replace('DNS:auth.', 'DNS:manage.mailtest.invalid,DNS:auth.')
write('data/tls/openssl.cnf', ssl)
subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','7','-config',str(root/'data/tls/openssl.cnf'),'-keyout',str(root/'data/tls/key.pem'),'-out',str(root/'data/tls/cert.pem')],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
(root/'data/tls/key.pem').chmod(0o600)
users = (root/'fixture/samba-users.py').read_text().replace("'smtp:work@mailtest.invalid'", "'smtp:work@mailtest.invalid','smtp:projects@mailtest.invalid'")
write('fixture/samba-users.py', users, 0o644)
llng = json.loads((root/'data/llng/custom.json').read_text())
llng = json.loads(json.dumps(llng).replace('18883','18884'))
options = llng['oidcRPMetaDataOptions'].pop('roundcube')
options.update(oidcRPMetaDataOptionsClientID='mail-poc-go',oidcRPMetaDataOptionsRedirectUris='https://manage.mailtest.invalid:18884/callback')
llng['oidcRPMetaDataOptions']['mailgo'] = options
llng['oidcRPMetaDataExportedVars']['mailgo'] = llng['oidcRPMetaDataExportedVars'].pop('roundcube')
write('data/llng/custom.json',json.dumps(llng))
write('data/dms/config/postfix-accounts.cf', ''.join(f'box-0{i}@boxes.mailtest.invalid|{{PLAIN}}{secrets.token_urlsafe(48)}|userdb_mail_path=/var/mail/box-0{i} userdb_home=/var/mail/box-0{i}/home\n' for i in (1,2,3)))
write('data/dms/config/postfix-virtual.cf', 'alice@mailtest.invalid box-01@boxes.mailtest.invalid\nshopping@mailtest.invalid box-01@boxes.mailtest.invalid\nwork@mailtest.invalid box-01@boxes.mailtest.invalid\nprojects@mailtest.invalid box-02@boxes.mailtest.invalid\nbob@mailtest.invalid box-03@boxes.mailtest.invalid\n')
write('data/dms/config/dovecot.cf', f'''ssl_client_ca_file = /certs/cert.pem
auth_mechanisms = oauthbearer xoauth2
oauth2 {{
  introspection_url = https://dovecot:{secret['dovecot']}@manage.mailtest.invalid:18884/internal/introspect
  introspection_mode = post
  username_attribute = username
  active_attribute = active
  active_value = true
  force_introspection = yes
  send_auth_headers = yes
}}
oauth2_fields {{
  user_mailgo_sid = %{{oauth2:mailgo_sid}}
  userdb_user_mailgo_sid = %{{oauth2:mailgo_sid}}
}}
''')
write('data/dms/config/postfix-main.cf', '''smtpd_recipient_restrictions = reject_unauth_destination
smtpd_relay_restrictions = permit_sasl_authenticated, reject_unauth_destination
milter_default_action = tempfail
''')
write('data/dms/config/rspamd-key', base64.b64encode(('rspamd:'+secret['rspamd']).encode()).decode(), 0o644)
write('fixture/sender.lua', SENDER_ADAPTER, 0o644)
write('fixture/rspamd-options.inc', 'dns { timeout = 1s; retransmits = 1; }\n', 0o644)
write('fixture/rspamd-normal.inc', 'count = 1;\n', 0o644)
write('fixture/rspamd-proxy.inc', 'count = 1;\n', 0o644)
from directory_prepare import BACKEND, extend
write('fixture/mailgo.php', MAILGO_PLUGIN + BACKEND, 0o644)
rc = {
 'oauth_provider':None, 'use_https':True, 'des_key':secret['des_key'], 'product_name':'Go Mail PoC',
 'identities_level':1, 'enable_installer':False, 'spellcheck_engine':'', 'mailgo_secret':secret['roundcube'],
 'imap_auth_type':'XOAUTH2','smtp_auth_type':'XOAUTH2','smtp_user':'%u','smtp_pass':'%p',
 'imap_conn_options':{'ssl':{'verify_peer':True,'verify_peer_name':True,'cafile':'/certs/cert.pem'}},
 'smtp_conn_options':{'ssl':{'verify_peer':True,'verify_peer_name':True,'cafile':'/certs/cert.pem'}},
}
encoded=base64.b64encode(json.dumps(rc).encode()).decode()
write('data/roundcube/config/zz-poc.php', "<?php\n$config = array_replace($config, json_decode(base64_decode('"+encoded+"'), true));\n",0o644)
nginx=(root/'fixture/nginx.conf').read_text().replace('18883','18884')
nginx=nginx.replace('http {','''http {
  server {
    listen 18884 ssl;
    server_name manage.mailtest.invalid;
    ssl_certificate /certs/cert.pem;
    ssl_certificate_key /certs/key.pem;
    location / { proxy_pass http://manager:8080; proxy_set_header Host $http_host; proxy_set_header X-Forwarded-Proto https; }
  }
''')
write('fixture/nginx.conf',nginx,0o644)
cfg=json.loads((root/'compose.json').read_text()); services=cfg['services']
services['mail']['ports']=['19994:993','15888:587','15254:25']
services['mail']['mem_limit']='512m'
services['mail']['environment'].update(ENABLE_RSPAMD='1',ENABLE_RSPAMD_REDIS='0',SPOOF_PROTECTION='0',OAUTH2_INTROSPECTION_URL='https://manage.mailtest.invalid:18884/internal/introspect')
services['mail']['volumes'] += ['./fixture/sender.lua:/etc/rspamd/rspamd.local.lua:ro','./fixture/rspamd-options.inc:/etc/rspamd/local.d/options.inc:ro','./fixture/rspamd-normal.inc:/etc/rspamd/local.d/worker-normal.inc:ro','./fixture/rspamd-proxy.inc:/etc/rspamd/local.d/worker-proxy.inc:ro']
services['webmail']['environment']['ROUNDCUBEMAIL_PLUGINS']='archive,zipdownload,mailgo'
services['webmail']['volumes']+=['./fixture/mailgo.php:/var/www/html/plugins/mailgo/mailgo.php:ro']
services['proxy']['ports']=['18884:18884']
services['proxy']['networks']['mailpoc']['aliases']+=['manage.mailtest.invalid']
common={'restart':'no','labels':{'anas.mail-poc':'true'},'networks':['mailpoc']}
services['postgres']={**common,'image':'ghcr.io/anas-project/anas-postgres:18.4.0-r5','mem_limit':'160m','environment':{'POSTGRES_USER':'mailgo','POSTGRES_PASSWORD':secret['postgres'],'POSTGRES_DB':'mailgo'},'volumes':['./data/postgres:/var/lib/postgresql']}
services['manager']={**common,'image':'nginx:alpine','mem_limit':'96m','entrypoint':['/fixture/mail-go'],'stop_signal':'SIGTERM','environment':{'GOMEMLIMIT':'70MiB'},'volumes':['./fixture/mail-go:/fixture/mail-go:ro','./data/secrets.json:/input/secrets.json:ro','./data/tls:/certs:ro','./data/dms/config:/dms']}
extend(root, source, cfg, secret, write)
write('compose.json',json.dumps(cfg,indent=2))
print('Go fixture prepared; synthetic credentials remain private')
