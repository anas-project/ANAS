<?php
// Loaded by the fixed upstream entrypoint after its generated Docker config.
$path = '/run/anas/mail-webmail/config.json';
$info = @lstat($path);
if (!$info || is_link($path) || !is_file($path) || ($info['mode'] & 0077) !== 0 || $info['size'] > 32768) {
    throw new RuntimeException('Mail webmail private binding unavailable');
}
$binding = json_decode(file_get_contents($path), true, 16, JSON_THROW_ON_ERROR);
$keys = ['go_origin', 'public_origin', 'internal_url', 'service_token', 'ca_file', 'dms_host', 'encryption_key', 'db_dsnw'];
if (!is_array($binding) || array_diff($keys, array_keys($binding)) || array_diff(array_keys($binding), array_merge($keys, ['language','company_directory']))) {
    throw new RuntimeException('Mail webmail binding incomplete');
}
$languages = ['en'=>'en_US','zh-CN'=>'zh_CN','zh-TW'=>'zh_TW','de'=>'de_DE','es'=>'es_ES','fr'=>'fr_FR','ja'=>'ja_JP','ko'=>'ko_KR'];
$binding['language'] = $binding['language'] ?? 'en';
$binding['company_directory'] = $binding['company_directory'] ?? false;
if (!is_string($binding['language']) || !isset($languages[$binding['language']]) || !is_bool($binding['company_directory'])) {
    throw new RuntimeException('Mail webmail presentation binding invalid');
}
foreach ($keys as $key) {
    if (!is_string($binding[$key]) || $binding[$key] === '' || preg_match('/[\x00\r\n]/', $binding[$key])) {
        throw new RuntimeException('Mail webmail binding invalid');
    }
}
foreach (['go_origin', 'public_origin', 'internal_url'] as $key) {
    $url = parse_url($binding[$key]);
    if (!$url || ($url['scheme'] ?? '') !== 'https' || empty($url['host'])
        || isset($url['user']) || isset($url['pass']) || isset($url['path']) || isset($url['query']) || isset($url['fragment'])) {
        throw new RuntimeException('Mail webmail origin invalid');
    }
}
$go = parse_url($binding['go_origin']);
$internal = parse_url($binding['internal_url']);
if ($go['host'] !== $internal['host'] || ($internal['port'] ?? 0) !== 8443
    || parse_url($binding['public_origin'], PHP_URL_HOST) === $go['host']
    || !preg_match('/^[a-f0-9]{64}$/D', $binding['service_token'])
    || !preg_match('/^[a-f0-9]{64}$/D', $binding['encryption_key'])
    || $binding['service_token'] === $binding['encryption_key']
    || !preg_match('/^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$/D', $binding['dms_host'])
    || $binding['ca_file'][0] !== '/' || !is_file($binding['ca_file'])
    || strpos($binding['db_dsnw'], 'pgsql://') !== 0) {
    throw new RuntimeException('Mail webmail service binding invalid');
}
$config['db_dsnw'] = $binding['db_dsnw'];
$config['des_key'] = hex2bin($binding['encryption_key']);
unset($binding['db_dsnw'], $binding['encryption_key']);
$config['anas_mail'] = $binding;
$config['plugins'] = ['anas_mail', 'archive', 'zipdownload'];
$config['product_name'] = 'ANAS Mail';
$config['skin'] = 'elastic';
$config['language'] = $languages[$binding['language']];
$config['session_storage'] = 'db';
$config['session_name'] = '__Host-anas-webmail';
$config['session_auth_name'] = '__Host-anas-webmail-auth';
$config['session_path'] = '/';
$config['session_domain'] = '';
$config['session_samesite'] = 'Lax';
$config['use_https'] = true;
$config['ip_check'] = false;
$config['login_autocomplete'] = 0;
$config['imap_host'] = 'ssl://' . $binding['dms_host'] . ':993';
$config['smtp_host'] = 'tls://' . $binding['dms_host'] . ':587';
$config['imap_auth_type'] = 'XOAUTH2';
$config['smtp_auth_type'] = 'XOAUTH2';
$config['imap_conn_options'] = ['ssl' => ['verify_peer' => true, 'verify_peer_name' => true, 'allow_self_signed' => false, 'cafile' => $binding['ca_file']]];
$config['smtp_conn_options'] = $config['imap_conn_options'];
$config['identities_level'] = 1;
$config['enable_installer'] = false;
$config['enable_spellcheck'] = false;
$config['password_charset'] = 'UTF-8';
$config['imap_debug'] = false;
$config['smtp_debug'] = false;
$config['session_debug'] = false;
$config['log_logins'] = false;
