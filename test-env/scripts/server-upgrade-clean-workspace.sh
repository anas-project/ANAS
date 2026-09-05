#!/usr/bin/env bash
# Remove one explicitly scoped upgrade workspace, deleting ANAS-managed Btrfs
# snapshots through the CLI before the ordinary directory tree.
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <anas> <workspace>" >&2
  exit 2
fi
anas=$1
workspace=$2
[[ -x "$anas" && -f "$anas" && ! -L "$anas" ]] || { echo "invalid anas binary: $anas" >&2; exit 2; }
[[ "$workspace" = /* ]] || { echo "workspace must be absolute" >&2; exit 2; }
case "$workspace" in
  /tmp/anas-upgrade-*|/srv/anas-upgrade-*|/data/anas-upgrade-*) ;;
  *) echo "workspace is not in an anas-upgrade test scope: $workspace" >&2; exit 2 ;;
esac
[[ -d "$workspace" && ! -L "$workspace" ]] || { echo "upgrade workspace is not a directory: $workspace" >&2; exit 2; }

if [[ -d "$workspace/snapshots" && ! -L "$workspace/snapshots" ]]; then
  shopt -s nullglob
  for snapshot in "$workspace"/snapshots/*; do
    [[ -d "$snapshot" && ! -L "$snapshot" ]] || {
      echo "unexpected upgrade snapshot entry: $snapshot" >&2
      exit 1
    }
    snapshot_id=${snapshot##*/}
    [[ "$snapshot_id" =~ ^[A-Za-z0-9._-]+$ ]] || {
      echo "invalid upgrade snapshot identity: $snapshot_id" >&2
      exit 1
    }
    "$anas" snapshot delete "$snapshot_id" -w "$workspace" --force -y --json >/dev/null
  done
fi

rm -rf -- "$workspace"
[[ ! -e "$workspace" ]] || { echo "upgrade workspace cleanup was incomplete: $workspace" >&2; exit 1; }
