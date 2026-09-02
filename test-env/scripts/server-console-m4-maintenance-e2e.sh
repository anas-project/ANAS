#!/usr/bin/env bash
# REQUIREMENTS: CONSOLE-R-133 CONSOLE-R-155
set -euo pipefail

if [[ $# -ne 5 ]]; then
  echo "usage: $0 ANAS RUNNER_TEST MODULE_ROOT LEGO_HOOK TRAEFIK_HOOK" >&2
  exit 2
fi

anas_bin=$1
runner_test=$2
module_root=$3
lego_hook=$4
traefik_hook=$5

fail() {
  echo "M4 maintenance E2E failed: $*" >&2
  if [[ -n ${log:-} && -f $log ]]; then
    tail -n 160 "$log" >&2 || true
  fi
  exit 1
}

[[ $(id -u) -eq 0 ]] || fail "run through sudo for loop-backed Btrfs setup"
for binary in "$anas_bin" "$runner_test" "$lego_hook" "$traefik_hook"; do
  [[ -x $binary && -f $binary && ! -L $binary ]] || fail "missing executable $binary"
done
[[ -d $module_root && ! -L $module_root ]] || fail "MODULE_ROOT must be an unpacked directory"
[[ -d $(dirname -- "$module_root")/contracts ]] || fail "contracts must be adjacent to MODULE_ROOT"
for command_name in btrfs docker install losetup mkfs.btrfs mount mountpoint python3 sha256sum stat umount; do
  command -v "$command_name" >/dev/null || fail "$command_name is required"
done
docker info >/dev/null 2>&1 || fail "Docker daemon is unavailable"

workdir=$(mktemp -d /tmp/anas-m4.XXXXXX)
image=$workdir/btrfs.img
mount_root=$workdir/btrfs
workspace=$mount_root/workspace
backup_target=$mount_root/backup-target
config=$workdir/config.yml
log=$workdir/e2e.log
loop_device=
deployment_started=no
fixture_root=$workdir/fixture
fixture_modules=$fixture_root/modules

cleanup() {
  set +e
  if [[ $deployment_started == yes ]]; then
    "$anas_bin" stop -w "$workspace" >>"$log" 2>&1
  fi
  docker ps -aq --filter 'name=anasm4_' | xargs -r docker rm -f >>"$log" 2>&1
  docker network ls -q --filter 'name=anasm4_' | xargs -r docker network rm >>"$log" 2>&1
  if mountpoint -q "$mount_root"; then
    umount "$mount_root"
  fi
  if [[ -n $loop_device ]]; then
    losetup -d "$loop_device"
  fi
  case "$workdir" in
    /tmp/anas-m4.*) rm -rf -- "$workdir" ;;
    *) echo "refusing to clean unexpected path: $workdir" >&2 ;;
  esac
}
trap cleanup EXIT HUP INT TERM

mkdir -p "$fixture_root"
cp -a "$module_root" "$fixture_modules"
cp -a "$(dirname -- "$module_root")/contracts" "$fixture_root/contracts"
install -m 0700 "$lego_hook" "$fixture_modules/lego/hook-bin"
install -m 0700 "$traefik_hook" "$fixture_modules/traefik/hook-bin"
python3 - "$fixture_modules/lego/module.yml" "$fixture_modules/traefik/module.yml" <<'PY'
from pathlib import Path
import sys

old = "command:\n      - go\n      - run\n      - ./hook"
new = "command:\n      - ./hook-bin"
for raw in sys.argv[1:]:
    path = Path(raw)
    source = path.read_text(encoding="utf-8")
    if source.count(old) != 1:
        raise SystemExit(f"expected one development hook command in {path}")
    path.write_text(source.replace(old, new), encoding="utf-8")
PY

truncate -s 1536M "$image"
loop_device=$(losetup --find --show "$image")
mkfs.btrfs -q -f "$loop_device"
mkdir -p "$mount_root"
mount -t btrfs -o user_subvol_rm_allowed "$loop_device" "$mount_root"

cat >"$config" <<'YAML'
modules:
  traefik: {}
global:
  chinese_speedup: true
  chinese_build_speedup: true
  base_domain: console-m4.test
  email: admin@console-m4.test
  timezone: Asia/Shanghai
  virtual_domain: true
rollback:
  snapshot:
    backend: btrfs
