#!/bin/sh

echo "Run script"

echo "Setting DNS server to $LEGO_DNS_SERVER"
echo "nameserver $LEGO_DNS_SERVER" > /etc/resolv.conf

# Publish a usable certificate before anything else starts. ACME issuance can
# take minutes or fail outright, and every other module waits on this directory.
/root/ca.sh bootstrap || exit 1

if [ "${VIRTUAL_DOMAIN:-false}" = "true" ]; then
  echo "Virtual domain: not attempting ACME; serving the internal certificate"
else
  /root/cert.sh
fi

# A deployment issued before the modes were corrected is still carrying lego's
# 0600 on public certificate material, and nothing re-publishes it until the
# next renewal. Consumers that verify TLS as a non-root user stay broken until
# then, so normalize what is already on disk at every start.
for artifact in "/certs/certificates/$LEGO_CERT_NAME" "/certs/certificates/$LEGO_CA_CERT_NAME"; do
  [ -f "$artifact" ] && chmod 0644 "$artifact"
done
[ -f "/certs/certificates/$LEGO_KEY_NAME" ] && chmod 0600 "/certs/certificates/$LEGO_KEY_NAME"

echo "Run cron"
exec crond -l 2 -f
