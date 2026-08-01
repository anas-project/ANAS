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

bind_variables='${SAMBA_DC_DNS_ALLOWED_NETWORKS} ${SAMBA_DC_DNS_CACHE_SIZE} ${SAMBA_DC_DNS_FORWARDERS} ${SAMBA_DC_DNS_SERVER}'
for name in $(printf '%s\n' "$bind_variables" | grep -o '[A-Z][A-Z0-9_]*'); do
  eval 'present=${'"$name"'+x}'
  if [[ "$present" != x ]]; then
    echo "missing required environment variable: $name" >&2
    exit 1
  fi
done
envsubst "$bind_variables" < /etc/bind/named.conf.envsubst > /etc/bind/named.conf.tmp
if grep -n '\${[A-Z][A-Z0-9_]*}' /etc/bind/named.conf.tmp; then
  echo "unresolved variables in named.conf" >&2
  exit 1
fi
mv /etc/bind/named.conf.tmp /etc/bind/named.conf
named-checkconf /etc/bind/named.conf
