#!/bin/sh
set -eu

marker=/tmp/anas-blueprints.ready
rm -f "$marker"

until ak shell -c "$(cat /opt/anas/bin/blueprints-ready.py)" >/dev/null 2>&1; do
  sleep 5
done

: > "$marker"
