#!/bin/sh
# Internal certificate authority.
#
# Every deployment has one, regardless of whether ACME is used. ACME issuance
# is not instant — DNS-01 has to propagate, and it can fail outright on a
# domain that cannot be validated — so without a local issuer the services
# would have no certificate at all during that window. Each module used to paper
# over that by generating its own self-signed certificate, which is why nothing
# trusted anything: there were as many issuers as there were modules.
#
# The CA private key lives beside the ACME account under LEGO_DATA_PATH and
# never enters the shared certificates/ directory that other modules mount, the
# runner environment, or the secret store.
set -eu

CA_DIR=/certs/ca
OUT=/certs/certificates
CA_KEY="$CA_DIR/ca.key"
CA_CRT="$CA_DIR/ca.crt"
ISSUER_MARK="$OUT/.issuer"
INTERNAL_CRT="$OUT/anas-internal.crt"
INTERNAL_KEY="$OUT/anas-internal.key"

# The CA outlives everything else: rotating it invalidates the copy every user
# installed on their own devices, so it is deliberately long-lived and is only
# reported on while valid. Missing or invalid CA material must be rebuilt.
# Sixty years puts expiry past the
# lifetime of any deployment that installs it.
CA_DAYS=21900
LEAF_DAYS=730
RENEW_BEFORE_DAYS=90

log() { echo "[ca] $*"; }

ensure_ca() {
  if openssl verify -CAfile "$CA_CRT" "$CA_CRT" >/dev/null 2>&1 \
    && openssl x509 -in "$CA_CRT" -noout -checkend 0 >/dev/null 2>&1; then
    ca_pub=$(openssl x509 -in "$CA_CRT" -noout -pubkey 2>/dev/null) || ca_pub=""
    key_pub=$(openssl pkey -in "$CA_KEY" -pubout 2>/dev/null) || key_pub=""
    if [ -n "$ca_pub" ] && [ "$ca_pub" = "$key_pub" ]; then
      chmod 0600 "$CA_KEY"
      return 0
    fi
  fi
  log "generating internal CA for $BASE_DOMAIN (valid ${CA_DAYS}d)"
  mkdir -p "$CA_DIR"
  chmod 0700 "$CA_DIR"
  openssl req -x509 -newkey rsa:4096 -sha256 -nodes \
    -days "$CA_DAYS" \
    -keyout "$CA_KEY" -out "$CA_CRT" \
    -subj "/CN=ANAS internal CA ${BASE_DOMAIN}/O=ANAS" \
    -addext "basicConstraints=critical,CA:TRUE,pathlen:0" \
    -addext "keyUsage=critical,keyCertSign,cRLSign" 2>/dev/null
  chmod 0600 "$CA_KEY"
  chmod 0644 "$CA_CRT"
}

# leaf_is_current is true when the published certificate still has enough life
# left and still covers the configured domain. A domain change has to force a
# re-issue even when the old certificate has not expired.
leaf_is_current() {
  crt="$1"
  [ -s "$crt" ] || return 1
  openssl x509 -in "$crt" -noout -checkend "${2:-$((RENEW_BEFORE_DAYS * 86400))}" >/dev/null 2>&1 || return 1
  openssl x509 -in "$crt" -noout -ext subjectAltName 2>/dev/null \
    | tr ',' '\n' | sed 's/^[[:space:]]*//' | grep -Fx "DNS:*.${BASE_DOMAIN}" >/dev/null || return 1
  openssl verify -partial_chain -trusted "$crt" "$crt" >/dev/null 2>&1 || return 1
  if [ "$crt" = "$OUT/$LEGO_CERT_NAME" ] \
    && [ "$(cat "$ISSUER_MARK" 2>/dev/null || echo unknown)" = internal ]; then
    openssl verify -CAfile "$CA_CRT" "$crt" >/dev/null 2>&1 || return 1
  fi
  key="${3:-$OUT/$LEGO_KEY_NAME}"
  cert_pub=$(openssl x509 -in "$crt" -noout -pubkey 2>/dev/null) || return 1
  key_pub=$(openssl pkey -in "$key" -pubout 2>/dev/null) || return 1
  [ "$cert_pub" = "$key_pub" ] || return 1
}

