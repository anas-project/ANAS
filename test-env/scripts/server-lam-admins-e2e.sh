#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$script_dir/server-require-isolated-docker.sh"

docker_cmd=${DOCKER_CMD:-docker}
prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_anchor_}
dc=${prefix}samba_dc
lam=${prefix}lam
suffix=$(date +%H%M%S)
username=${ANAS_TEST_LAM_USERNAME:-lam_e2e_${suffix}}
password=${ANAS_TEST_LAM_PASSWORD:-Anas-Lam-${suffix}-E2e!}

dc_exec() {
  "$docker_cmd" exec "$dc" "$@"
}

cleanup() {
  dc_exec samba-tool user delete "$username" >/dev/null 2>&1 || true
}
trap cleanup EXIT HUP INT TERM

section() {
  printf '\n== %s ==\n' "$1"
}

lam_login_probe() {
  local expected=$1 probe_password=${2:-$password}
  "$docker_cmd" exec -i "$lam" php -- "$username" "$probe_password" "$expected" <<'PHP'
<?php
declare(strict_types=1);

[$script, $username, $password, $expected] = $argv;
$profilePath = '/var/lib/ldap-account-manager/config/lam.conf';
$profile = json_decode((string) file_get_contents($profilePath), true, 512, JSON_THROW_ON_ERROR);

if (($profile['loginMethod'] ?? '') !== 'search' || ($profile['Admins'] ?? null) !== '') {
    fwrite(STDERR, "LAM is not configured for search-only administrator login\n");
    exit(20);
}

$ldap = ldap_connect((string) $profile['ServerURL']);
if ($ldap === false) {
    fwrite(STDERR, "cannot connect to the configured LDAP URL\n");
    exit(21);
}
ldap_set_option($ldap, LDAP_OPT_PROTOCOL_VERSION, 3);
ldap_set_option($ldap, LDAP_OPT_REFERRALS, 0);
if (!@ldap_bind($ldap, (string) $profile['loginSearchDN'], (string) $profile['loginSearchPassword'])) {
    fwrite(STDERR, "LAM search service account cannot bind\n");
    exit(22);
}

$escapedUsername = ldap_escape($username, '', LDAP_ESCAPE_FILTER);
$filter = str_replace('%USER%', $escapedUsername, (string) $profile['loginSearchFilter']);
$search = @ldap_search($ldap, (string) $profile['loginSearchSuffix'], $filter, ['distinguishedName']);
$entries = $search === false ? ['count' => 0] : ldap_get_entries($ldap, $search);
$count = (int) ($entries['count'] ?? 0);

if ($expected === 'no-match') {
    if ($count !== 0) {
        fwrite(STDERR, "login filter unexpectedly authorized the user\n");
        exit(23);
    }
    exit(0);
}
if ($count !== 1) {
    fwrite(STDERR, "login filter did not resolve exactly one authorized user\n");
    exit(24);
}

$userDn = (string) ($entries[0]['distinguishedname'][0] ?? $entries[0]['dn'] ?? '');
$authenticated = $userDn !== '' && @ldap_bind($ldap, $userDn, $password);
if ($expected === 'auth-fail') {
    exit($authenticated ? 25 : 0);
}
if ($expected !== 'success' || !$authenticated) {
    fwrite(STDERR, "authorized user could not bind with their own password\n");
    exit(26);
}
PHP
}

for container in "$dc" "$lam"; do
  "$docker_cmd" inspect "$container" >/dev/null
done

admin_group=$(dc_exec printenv SAMBA_DC_ADMIN_GROUP_NAME)
test -n "$admin_group"
cleanup

section "create an enabled directory user outside Admins"
dc_exec samba-tool user add "$username" "$password" --userou='OU=People' >/dev/null
lam_login_probe no-match
printf 'non-member rejected: %s\n' "$username"

section "Admins membership enables login with the user password"
dc_exec samba-tool group addmembers "$admin_group" "$username" >/dev/null
lam_login_probe success
lam_login_probe auth-fail 'Definitely-Wrong-LAM-E2E-Password!'
printf 'member accepted and wrong password rejected: %s\n' "$username"

section "disabled members stay rejected"
dc_exec samba-tool user disable "$username" >/dev/null
lam_login_probe no-match
dc_exec samba-tool user enable "$username" >/dev/null
lam_login_probe success
printf 'disabled filter and re-enable path verified\n'

section "removing Admins membership revokes login"
dc_exec samba-tool group removemembers "$admin_group" "$username" >/dev/null
lam_login_probe no-match
printf 'PASS: LAM authorizes enabled Samba Admins members with their own credentials\n'
