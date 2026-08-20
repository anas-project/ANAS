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
  sh "$ROOT_DIR/modules/traefik/traefik/anas-entrypoint.sh" || exit 1
grep -q 'certFile: /certs/fullchain.pem' "$test_dir/traefik/cert.yml" || exit 1
grep -q 'keyFile: /certs/key.pem' "$test_dir/traefik/cert.yml" || exit 1
# No route declarations means no routes file at all, so a stale one from an
# earlier release cannot keep advertising a route that is now gone.
[ ! -e "$test_dir/traefik/routes.yml" ] || exit 1

# Declared routes for services the Docker provider cannot see.
mkdir -p "$test_dir/traefik-routes"
env \
  ANAS_CONFIG_DIR="$test_dir/traefik-routes" \
  ANAS_TRAEFIK_BINARY=/usr/bin/true \
  LEGO_CERT_NAME=fullchain.pem \
  LEGO_KEY_NAME=key.pem \
  'ANAS_TRAEFIK_ROUTE__DDNS_GO__RULE=Host(`ddns-go.example.test`)' \
  ANAS_TRAEFIK_ROUTE__DDNS_GO__URL=http://172.18.0.1:9876 \
  ANAS_TRAEFIK_ROUTE__DDNS_GO__MIDDLEWARES=forward-auth,compress \
  sh "$ROOT_DIR/modules/traefik/traefik/anas-entrypoint.sh" || exit 1
grep -q '    ddns-go:' "$test_dir/traefik-routes/routes.yml" || exit 1
grep -q 'rule: "Host(`ddns-go.example.test`)"' "$test_dir/traefik-routes/routes.yml" || exit 1
grep -q 'url: "http://172.18.0.1:9876"' "$test_dir/traefik-routes/routes.yml" || exit 1
grep -q -- '- "forward-auth"' "$test_dir/traefik-routes/routes.yml" || exit 1
grep -q -- '- "compress"' "$test_dir/traefik-routes/routes.yml" || exit 1
grep -q -- '- "https"' "$test_dir/traefik-routes/routes.yml" || exit 1
grep -q 'tls: {}' "$test_dir/traefik-routes/routes.yml" || exit 1
# The certificate store is still written beside the routes.
grep -q 'certFile: /certs/fullchain.pem' "$test_dir/traefik-routes/cert.yml" || exit 1

# A quote in a rule must be escaped into the scalar, not close it.
mkdir -p "$test_dir/traefik-quote"
env \
  ANAS_CONFIG_DIR="$test_dir/traefik-quote" \
  ANAS_TRAEFIK_BINARY=/usr/bin/true \
  LEGO_CERT_NAME=fullchain.pem \
  LEGO_KEY_NAME=key.pem \
  'ANAS_TRAEFIK_ROUTE__ODD__RULE=Header(`X-Q`, `a"b\c`)' \
  ANAS_TRAEFIK_ROUTE__ODD__URL=http://10.0.0.1:1 \
  sh "$ROOT_DIR/modules/traefik/traefik/anas-entrypoint.sh" || exit 1
grep -q 'rule: "Header(`X-Q`, `a\\"b\\\\c`)"' "$test_dir/traefik-quote/routes.yml" || exit 1

# A newline is the one character that could end the scalar and inject YAML, so
# it is refused rather than escaped.
mkdir -p "$test_dir/traefik-newline"
if env \
   ANAS_CONFIG_DIR="$test_dir/traefik-newline" \
   ANAS_TRAEFIK_BINARY=/usr/bin/true \
   LEGO_CERT_NAME=fullchain.pem \
   LEGO_KEY_NAME=key.pem \
   ANAS_TRAEFIK_ROUTE__EVIL__RULE="Host(\`a\`)
      injected: true" \
   ANAS_TRAEFIK_ROUTE__EVIL__URL=http://10.0.0.1:1 \
     sh "$ROOT_DIR/modules/traefik/traefik/anas-entrypoint.sh" 2>/dev/null; then
  echo "entrypoint accepted a route rule containing a newline" >&2
  exit 1
fi

# A rule without an upstream is a declaration error, not a silent no-op.
mkdir -p "$test_dir/traefik-nourl"
if env \
   ANAS_CONFIG_DIR="$test_dir/traefik-nourl" \
   ANAS_TRAEFIK_BINARY=/usr/bin/true \
   LEGO_CERT_NAME=fullchain.pem \
   LEGO_KEY_NAME=key.pem \
   'ANAS_TRAEFIK_ROUTE__NOURL__RULE=Host(`x.example.test`)' \
     sh "$ROOT_DIR/modules/traefik/traefik/anas-entrypoint.sh" 2>/dev/null; then
  echo "entrypoint accepted a route without an upstream URL" >&2
  exit 1
fi

