#!/bin/sh

set -eu

for name in LAM_ADMIN_PASSWORD LAM_LANGUAGE SAMBA_DC_ADMIN_DN SAMBA_DC_BASE_COMPUTERS_DN SAMBA_DC_BASE_DN SAMBA_DC_BASE_GROUPS_DN SAMBA_DC_BASE_USERS_DN SAMBA_DC_DOMAIN SAMBA_DC_LDAPS_SERVER_URL; do
  eval 'present=${'"$name"'+x}'
  if [ "$present" != x ]; then
    echo "missing required environment variable: $name" >&2
    exit 1
  fi
done

php /opt/anas/configure.php

exec /usr/local/bin/start.sh
