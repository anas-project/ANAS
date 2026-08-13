<?php

declare(strict_types=1);

$names = [
    'LAM_ADMIN_PASSWORD',
    'LAM_LANGUAGE',
    'SAMBA_DC_ADMIN_GROUP_DN',
    'SAMBA_DC_BASE_COMPUTERS_DN',
    'SAMBA_DC_BASE_DN',
    'SAMBA_DC_BASE_GROUPS_DN',
    'SAMBA_DC_BASE_USERS_DN',
    'SAMBA_DC_DOMAIN',
    'SAMBA_DC_LDAP_BIND_DN',
    'SAMBA_DC_LDAP_BIND_PASSWORD',
    'SAMBA_DC_LDAPS_SERVER_URL',
    'TZ',
];

foreach ($names as $name) {
    $value = getenv($name);
    if ($value === false) {
        fwrite(STDERR, "missing required environment variable: {$name}\n");
        exit(1);
    }
}

function readJson(string $path): array
{
    $contents = file_get_contents($path);
    if ($contents === false) {
        throw new RuntimeException("cannot read {$path}");
    }
    $decoded = json_decode($contents, true, 512, JSON_THROW_ON_ERROR);
    if (!is_array($decoded)) {
        throw new RuntimeException("invalid JSON object in {$path}");
    }
    return $decoded;
}

function passwordHash(string $password): string
{
    $salt = random_bytes(4);
    return '{SSHA}' . base64_encode(sha1($password . $salt, true)) . ' ' . base64_encode($salt);
}

function writeJson(string $path, array $data): void
{
    $temporaryPath = $path . '.tmp';
    $contents = json_encode($data, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_THROW_ON_ERROR) . "\n";
    if (file_put_contents($temporaryPath, $contents, LOCK_EX) === false || !rename($temporaryPath, $path)) {
        throw new RuntimeException("cannot write {$path}");
    }
    chmod($path, 0600);
}

$password = (string) getenv('LAM_ADMIN_PASSWORD');
$passwordHash = passwordHash($password);

$mainPath = '/etc/ldap-account-manager/config.cfg';
$main = readJson($mainPath);
// This is the default server-profile name shown by LAM, not a local username.
// The main application login uses the LDAP search policy in the `lam` profile.
$main['default'] = 'lam';
$main['password'] = $passwordHash;
writeJson($mainPath, $main);

$samplePath = '/var/lib/ldap-account-manager/config/windows_samba4.sample.conf';
$profilePath = '/var/lib/ldap-account-manager/config/lam.conf';
$profile = readJson($samplePath);
$language = (string) getenv('LAM_LANGUAGE');
if (!str_ends_with($language, '.utf8')) {
    $language .= '.utf8';
}
$profile['ServerURL'] = (string) getenv('SAMBA_DC_LDAPS_SERVER_URL');
$profile['useTLS'] = 'no';
$profile['Admins'] = '';
$profile['Passwd'] = $passwordHash;
$profile['defaultLanguage'] = $language;
$profile['serverDisplayName'] = (string) getenv('SAMBA_DC_DOMAIN');
$profile['loginMethod'] = 'search';
$profile['loginSearchSuffix'] = (string) getenv('SAMBA_DC_BASE_DN');
$profile['loginSearchFilter'] = '(&(objectCategory=person)(objectClass=user)'
    . '(!(userAccountControl:1.2.840.113556.1.4.803:=2))'
    . '(sAMAccountName=%USER%)'
    . '(memberOf:1.2.840.113556.1.4.1941:=' . (string) getenv('SAMBA_DC_ADMIN_GROUP_DN') . '))';
$profile['loginSearchDN'] = (string) getenv('SAMBA_DC_LDAP_BIND_DN');
$profile['loginSearchPassword'] = (string) getenv('SAMBA_DC_LDAP_BIND_PASSWORD');
$profile['timeZone'] = (string) getenv('TZ');
$profile['activeTypes'] = 'user,group,host';
$profile['typeSettings']['suffix_user'] = (string) getenv('SAMBA_DC_BASE_USERS_DN');
$profile['typeSettings']['attr_user'] = '#cn;#displayName;#sAMAccountName;#name;#userPrincipalName;#givenName;#sn;#mail';
$profile['typeSettings']['modules_user'] = 'windowsUser';
$profile['typeSettings']['suffix_group'] = (string) getenv('SAMBA_DC_BASE_GROUPS_DN');
$profile['typeSettings']['attr_group'] = '#cn;#name;#member;#description;#gidNumber';
$profile['typeSettings']['modules_group'] = 'windowsGroup';
$profile['typeSettings']['suffix_host'] = (string) getenv('SAMBA_DC_BASE_COMPUTERS_DN');
$profile['typeSettings']['attr_host'] = '#cn;#description;#location';
$profile['typeSettings']['modules_host'] = 'windowsHost';
$profile['typeSettings']['suffix_smbDomain'] = (string) getenv('SAMBA_DC_BASE_DN');
$profile['moduleSettings']['windowsUser_domains'] = [(string) getenv('SAMBA_DC_DOMAIN')];
$profile['toolSettings']['treeViewSuffix'] = (string) getenv('SAMBA_DC_BASE_DN');
writeJson($profilePath, $profile);

chown($mainPath, 'www-data');
chgrp($mainPath, 'www-data');
chown($profilePath, 'www-data');
chgrp($profilePath, 'www-data');
