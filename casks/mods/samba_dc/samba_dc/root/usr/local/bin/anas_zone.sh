#!/usr/bin/with-contenv bash

waiting_samba() {
  while :
  do
    if samba-tool domain level show >/dev/null 2>&1; then
      return
    fi
    echo "Waiting Samba AD online..."
    sleep 2
  done
}

waiting_dns() {
  while :
  do
    if nc -z 127.0.0.1 53 >/dev/null 2>&1; then
      return
    fi
    echo "Waiting DNS online..."
    sleep 2
  done
}

ensure_a_record() {
  local name="$1"
  local existing address
  local credentials="$SAMBA_DC_ADMINISTRATOR_NAME%$SAMBA_DC_ADMINISTRATOR_PASSWORD"

  existing=$(samba-tool dns query 127.0.0.1 "$BASE_DOMAIN" "$name" A \
    -U "$credentials" 2>/dev/null | awk '$1 == "A:" { print $2 }' || true)
  if [ "$existing" = "$HOST_IP" ]; then
    return 0
  fi

  for address in $existing; do
    samba-tool dns delete 127.0.0.1 "$BASE_DOMAIN" "$name" A "$address" \
      -U "$credentials" >/dev/null 2>&1 || return 1
  done
  samba-tool dns add 127.0.0.1 "$BASE_DOMAIN" "$name" A "$HOST_IP" \
    -U "$credentials" >/dev/null 2>&1
}

verify_a_record() {
  local name="$1"
  host "$name.$BASE_DOMAIN" 127.0.0.1 2>/dev/null | \
    grep -Fq "has address $HOST_IP"
}

sleep 5
echo "Add domain resolve"
waiting_samba

record_names=""
for domain in $(echo "$DOMAINS" | tr "," "\n")
do
  domain_arr=( $(echo "$domain" | tr "/" " ") )
  if [ "${domain_arr[0]}" == "inner" ]; then
    name="${domain_arr[1]}"
    echo "Ensure $name.$BASE_DOMAIN. 3600 IN A $HOST_IP"
    while ! ensure_a_record "$name"; do
      echo "DNS database update failed, waiting retry..."
      sleep 2
    done
    record_names="$record_names $name"
  elif [ "${domain_arr[0]}" == "dhcp" ]; then
    echo "dhcp TODO"
  fi
done

# BIND reads the Samba database through SSHFS.  Writing the database through
# the DLZ mount is unsafe, so publish only after all local updates are complete.
touch /var/lib/samba/.anas-zone-ready
waiting_dns
printf 'nameserver 127.0.0.1\n' > /etc/resolv.conf

for name in $record_names; do
  while ! verify_a_record "$name"; do
    echo "Waiting for DNS record $name.$BASE_DOMAIN..."
    sleep 2
  done
done

echo "Add domain completed"
touch /run/anas-zone.ready
