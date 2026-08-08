#!/bin/sh
# ACME issuance. Runs only when the deployment expects a publicly trusted
# certificate; ca.sh has already put an internal one in place, so a failure
# here degrades to the internal issuer instead of leaving services with no
# certificate at all.

OUT=/certs/certificates
ISSUER_MARK="$OUT/.issuer"

# lego writes every artifact 0600. The leaf certificate and the issuer chain
# are public material, and the consumers that verify TLS do not run as root --
# the anchor worker is nobody, oauth2_proxy and authentik are their own users.
# Left at 0600 they cannot read the chain, and the failure surfaces far from
# here as "certificate signed by unknown authority" or an LDAPS bind that
# reports Permission denied. Only the private key stays owner-only. ca.sh
# already makes the same distinction for the internal issuer.
cert_mode_for() {
  case "$1" in
  *"$LEGO_KEY_NAME") echo 0600 ;;
  *) echo 0644 ;;
  esac
}

cert_mode() {
  chmod "$(cert_mode_for "$1")" "$1"
}

# run.sh makes this decision at startup, but cron calls this script directly
# and would otherwise attempt ACME every night on a deployment that has
# declared it cannot use it -- failing on an empty provider and writing an
# error to the log that looks like a real problem.
if [ "${ANAS_VIRTUAL_DOMAIN:-false}" = "true" ]; then
  echo "Virtual domain: not attempting ACME; the internal certificate stays in place"
  exit 0
fi

# A missing provider is a configuration error, not something to discover from
# lego's "unrecognized DNS provider" after an account has already been
# registered.
if [ -z "${LEGO_PROVIDER_CODE:-}" ]; then
  echo "no DNS provider is configured for the ACME DNS-01 challenge; set services.lego.env.dns_provider" >&2
  exit 1
fi

mkdir -p /certs/certs1000 "$OUT"

# DNS vendor credentials arrive namespaced to this cask (LEGO_TENCENTCLOUD_*)
# so that a second engine driving the same vendor with a different account
# cannot read them. lego itself reads the unprefixed names, so the translation
# happens here, in the only process that needs them.
for key in ${LEGO_DNS_CRED_KEYS:-}; do
  eval 'value=${LEGO_'"$key"'-}'
  if [ -z "$value" ]; then
    echo "missing DNS credential LEGO_$key for provider $LEGO_PROVIDER_CODE" >&2
    exit 1
  fi
  export "$key=$value"
done

set -- run -a -m="$LEGO_EMAIL" \
  --domains "$BASE_DOMAIN" --domains "*.$BASE_DOMAIN" \
  --path /certs --pem \
  --dns "$LEGO_PROVIDER_CODE" --dns.resolvers "$LEGO_DNS_SERVER" \
  --renew-days 30

# Staging has no rate limits, so it is the right target while a deployment is
# being brought up. Production allows only a handful of identical certificates
# per week and a burnt limit locks the domain out for that long.
if [ -n "${LEGO_ACME_SERVER:-}" ]; then
  echo "Using ACME server $LEGO_ACME_SERVER"
  set -- "$@" --server "$LEGO_ACME_SERVER"
fi

# adopt moves what lego just issued to the names the rest of the deployment
# reads.
#
# lego picks its own output basename and offers no flag to override it, and the
# rule is not simply "the first domain": a run here wrote
# finance.hlong.wang.crt.* while the contract names
# finance.hlong.wang.{crt,key,issuer.crt}. Traefik went on serving the internal
# certificate that still occupied those names, and nothing reported a problem --
# the certificate was issued, stored, and ignored.
#
# So the basename is discovered rather than predicted. Each resource lego writes
# has a .json recording its domains, which is what identifies ours among any
# older ones left in the directory.
adopt() {
  base=""
  for meta in "$OUT"/*.json; do
    [ -f "$meta" ] || continue
    grep -q "\"$BASE_DOMAIN\"" "$meta" || continue
    base="${meta%.json}"
  done
  if [ -z "$base" ]; then
    echo "lego reported success but wrote no certificate resource for $BASE_DOMAIN" >&2
    ls -1 "$OUT" >&2
    return 1
  fi
  for pair in "crt:$LEGO_CERT_NAME" "key:$LEGO_KEY_NAME" "issuer.crt:$LEGO_CA_CERT_NAME"; do
    src="$base.${pair%%:*}"
    dst="$OUT/${pair#*:}"
    if [ ! -s "$src" ]; then
      echo "expected $src from lego, but it is missing or empty" >&2
      return 1
    fi
    if [ "$src" = "$dst" ]; then
      cert_mode "$dst"
    else
      install -m "$(cert_mode_for "$dst")" "$src" "$dst"
    fi
  done
  echo "Published $(basename "$base").* as ${LEGO_CERT_NAME%.crt}.{crt,key,issuer.crt}"
}

publish() {
  echo "Copy certificates(root) to certs1000(user 1000)"
  cp "$OUT"/* /certs/certs1000/ 2>/dev/null
  chown 1000:1000 -R /certs/certs1000
  echo acme > "$ISSUER_MARK"
}

# A self-signed result under the contract name means the internal certificate
# is still in place and the adopted one did not land, which otherwise looks
# exactly like success.
verify_published() {
  subject=$(openssl x509 -in "$OUT/$LEGO_CERT_NAME" -noout -subject 2>/dev/null | sed 's/^subject=//')
  issuer=$(openssl x509 -in "$OUT/$LEGO_CERT_NAME" -noout -issuer 2>/dev/null | sed 's/^issuer=//')
  if [ -n "$subject" ] && [ "$subject" = "$issuer" ]; then
    echo "$LEGO_CERT_NAME is still self-signed after issuance" >&2
    return 1
  fi
  echo "Serving certificate issued by:$issuer"
}

echo "Applying for or renewing the ACME certificate"
if /lego "$@" && adopt && verify_published; then
  publish
  exit 0
fi

echo "ACME issuance failed; the internal certificate stays in place" >&2
exit 1
