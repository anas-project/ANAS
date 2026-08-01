#!/bin/sh

set -eu

docker_cmd=${DOCKER_CMD:-docker}
prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_tmpl_}
entry_ip=${ANAS_TEST_ENTRY_IP:-10.252.0.2}
entry_port=${ANAS_TEST_ENTRY_PORT:-9000}
domain=${ANAS_TEST_DOMAIN:-nas.test}

container() {
  printf '%s%s' "$prefix" "$1"
}

section() {
  printf '\n== %s ==\n' "$1"
}

section "container stability"
expected="lego samba_dc samba_fs traefik mariadb meshcentral lam eturnal"
for service in $expected; do
  name=$(container "$service")
  state=$($docker_cmd inspect --format '{{.State.Status}}' "$name")
  health=$($docker_cmd inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$name")
  restarts=$($docker_cmd inspect --format '{{.RestartCount}}' "$name")
  printf '%s state=%s health=%s restarts=%s\n' "$name" "$state" "$health" "$restarts"
  test "$state" = running
  test "$health" != starting
  test "$health" != unhealthy
  test "$restarts" -eq 0
done

section "Traefik runtime configuration"
$docker_cmd exec "$(container traefik)" sh -ceu '
  test "$(stat -c %a /run/anas/cert.yml)" = 600
  grep -Fq "certFile: /certs/" /run/anas/cert.yml
  grep -Fq "keyFile: /certs/" /run/anas/cert.yml
  ! grep -Eq "\$\{[A-Z][A-Z0-9_]*\}" /run/anas/cert.yml
  traefik healthcheck --ping
'

section "Eturnal runtime configuration"
$docker_cmd exec "$(container eturnal)" sh -ceu '
  test "$(stat -c %a /run/anas/eturnal.yml)" = 600
  grep -Fq "relay_min_port:" /run/anas/eturnal.yml
  grep -Fq "relay_max_port:" /run/anas/eturnal.yml
  ! grep -Eq "\$\{[A-Z][A-Z0-9_]*\}" /run/anas/eturnal.yml
  eturnalctl status
'

section "MeshCentral runtime configuration"
$docker_cmd exec "$(container meshcentral)" sh -ceu '
  test "$(stat -c %a /run/anas/config.json)" = 600
  node -e '\''
    const fs = require("fs");
    const text = fs.readFileSync("/run/anas/config.json", "utf8");
    const config = JSON.parse(text);
    const domain = config.domains[""];
    if (!config.settings.tlsOffload || !config.settings.mySQL.password) process.exit(1);
    if (!domain.ldapOptions.bindDN || !domain.ldapOptions.searchBase) process.exit(1);
    if (!domain.ldapSyncWithUserGroups.filter) process.exit(1);
    if (/\$\{[A-Z][A-Z0-9_]*\}|#\{envs\[/.test(text)) process.exit(1);
    require("ldapauth-fork");
    require("mysql");
  '\''
'

section "LAM runtime configuration"
$docker_cmd exec "$(container lam)" sh -ceu '
  config=/var/lib/ldap-account-manager/config/lam.conf
  test -s "$config"
  ! grep -Eq "\$\{[A-Z][A-Z0-9_]*\}" "$config"
  grep -Fq '\''$INFO.userPasswordClearText$'\'' "$config"
'

section "Samba generated configuration and trust"
$docker_cmd exec "$(container samba_dc)" sh -ceu '
  ! grep -Eq "\$\{[A-Z][A-Z0-9_]*\}" /etc/samba/smb.conf /etc/bind/named.conf
  testparm -s /etc/samba/smb.conf >/dev/null
  named-checkconf /etc/bind/named.conf
  samba-tool domain level show >/dev/null
  for port in 88 389 636; do nc -z 127.0.0.1 "$port"; done
'
$docker_cmd exec "$(container samba_fs)" sh -ceu '
  ! grep -Eq "\$\{[A-Z][A-Z0-9_]*\}" /etc/samba/smb.conf /etc/samba/smbusers /etc/krb5.conf
  testparm -s /etc/samba/smb.conf >/dev/null
  wbinfo -t
'

section "MariaDB"
$docker_cmd exec "$(container mariadb)" sh -ceu '
  mariadb --batch --skip-column-names --user=root --password="$MYSQL_ROOT_PASSWORD" -e "select 1" | grep -qx 1
'

section "DNS and HTTPS routes"
for label in traefik lam meshcentral; do
  result=$(dig +short @"$entry_ip" "$label.$domain" A | tail -n 1)
  printf '%s.%s=%s\n' "$label" "$domain" "$result"
  test "$result" = "$entry_ip"
done

dashboard_code=$(curl -skS --connect-timeout 5 --max-time 20 --output /dev/null --write-out '%{http_code}' \
  --resolve "traefik.$domain:$entry_port:$entry_ip" "https://traefik.$domain:$entry_port/")
printf 'traefik.%s=%s\n' "$domain" "$dashboard_code"
test "$dashboard_code" = 401

for label in lam meshcentral; do
  code=$(curl -skS --connect-timeout 5 --max-time 20 --output /dev/null --write-out '%{http_code}' \
    --resolve "$label.$domain:$entry_port:$entry_ip" "https://$label.$domain:$entry_port/")
  printf '%s.%s=%s\n' "$label" "$domain" "$code"
  case "$code" in
    2*|3*|401|403) ;;
    *) exit 1 ;;
  esac
done

printf '\nall template runtime probes passed\n'