mkdir -p "$test_dir/eturnal"
mkdir -p "$test_dir/eturnal-seed/bin" "$test_dir/eturnal-runtime"
printf '#!/bin/sh\nexit 0\n' >"$test_dir/eturnal-seed/bin/eturnalctl"
chmod 0755 "$test_dir/eturnal-seed/bin/eturnalctl"
ANAS_CONFIG_DIR="$test_dir/eturnal" \
ANAS_ETURNAL_RUNTIME_SEED="$test_dir/eturnal-seed" \
ANAS_ETURNAL_RUNTIME_DIR="$test_dir/eturnal-runtime" \
TURN_PORT=3478 \
TURN_RELAY_MIN_PORT=49152 \
TURN_RELAY_MAX_PORT=49200 \
TURN_SECRET="quote'safe" \
  sh "$ROOT_DIR/modules/eturnal/eturnal/anas-entrypoint.sh" /usr/bin/true || exit 1
grep -q "secret: 'quote''safe'" "$test_dir/eturnal/eturnal.yml" || exit 1
test -x "$test_dir/eturnal-runtime/bin/eturnalctl" || exit 1

# Docker restart preserves the tmpfs backing /opt/eturnal. The entrypoint must
# reuse its own completed runtime and still regenerate configuration, rather
# than rejecting the non-empty directory and putting the container in a loop.
ANAS_CONFIG_DIR="$test_dir/eturnal" \
ANAS_ETURNAL_RUNTIME_SEED="$test_dir/eturnal-seed" \
ANAS_ETURNAL_RUNTIME_DIR="$test_dir/eturnal-runtime" \
TURN_PORT=3478 \
TURN_RELAY_MIN_PORT=49152 \
TURN_RELAY_MAX_PORT=49200 \
TURN_SECRET="restart-safe" \
  sh "$ROOT_DIR/modules/eturnal/eturnal/anas-entrypoint.sh" /usr/bin/true || exit 1
grep -q "secret: 'restart-safe'" "$test_dir/eturnal/eturnal.yml" || exit 1
test -x "$test_dir/eturnal-runtime/bin/eturnalctl" || exit 1

