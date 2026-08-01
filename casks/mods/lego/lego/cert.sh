#!/bin/sh
# ACME issuance. Runs only when the deployment expects a publicly trusted
# certificate; ca.sh has already put an internal one in place, so a failure
# here degrades to the internal issuer instead of leaving services with no
# certificate at all.

OUT=/certs/certificates
ISSUER_MARK="$OUT/.issuer"

mkdir -p /certs/certs1000 "$OUT"

set -- run -a -m="$LEGO_EMAIL" \
  --domains "$BASE_DOMAIN" --domains "*.$BASE_DOMAIN" \
  --path /certs --pem \
  --dns "$DNS_PROVIDER" --dns.resolvers "$LEGO_DNS_SERVER" \
  --renew-days 30

# Staging has no rate limits, so it is the right target while a deployment is
# being brought up. Production allows only a handful of identical certificates
# per week and a burnt limit locks the domain out for that long.
if [ -n "${LEGO_ACME_SERVER:-}" ]; then
  echo "Using ACME server $LEGO_ACME_SERVER"
  set -- "$@" --server "$LEGO_ACME_SERVER"
fi

publish() {
  echo "Copy certificates(root) to certs1000(user 1000)"
  cp "$OUT"/* /certs/certs1000/ 2>/dev/null
  chown 1000:1000 -R /certs/certs1000
  echo acme > "$ISSUER_MARK"
}

echo "Applying for or renewing the ACME certificate"
if /lego "$@"; then
  publish
  exit 0
fi

echo "ACME issuance failed; the internal certificate stays in place" >&2
exit 1
