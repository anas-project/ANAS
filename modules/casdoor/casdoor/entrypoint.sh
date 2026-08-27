#!/bin/sh
set -eu

# A container owns its process namespace, so Casdoor must not search for and
# kill a same-port "old instance". In the bootstrap child that lookup resolves
# to the child itself and exits it with SIGKILL before init data is committed.
rm -f /usr/bin/lsof

# The long-running process owns the rendered init file as UID 1000. Docker
# preserves /tmp across a process restart, so recreate this runtime-only file
# from scratch before the root entrypoint renders the next bootstrap input.
rm -f /tmp/init_data.json /tmp/anas-casdoor-ready
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
until /opt/anas/bin/casdoor-helper service-healthcheck >/dev/null 2>&1; do
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

# Casdoor reports HTTP health before the long-running process has necessarily
# finished applying init data. Reconcile the recovery credential after that
# process is live, and publish readiness only after the database projection is
# verified. This also makes a plain Docker process restart self-contained.
(
  attempt=0
  until /opt/anas/bin/casdoor-helper service-healthcheck >/dev/null 2>&1; do
    attempt=$((attempt + 1))
    if [ "$attempt" -ge 90 ]; then
      echo "Casdoor long-running process did not become healthy within 90 seconds" >&2
      exit 1
    fi
    sleep 1
  done
  /opt/anas/bin/casdoor-helper set-password built-in \
    "$CASDOOR_LOCAL_ADMIN__BREAK_GLASS_USERNAME" \
    </run/secrets/casdoor-break-glass-password
  touch /tmp/anas-casdoor-ready
) &

exec /opt/anas/bin/casdoor-helper exec-as 1000 1000 "$@"