# Every inherited LLNG/Eturnal image volume must have an explicit Compose mount.
# Otherwise Docker silently creates one anonymous volume per uncovered path.
for contract in \
  'modules/llng/docker-compose.yml|/var/lib/lemonldap-ng/conf' \
  'modules/llng/docker-compose.yml|/etc/lemonldap-ng' \
  'modules/llng/docker-compose.yml|/etc/nginx/sites-enabled' \
  'modules/llng/docker-compose.yml|/var/lib/lemonldap-ng/psessions' \
  'modules/llng/docker-compose.yml|/var/lib/lemonldap-ng/sessions' \
  'modules/eturnal/docker-compose.yml|/opt/eturnal'; do
  compose_file=${contract%%|*}
  target=${contract#*|}
  if ! grep -Fq "$target" "$ROOT_DIR/$compose_file"; then
    echo "$compose_file does not explicitly cover inherited volume $target" >&2
    exit 1
  fi
done

# Eturnal restores its executable release into this tmpfs. Docker defaults
# tmpfs mounts to noexec, so merely covering the inherited VOLUME is not enough.
grep -Eq '/opt/eturnal:([^,]*,)*exec(,|$)' \
  "$ROOT_DIR/modules/eturnal/docker-compose.yml" || {
  echo "Eturnal runtime tmpfs must be executable" >&2
  exit 1
}

mkdir -p \
  "$test_dir/llng-seed/etc-lemonldap-ng" \
  "$test_dir/llng-seed/etc-nginx-sites-enabled" \
  "$test_dir/llng-etc" \
  "$test_dir/llng-nginx" \
  "$test_dir/llng-psessions" \
  "$test_dir/llng-sessions"
printf 'seeded\n' >"$test_dir/llng-seed/etc-lemonldap-ng/lemonldap-ng.ini"
printf 'seeded\n' >"$test_dir/llng-seed/etc-nginx-sites-enabled/portal-nginx.conf"
env \
  ANAS_LLNG_RUNTIME_SEED_ROOT="$test_dir/llng-seed" \
  ANAS_LLNG_ETC_DIR="$test_dir/llng-etc" \
  ANAS_LLNG_NGINX_SITES_DIR="$test_dir/llng-nginx" \
  ANAS_LLNG_PSESSIONS_DIR="$test_dir/llng-psessions" \
  ANAS_LLNG_SESSIONS_DIR="$test_dir/llng-sessions" \
  ANAS_LLNG_RUNTIME_UID="$(id -u)" \
  ANAS_LLNG_RUNTIME_GID="$(id -g)" \
  sh "$ROOT_DIR/modules/llng/llng/restore-runtime.sh" || exit 1
grep -q seeded "$test_dir/llng-etc/lemonldap-ng.ini" || exit 1
grep -q seeded "$test_dir/llng-nginx/portal-nginx.conf" || exit 1
test -d "$test_dir/llng-psessions/lock" || exit 1
test -d "$test_dir/llng-sessions/lock" || exit 1

# A fresh Nextcloud volume exposes HTTP before its post-install tasks have
# downloaded notify_push. The sidecar must wait for the executable instead of
# entering a restart loop with exit 127.
grep -Fq 'while [ ! -x "$$custom_push_path" ] && [ ! -x "$$bundled_push_path" ]; do' \
  "$ROOT_DIR/modules/nextcloud/docker-compose.yml" || exit 1

# Nextcloud requests a database resource and names a dedicated principal. It
# must not consume PostgreSQL's administrator credential from the provider.
grep -Fq 'contract: relational_database' "$ROOT_DIR/modules/nextcloud/module.yml" || exit 1
grep -Fq 'principal: nextcloud' "$ROOT_DIR/modules/nextcloud/module.yml" || exit 1
if grep -Eq '^    - POSTGRES_(USERNAME|PASSWORD)$' "$ROOT_DIR/modules/nextcloud/module.yml"; then
  echo "Nextcloud consumes PostgreSQL administrator credentials" >&2
  exit 1
fi

# Authentik's worker must not race the server's first-run database migrations.
awk '
  /^  anas_authentik_worker:/ { worker = 1; next }
  worker && /^  [^ ]/ { exit }
  worker && /anas_authentik:/ { server = 1 }
  server && /condition: service_healthy/ { found = 1 }
  END { exit !found }
' "$ROOT_DIR/modules/authentik/docker-compose.yml" || exit 1

# The managed break-glass projection is deliberately root:root 0600. The
# server entrypoint must read it as root and drop privileges before launching
# authentik; widening the secret file permissions is not acceptable.
awk '
  /^  anas_authentik:/ { server = 1; next }
  server && /^  [^ ]/ { exit }
  server && /user: root/ { found = 1 }
  END { exit !found }
' "$ROOT_DIR/modules/authentik/docker-compose.yml" || exit 1
grep -Fq 'setpriv --reuid=1000 --regid=1000 --init-groups ak "$@"' \
  "$ROOT_DIR/modules/authentik/authentik/server-entrypoint.sh" || exit 1

if command -v node >/dev/null 2>&1; then
  node --test \
    "$ROOT_DIR/modules/meshcentral/meshcentral/enforce-oidc-only.test.js" \
    "$ROOT_DIR/modules/meshcentral/meshcentral/wait-for-oidc.test.js" || exit 1

  mkdir -p "$test_dir/meshcentral"
  env \
    MESHCENTRAL_DOMAIN=mesh.example.test \
    MESHCENTRAL_DOMAIN_FULL=https://mesh.example.test:9000 \
    TRAEFIK_BASE_PORT=9000 \
    MESHCENTRAL_MPS_PORT=4433 \
    TRAEFIK_IP=172.20.0.2 \
    MESHCENTRAL_DB_TYPE=postgres \
    MESHCENTRAL_DB_HOST=db \
    MESHCENTRAL_DB_PORT=5432 \
    MESHCENTRAL_DB_USERNAME=mesh \
    MESHCENTRAL_DB_NAME=meshcentral \
    'MESHCENTRAL_DB_PASSWORD=quote" slash\ dollar$ unicode密码' \
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
    SAMBA_DC_ADMIN_GROUP_NAME=Admins \
    MESHCENTRAL_OIDC_ISSUER_URL=https://auth.example.test:9000/application/o/meshcentral/ \
    MESHCENTRAL_OIDC_CLIENT_ID=meshcentral \
    'MESHCENTRAL_OIDC_CLIENT_SECRET=oidc"secret\value' \
    'MESHCENTRAL_OIDC_SCOPES=openid email' \
    node "$ROOT_DIR/modules/meshcentral/meshcentral/configure.js" \
      "$ROOT_DIR/modules/meshcentral/meshcentral/config.base.json" \
      "$test_dir/meshcentral/config.json" || exit 1
  node -e '
    const config = require(process.argv[1]);
    if (config.settings.postgres.password !== `quote" slash\\ dollar$ unicode密码`) process.exit(1);
    if (config.settings.postgres.database !== "meshcentral") process.exit(5);
    if (config.settings.postgres.createdatabase !== false) process.exit(6);
    if (config.settings.mySQL !== undefined) process.exit(7);
    if (!config.domains[""].ldapUserRequiredGroupMembership.endsWith("OU=Apps,DC=example,DC=test")) process.exit(2);
    if (config.domains[""].ldapUserKey !== "anasIdentityAnchor") process.exit(3);
    if (config.domains[""].ldapUserBinaryKey !== undefined) process.exit(4);
    const oidc = config.domains[""].authStrategies.oidc;
    if (oidc.client.redirect_uri !== "https://mesh.example.test:9000/auth-oidc-callback") process.exit(8);
    if (oidc.client.client_secret !== `oidc"secret\\value`) process.exit(9);
    if (!oidc.groups.required.includes("APP_meshcentral")) process.exit(10);
    if (oidc.custom.claims.uuid !== "anasIdentityAnchor") process.exit(11);
    if (oidc.groups.scope !== "profile") process.exit(12);
    if (config.domains[""].showPasswordLogin !== false) process.exit(13);
    if (config.domains[""].unknownUserRootRedirect !== "/auth-oidc") process.exit(14);
  ' "$test_dir/meshcentral/config.json" || exit 1
fi

echo "container configuration tests passed"
