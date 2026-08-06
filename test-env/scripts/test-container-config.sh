#!/usr/bin/env sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM

mkdir -p "$test_dir/traefik"
ANAS_CONFIG_DIR="$test_dir/traefik" \
ANAS_TRAEFIK_BINARY=/usr/bin/true \
LEGO_CERT_NAME=fullchain.pem \
LEGO_KEY_NAME=key.pem \
  sh "$ROOT_DIR/casks/mods/traefik/traefik/anas-entrypoint.sh" || exit 1
grep -q 'certFile: /certs/fullchain.pem' "$test_dir/traefik/cert.yml" || exit 1
grep -q 'keyFile: /certs/key.pem' "$test_dir/traefik/cert.yml" || exit 1

mkdir -p "$test_dir/eturnal"
ANAS_CONFIG_DIR="$test_dir/eturnal" \
TURN_PORT=3478 \
TURN_RELAY_MIN_PORT=49152 \
TURN_RELAY_MAX_PORT=49200 \
TURN_SECRET="quote'safe" \
  sh "$ROOT_DIR/casks/mods/eturnal/eturnal/anas-entrypoint.sh" /usr/bin/true || exit 1
grep -q "secret: 'quote''safe'" "$test_dir/eturnal/eturnal.yml" || exit 1

if command -v node >/dev/null 2>&1; then
  mkdir -p "$test_dir/meshcentral"
  env \
    MESHCENTRAL_DOMAIN=mesh.example.test \
    TRAEFIK_BASE_PORT=9000 \
    MESHCENTRAL_MPS_PORT=4433 \
    TRAEFIK_IP=172.20.0.2 \
    MYSQL_HOST=db \
    MYSQL_PORT=3306 \
    MYSQL_USERNAME=mesh \
    'MYSQL_PASSWORD=quote" slash\ dollar$ unicode密码' \
    TRAEFIK_DOMAIN_FULL=https://proxy.example.test \
    'MESHCENTRAL_TITLE=NAS "Control"' \
    MESHCENTRAL_SUBTITLE=Devices \
    SAMBA_DC_LDAPS_SERVER_URL_PORT=ldaps://dc:636 \
    SAMBA_DC_LDAP_BIND_DN=CN=svc,DC=example,DC=test \
    'SAMBA_DC_LDAP_BIND_PASSWORD=ldap"secret\value' \
    SAMBA_DC_BASE_USERS_DN=OU=Users,DC=example,DC=test \
    'MESHCENTRAL_USER_LOGIN_FILTER=(sAMAccountName={{username}})' \
    SAMBA_DC_BASE_GROUPS_ROLE_DN=OU=Roles,DC=example,DC=test \
    'SAMBA_DC_GROUP_CLASS_FILTER=(objectClass=group)' \
    SAMBA_DC_ADMIN_NAME=admin \
    SAMBA_DC_USER_DISPLAY_NAME=displayName \
    SAMBA_DC_USER_EMAIL=mail \
    SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE=anasIdentityAnchor \
    SAMBA_DC_APP_FILTER=true \
    SAMBA_DC_BASE_APP_DN=OU=Apps,DC=example,DC=test \
    SAMBA_DC_ADMIN_GROUP_DN=CN=Admins,OU=Roles,DC=example,DC=test \
    node "$ROOT_DIR/casks/mods/meshcentral/meshcentral/configure.js" \
      "$ROOT_DIR/casks/mods/meshcentral/meshcentral/config.base.json" \
      "$test_dir/meshcentral/config.json" || exit 1
  node -e '
    const config = require(process.argv[1]);
    if (config.settings.mySQL.password !== `quote" slash\\ dollar$ unicode密码`) process.exit(1);
    if (!config.domains[""].ldapUserRequiredGroupMembership.endsWith("OU=Apps,DC=example,DC=test")) process.exit(2);
    if (config.domains[""].ldapUserKey !== "anasIdentityAnchor") process.exit(3);
    if (config.domains[""].ldapUserBinaryKey !== undefined) process.exit(4);
  ' "$test_dir/meshcentral/config.json" || exit 1
fi

echo "container configuration tests passed"
