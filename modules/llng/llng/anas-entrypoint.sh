#!/bin/bash

set -euo pipefail

# These paths are inherited Docker VOLUME declarations. Compose supplies fresh
# tmpfs mounts so they are explicit, non-persistent runtime state instead of
# anonymous Docker volumes. LLNG's authoritative configuration is mounted
# separately at /var/lib/lemonldap-ng/conf and sessions are stored in the DB.
/usr/local/bin/anas-llng-restore-runtime

for name in BASE_DOMAIN DB_HOST DB_PASSWORD DB_POST DB_USER LLNG_DB_NAME LLNG_DB_TYPE LLNG_DOMAIN LLNG_DOMAIN_FULL LLNG_LDAP_AUTH_FILTER LLNG_LDAP_MAIL_FILTER LLNG_MANAGER_DOMAIN LLNG_MANAGER_DOMAIN_FULL SAMBA_DC_ADMIN_GROUP_NAME SAMBA_DC_BASE_GROUPS_DN SAMBA_DC_BASE_GROUPS_ROLE_DN SAMBA_DC_BASE_USERS_DN SAMBA_DC_LDAPS_PORT SAMBA_DC_LDAPS_SERVER_URL SAMBA_DC_PASSWORD_BIND_DN SAMBA_DC_PASSWORD_BIND_PASSWORD SAMBA_DC_USER_COMPLEX_PASS SAMBA_DC_USER_MIN_PASS_LENGTH SAMBA_DC_USER_PASSWORD_HISTORY SERVER_NAME TRAEFIK_DOMAIN_FULL; do
  if [ -z "${!name:-}" ]; then
    echo "missing required environment variable: $name" >&2
    exit 1
  fi
done

# Keep the first-login password instructions aligned with the Samba domain
# policy. The catalog lives in the image layer, so this is safely regenerated
# on every container start and never becomes persistent configuration drift.
/usr/local/bin/anas-llng-configure-password-guidance

ca="/certs/${ANAS_TLS_INTERNAL_CA_NAME:-anas-internal-ca.crt}"
if [ -s "$ca" ]; then
  install -m 0644 "$ca" /usr/local/share/ca-certificates/anas-internal-ca.crt
  update-ca-certificates
fi

until nc -z "$DB_HOST" "$DB_POST"; do
  echo "Waiting for $DB_HOST:$DB_POST"
  sleep 2
done

if [ "$LLNG_DB_TYPE" = postgres ]; then
  browseable_db_config=Apache::Session::Browseable::PgJSON
  db_config="DBI:Pg:database=${LLNG_DB_NAME};host=${DB_HOST};port=${DB_POST}"
  sed "s/{{LLNG_DB_NAME}}/${LLNG_DB_NAME}/g" /opt/anas/postgre_init.sql >/tmp/anas-llng-init.sql
  PGPASSWORD="$DB_PASSWORD" psql -v ON_ERROR_STOP=1 -h "$DB_HOST" -p "$DB_POST" -U "$DB_USER" -f /tmp/anas-llng-init.sql
elif [ "$LLNG_DB_TYPE" = mariadb ]; then
  browseable_db_config=Apache::Session::Browseable::MySQL
  db_config="DBI:mysql:database=${LLNG_DB_NAME};host=${DB_HOST};port=${DB_POST}"
  sed "s/{{LLNG_DB_NAME}}/${LLNG_DB_NAME}/g" /opt/anas/mysql_init.sql >/tmp/anas-llng-init.sql
  mysql -h "$DB_HOST" -P "$DB_POST" -u "$DB_USER" -p"$DB_PASSWORD" </tmp/anas-llng-init.sql
else
  echo "unsupported LLNG_DB_TYPE: $LLNG_DB_TYPE" >&2
  exit 1
fi
export browseable_db_config db_config

template_variables='${LLNG_MANAGER_DOMAIN_FULL} ${LLNG_MANAGER_DOMAIN} ${LLNG_DOMAIN} ${SAMBA_DC_ADMIN_GROUP_NAME} ${BASE_DOMAIN} ${LLNG_DOMAIN_FULL} ${SAMBA_DC_LDAPS_SERVER_URL} ${SAMBA_DC_LDAPS_PORT} ${SAMBA_DC_PASSWORD_BIND_DN} ${SAMBA_DC_PASSWORD_BIND_PASSWORD} ${SAMBA_DC_BASE_USERS_DN} ${SAMBA_DC_BASE_GROUPS_ROLE_DN} ${SAMBA_DC_BASE_GROUPS_DN} ${LLNG_LDAP_AUTH_FILTER} ${LLNG_LDAP_MAIL_FILTER} ${SERVER_NAME} ${TRAEFIK_DOMAIN_FULL} ${browseable_db_config} ${db_config} ${DB_USER} ${DB_PASSWORD}'
cp /opt/anas/lmConf.json /tmp/anas-lmConf.template
for name in LLNG_MANAGER_DOMAIN_FULL LLNG_MANAGER_DOMAIN LLNG_DOMAIN SAMBA_DC_ADMIN_GROUP_NAME BASE_DOMAIN LLNG_DOMAIN_FULL SAMBA_DC_LDAPS_SERVER_URL SAMBA_DC_LDAPS_PORT SAMBA_DC_PASSWORD_BIND_DN SAMBA_DC_PASSWORD_BIND_PASSWORD SAMBA_DC_BASE_USERS_DN SAMBA_DC_BASE_GROUPS_ROLE_DN SAMBA_DC_BASE_GROUPS_DN LLNG_LDAP_AUTH_FILTER LLNG_LDAP_MAIL_FILTER SERVER_NAME TRAEFIK_DOMAIN_FULL browseable_db_config db_config DB_USER DB_PASSWORD; do
  printf -v replacement '${%s}' "$name"
  sed -i "s/{{${name}}}/${replacement}/g" /tmp/anas-lmConf.template
done
envsubst "$template_variables" </tmp/anas-lmConf.template >/tmp/anas-lmConf.json
jq empty /tmp/anas-lmConf.json
perl -MYAML=Dump -MJSON=decode_json -0777 -ne 'print Dump(decode_json($_))' \
  /tmp/anas-lmConf.json >/tmp/llng_config_custom.yaml

/usr/local/bin/anas-configure-clients.sh &

exec /bin/sh /docker-entrypoint.sh
