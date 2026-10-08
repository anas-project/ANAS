#!/bin/sh
set -eu

: "${ANAS_TLS_TRUST_BUNDLE_NAME:?ANAS_TLS_TRUST_BUNDLE_NAME is required}"

install -m 0444 "/certs/${ANAS_TLS_TRUST_BUNDLE_NAME}" /tmp/anas-samba-ad-ca.crt
# The native HTTP task sender uses requests, whose CA store is separate from
# the CertificateKeyPair imported for LDAPS. Keep peer verification enabled
# and use the same managed public/internal trust bundle for backchannel TLS.
export REQUESTS_CA_BUNDLE=/tmp/anas-samba-ad-ca.crt
ak import_certificate \
  --certificate /tmp/anas-samba-ad-ca.crt \
  --name anas-samba-ad-ca

# The worker applies custom blueprints asynchronously. Compose consumers must
# not start merely because the worker process is alive: OIDC discovery would
# still return 404 and fail-closed consumers would enter a restart loop.
/opt/anas/bin/wait-blueprints &

exec ak worker
