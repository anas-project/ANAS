#!/usr/bin/with-contenv bash
set -euo pipefail

base_dir=${1:?home base directory is required}
user=${2:?user name is required}

if [ "$user" != "$(basename "$user")" ] || ! getent passwd "$user" >/dev/null; then
  echo "Invalid or unknown AD user: $user" >&2
  exit 1
fi

home_dir="$base_dir/$user"
echo "User login $user"
if [ ! -d "$home_dir" ]; then
  echo "Create user home dir $user"
  install -d -m 0700 -o "$user" -g "Domain Users" "$home_dir"
fi
