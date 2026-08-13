#!/bin/bash
set -eu

occ() {
  runuser -u www-data -- php /var/www/html/occ "$@"
}

waiting_installed() {
  while :
  do
    if occ status --no-ansi 2>/dev/null | grep -q 'installed: true'; then
      echo "Execute nextcloud tasks"
      return
    fi
    echo "Waiting nextcloud install finished..."
    sleep 5
  done
}

waiting_port() { # $1 url, $2 port
  while :
  do
    echo "nc -zv $1 $2"
    if nc -z "$1" "$2"; then
      echo "$1:$2 online"
      return
    fi
    echo "Waiting $1:$2 online..."
    sleep 2
  done
}

sleep 5

waiting_installed

echo "Waiting LDAP online"
waiting_port "$SAMBA_DC_HOST" "$SAMBA_DC_LDAPS_PORT"

exec /usr/local/bin/task.sh
