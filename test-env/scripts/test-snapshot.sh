#!/usr/bin/env sh
# End-to-end Btrfs data-snapshot tests. Requires a workspace on Btrfs.
#
# S1 `anas init` creates data/ as a subvolume when the workspace is on Btrfs.
# S2 An apply that changes configuration takes a data snapshot beforehand.
# S3 The snapshot lands in <workspace>/snapshots, not inside .anas.
# S4 `rollback --restore-data` reverts the data to that snapshot: everything
#    written after it is gone, everything from before it is back.
#
# S4 is the counterpart to R1 in test-rollback.sh. Together they pin down the
# distinction the two operations exist for: a plain rollback keeps the data, a
# data restore rewinds it. Getting either one backwards loses user data.
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

ws=${ANAS_SNAPSHOT_WORKSPACE:-/data/anas-snapshot-test/ws}
config=${ANAS_SNAPSHOT_CONFIG:-$CONFIG_DIR/snapshot.yml}
prefix=${ANAS_SNAPSHOT_PREFIX:-anassn_}
log="$REPORT_DIR/snapshot.log"
failures=0

# Skipping has to be loud. A silent pass on a machine without Btrfs would make
# the suite green everywhere and only fail where it actually matters.
fstype=$(df -T "$(dirname -- "$ws")" 2>/dev/null | awk 'NR==2 {print $2}')
if [ "$fstype" != "btrfs" ]; then
  echo "SKIP test-snapshot.sh: $(dirname -- "$ws") is $fstype, not btrfs" >&2
  echo "SKIP: snapshot tests require a Btrfs workspace (set ANAS_SNAPSHOT_WORKSPACE)"
  exit 0
fi

cleanup() {
  run_anas stop -w "$ws" >>"$log" 2>&1 || true
}
trap cleanup EXIT INT TERM

fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}

active_deployment() {
  sed -n 's/^active_deployment: //p' "$ws/.anas/state/active.yml"
}

# `btrfs subvolume show` needs CAP_SYS_ADMIN for its tree-search ioctl, so it
# fails for an ordinary user even where creating and snapshotting subvolumes
# succeeds. A subvolume root is identified without privilege by its inode
# number, which is always 256 — the same test the runner itself uses.
is_subvolume() {
  [ "$(stat -c %i "$1" 2>/dev/null)" = "256" ]
}

{
  echo "== S1: init creates data/ as a Btrfs subvolume =="
  make_workspace "$ws" "$config"
  if ! is_subvolume "$ws/data"; then
    fail "$ws/data is not a Btrfs subvolume; snapshots cannot be taken from it"
  fi

  echo "== baseline apply =="
  run_anas apply --build -w "$ws" --update-lock
  first=$(active_deployment)
  echo "baseline deployment: $first"

  # Written before the snapshot: must come back after the restore.
  echo "before-snapshot" >"$ws/data/marker-before"

  echo "== S2: a second apply snapshots the data first =="
  run_anas config set core.timezone Europe/Berlin -w "$ws"
  run_anas apply --build -w "$ws" --update-lock
  second=$(active_deployment)
  echo "second deployment: $second"

  snapshot_id=$(sed -n 's/^snapshot_id: //p' "$ws/.anas/state/deployments/$second.yml")
  if [ -z "$snapshot_id" ]; then
    fail "apply did not record a data snapshot for $second"
  else
    echo "snapshot: $snapshot_id"
  fi

  echo "== S3: the snapshot lives beside .anas, not inside it =="
  if [ ! -d "$ws/snapshots/$snapshot_id" ]; then
    fail "snapshot $snapshot_id is not under $ws/snapshots"
  fi
  if [ -d "$ws/.anas/snapshots" ]; then
    fail "$ws/.anas/snapshots exists; snapshots must be a sibling of .anas so a data restore cannot take the runtime state with it"
  fi
  if ! is_subvolume "$ws/snapshots/$snapshot_id/data"; then
    fail "snapshot data at $ws/snapshots/$snapshot_id/data is not a Btrfs subvolume"
  fi
  # Written after the snapshot: must be gone after the restore.
  echo "after-snapshot" >"$ws/data/marker-after"

  echo "== S4: rollback --restore-data rewinds the data =="
  run_anas rollback "$first" -w "$ws" --restore-data --yes

  if [ "$(active_deployment)" != "$first" ]; then
    fail "restore left $(active_deployment) active, expected $first"
  fi
  if [ ! -f "$ws/data/marker-before" ]; then
    fail "data written before the snapshot did not come back"
  fi
  if [ -f "$ws/data/marker-after" ]; then
    fail "data written after the snapshot survived the restore"
  fi
  echo "snapshot checks complete"
} >"$log" 2>&1 || {
  status=$?
  cat "$log"
  echo "snapshot test aborted with status $status" >&2
  exit "$status"
}

cat "$log"

if [ "$failures" -ne 0 ]; then
  echo "$failures snapshot assertion(s) failed" >&2
  exit 1
fi
echo "snapshot tests passed"