env:
  CONTAINER_PREFIX: anasm4_
  NETWORK_PREFIX: anasm4_
  TRAEFIK_BASE_PORT: "19446"
YAML

"$anas_bin" init "$workspace" --config "$config" --module-root "$fixture_modules" -y >>"$log" 2>&1 ||
  fail "initialize Btrfs workspace"
"$anas_bin" apply --build --update-lock --module-root "$fixture_modules" -w "$workspace" -y >>"$log" 2>&1 ||
  fail "activate real Docker deployment"
deployment_started=yes
docker ps --filter 'name=anasm4_' --format '{{.ID}}' | grep -q . || fail "real deployment has no running container"

if ! btrfs subvolume show "$workspace/userdata" >/dev/null 2>&1; then
  rmdir "$workspace/userdata" 2>/dev/null || fail "userdata is not an empty directory"
  btrfs subvolume create "$workspace/userdata" >/dev/null
fi
printf 'data-before\n' >"$workspace/data/m4-marker"
printf 'userdata-before\n' >"$workspace/userdata/m4-user-marker"

snapshot_json=$workdir/snapshot.json
"$anas_bin" snapshot create -w "$workspace" --include-userdata --label m4-e2e --json >"$snapshot_json" ||
  fail "create fixture snapshot through the supported non-CAP_SYS_ADMIN subset"
snapshot_id=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["snapshot"]["id"])' "$snapshot_json")
[[ -n $snapshot_id ]] || fail "snapshot response has no ID"

printf 'data-after\n' >"$workspace/data/m4-marker"
printf 'userdata-after\n' >"$workspace/userdata/m4-user-marker"
printf 'must-survive-default-restore\n' >"$workspace/userdata/after-snapshot"

mkdir -p "$backup_target/backup-fixture"
active_deployment=$(sed -n 's/^active_deployment: //p' "$workspace/.anas/state/active.yml" | head -n 1)
cat >"$backup_target/backup-fixture/backup.yml" <<YAML
api_version: anas.dev/backup/v1
backup_id: backup-fixture
mode: snapshot
created_at: "2026-09-03T00:00:00Z"
incremental: false
size_bytes: 0
deployment_id: $active_deployment
channels: [data, metadata, userdata]
complete: true
YAML

ANAS_M4_E2E_WORKSPACE=$workspace \
ANAS_M4_E2E_TARGET=$backup_target \
ANAS_M4_E2E_SNAPSHOT=$snapshot_id \
ANAS_M4_E2E_ANAS=$anas_bin \
  "$runner_test" -test.v -test.count=1 -test.run '^TestMaintenanceRealBtrfsTerminalDescriptorE2E$' >>"$log" 2>&1 ||
  fail "descriptor/CLI dry-run contract"

# R-155 is deliberately separate from the descriptor test above: this is the
# destructive real restore proving that omission of --restore-userdata keeps
# files created after the snapshot.
restore_json=$workdir/restore.json
"$anas_bin" snapshot restore "$snapshot_id" -w "$workspace" -y --json >"$restore_json" ||
  fail "run real default restore"
grep -q '^data-before$' "$workspace/data/m4-marker" || fail "default restore did not rewind data/"
grep -q '^userdata-after$' "$workspace/userdata/m4-user-marker" || fail "default restore rewound userdata/"
[[ -f $workspace/userdata/after-snapshot ]] || fail "default restore deleted post-snapshot userdata"
"$anas_bin" start -w "$workspace" >>"$log" 2>&1 || fail "restored deployment did not start"
docker ps --filter 'name=anasm4_' --format '{{.ID}}' | grep -q . || fail "restored Docker deployment has no running container"

echo "environment=$(. /etc/os-release; printf '%s-%s' "$ID" "$VERSION_ID") $(uname -r) $(uname -m) docker=$(docker version --format '{{.Server.Version}}') btrfs=$(btrfs --version | head -n 1)"
echo "artifact_sha256 anas=$(sha256sum "$anas_bin" | awk '{print $1}') runner_test=$(sha256sum "$runner_test" | awk '{print $1}')"
echo "CONSOLE-R-133 PASS server descriptors round-tripped to argv; real snapshot/backup restore dry-runs and confirmation/parser fixtures made no mutation"
echo "CONSOLE-R-155 PASS real Btrfs snapshot containing userdata restored data while preserving post-snapshot userdata; restored Docker deployment restarted successfully"
