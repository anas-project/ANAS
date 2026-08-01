#!/bin/sh

set -eu

required() {
  eval 'value=${'"$1"'-}'
  if [ -z "$value" ]; then
    echo "missing required environment variable: $1" >&2
    exit 1
  fi
}

required LEGO_CERT_NAME
required LEGO_KEY_NAME

# These values are certificate basenames, not arbitrary paths or YAML. Keeping
# the accepted alphabet narrow prevents path traversal and structured-data
# injection without adding a template engine to the image.
for value in "$LEGO_CERT_NAME" "$LEGO_KEY_NAME"; do
  case "$value" in
    *[!A-Za-z0-9._-]*|.*|*..*)
      echo "certificate names must be simple basenames" >&2
      exit 1
      ;;
  esac
done

umask 077
config_dir=${ANAS_CONFIG_DIR:-/run/anas}
mkdir -p "$config_dir"
cat > "$config_dir/cert.yml.tmp" <<EOF
tls:
  certificates:
    - certFile: /certs/$LEGO_CERT_NAME
      keyFile: /certs/$LEGO_KEY_NAME
      stores:
        - default
EOF
mv "$config_dir/cert.yml.tmp" "$config_dir/cert.yml"

exec "${ANAS_TRAEFIK_BINARY:-traefik}" "$@"
