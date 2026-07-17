#!/usr/bin/with-contenv bash
set -euo pipefail

if [ ! -s /var/lib/samba/bind-dns/named.conf ]; then
  echo "Samba BIND9-DLZ configuration is missing" >&2
  exit 1
fi

install -d -o bind -g bind -m 0755 /run/named /var/cache/bind /var/cache/bind/master
install -o bind -g bind -m 0644 /var/cache/bind-source/localhost.zone /var/cache/bind/master/localhost.zone
install -o bind -g bind -m 0644 /var/cache/bind-source/0.0.127.zone /var/cache/bind/master/0.0.127.zone

chgrp bind /var/lib/samba/private/dns.keytab
chmod 0640 /var/lib/samba/private/dns.keytab

envsubst < /etc/bind/named.conf.j3 > /etc/bind/named.conf
named-checkconf /etc/bind/named.conf
