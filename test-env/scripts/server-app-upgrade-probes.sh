#!/usr/bin/env bash
set -euo pipefail

socket=${ANAS_TEST_DOCKER_SOCKET:-/run/anas-app-docker.sock}
prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_app_}
entry_ip=${ANAS_TEST_ENTRY_IP:-10.251.0.2}
entry_port=${ANAS_TEST_ENTRY_PORT:-9000}
domain=${ANAS_TEST_DOMAIN:-nas.test}
admin_password=${ANAS_TEST_ADMIN_PASSWORD:-AnasTest1!}

docker_cmd() {
  docker -H "unix://$socket" "$@"
}

container() {
  printf '%s%s' "$prefix" "$1"
}

section() {
  printf '\n== %s ==\n' "$1"
}

https_code() {
  local host=$1
  local path=${2:-/}
  curl -skS --connect-timeout 10 --max-time 60 --output /dev/null --write-out '%{http_code}' \
    --resolve "$host:$entry_port:$entry_ip" "https://$host:$entry_port$path"
}

https_get() {
  local host=$1
  local path=${2:-/}
  curl -skS --connect-timeout 10 --max-time 60 \
    --resolve "$host:$entry_port:$entry_ip" "https://$host:$entry_port$path"
}

occ() {
  docker_cmd exec "$(container nextcloud)" runuser -u www-data -- php /var/www/html/occ "$@"
}

