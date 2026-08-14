#!/usr/bin/env sh
set -eu

docker_cmd=${DOCKER_CMD:-docker}
prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_test_}
entry_ip=${ANAS_TEST_ENTRY_IP:-10.254.0.2}
entry_port=${ANAS_TEST_ENTRY_PORT:-9000}
dns_ip=${ANAS_TEST_DNS_IP:-10.254.0.1}
domain=${ANAS_TEST_DOMAIN:-nas.test}
write_markers=${ANAS_PROBE_WRITE_MARKERS:-true}

container() {
  printf '%s%s' "$prefix" "$1"
}

section() {
  printf '\n== %s ==\n' "$1"
}

section "container stability"
container_ids=$($docker_cmd ps --all --quiet)
test -n "$container_ids"
for id in $container_ids; do
  name=$($docker_cmd inspect --format '{{.Name}}' "$id" | sed 's|^/||')
  state=$($docker_cmd inspect --format '{{.State.Status}}' "$id")
  health=$($docker_cmd inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$id")
  restarts=$($docker_cmd inspect --format '{{.RestartCount}}' "$id")
  printf '%s state=%s health=%s restarts=%s\n' "$name" "$state" "$health" "$restarts"
  test "$state" = running
  test "$health" != starting
  test "$health" != unhealthy
  test "$restarts" -eq 0
done

section "Samba AD and file server"
$docker_cmd exec "$(container samba_dc)" samba-tool domain level show
$docker_cmd exec "$(container samba_dc)" sh -ceu 'samba-tool user show "$SAMBA_DC_ADMIN_NAME" >/dev/null; for port in 88 389 636; do nc -z 127.0.0.1 "$port"; done; echo "AD administrator and ports: ok"'
$docker_cmd exec "$(container samba_fs)" wbinfo -t

section "DNS and BIND"
dc_result=$(dig +short @"$dns_ip" "fengoffice.$domain" A | tail -n 1)
printf 'fengoffice.%s=%s\n' "$domain" "$dc_result"
test "$dc_result" = "$dns_ip"
for label in nas traefik auth nc meshcentral netbird lam ddns; do
  result=$(dig +short @"$dns_ip" "$label.$domain" A | tail -n 1)
  printf '%s.%s=%s\n' "$label" "$domain" "$result"
  test "$result" = "$entry_ip"
done
named_count=$($docker_cmd exec "$(container samba_dc)" sh -c 'pgrep -x named | wc -l')
printf 'named_processes=%s\n' "$named_count"
test "$named_count" -eq 1

section "Nextcloud"
$docker_cmd exec "$(container nextcloud)" occ status
$docker_cmd exec "$(container nextcloud)" occ ldap:test-config s01
$docker_cmd exec "$(container nextcloud)" occ user:info admin
$docker_cmd exec "$(container nextcloud)" occ app:list
for app in previewgenerator memories user_oidc user_ldap notify_push spreed; do
  $docker_cmd exec "$(container nextcloud)" occ integrity:check-app "$app"
done
$docker_cmd exec "$(container nextcloud)" sh -ceu 'test -f /run/nextcloud-tasks.ready; test -f /data/.anas-state/memories-places.ready; test "$(curl -fsS -o /dev/null -w "%{http_code}" http://127.0.0.1:12345/ping)" = 200; echo "task, memories, and ping markers: ok"'
$docker_cmd exec "$(container postgres)" sh -ceu 'psql -U "$POSTGRES_USER" -d nextcloud -Atc "select count(*) from oc_memories_planet"' | sed 's/^/memories_planet_rows=/'

section "application integrations"
$docker_cmd exec "$(container meshcentral)" node -e "require('ldapauth-fork'); require('mysql2'); require('openid-client'); require('passport'); console.log('meshcentral_node_dependencies=ok')"
discovery_url="https://auth.$domain:$entry_port/realms/master/.well-known/openid-configuration"
discovery=
attempt=0
until discovery=$(curl -skSf --connect-timeout 5 --max-time 20 \
  --resolve "auth.$domain:$entry_port:$entry_ip" "$discovery_url"); do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 12 ]; then
    printf 'Keycloak discovery did not become ready after %s attempts\n' "$attempt" >&2
    exit 1
  fi
  sleep 10
done
printf '%s\n' "$discovery" | grep -Fq "\"issuer\":\"https://auth.$domain:$entry_port/realms/master\""
printf 'keycloak_master_discovery=ok\n'
for label in auth nc lam meshcentral netbird; do
  code=$(curl -skS --connect-timeout 10 --max-time 30 --output /dev/null --write-out '%{http_code}' \
    --resolve "$label.$domain:$entry_port:$entry_ip" "https://$label.$domain:$entry_port/")
  printf '%s.%s=%s\n' "$label" "$domain" "$code"
  case "$code" in
    2*|3*|401|403) ;;
    *) exit 1 ;;
  esac
done

section "database probes"
if [ "$write_markers" = true ]; then
  $docker_cmd exec "$(container postgres)" sh -ceu 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres -c "create table if not exists anas_validation_probe (id integer primary key, note text not null); insert into anas_validation_probe values (1, '\''server-functional-probe'\'') on conflict (id) do update set note=excluded.note;" >/dev/null'
  $docker_cmd exec "$(container mariadb)" sh -ceu 'mariadb --user=root --password="$MYSQL_ROOT_PASSWORD" -e "create database if not exists anas_validation; create table if not exists anas_validation.probe (id integer primary key, note varchar(64) not null); replace into anas_validation.probe values (1, '\''server-functional-probe'\'');"'
fi
$docker_cmd exec "$(container postgres)" sh -ceu 'psql -U "$POSTGRES_USER" -d postgres -Atc "select id || '\''|'\'' || note from anas_validation_probe order by id"' | sed 's/^/postgres=/'
$docker_cmd exec "$(container mariadb)" sh -ceu 'mariadb --batch --skip-column-names --user=root --password="$MYSQL_ROOT_PASSWORD" -e "select concat(id, '\''|'\'', note) from anas_validation.probe order by id"' | sed 's/^/mariadb=/'

section "TLS fallback"
openssl s_client -connect "$dns_ip:636" -servername "fengoffice.$domain" </dev/null 2>/dev/null \
  | openssl x509 -noout -subject -ext subjectAltName

printf '\nall functional probes passed\n'
