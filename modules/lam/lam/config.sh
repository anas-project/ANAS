#!/bin/sh

set -eu

for name in LAM_ADMIN_PASSWORD LAM_LANGUAGE SAMBA_DC_ADMIN_GROUP_DN SAMBA_DC_BASE_COMPUTERS_DN SAMBA_DC_BASE_DN SAMBA_DC_BASE_GROUPS_DN SAMBA_DC_BASE_USERS_DN SAMBA_DC_DOMAIN SAMBA_DC_LDAP_BIND_DN SAMBA_DC_LDAP_BIND_PASSWORD SAMBA_DC_LDAPS_SERVER_URL TRAEFIK_HOSTNAME TZ; do
  eval 'present=${'"$name"'+x}'
  if [ "$present" != x ]; then
    echo "missing required environment variable: $name" >&2
    exit 1
  fi
done

{
  printf '%s\n' \
    'RemoteIPHeader X-Forwarded-For' \
    "RemoteIPInternalProxy $TRAEFIK_HOSTNAME"
  if [ -n "${TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS:-}" ]; then
    old_ifs=$IFS
    IFS=,
    for trusted_proxy in $TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS; do
      trusted_proxy=$(printf '%s' "$trusted_proxy" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
      case "$trusted_proxy" in
        ''|*[!0-9A-Fa-f:./]*)
          echo "invalid trusted upstream proxy IP or CIDR: $trusted_proxy" >&2
          exit 1
          ;;
      esac
      printf 'RemoteIPInternalProxy %s\n' "$trusted_proxy"
    done
    IFS=$old_ifs
  fi
} >/etc/apache2/conf-available/anas-real-ip.conf
a2enconf anas-real-ip >/dev/null

ca="/certs/${ANAS_TLS_INTERNAL_CA_NAME:-anas-internal-ca.crt}"
if [ -s "$ca" ]; then
  install -m 0644 "$ca" /usr/local/share/ca-certificates/anas-internal-ca.crt
  update-ca-certificates
fi

php /opt/anas/configure.php

php_ini=$(echo /etc/php/*/apache2/php.ini)
sed -i \
  -e 's/^max_execution_time =.*/max_execution_time = 60/' \
  -e 's/^post_max_size =.*/post_max_size = 100M/' \
  -e 's/^upload_max_filesize =.*/upload_max_filesize = 100M/' \
  -e 's/^memory_limit =.*/memory_limit = 256M/' \
  "$php_ini"

rm -f /run/apache2/apache2.pid
export APACHE_CONFDIR=/etc/apache2
. /etc/apache2/envvars
exec /usr/sbin/apache2 -DFOREGROUND
