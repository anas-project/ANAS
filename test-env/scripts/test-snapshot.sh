#!/usr/bin/env sh
# End-to-end Btrfs snapshot tests. Requires a workspace on Btrfs.
#
# S1  `anas init` creates data/ as a subvolume when the workspace is on Btrfs.
# S2  An apply that changes configuration takes a data snapshot beforehand.
# S3  The snapshot lands in <workspace>/snapshots, not inside .anas.
# S4  A snapshot is self-sufficient: config, lock, secrets, the deployment
#     state, a full artifact copy and the data are all physically present.
# S5  `snapshot create` / `list` / `show` / `path` agree with each other.
# S6  `pin` protects a snapshot from `delete` and from `prune`.
# S7  `prune --dry-run` reports without deleting; `--keep` bounds the rest.
# S8  `verify` catches a data subvolume deleted out from under the metadata.
# S9  `snapshot restore` rewinds data, config and the active deployment, and
#     leaves a pre_restore snapshot so the restore itself is undoable.
# S10 `restore` refuses to infer the workspace from ANAS_WORKSPACE or cwd.
# S11 `rollback` no longer has --restore-data, and does not touch data.
#
# S9 and S11 together pin down the distinction the two operations exist for: a
# rollback keeps the data, a restore rewinds it. Getting either one backwards
# loses user data.
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

ws=${ANAS_SNAPSHOT_WORKSPACE:-/data/anas-snapshot-test/ws}
config=${ANAS_SNAPSHOT_CONFIG:-$CONFIG_DIR/snapshot.yml}
log="$REPORT_DIR/snapshot.log"
failures=0

# Skipping has to be loud. A silent pass on a machine without Btrfs would make
# the suite green everywhere and only fail where it actually matters.
#
# The parent has to exist before the filesystem can be identified, or a first
# run on a clean machine reports "not btrfs" and skips the entire suite on
# exactly the host it was written for.
mkdir -p "$(dirname -- "$ws")"
fstype=$(df -T "$(dirname -- "$ws")" 2>/dev/null | awk 'NR==2 {print $2}')
if [ "$fstype" != "btrfs" ]; then
  echo "SKIP test-snapshot.sh: $(dirname -- "$ws") is $fstype, not btrfs" >&2
  echo "SKIP: snapshot tests require a Btrfs workspace (set ANAS_SNAPSHOT_WORKSPACE)"
  exit 0
fi

# The snapshot contract is partly an exit-code contract, and `go run` collapses
# every non-zero program status to 1, so this suite drives a built binary.
anas_bin="$ROOT_DIR/.anas-test/bin/anas"
mkdir -p "$(dirname -- "$anas_bin")"
go build -o "$anas_bin" ./cmd/anas

anas() {
  "$anas_bin" "$@"
}

