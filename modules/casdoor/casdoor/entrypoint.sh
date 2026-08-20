#!/bin/sh
set -eu

/opt/anas/bin/casdoor-helper render-init \
  /opt/anas/conf/init_data.template.json \
  /run/secrets/casdoor-break-glass-password \
  /tmp/init_data.json

chown 1000:1000 /tmp/init_data.json
mkdir -p /data/anas-dirwatch
chown 1000:1000 /data/anas-dirwatch

# Casdoor initializes LDAP auto-synchronizers before importing init_data.json.
# Run one short bootstrap instance so tables and managed objects exist before
# the long-running process scans LDAP configuration. This is repeated on each
# container start so a changed managed LDAP record is active immediately.
/opt/anas/bin/casdoor-helper exec-as 1000 1000 "$@" &
bootstrap_pid=$!
trap 'kill -TERM "$bootstrap_pid" 2>/dev/null || true' EXIT INT TERM

attempt=0
until /opt/anas/bin/casdoor-helper healthcheck >/dev/null 2>&1; do
  if ! kill -0 "$bootstrap_pid" 2>/dev/null; then
    if wait "$bootstrap_pid"; then
      bootstrap_status=1
    else
      bootstrap_status=$?
    fi
    echo "Casdoor bootstrap exited before becoming healthy" >&2
    exit "$bootstrap_status"
  fi
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 90 ]; then
    echo "Casdoor bootstrap did not become healthy within 90 seconds" >&2
    exit 1
  fi
  sleep 1
done

kill -TERM "$bootstrap_pid" 2>/dev/null || true
wait "$bootstrap_pid" 2>/dev/null || true
trap - EXIT INT TERM

exec /opt/anas/bin/casdoor-helper exec-as 1000 1000 "$@"
