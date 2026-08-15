#!/usr/bin/env bash
set -euo pipefail

# Capability probe for parameters declared hot_reload/reconcile. This script
# intentionally runs only against the isolated server Docker daemon: it makes
# reversible changes to the test AD and Nextcloud databases, and creates two
# short-lived standalone containers for LAM and Samba FS.

DOCKER_HOST_URI=${ANAS_TEST_DOCKER_HOST:-unix:///run/anas-anchor-docker.sock}
PREFIX=${ANAS_TEST_CONTAINER_PREFIX:-anas_anchor_}
SAMBA_DC=${ANAS_TEST_SAMBA_DC_CONTAINER:-${PREFIX}samba_dc}
NEXTCLOUD=${ANAS_TEST_NEXTCLOUD_CONTAINER:-${PREFIX}nextcloud}
LEGO=${ANAS_TEST_LEGO_CONTAINER:-${PREFIX}lego}
LAM_IMAGE=${ANAS_TEST_LAM_IMAGE:-docker.cnb.cool/anas.dev/anas/anas-lam:9.6.0-r5}
SAMBA_FS_IMAGE=${ANAS_TEST_SAMBA_FS_IMAGE:-docker.cnb.cool/anas.dev/anas/anas-samba-fs:4.23.6-r2}
PROBE_SUFFIX=$$
LAM_PROBE=anas_probe_lam_reconcile_${PROBE_SUFFIX}
SAMBA_FS_PROBE=anas_probe_samba_fs_reconcile_${PROBE_SUFFIX}

docker_test() {
  docker -H "$DOCKER_HOST_URI" "$@"
}

fail() {
  echo "parameter in-place capability E2E failed: $*" >&2
  exit 1
}

for container in "$SAMBA_DC" "$NEXTCLOUD" "$LEGO"; do
  docker_test inspect "$container" >/dev/null 2>&1 ||
    fail "required isolated-test container is missing: $container"
done

password_settings=$(docker_test exec "$SAMBA_DC" samba-tool domain passwordsettings show)
setting_value() {
  printf '%s\n' "$password_settings" | awk -F': ' -v label="$1" '$1 == label {print $2}'
}

old_complexity=$(setting_value 'Password complexity')
old_history=$(setting_value 'Password history length')
old_min_length=$(setting_value 'Minimum password length')
old_min_age=$(setting_value 'Minimum password age (days)')
old_max_age=$(setting_value 'Maximum password age (days)')
old_lockout_duration=$(setting_value 'Account lockout duration (mins)')
old_lockout_threshold=$(setting_value 'Account lockout threshold (attempts)')
old_lockout_reset=$(setting_value 'Reset account lockout after (mins)')

for value in "$old_complexity" "$old_history" "$old_min_length" "$old_min_age" \
  "$old_max_age" "$old_lockout_duration" "$old_lockout_threshold" "$old_lockout_reset"; do
  [ -n "$value" ] || fail "could not parse the existing Samba password policy"
done

if [ "$old_complexity" = on ]; then alt_complexity=off; else alt_complexity=on; fi
if [ "$old_history" -eq 5 ]; then alt_history=6; else alt_history=5; fi
if [ "$old_min_length" -eq 9 ]; then alt_min_length=10; else alt_min_length=9; fi
if [ "$old_min_age" -eq 0 ]; then alt_min_age=1; else alt_min_age=0; fi
if [ "$old_max_age" -eq 91 ]; then alt_max_age=92; else alt_max_age=91; fi
if [ "$old_lockout_duration" -eq 31 ]; then alt_lockout_duration=32; else alt_lockout_duration=31; fi
if [ "$old_lockout_threshold" -eq 11 ]; then alt_lockout_threshold=12; else alt_lockout_threshold=11; fi
if [ "$old_lockout_reset" -eq 31 ]; then alt_lockout_reset=32; else alt_lockout_reset=31; fi

old_language=$(docker_test exec "$NEXTCLOUD" runuser -u www-data -- \
  php /var/www/html/occ config:system:get default_language)
old_locale=$(docker_test exec "$NEXTCLOUD" runuser -u www-data -- \
  php /var/www/html/occ config:system:get default_locale)

restore_live_state() {
  docker_test exec "$SAMBA_DC" samba-tool domain passwordsettings set \
    --complexity="$old_complexity" \
    --history-length="$old_history" \
    --min-pwd-length="$old_min_length" \
    --min-pwd-age="$old_min_age" \
    --max-pwd-age="$old_max_age" \
    --account-lockout-threshold="$old_lockout_threshold" \
    --account-lockout-duration="$old_lockout_duration" \
    --reset-account-lockout-after="$old_lockout_reset" >/dev/null 2>&1 || true
  docker_test exec "$NEXTCLOUD" runuser -u www-data -- \
    php /var/www/html/occ config:system:set default_language --value="$old_language" >/dev/null 2>&1 || true
  docker_test exec "$NEXTCLOUD" runuser -u www-data -- \
    php /var/www/html/occ config:system:set default_locale --value="$old_locale" >/dev/null 2>&1 || true
  docker_test rm -f "$LAM_PROBE" "$SAMBA_FS_PROBE" >/dev/null 2>&1 || true
}
trap restore_live_state EXIT

echo '== all eight Samba hot_reload settings change online =='
samba_before=$(docker_test inspect -f '{{.Id}}' "$SAMBA_DC")
docker_test exec "$SAMBA_DC" samba-tool domain passwordsettings set \
  --complexity="$alt_complexity" \
  --history-length="$alt_history" \
  --min-pwd-length="$alt_min_length" \
  --min-pwd-age="$alt_min_age" \
  --max-pwd-age="$alt_max_age" \
  --account-lockout-threshold="$alt_lockout_threshold" \
  --account-lockout-duration="$alt_lockout_duration" \
  --reset-account-lockout-after="$alt_lockout_reset" >/dev/null 2>&1
changed=$(docker_test exec "$SAMBA_DC" samba-tool domain passwordsettings show)
printf '%s\n' "$changed" | grep -Fq "Password complexity: $alt_complexity"
printf '%s\n' "$changed" | grep -Fq "Password history length: $alt_history"
printf '%s\n' "$changed" | grep -Fq "Minimum password length: $alt_min_length"
printf '%s\n' "$changed" | grep -Fq "Minimum password age (days): $alt_min_age"
printf '%s\n' "$changed" | grep -Fq "Maximum password age (days): $alt_max_age"
printf '%s\n' "$changed" | grep -Fq "Account lockout duration (mins): $alt_lockout_duration"
printf '%s\n' "$changed" | grep -Fq "Account lockout threshold (attempts): $alt_lockout_threshold"
printf '%s\n' "$changed" | grep -Fq "Reset account lockout after (mins): $alt_lockout_reset"
[ "$samba_before" = "$(docker_test inspect -f '{{.Id}}' "$SAMBA_DC")" ] ||
  fail 'Samba DC container changed during the online policy update'

echo '== Nextcloud language and locale reconcile online =='
nextcloud_before=$(docker_test inspect -f '{{.Id}}' "$NEXTCLOUD")
docker_test exec "$NEXTCLOUD" runuser -u www-data -- \
  php /var/www/html/occ config:system:set default_language --value=de >/dev/null
docker_test exec "$NEXTCLOUD" runuser -u www-data -- \
  php /var/www/html/occ config:system:set default_locale --value=de_DE >/dev/null
[ "$(docker_test exec "$NEXTCLOUD" runuser -u www-data -- php /var/www/html/occ config:system:get default_language)" = de ]
[ "$(docker_test exec "$NEXTCLOUD" runuser -u www-data -- php /var/www/html/occ config:system:get default_locale)" = de_DE ]
[ "$nextcloud_before" = "$(docker_test inspect -f '{{.Id}}' "$NEXTCLOUD")" ] ||
  fail 'Nextcloud container changed during the online localization update'

echo '== LAM default language reconciles online =='
docker_test run -d --name "$LAM_PROBE" \
  -e LAM_ADMIN_PASSWORD='Probe-Only-1!' \
  -e LAM_LANGUAGE=en_US.utf8 \
  -e SAMBA_DC_ADMIN_GROUP_DN='CN=Admins,DC=nas,DC=test' \
  -e SAMBA_DC_BASE_COMPUTERS_DN='CN=Computers,DC=nas,DC=test' \
  -e SAMBA_DC_BASE_DN='DC=nas,DC=test' \
  -e SAMBA_DC_BASE_GROUPS_DN='OU=Groups,DC=nas,DC=test' \
  -e SAMBA_DC_BASE_USERS_DN='OU=Users,DC=nas,DC=test' \
  -e SAMBA_DC_DOMAIN=nas.test \
  -e SAMBA_DC_LDAP_BIND_DN='CN=svc,DC=nas,DC=test' \
  -e SAMBA_DC_LDAP_BIND_PASSWORD='Probe-Bind-1!' \
  -e SAMBA_DC_LDAPS_SERVER_URL='ldaps://dc.nas.test' \
  -e TZ=UTC "$LAM_IMAGE" >/dev/null
for _ in $(seq 1 30); do
  docker_test exec "$LAM_PROBE" pgrep -x apache2 >/dev/null 2>&1 && break
  sleep 1
done
docker_test exec "$LAM_PROBE" pgrep -x apache2 >/dev/null || fail 'LAM did not start'
lam_before=$(docker_test inspect -f '{{.Id}}' "$LAM_PROBE")
docker_test exec -e LAM_LANGUAGE=de_DE.utf8 "$LAM_PROBE" php /opt/anas/configure.php
lam_language=$(docker_test exec "$LAM_PROBE" php -r \
  '$x=json_decode(file_get_contents("/var/lib/ldap-account-manager/config/lam.conf"),true); echo $x["defaultLanguage"];')
[ "$lam_language" = de_DE.utf8 ] || fail "LAM stored language $lam_language"
[ "$lam_before" = "$(docker_test inspect -f '{{.Id}}' "$LAM_PROBE")" ] ||
  fail 'LAM container changed during the online language update'

echo '== Samba FS can reload smb.conf and reconcile ACLs online =='
docker_test run -d --name "$SAMBA_FS_PROBE" --entrypoint /bin/sh "$SAMBA_FS_IMAGE" -lc '
  mkdir -p /probe /probe2 /run/samba
  cat >/tmp/probe-smb.conf <<EOF
[global]
  server role = standalone server
  map to guest = Bad User
  interfaces = lo
  bind interfaces only = Yes
[Share]
  path = /probe
  guest ok = No
  read only = Yes
EOF
  exec smbd -F --no-process-group -s /tmp/probe-smb.conf
' >/dev/null
for _ in $(seq 1 20); do
  docker_test exec "$SAMBA_FS_PROBE" pgrep -x smbd >/dev/null 2>&1 && break
  sleep 1
done
docker_test exec "$SAMBA_FS_PROBE" pgrep -x smbd >/dev/null || fail 'Samba FS probe did not start'
samba_fs_before=$(docker_test inspect -f '{{.Id}}' "$SAMBA_FS_PROBE")
docker_test exec "$SAMBA_FS_PROBE" sh -lc '
  sed -i "s/guest ok = No/guest ok = Yes/" /tmp/probe-smb.conf
  cat >>/tmp/probe-smb.conf <<EOF
[Online]
  path = /probe2
  guest ok = Yes
  read only = No
EOF
  testparm -s /tmp/probe-smb.conf >/dev/null
  smbcontrol all reload-config
  setfacl -m u:nobody:r-x /probe
  getfacl -cp /probe | grep -Fq "user:nobody:r-x"
'
for _ in $(seq 1 10); do
  docker_test exec "$SAMBA_FS_PROBE" smbclient -N -L 127.0.0.1 2>/dev/null | grep -Fq Online && break
  sleep 1
done
docker_test exec "$SAMBA_FS_PROBE" smbclient -N -L 127.0.0.1 2>/dev/null | grep -Fq Online ||
  fail 'the running smbd process did not publish the reloaded share'
[ "$samba_fs_before" = "$(docker_test inspect -f '{{.Id}}' "$SAMBA_FS_PROBE")" ] ||
  fail 'Samba FS container changed during config and ACL reconciliation'

echo '== domain route labels and long-lived lego env are not mutable in place =='
if docker_test update --label anas.probe=value "$NEXTCLOUD" >/dev/null 2>&1; then
  fail 'Docker unexpectedly accepted an in-place container-label update'
fi
docker_test exec -e VIRTUAL_DOMAIN=false -e LEGO_PROVIDER_CODE=probe "$LEGO" sh -lc \
  'test "$VIRTUAL_DOMAIN" = false && test "$LEGO_PROVIDER_CODE" = probe'
docker_test exec "$LEGO" sh -lc \
  'tr "\0" "\n" </proc/1/environ | grep -Fx VIRTUAL_DOMAIN=true' >/dev/null ||
  fail 'the prepared virtual-domain fixture does not expose the expected lego process environment'

restore_live_state
trap - EXIT
echo 'parameter in-place capability E2E passed; live values restored'