cleanup() {
  anas stop -w "$ws" >>"$log" 2>&1 || true
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

# Snapshot ids are only known at runtime, so every assertion reads them back
# out of the JSON contract rather than guessing at directory names.
snapshot_ids() {
  anas snapshot list -w "$ws" --json 2>/dev/null |
    sed -n 's/^ *"id": "\(.*\)",$/\1/p'
}

snapshot_field() {
  anas snapshot show "$1" -w "$ws" --json 2>/dev/null |
    sed -n "s/^ *\"$2\": \"\{0,1\}\([^\",]*\)\"\{0,1\},\{0,1\}$/\1/p" | head -1
}

# The exit-code table in docs/contracts/README.md is part of the contract, so
# the tests assert on the number rather than merely on failure.
expect_exit() {
  want=$1
  shift
  set +e
  "$@" >/dev/null 2>&1
  got=$?
  set -e
  [ "$got" = "$want" ] || fail "expected exit $want from '$*', got $got"
}

# For commands that predate the contract and only promise "non-zero".
expect_failure() {
  set +e
  "$@" >/dev/null 2>&1
  got=$?
  set -e
  [ "$got" != "0" ] || fail "expected '$*' to fail"
}

# Creating and snapshotting subvolumes work unprivileged; deleting one does
# not. BTRFS_IOC_SNAP_DESTROY needs CAP_SYS_ADMIN unless the filesystem was
# mounted with user_subvol_rm_allowed, and a read-only snapshot cannot be
# removed with rm either. So whether space can be reclaimed is a property of
# the host, and the assertions below branch on it instead of assuming.
probe="$(dirname -- "$ws")/.subvol-delete-probe.$$"
rm -rf "$probe" 2>/dev/null || true
if btrfs subvolume create "$probe" >/dev/null 2>&1 &&
   btrfs subvolume delete "$probe" >/dev/null 2>&1; then
  reclaim_ok=yes
else
  reclaim_ok=no
  rmdir "$probe" 2>/dev/null || true
  echo "note: $(dirname -- "$ws") cannot delete subvolumes unprivileged;" >&2
  echo "      testing the diagnostic instead of the reclamation." >&2
  echo "      mount -o remount,user_subvol_rm_allowed to exercise the real path." >&2
fi

{
  echo "== S1: init creates data/ as a Btrfs subvolume =="
  make_workspace "$ws" "$config"
  if ! is_subvolume "$ws/data"; then
    fail "$ws/data is not a Btrfs subvolume; snapshots cannot be taken from it"
  fi

  echo "== baseline apply =="
  anas apply --build -w "$ws" --update-lock
  first=$(active_deployment)
  echo "baseline deployment: $first"

  echo "== prerequisite: the deployment carries the config it was built from =="
  if [ ! -f "$ws/.anas/deployments/$first/config.source.yml" ]; then
    fail "deployment $first has no config.source.yml; a snapshot of it could not be restored alone"
  fi
  mode=$(stat -c %a "$ws/.anas/deployments/$first/config.source.yml" 2>/dev/null || echo "")
  [ "$mode" = "400" ] || fail "config.source.yml is mode $mode, want 400 (0600 written, then sealed)"

  echo "== prerequisite: the artifact is sealed read-only =="
  writable=$(find "$ws/.anas/deployments/$first" -type f -perm -u=w | head -5)
  if [ -n "$writable" ]; then
    fail "sealed deployment still has writable files: $writable"
  fi
  envmode=$(stat -c %a "$ws/.anas/deployments/$first/casks/traefik/.env" 2>/dev/null || echo "")
  [ "$envmode" = "400" ] || fail ".env is mode $envmode, want 400 — sealing must not widen access"

  # Written before the snapshot: must come back after the restore.
  echo "before-snapshot" >"$ws/data/marker-before"

  echo "== S2: a second apply snapshots the data first =="
  anas config set core.timezone Europe/Berlin -w "$ws"
  anas apply --build -w "$ws" --update-lock
  second=$(active_deployment)
  echo "second deployment: $second"

  auto=$(snapshot_ids | head -1)
  if [ -z "$auto" ]; then
    fail "apply did not create a data snapshot before switching to $second"
  else
    echo "automatic snapshot: $auto"
  fi
  reason=$(snapshot_field "$auto" reason)
  [ "$reason" = "pre_apply" ] || fail "automatic snapshot reason is '$reason', want pre_apply"
  # The snapshot must belong to the deployment it captured, not the new one.
  captured=$(snapshot_field "$auto" deployment_id)
  [ "$captured" = "$first" ] || fail "snapshot records deployment '$captured', want $first"
  # A snapshot is a point in time; nothing points at it from deployment state.
  if grep -q "^snapshot_id:" "$ws/.anas/state/deployments/$second.yml" 2>/dev/null; then
    fail "deployment state still records a snapshot_id; a snapshot is not bound to one transition"
  fi

  echo "== S3: the snapshot lives beside .anas, not inside it =="
  if [ ! -d "$ws/snapshots/$auto" ]; then
    fail "snapshot $auto is not under $ws/snapshots"
  fi
  if [ -d "$ws/.anas/snapshots" ]; then
    fail "$ws/.anas/snapshots exists; snapshots must be a sibling of .anas so a data restore cannot take the runtime state with it"
  fi
  if ! is_subvolume "$ws/snapshots/$auto/data"; then
    fail "snapshot data at $ws/snapshots/$auto/data is not a Btrfs subvolume"
  fi
  if is_subvolume "$ws/snapshots"; then
    fail "$ws/snapshots is a subvolume; only the source of a snapshot has to be one, and making the container one cuts off the hard-link tier"
  fi

  echo "== S4: the snapshot is self-sufficient =="
  for f in meta/config.yml meta/config.lock.yml meta/secrets.generated.yml \
           meta/deployment-state.yml deployment/deployment.yml \
           deployment/config.source.yml snapshot.yml; do
    [ -f "$ws/snapshots/$auto/$f" ] || fail "snapshot $auto is missing $f"
  done
  # active.yml must not be carried: its previous_deployments would name
  # deployments the snapshot does not contain.
  if [ -f "$ws/snapshots/$auto/meta/active.yml" ]; then
    fail "the snapshot copied active.yml, whose previous_deployments would be dangling"
  fi
  # The captured config must be the deployment's, not the edited one on disk.
  if ! grep -q "Asia/Shanghai" "$ws/snapshots/$auto/meta/config.yml"; then
    fail "snapshot captured the edited config instead of the one $first was built from"
  fi
  snapmode=$(stat -c %a "$ws/snapshots/$auto/meta/config.yml")
  [ "$snapmode" = "600" ] || fail "meta/config.yml is mode $snapmode, want 600 — it holds plaintext secrets"

  # Written after the automatic snapshot: must be gone after a restore of it.
  echo "after-snapshot" >"$ws/data/marker-after"

  echo "== S5: create, list, show and path agree =="
  anas snapshot create -w "$ws" --label "manual checkpoint" --json >"$REPORT_DIR/snapshot-create.json"
  manual=$(sed -n 's/^ *"id": "\(.*\)",$/\1/p' "$REPORT_DIR/snapshot-create.json" | head -1)
  [ -n "$manual" ] || fail "snapshot create emitted no id"
  echo "manual snapshot: $manual"
  [ "$(snapshot_field "$manual" kind)" = "manual" ] || fail "explicit snapshot is not kind=manual"
  path=$(anas snapshot path "$manual" -w "$ws" 2>/dev/null | tail -1)
  [ "$path" = "$ws/snapshots/$manual/data" ] || fail "snapshot path printed '$path'"
  # A Btrfs snapshot is a readable directory in its own right: recovering one
  # file must not require rewinding the whole workspace.
  [ -f "$path/marker-after" ] || fail "the manual snapshot's data is not readable at $path"

  echo "== S6: pinning protects against delete and prune =="
  anas snapshot pin "$manual" -w "$ws" --json >/dev/null
  [ "$(snapshot_field "$manual" pinned)" = "true" ] || fail "pin did not stick"
  expect_exit 4 anas snapshot delete "$manual" -w "$ws" -y --json
  anas snapshot unpin "$manual" -w "$ws" --json >/dev/null
  [ "$(snapshot_field "$manual" pinned)" = "false" ] || fail "unpin did not stick"
  anas snapshot pin "$manual" -w "$ws" --json >/dev/null

  echo "== S7: prune reports before it deletes =="
  before=$(snapshot_ids | wc -l)
  anas snapshot prune -w "$ws" --dry-run --keep 0 --json >"$REPORT_DIR/snapshot-prune.json"
  grep -q '"would_delete"' "$REPORT_DIR/snapshot-prune.json" || fail "prune --dry-run did not report would_delete"
  after=$(snapshot_ids | wc -l)
  [ "$before" = "$after" ] || fail "prune --dry-run deleted something ($before -> $after)"
  # A non-interactive caller with no -y must fail immediately, not block.
  expect_exit 3 anas snapshot prune -w "$ws" --keep 0 --json </dev/null

  echo "== S8: verify catches a data subvolume gone from under the metadata =="
  anas snapshot verify -w "$ws" --json >"$REPORT_DIR/snapshot-verify.json"
  grep -q '"ok": true' "$REPORT_DIR/snapshot-verify.json" || fail "verify reported problems on a healthy workspace"
  scratch=$(anas snapshot create -w "$ws" --json 2>/dev/null | sed -n 's/^ *"id": "\(.*\)",$/\1/p' | head -1)
  # Moved rather than deleted: renaming a subvolume is an ordinary directory
  # rename, so this reproduces "metadata present, data not where it says" on
  # hosts where deleting a subvolume needs privilege. That is the condition
  # verify exists to surface — otherwise it stays invisible until a restore.
  mv "$ws/snapshots/$scratch/data" "$ws/snapshots/$scratch/data.moved-aside"
  anas snapshot verify "$scratch" -w "$ws" --json >"$REPORT_DIR/snapshot-verify-bad.json" 2>/dev/null || true
  grep -q 'subvolume_missing' "$REPORT_DIR/snapshot-verify-bad.json" || fail "verify did not report the missing subvolume"
  expect_exit 1 anas snapshot verify "$scratch" -w "$ws" --json
  mv "$ws/snapshots/$scratch/data.moved-aside" "$ws/snapshots/$scratch/data"

  echo "== S8b: reclaiming space =="
  if [ "$reclaim_ok" = "yes" ]; then
    anas snapshot delete "$scratch" -w "$ws" -y --json >/dev/null
    snapshot_ids | grep -q "^$scratch\$" && fail "snapshot $scratch survived delete"
    # Only auto, unpinned snapshots are collected, and the pinned manual one
    # must be left alone even at --keep 0.
    anas snapshot prune -w "$ws" --keep 0 -y --json >"$REPORT_DIR/snapshot-prune-run.json"
    snapshot_ids | grep -q "^$manual\$" || fail "prune reclaimed the pinned snapshot $manual"
  else
    # A host that cannot reclaim must still say so precisely rather than
    # surfacing a bare EPERM from btrfs.
    expect_exit 4 anas snapshot delete "$scratch" -w "$ws" -y --json
    anas snapshot delete "$scratch" -w "$ws" -y --json >"$REPORT_DIR/snapshot-delete-denied.json" 2>/dev/null || true
    grep -q 'subvolume_delete_denied' "$REPORT_DIR/snapshot-delete-denied.json" ||
      fail "delete on a filesystem without user_subvol_rm_allowed did not report subvolume_delete_denied"
  fi

  echo "== S10: restore refuses an inferred workspace =="
  expect_exit 2 env ANAS_WORKSPACE="$ws" "$anas_bin" snapshot restore "$auto" --json

  echo "== S11: rollback no longer restores data =="
  expect_failure anas rollback "$first" -w "$ws" --restore-data --yes
  anas rollback "$first" -w "$ws" --allow-risky
  if [ "$(active_deployment)" != "$first" ]; then
    fail "rollback left $(active_deployment) active, expected $first"
  fi
  if [ ! -f "$ws/data/marker-after" ]; then
    fail "rollback rewound the data; it must only switch the artifact"
  fi

  echo "== S9: restore rewinds data, config and the active deployment =="
  anas snapshot restore "$auto" -w "$ws" --dry-run --json >"$REPORT_DIR/snapshot-restore-dry.json"
  grep -q '"would_replace"' "$REPORT_DIR/snapshot-restore-dry.json" || fail "restore --dry-run did not list what it would replace"
  [ -f "$ws/data/marker-after" ] || fail "restore --dry-run modified the data"

  anas snapshot restore "$auto" -w "$ws" -y --json >"$REPORT_DIR/snapshot-restore.json"
  if [ ! -f "$ws/data/marker-before" ]; then
    fail "data written before the snapshot did not come back"
  fi
  if [ -f "$ws/data/marker-after" ]; then
    fail "data written after the snapshot survived the restore"
  fi
  if ! is_subvolume "$ws/data"; then
    fail "restored data is not a Btrfs subvolume; the next snapshot would be impossible"
  fi
  if [ "$(active_deployment)" != "$first" ]; then
    fail "restore left $(active_deployment) active, expected $first"
  fi
  # The config must come back with the data: restoring an older secret store
  # against newer data would leave keys that no longer match.
  grep -q "Asia/Shanghai" "$ws/config.yml" || fail "restore did not put back the snapshot's config"
  # The restore itself has to be undoable.
  pre=$(sed -n 's/^ *"pre_restore_snapshot": "\(.*\)",\{0,1\}$/\1/p' "$REPORT_DIR/snapshot-restore.json" | head -1)
  [ -n "$pre" ] || fail "restore did not record a pre_restore snapshot"
  [ "$(snapshot_field "$pre" reason)" = "pre_restore" ] || fail "the pre-restore snapshot has the wrong reason"
  [ -f "$ws/snapshots/$pre/data/marker-after" ] || fail "the pre-restore snapshot did not capture the data it discarded"

  echo "== the restored workspace still starts =="
  anas start -w "$ws"

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