wait_for_stack() {
  local attempt name state health ready
  for attempt in $(seq 1 180); do
    ready=true
    for name in "$@"; do
      if ! docker_cmd inspect "$name" >/dev/null 2>&1; then
        ready=false
        continue
      fi
      state=$(docker_cmd inspect --format '{{.State.Status}}' "$name")
      health=$(docker_cmd inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$name")
      if [ "$state" != running ] || [ "$health" = starting ] || [ "$health" = unhealthy ]; then
        ready=false
      fi
    done
    if [ "$ready" = true ]; then
      return 0
    fi
    sleep 10
  done
  docker_cmd ps -a
  return 1
}

expected=(
  "$(container traefik)"
  "$(container samba_dc)"
  "$(container postgres)"
  "$(container mariadb)"
  "$(container llng)"
  "$(container nextcloud)"
  "$(container nextcloud_cron)"
  "$(container nextcloud_push)"
  "$(container nextcloud_imaginary)"
  "$(container nextcloud_talk)"
  "$(container collabora)"
  "$(container meshcentral)"
  "$(container netbird_dashboard)"
  "$(container netbird_signal)"
  "$(container netbird_relay)"
  "$(container netbird_management)"
  "$(container lam)"
  "$(container eturnal)"
)

section "wait for application stack"
wait_for_stack "${expected[@]}"

section "container stability"
for name in "${expected[@]}"; do
  state=$(docker_cmd inspect --format '{{.State.Status}}' "$name")
  health=$(docker_cmd inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$name")
  restarts=$(docker_cmd inspect --format '{{.RestartCount}}' "$name")
  printf '%s state=%s health=%s restarts=%s\n' "$name" "$state" "$health" "$restarts"
  test "$state" = running
  test "$health" != unhealthy
  test "$restarts" -eq 0
done

section "service versions"
docker_cmd exec "$(container llng)" dpkg-query -W -f='llng=${Version}\n' lemonldap-ng
docker_cmd exec "$(container lam)" dpkg-query -W -f='lam=${Version}\n' ldap-account-manager
docker_cmd exec "$(container meshcentral)" node -e "console.log('meshcentral=' + require('/opt/meshcentral/meshcentral/package.json').version)"
docker_cmd inspect --format 'netbird_management={{.Config.Image}}' "$(container netbird_management)"
docker_cmd exec "$(container postgres)" postgres --version

section "nextcloud core and apps"
status_json=$(occ status --output=json)
printf '%s\n' "$status_json" | jq .
printf '%s\n' "$status_json" | jq -e '.installed == true and .maintenance == false and .versionstring == "34.0.2"' >/dev/null
apps_json=$(occ app:list --output=json)
printf '%s\n' "$apps_json" | jq '{enabled: [.enabled | keys[]]}'
printf '%s\n' "$apps_json" | jq -e '
  .enabled.richdocuments == "11.1.0" and
  .enabled.spreed == "24.0.3" and
  .enabled.previewgenerator == "5.14.0" and
  .enabled.notify_push == "1.3.5" and
  .enabled.memories == "8.1.0" and
  .enabled.user_oidc == "8.10.1"
' >/dev/null
for app in richdocuments spreed previewgenerator notify_push memories user_oidc user_ldap; do
  printf '%s\n' "$apps_json" | jq -e --arg app "$app" '.enabled[$app] != null' >/dev/null
  occ integrity:check-app "$app"
done
occ ldap:test-config s01
occ user:info admin
occ group:list admin | grep admin >/dev/null
occ config:system:get preview_imaginary_url | grep -F "$(container nextcloud_imaginary):9000" >/dev/null
test -n "$(occ config:system:get preview_imaginary_key)"
occ config:system:get redis host | grep -F "$(container nextcloud_redis)" >/dev/null
occ config:system:get memcache.locking | grep -F '\OC\Memcache\Redis' >/dev/null
occ config:app:get notify_push base_endpoint | grep -F "https://nc.$domain:$entry_port/push" >/dev/null
occ notify_push:self-test
occ talk:signaling:list
occ talk:turn:list
occ talk:stun:list
docker_cmd exec "$(container nextcloud)" test -f /run/nextcloud-tasks.ready
docker_cmd exec "$(container nextcloud)" test -f /var/www/html/.anas-state/memories-places.ready
docker_cmd exec "$(container postgres)" sh -ceu 'psql -U "$POSTGRES_USER" -d nextcloud -Atc "select count(*) from oc_memories_planet"' | sed 's/^/memories_planet_rows=/'

section "nextcloud web and WebDAV"
test "$(https_code "nc.$domain" /status.php)" = 200
test "$(https_code "nc.$domain" /apps/user_oidc/login/1)" = 302
test "$(https_code "nc.$domain" /talk/api/v1/welcome)" = 200
probe_file=$(mktemp)
download_file=$(mktemp)
printf 'anas-nextcloud-34-regression\n' >"$probe_file"
curl -skS --fail --resolve "nc.$domain:$entry_port:$entry_ip" \
  -u "admin_nc:$admin_password" -T "$probe_file" \
  "https://nc.$domain:$entry_port/remote.php/dav/files/admin_nc/anas-regression.txt"
curl -skS --fail --resolve "nc.$domain:$entry_port:$entry_ip" \
  -u "admin_nc:$admin_password" \
  "https://nc.$domain:$entry_port/remote.php/dav/files/admin_nc/anas-regression.txt" -o "$download_file"
cmp "$probe_file" "$download_file"
curl -skS --fail --resolve "nc.$domain:$entry_port:$entry_ip" \
  -u "admin_nc:$admin_password" -X PROPFIND -H 'Depth: 0' \
  "https://nc.$domain:$entry_port/remote.php/dav/files/admin_nc/anas-regression.txt" | grep -F anas-regression.txt >/dev/null
rm -f "$probe_file" "$download_file"

section "office and application integrations"
test "$(https_code "collabora.$domain" /hosting/discovery)" = 200
occ config:app:get richdocuments wopi_url | grep -F "https://collabora.$domain:$entry_port" >/dev/null
occ config:app:get richdocuments public_wopi_url | grep -F "https://collabora.$domain:$entry_port" >/dev/null
occ richdocuments:activate-config
test "$(https_code "lam.$domain" /)" = 200
https_get "lam.$domain" / | grep -F 'LDAP Account Manager' >/dev/null
docker_cmd exec "$(container lam)" php -r '$c=json_decode(file_get_contents("/var/lib/ldap-account-manager/config/lam.conf"), true, 512, JSON_THROW_ON_ERROR); if (($c["ServerURL"] ?? "") === "") exit(1); echo "lam_profile=ok\n";'
docker_cmd exec "$(container lam)" php -r '$c=ldap_connect(getenv("SAMBA_DC_LDAPS_SERVER_URL")); ldap_set_option($c, LDAP_OPT_PROTOCOL_VERSION, 3); if (!ldap_bind($c, getenv("SAMBA_DC_ADMIN_DN"), getenv("LAM_ADMIN_PASSWORD"))) exit(1); echo "lam_ldaps_bind=ok\n";'
test "$(https_code "meshcentral.$domain" /)" = 200
docker_cmd exec "$(container meshcentral)" node -e "require('ldapauth-fork'); require('mysql2'); require('openid-client'); require('passport'); console.log('meshcentral_dependencies=ok')"
docker_cmd exec "$(container meshcentral)" node -e "JSON.parse(require('fs').readFileSync('/run/anas/config.json')); console.log('meshcentral_config=ok')"

section "llng SSO"
discovery=$(https_get "auth.$domain" /.well-known/openid-configuration)
printf '%s\n' "$discovery" | jq -e --arg issuer "https://auth.$domain:$entry_port/" '.issuer == $issuer' >/dev/null
test "$(https_code "auth.$domain" /saml/metadata)" = 200
manager_code=$(https_code "auth-manager.$domain" /)
case "$manager_code" in 200|302) ;; *) exit 1 ;; esac
docker_cmd exec "$(container llng)" jq -e '.oidcRPMetaDataOptions.netbird and .oidcRPMetaDataOptions.nextcloud and .applicationList."1apps".nextcloud' /var/lib/lemonldap-ng/conf/lmConf-1.json >/dev/null
cookie_file=$(mktemp)
login_page=$(mktemp)
curl -skS --resolve "auth.$domain:$entry_port:$entry_ip" -c "$cookie_file" -b "$cookie_file" \
  "https://auth.$domain:$entry_port/" -o "$login_page"
