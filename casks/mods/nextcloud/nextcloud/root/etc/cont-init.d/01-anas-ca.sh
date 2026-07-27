#!/usr/bin/with-contenv bash
# Trust the deployment's internal certificate authority.
#
# This runs unconditionally, not only in virtual-domain mode. ACME issuance is
# never instant and can fail, so the certificate a sibling service presents may
# legitimately be the internal one at any point — during first start, during a
# renewal window, or after an ACME outage. A container that only trusts public
# roots fails those cases with an error that surfaces far from its cause: the
# symptom here was "Lost connection to LDAP server" while the actual fault was
# in the certificate issuer.
set -eu

ca="/certs/${ANAS_TLS_INTERNAL_CA_NAME:-anas-internal-ca.crt}"
if [ ! -s "$ca" ]; then
  echo "No internal CA published; relying on public trust roots only"
  exit 0
fi

install -m 0644 "$ca" /usr/local/share/ca-certificates/anas-internal-ca.crt
update-ca-certificates
echo "Installed the ANAS internal CA into the system trust store"
