#!/bin/sh

echo "Run script"

echo "Setting DNS server to $LEGO_DNS_SERVER"
echo "nameserver $LEGO_DNS_SERVER" > /etc/resolv.conf

# Publish a usable certificate before anything else starts. ACME issuance can
# take minutes or fail outright, and every other module waits on this directory.
/root/ca.sh bootstrap

# One pinned path that is a complete trust anchor whichever issuer is serving.
# The issuer chain alone is not: under ACME it stops at an intermediate whose
# root is only in the system store, so a consumer pinning it fails with
# "unable to get issuer certificate". The internal root alone is not either,
# and a consumer that replaces the system store with it cannot verify the
# public certificate. Rebuilt every start so a re-bootstrapped internal CA and
# an updated system store both land here.
publish_trust_bundle() {
  bundle=/certs/certificates/anas-trust-bundle.crt
  tmp="$bundle.tmp"
  : >"$tmp"
  [ -f /etc/ssl/certs/ca-certificates.crt ] && cat /etc/ssl/certs/ca-certificates.crt >>"$tmp"
  [ -f /certs/certificates/anas-internal-ca.crt ] && cat /certs/certificates/anas-internal-ca.crt >>"$tmp"
  if [ ! -s "$tmp" ]; then
    echo "refusing to publish an empty trust bundle" >&2
    rm -f "$tmp"
    return 1
  fi
  chmod 0644 "$tmp"
  mv "$tmp" "$bundle"
  echo "Published anas-trust-bundle.crt ($(grep -c 'BEGIN CERTIFICATE' "$bundle") certificates)"
}
publish_trust_bundle

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
