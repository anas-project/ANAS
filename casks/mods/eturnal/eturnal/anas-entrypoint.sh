#!/bin/sh

set -eu

required() {
  eval 'value=${'"$1"'-}'
  if [ -z "$value" ]; then
    echo "missing required environment variable: $1" >&2
    exit 1
  fi
}

port() {
  required "$1"
  eval 'value=${'"$1"'}'
  case "$value" in
    *[!0-9]*)
      echo "$1 must be an integer" >&2
      exit 1
      ;;
  esac
  if [ "$value" -lt 1 ] || [ "$value" -gt 65535 ]; then
    echo "$1 must be between 1 and 65535" >&2
    exit 1
  fi
}

port TURN_PORT
port TURN_RELAY_MIN_PORT
port TURN_RELAY_MAX_PORT
required TURN_SECRET

if [ "$TURN_RELAY_MIN_PORT" -gt "$TURN_RELAY_MAX_PORT" ]; then
  echo "TURN_RELAY_MIN_PORT must not exceed TURN_RELAY_MAX_PORT" >&2
  exit 1
fi
if printf '%s' "$TURN_SECRET" | LC_ALL=C grep -q '[[:cntrl:]]'; then
  echo "TURN_SECRET must not contain control characters" >&2
  exit 1
fi

# YAML single-quoted scalars escape an apostrophe by doubling it. The generated
# default is hexadecimal, but doing the escaping also keeps explicit secrets
# safe without pulling a YAML template engine into the runtime image.
escaped_secret=$(printf '%s' "$TURN_SECRET" | sed "s/'/''/g")

umask 077
config_dir=${ANAS_CONFIG_DIR:-$ETURNAL_ETC_DIR}
mkdir -p "$config_dir"
cat > "$config_dir/eturnal.yml.tmp" <<EOF
eturnal:
  listen:
    - ip: "::"
      port: $TURN_PORT
      transport: udp
    - ip: "::"
      port: $TURN_PORT
      transport: tcp
  log_dir: stdout
  log_level: info
  secret: '$escaped_secret'
  strict_expiry: false
  relay_min_port: $TURN_RELAY_MIN_PORT
  relay_max_port: $TURN_RELAY_MAX_PORT
  whitelist_peers:
    - "127.0.0.1"
    - "::1"
    - "0.0.0.0/0"
    - "::/0"
EOF
mv "$config_dir/eturnal.yml.tmp" "$config_dir/eturnal.yml"

export ETURNAL_ETC_DIR="$config_dir"
exec "$@"
