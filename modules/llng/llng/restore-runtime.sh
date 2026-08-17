#!/bin/sh

set -eu

seed_root=${ANAS_LLNG_RUNTIME_SEED_ROOT:-/opt/anas/runtime-seed}
etc_llng=${ANAS_LLNG_ETC_DIR:-/etc/lemonldap-ng}
nginx_sites=${ANAS_LLNG_NGINX_SITES_DIR:-/etc/nginx/sites-enabled}
psessions=${ANAS_LLNG_PSESSIONS_DIR:-/var/lib/lemonldap-ng/psessions}
sessions=${ANAS_LLNG_SESSIONS_DIR:-/var/lib/lemonldap-ng/sessions}

restore_runtime_dir() {
  seed=$1
  target=$2
  if [ ! -d "$seed" ]; then
    echo "runtime seed is missing: $seed" >&2
    exit 1
  fi
  if find "$target" -mindepth 1 -print -quit | grep -q .; then
    echo "runtime directory is not empty: $target" >&2
    exit 1
  fi
  cp -a "$seed"/. "$target"/
}

restore_runtime_dir "$seed_root/etc-lemonldap-ng" "$etc_llng"
restore_runtime_dir "$seed_root/etc-nginx-sites-enabled" "$nginx_sites"
install -d -o "${ANAS_LLNG_RUNTIME_UID:-33}" -g "${ANAS_LLNG_RUNTIME_GID:-33}" -m 0770 \
  "$psessions/lock" \
  "$sessions/lock"
