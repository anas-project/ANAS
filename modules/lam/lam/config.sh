#!/bin/sh

set -eu

for name in LAM_ADMIN_PASSWORD LAM_LANGUAGE SAMBA_DC_ADMIN_DN SAMBA_DC_BASE_COMPUTERS_DN SAMBA_DC_BASE_DN SAMBA_DC_BASE_GROUPS_DN SAMBA_DC_BASE_USERS_DN SAMBA_DC_DOMAIN SAMBA_DC_LDAPS_SERVER_URL TZ; do
  eval 'present=${'"$name"'+x}'
  if [ "$present" != x ]; then
    echo "missing required environment variable: $name" >&2
    exit 1
  fi
done

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
