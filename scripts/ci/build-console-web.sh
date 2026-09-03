#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <output-archive>" >&2
  exit 2
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
output="$1"
case "$output" in
  /*) ;;
  *) output="$(pwd)/$output" ;;
esac

cd "$repo_root"
npm --prefix web run build
mkdir -p "$(dirname "$output")"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
mkdir -p "$work_dir/stage"
cp -R internal/webui/dist "$work_dir/stage/dist"
find "$work_dir/stage/dist" -type d -exec chmod 0755 {} +
find "$work_dir/stage/dist" -type f -exec chmod 0644 {} +
find "$work_dir/stage/dist" -exec touch -t 197001010000 {} +
(
  cd "$work_dir/stage"
  LC_ALL=C find dist -print | LC_ALL=C sort >"$work_dir/files"
)

if tar --version 2>/dev/null | grep -q 'GNU tar'; then
  tar --no-recursion --no-xattrs --owner=0 --group=0 --numeric-owner -C "$work_dir/stage" -cf - -T "$work_dir/files" | gzip -n >"$work_dir/archive.tar.gz"
else
  tar --no-recursion --no-xattrs --uid 0 --gid 0 --uname root --gname root -C "$work_dir/stage" -cf - -T "$work_dir/files" | gzip -n >"$work_dir/archive.tar.gz"
fi
mv "$work_dir/archive.tar.gz" "$output"
