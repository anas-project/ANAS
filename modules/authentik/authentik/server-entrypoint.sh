#!/bin/sh
set -eu

password_file=/run/secrets/authentik-break-glass-password
if [ -z "$password_file" ] || [ ! -r "$password_file" ]; then
  echo "missing managed authentik break-glass password file" >&2
  exit 1
fi
AUTHENTIK_BOOTSTRAP_PASSWORD=$(sed -n '1p' "$password_file")
export AUTHENTIK_BOOTSTRAP_PASSWORD

if [ "$(id -u)" = 0 ]; then
  exec setpriv --reuid=1000 --regid=1000 --init-groups ak "$@"
fi
exec ak "$@"
