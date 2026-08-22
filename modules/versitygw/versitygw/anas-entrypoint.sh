#!/bin/sh
set -eu

runtime_uid=${VERSITYGW_RUNTIME_UID:-1000}
runtime_gid=${VERSITYGW_RUNTIME_GID:-1000}

case "$runtime_uid:$runtime_gid" in
  *[!0-9:]*|:*|*:)
    echo "VersityGW runtime UID/GID must be numeric" >&2
    exit 1
    ;;
esac

# Docker creates a missing bind source as root. Limit privileged initialization
# to the mount root; recursively changing an object tree would make startup
# proportional to stored data and could rewrite intentional ownership.
for directory in /data/objects /data/iam; do
  mkdir -p "$directory"
  chown "$runtime_uid:$runtime_gid" "$directory"
  chmod 0700 "$directory"
done

umask 0077
exec su-exec "$runtime_uid:$runtime_gid" /usr/local/bin/docker-entrypoint.sh "$@"
