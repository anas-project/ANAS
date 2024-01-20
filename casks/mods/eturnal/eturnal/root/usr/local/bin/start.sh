#!/bin/sh

myself=${0##*/}

info()
{
	echo "$myself: $*"
}

error()
{
	echo >&2 "$myself: $*"
}

cat << TURN_CONF > "$HOME/etc/eturnal.yml"
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
  secret: "$TURN_SECRET"
  strict_expiry: false
  relay_min_port: $TURN_RELAY_MIN_PORT
  relay_max_port: $TURN_RELAY_MAX_PORT
  whitelist_peers:
  - 127.0.0.1
  - ::1
  - 0.0.0.0/0 #TODO - disallow all ipv4
  - ::/0
TURN_CONF

exec eturnalctl foreground