issue_internal_leaf() {
  log "issuing internal wildcard for ${BASE_DOMAIN} (valid ${LEAF_DAYS}d)"
  mkdir -p "$OUT"
  tmp=$(mktemp -d)
  # One wildcard covering the apex and every service subdomain, matching the
  # shape an ACME DNS-01 issuance produces so consumers see no difference.
  cat > "$tmp/csr.cnf" <<EOF
[req]
distinguished_name=dn
req_extensions=ext
prompt=no
[dn]
CN=${BASE_DOMAIN}
[ext]
subjectAltName=DNS:${BASE_DOMAIN},DNS:*.${BASE_DOMAIN}
EOF
  openssl req -newkey rsa:2048 -nodes \
    -keyout "$tmp/leaf.key" -out "$tmp/leaf.csr" -config "$tmp/csr.cnf" 2>/dev/null
  openssl x509 -req -in "$tmp/leaf.csr" \
    -CA "$CA_CRT" -CAkey "$CA_KEY" -CAcreateserial \
    -days "$LEAF_DAYS" -sha256 \
    -extfile "$tmp/csr.cnf" -extensions ext \
    -out "$tmp/leaf.crt" 2>/dev/null

  install -m 0644 "$tmp/leaf.crt" "$INTERNAL_CRT"
  install -m 0600 "$tmp/leaf.key" "$INTERNAL_KEY"
  rm -rf "$tmp"
}

publish_internal_leaf() {
  install -m 0644 "$INTERNAL_CRT" "$OUT/$LEGO_CERT_NAME"
  install -m 0600 "$INTERNAL_KEY" "$OUT/$LEGO_KEY_NAME"
  install -m 0644 "$CA_CRT" "$OUT/$LEGO_CA_CERT_NAME"
  echo internal > "$ISSUER_MARK"
}

ensure_internal_leaf() {
  if ! leaf_is_current "$INTERNAL_CRT" "$((RENEW_BEFORE_DAYS * 86400))" "$INTERNAL_KEY" \
    || ! openssl verify -CAfile "$CA_CRT" "$INTERNAL_CRT" >/dev/null 2>&1; then
    issue_internal_leaf
  fi
  chmod 0600 "$INTERNAL_KEY"
}

publish_ca_only() {
  mkdir -p "$OUT"
  install -m 0644 "$CA_CRT" "$OUT/anas-internal-ca.crt"
}

# Samba refuses to start its LDAP server when the TLS private key is readable
# beyond its owner, and a domain controller that will not start takes the whole
# deployment down with it. Every consumer reads the key as root, and lego's own
# ACME output already has this mode, so nothing is lost by enforcing it.
#
# This runs on every invocation rather than only at issuance: a key published
# by an earlier version of this script is still on disk, and a certificate that
# is still current is deliberately never rewritten.
harden_key() {
  [ ! -f "$OUT/$LEGO_KEY_NAME" ] || chmod 0600 "$OUT/$LEGO_KEY_NAME"
}

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

ensure_ca
# The internal CA certificate is always published under a stable name so a
# consumer can trust it even while ACME is serving the traffic: during renewal
# or an ACME outage the serving certificate can fall back to this issuer.
publish_ca_only
publish_trust_bundle
ensure_internal_leaf

case "${1:-bootstrap}" in
  bootstrap)
    # Only fill in a serving certificate when there is not already a usable
    # one. This must never overwrite a live ACME certificate.
    # Public ACME leaves normally live for 90 days. The internal renewal
    # threshold must not replace a still-valid public leaf at every restart.
    if leaf_is_current "$OUT/$LEGO_CERT_NAME" 0; then
      log "existing certificate is current ($(cat "$ISSUER_MARK" 2>/dev/null || echo unknown)); leaving it in place"
    else
      publish_internal_leaf
    fi
    ;;
  renew)
    # Keep a valid public leaf; fall back even when its issuer marker says ACME.
    if ! leaf_is_current "$OUT/$LEGO_CERT_NAME" 0 \
      || { [ "$(cat "$ISSUER_MARK" 2>/dev/null || echo internal)" = internal ] \
        && ! leaf_is_current "$OUT/$LEGO_CERT_NAME"; }; then
      publish_internal_leaf
    fi
    if ! openssl x509 -in "$CA_CRT" -noout -checkend $((365 * 86400)) >/dev/null 2>&1; then
      log "WARNING: internal CA expires within a year; rotating it requires reinstalling the CA on every client device"
    fi
    ;;
  *)
    echo "usage: ca.sh [bootstrap|renew]" >&2
    exit 2
    ;;
esac

harden_key
