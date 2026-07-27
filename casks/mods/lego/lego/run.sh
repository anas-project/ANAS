#!/bin/sh

echo "Run script"

echo "Setting DNS server to $LEGO_DNS_SERVER"
echo "nameserver $LEGO_DNS_SERVER" > /etc/resolv.conf

# Publish a usable certificate before anything else starts. ACME issuance can
# take minutes or fail outright, and every other cask waits on this directory.
/root/ca.sh bootstrap

if [ "${ANAS_VIRTUAL_DOMAIN:-false}" = "true" ]; then
  echo "Virtual domain: not attempting ACME; serving the internal certificate"
else
  /root/cert.sh
fi

echo "Run cron"
exec crond -l 2 -f
