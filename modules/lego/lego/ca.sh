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

# The CA outlives everything else: rotating it invalidates the copy every user
# installed on their own devices, so it is deliberately long-lived and is only
# reported on, never rotated automatically. Sixty years puts expiry past the
# lifetime of any deployment that installs it.
CA_DAYS=21900
LEAF_DAYS=730
RENEW_BEFORE_DAYS=90

log() { echo "[ca] $*"; }

ensure_ca() {
  [ -s "$CA_KEY" ] && [ -s "$CA_CRT" ] && return 0
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
  openssl x509 -in "$crt" -noout -checkend $((RENEW_BEFORE_DAYS * 86400)) >/dev/null 2>&1 || return 1
  openssl x509 -in "$crt" -noout -text 2>/dev/null \
    | grep -q "DNS:\*\.${BASE_DOMAIN}\b" || return 1
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

  install -m 0644 "$tmp/leaf.crt" "$OUT/$LEGO_CERT_NAME"
  install -m 0600 "$tmp/leaf.key" "$OUT/$LEGO_KEY_NAME"
  # Consumers trust whatever signed the serving certificate. Under ACME this
  # file is the public intermediate; here it is our own root. Either way the
  # contract is the same, so no module has to know which mode is active.
  install -m 0644 "$CA_CRT" "$OUT/$LEGO_CA_CERT_NAME"
  echo internal > "$ISSUER_MARK"
  rm -rf "$tmp"
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
  [ -f "$OUT/$LEGO_KEY_NAME" ] || return 0
  chmod 0600 "$OUT/$LEGO_KEY_NAME"
}

ensure_ca
# The internal CA certificate is always published under a stable name so a
# consumer can trust it even while ACME is serving the traffic: during renewal
# or an ACME outage the serving certificate can fall back to this issuer.
publish_ca_only

case "${1:-bootstrap}" in
  bootstrap)
    # Only fill in a serving certificate when there is not already a usable
    # one. This must never overwrite a live ACME certificate.
    if leaf_is_current "$OUT/$LEGO_CERT_NAME"; then
      log "existing certificate is current ($(cat "$ISSUER_MARK" 2>/dev/null || echo unknown)); leaving it in place"
    else
      issue_internal_leaf
    fi
    ;;
  renew)
    # Called from cron. Only re-issues what this CA owns; an ACME-issued
    # certificate is lego's business, not ours.
    if [ "$(cat "$ISSUER_MARK" 2>/dev/null || echo internal)" = internal ] \
      && ! leaf_is_current "$OUT/$LEGO_CERT_NAME"; then
      issue_internal_leaf
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