login_token=$(sed -n 's/.*name="token" value="\([^"]*\)".*/\1/p' "$login_page")
test -n "$login_token"
curl -skS --resolve "auth.$domain:$entry_port:$entry_ip" -c "$cookie_file" -b "$cookie_file" \
  --data-urlencode "user=admin" --data-urlencode "password=$admin_password" \
  --data-urlencode "token=$login_token" "https://auth.$domain:$entry_port/" -o "$login_page"
grep -F lemonldap "$cookie_file" >/dev/null
curl -skS --resolve "auth.$domain:$entry_port:$entry_ip" -c "$cookie_file" -b "$cookie_file" \
  "https://auth.$domain:$entry_port/" | grep -Ei 'logout|menu|applications' >/dev/null
rm -f "$cookie_file" "$login_page"

section "netbird"
test "$(https_code "netbird.$domain" /)" = 200
api_code=$(https_code "netbird.$domain" /api/health)
case "$api_code" in 200|401|403|404) ;; *) exit 1 ;; esac
signal_ws_code=$(https_code "netbird.$domain" /ws-proxy/signal)
management_ws_code=$(https_code "netbird.$domain" /ws-proxy/management)
relay_code=$(https_code "netbird.$domain" /relay)
printf 'api=%s signal_ws=%s management_ws=%s relay=%s\n' "$api_code" "$signal_ws_code" "$management_ws_code" "$relay_code"
key_bytes=$(docker_cmd exec "$(container netbird_management)" sh -lc \
  'jq -r .DataStoreEncryptionKey /etc/netbird/management.json | base64 -d | wc -c')
test "$key_bytes" -eq 32
docker_cmd exec "$(container netbird_management)" jq -e \
  '.StoreConfig.Engine == "sqlite" and .Relay.Secret != "" and .TURNConfig.TimeBasedCredentials == true and .HttpConfig.Address == "0.0.0.0:33073"' \
  /etc/netbird/management.json >/dev/null

printf '\nall application upgrade probes passed\n'
