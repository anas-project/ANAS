#!/bin/sh
set -eu

: "${ANAS_TLS_TRUST_BUNDLE_NAME:?ANAS_TLS_TRUST_BUNDLE_NAME is required}"

install -m 0444 "/certs/${ANAS_TLS_TRUST_BUNDLE_NAME}" /tmp/anas-samba-ad-ca.crt
ak import_certificate \
  --certificate /tmp/anas-samba-ad-ca.crt \
  --name anas-samba-ad-ca

exec ak worker
