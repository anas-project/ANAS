#!/usr/bin/env sh
# End-to-end backup tests.
#
# B1  `capabilities` picks the right mode for each kind of destination, and
#     reports insufficient_privilege for the send modes rather than offering
#     them and failing after the containers are already down.
# B2  `plan` puts start_containers before the transfer.
# B3  Container data written as root makes `copy` unavailable, and it says so
#     before stopping anything.
# B4  `create --mode copy` round-trips: list, verify, restore into a second
#     workspace, and that workspace starts.
# B5  `verify` catches a truncated backup and a missing metadata channel.
# B6  A failed backup leaves the services running.
# B7  A backup killed mid-run is compensated by the next command.
# B8  The send modes move data through two channels. Needs root; skips loudly.
#
# B6 and B7 are the ones that matter most. Every other failure here is fixed by
# running the backup again; services left stopped is an outage that lasts until
# a human notices, and it is the single failure mode this feature is not
# allowed to have.
#
# B3 exists because the contract's mode table is wrong about copy. It lists
# only "destination writable", but every module that runs as root leaves data an
# ordinary user cannot read, and a copy that skipped what it cannot read would
# publish a backup with holes in it. The Btrfs modes are unaffected — none of
# them reads a file.
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

ws=${ANAS_BACKUP_WORKSPACE:-/data/anas-backup-test/ws}
restore_ws=${ANAS_BACKUP_RESTORE_WORKSPACE:-/data/anas-backup-test/restored}
config=${ANAS_BACKUP_CONFIG:-$CONFIG_DIR/backup.yml}
log="$REPORT_DIR/backup.log"
failures=0

root=$(dirname -- "$ws")
mkdir -p "$root"

# Skipping has to be loud. A silent pass on a machine without Btrfs makes the
# suite green everywhere and only fail where it actually matters.
fstype=$(df -T "$root" 2>/dev/null | awk 'NR==2 {print $2}')
if [ "$fstype" != "btrfs" ]; then
  echo "SKIP test-backup.sh: $root is $fstype, not btrfs" >&2
  echo "SKIP: backup tests require a Btrfs workspace (set ANAS_BACKUP_WORKSPACE)"
  exit 0
fi

# The contract is partly an exit-code contract, and `go run` collapses every
# non-zero program status to 1, so this suite drives a built binary.
anas_bin="$ROOT_DIR/.anas-test/bin/anas"
mkdir -p "$(dirname -- "$anas_bin")"
go build -o "$anas_bin" ./cmd/anas

anas() {
  "$anas_bin" "$@"
}

cleanup() {
  anas stop -w "$ws" >>"$log" 2>&1 || true
  anas stop -w "$restore_ws" >>"$log" 2>&1 || true
}
trap cleanup EXIT INT TERM

fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}

expect_exit() {
  want=$1
  shift
  set +e
  "$@" >/dev/null 2>&1
  got=$?
  set -e
  [ "$got" = "$want" ] || fail "expected exit $want from '$*', got $got"
}

# `btrfs subvolume show` needs CAP_SYS_ADMIN, so a subvolume root is identified
# without privilege by its inode number, which is always 256.
is_subvolume() {
  [ "$(stat -c %i "$1" 2>/dev/null)" = "256" ]
}

json_field() {
  sed -n "s/^ *\"$2\": \"\{0,1\}\([^\",]*\)\"\{0,1\},\{0,1\}$/\1/p" "$1" | head -1
}

# Reads the availability of one mode out of a capabilities document. The modes
# array is a list of objects, so the reason is whatever follows the id.
mode_state() {
  # $1 = json file, $2 = mode id
  awk -v want="\"$2\"" '
    $0 ~ /"id":/ { id = $2; sub(/,$/, "", id) }
    id == want && /"available":/ { avail = $2; sub(/,$/, "", avail) }
    id == want && /"reason":/ { reason = $2; gsub(/[",]/, "", reason) }
    id == want && /^    }/ { print avail " " reason; id = "" }
  ' "$1"
}

# Whether this run can exercise btrfs send at all. Reported loudly either way:
# a silent skip makes the suite green on the machine where it matters least.
if [ "$(id -u)" = "0" ]; then
  privileged=yes
else
  privileged=no
fi

# Modules run as root, so the data they write is not readable by the user running
# the tests. Copy mode legitimately refuses that (B3), which would leave the
# copy round-trip untestable unprivileged — so the setup takes ownership of the
# data once B3 has confirmed the refusal. That is not a workaround for a bug;
# it stands in for the deployments whose data the invoking user does own.
if sudo -n true 2>/dev/null; then
  can_chown=yes
else
  can_chown=no
fi

# A second destination that is not Btrfs, for the mode-selection assertions.
ext4_dest=${ANAS_BACKUP_EXT4_DEST:-/tmp/anas-backup-dest}
ext4_fstype=$(df -T /tmp 2>/dev/null | awk 'NR==2 {print $2}')

{
  echo "== setup =="
  make_workspace "$ws" "$config"
  anas apply --build -w "$ws" --update-lock
  echo "before-backup" >"$ws/data/marker-before"

  btrfs_dest="$root/dest-btrfs"
  mkdir -p "$btrfs_dest"
  rm -rf "$ext4_dest"
  mkdir -p "$ext4_dest"

  echo "== B1: capabilities picks a mode per destination =="
  anas backup capabilities -w "$ws" --json >"$REPORT_DIR/backup-caps-none.json"
  grep -q '"dest_not_specified"' "$REPORT_DIR/backup-caps-none.json" ||
    fail "capabilities without --to must report dest_not_specified"
  # data/ must be recognised as a subvolume, or every Btrfs mode is off the
  # table for the wrong reason.
  grep -q '"data_is_subvolume": true' "$REPORT_DIR/backup-caps-none.json" ||
    fail "capabilities does not see $ws/data as a subvolume"
  # A subvolume is not a mount point. Deciding that with st_dev would call
  # every subvolume one, which would rule out every mode.
  grep -q '"data_is_mountpoint": false' "$REPORT_DIR/backup-caps-none.json" ||
    fail "capabilities calls the data subvolume a mount point"

  anas backup capabilities -w "$ws" --to "$btrfs_dest" --json >"$REPORT_DIR/backup-caps-btrfs.json"
  echo "same-filesystem destination:"
  cat "$REPORT_DIR/backup-caps-btrfs.json"
  # The whole point of using the filesystem UUID rather than st_dev or f_fsid:
  # a destination on the same Btrfs must be recognised as such even though the
  # data subvolume reports a different st_dev and a different f_fsid.
  case "$(mode_state "$REPORT_DIR/backup-caps-btrfs.json" snapshot)" in
    true*) : ;;
    *) fail "snapshot mode unavailable for a destination on the same Btrfs: $(mode_state "$REPORT_DIR/backup-caps-btrfs.json" snapshot)" ;;
  esac

  if [ "$ext4_fstype" != "btrfs" ] && [ "$ext4_fstype" != "" ]; then
    anas backup capabilities -w "$ws" --to "$ext4_dest" --json >"$REPORT_DIR/backup-caps-ext4.json"
    case "$(mode_state "$REPORT_DIR/backup-caps-ext4.json" snapshot)" in
      *dest_not_btrfs*) : ;;
      *) fail "a $ext4_fstype destination should rule out snapshot mode with dest_not_btrfs, got $(mode_state "$REPORT_DIR/backup-caps-ext4.json" snapshot)" ;;
    esac
  else
    echo "SKIP: no non-btrfs destination available (/tmp is $ext4_fstype)"
  fi

  echo "== B1b: the send modes report privilege rather than failing later =="
  send_state=$(mode_state "$REPORT_DIR/backup-caps-btrfs.json" send)
  if [ "$privileged" = "no" ]; then
    case "$send_state" in
      *insufficient_privilege*) : ;;
      *) fail "unprivileged send mode should report insufficient_privilege, got '$send_state'" ;;
    esac
    # And attempting it anyway must fail on the precondition, not halfway
    # through with the containers already stopped.
    expect_exit 4 anas backup create -w "$ws" --to "$btrfs_dest" --mode send -y --json
  else
    case "$send_state" in
      true*) : ;;
      *) fail "running as root, send mode should be available, got '$send_state'" ;;
    esac
  fi

  echo "== B3: root-owned container data makes copy unavailable, and says so =="
  # The modules have run, so data/ holds files this user cannot read. Copy is the
  # only mode that reads files, so it is the only one that has to refuse — and
  # it has to refuse in `capabilities`, before anything is stopped.
  if [ "$privileged" = "no" ]; then
    case "$(mode_state "$REPORT_DIR/backup-caps-btrfs.json" copy)" in
      *insufficient_privilege*) : ;;
      *) fail "copy mode must report insufficient_privilege when data/ holds root-owned files, got '$(mode_state "$REPORT_DIR/backup-caps-btrfs.json" copy)'" ;;
    esac
    grep -q '"data_fully_readable": false' "$REPORT_DIR/backup-caps-btrfs.json" ||
      fail "capabilities did not notice that part of data/ is unreadable"
    # And the refusal must be a precondition, not a failure part way through.
    before_refuse=$(docker ps --filter "name=anasbk_" -q | wc -l)
    expect_exit 4 anas backup create -w "$ws" --to "$ext4_dest" --mode copy -y --json
    after_refuse=$(docker ps --filter "name=anasbk_" -q | wc -l)
    [ "$before_refuse" = "$after_refuse" ] ||
      fail "a refused backup stopped containers ($before_refuse -> $after_refuse)"
    # The Btrfs modes read through the filesystem, not through directory
    # permissions, so they must be unaffected by the same data.
    case "$(mode_state "$REPORT_DIR/backup-caps-btrfs.json" snapshot)" in
      true*) : ;;
      *) fail "root-owned data must not affect snapshot mode" ;;
    esac
  fi

  # From here on the copy path needs data this user can read, standing in for a
  # deployment whose data the invoking user owns. The deployment has to stay
  # stopped for it: lego rewrites its keys as root every time it starts, so
  # taking ownership and then starting the containers again puts the
  # unreadable files straight back.
  if [ "$can_chown" = "yes" ]; then
    anas stop -w "$ws"
    sudo chown -R "$(id -u):$(id -g)" "$ws/data"
  else
    echo "SKIP: no passwordless sudo, so the copy round-trip (B4) cannot run;"
    echo "SKIP: container data is root-owned and copy mode correctly refuses it."
    echo "SKIP test-backup.sh B4: needs sudo to take ownership of container data" >&2
  fi

  echo "== B2: plan restarts the containers before the transfer =="
  anas backup plan -w "$ws" --to "$ext4_dest" --mode copy --json >"$REPORT_DIR/backup-plan.json"
  ops=$(sed -n 's/^ *"op": "\(.*\)",\{0,1\}$/\1/p' "$REPORT_DIR/backup-plan.json")
  echo "plan ops: $(echo "$ops" | tr '\n' ' ')"
  start_line=$(echo "$ops" | grep -n '^start_containers$' | cut -d: -f1)
  copy_line=$(echo "$ops" | grep -n '^copy_files$' | cut -d: -f1)
  if [ -n "$start_line" ] && [ -n "$copy_line" ]; then
    [ "$start_line" -lt "$copy_line" ] ||
      fail "start_containers must come before the transfer (line $start_line vs $copy_line)"
  else
    fail "plan did not list both start_containers and copy_files"
  fi
  # plan must not have done anything.
  [ -z "$(ls -A "$ext4_dest" 2>/dev/null)" ] || fail "plan wrote to the destination"

  echo "== B4: copy mode round-trips =="
  if [ "$can_chown" != "yes" ]; then
    echo "SKIP: B4 needs readable data; see the note above."
  else
  anas backup create -w "$ws" --to "$ext4_dest" --mode copy -y --json >"$REPORT_DIR/backup-create.json"
  backup_id=$(json_field "$REPORT_DIR/backup-create.json" backup_id)
  [ -n "$backup_id" ] || fail "create emitted no backup_id"
  echo "backup: $backup_id"

  # Both channels, and the completion marker last.
  for f in backup.yml snapshot.yml meta/config.yml meta/config.lock.yml \
           meta/secrets.yml meta/deployment-state.yml \
           deployment/deployment.yml deployment/config.source.yml; do
    [ -e "$ext4_dest/$backup_id/$f" ] || fail "backup $backup_id is missing $f"
  done
  [ -e "$ext4_dest/$backup_id/data/marker-before" ] ||
    fail "the backup does not contain the data marker"
  # snapshots/ must not have been carried: it is a copy-on-write reference to
  # the same disk and copying it is both huge and pointless.
  [ ! -e "$ext4_dest/$backup_id/snapshots" ] || fail "the backup carried snapshots/"
  # Neither may the caches or the historical deployments.
  [ ! -e "$ext4_dest/$backup_id/go-build-cache" ] || fail "the backup carried a build cache"

  anas backup list --to "$ext4_dest" --json >"$REPORT_DIR/backup-list.json"
  grep -q "$backup_id" "$REPORT_DIR/backup-list.json" || fail "list did not show $backup_id"
  grep -q '"complete": true' "$REPORT_DIR/backup-list.json" || fail "the backup is not marked complete"

  anas backup verify --to "$ext4_dest" --json >"$REPORT_DIR/backup-verify.json"
  grep -q '"ok": true' "$REPORT_DIR/backup-verify.json" || fail "verify found problems in a fresh backup"

  echo "== B4b: restore into a second workspace, which then starts =="
  rm -rf "$restore_ws" 2>/dev/null || sudo rm -rf "$restore_ws"
  anas init "$restore_ws" -y >/dev/null
  # Restoring must refuse an inferred workspace; it replaces live data.
  expect_exit 2 env ANAS_WORKSPACE="$restore_ws" "$anas_bin" backup restore --from "$ext4_dest" --json

  anas backup restore --from "$ext4_dest" -w "$restore_ws" --backup-id "$backup_id" --dry-run --json \
    >"$REPORT_DIR/backup-restore-dry.json"
  grep -q '"would_replace"' "$REPORT_DIR/backup-restore-dry.json" ||
    fail "restore --dry-run did not list what it would replace"
  [ ! -f "$restore_ws/data/marker-before" ] || fail "restore --dry-run wrote data"

  anas backup restore --from "$ext4_dest" -w "$restore_ws" --backup-id "$backup_id" -y --json \
    >"$REPORT_DIR/backup-restore.json"
  grep -q '"ok": true' "$REPORT_DIR/backup-restore.json" || fail "restore reported failure"
  [ -f "$restore_ws/data/marker-before" ] || fail "the restored workspace has no data"
  [ -f "$restore_ws/config.yml" ] || fail "the restored workspace has no config"
  [ -f "$restore_ws/.anas/secrets.yml" ] || fail "the restored workspace has no secret store"
  # A restored data directory that is not a subvolume silently costs the
  # workspace every future snapshot.
  is_subvolume "$restore_ws/data" || fail "restored data is not a Btrfs subvolume"
  # The automatic structural check has to have run and passed.
  grep -q '"verify"' "$REPORT_DIR/backup-restore.json" || fail "restore did not report a verify result"

  echo "== the restored workspace starts =="
  # An artifact start: no re-render, which is the point. The backup has to
  # carry everything needed to run, not everything needed to rebuild. The
  # original deployment is already stopped, so the shared prefixes and port do
  # not collide.
  anas start -w "$restore_ws"
  sleep 3
  started=$(docker ps --filter "name=anasbk_" -q | wc -l)
  [ "$started" -gt 0 ] || fail "the restored workspace did not start"
  anas stop -w "$restore_ws"
  fi

  echo "== B5: verify catches damage =="
  if [ "$can_chown" != "yes" ]; then
    echo "SKIP: B5 needs a backup to damage; B4 did not run."
  else
  cp -a "$ext4_dest/$backup_id" "$ext4_dest/truncated-copy"
  # A backup directory is identified by its own manifest, so the copy has to
  # claim its own id or the listing reports two backups with one name.
  sed -i "s/^backup_id: .*/backup_id: truncated-copy/" "$ext4_dest/truncated-copy/backup.yml"
  rm -f "$ext4_dest/truncated-copy/meta/config.yml"
  anas backup verify --to "$ext4_dest" --json >"$REPORT_DIR/backup-verify-bad.json" 2>/dev/null || true
  grep -q 'metadata_stream_missing' "$REPORT_DIR/backup-verify-bad.json" ||
    fail "verify did not notice a missing metadata file"
  expect_exit 1 anas backup verify --to "$ext4_dest" --json
  rm -rf "$ext4_dest/truncated-copy"
  fi

  echo "== B6: a failed backup leaves the services running =="
  anas start -w "$ws"
  sleep 3
  before=$(docker ps --filter "name=anasbk_" -q | wc -l)
  [ "$before" -gt 0 ] || fail "could not get the test deployment running for B6"

  # A regular file where a directory belongs. Permission bits would not do:
  # this suite also runs as root, and root can write to a 0500 directory, so
  # the injection has to be one that privilege cannot paper over.
  broken="$root/dest-not-a-directory"
  rm -rf "$broken"
  printf 'not a directory\n' >"$broken"
  set +e
  anas backup create -w "$ws" --to "$broken" --mode copy -y --json >"$REPORT_DIR/backup-fail.json" 2>&1
  fail_status=$?
  set -e
  rm -f "$broken"
  [ "$fail_status" != "0" ] || fail "a backup to an invalid destination reported success"
  after=$(docker ps --filter "name=anasbk_" -q | wc -l)
  [ "$after" = "$before" ] ||
    fail "a failed backup changed the running container count ($before -> $after)"

  echo "== B7a: a backup killed mid-run leaves nothing stopped for long =="
  # The real thing rather than a simulation: start a backup, kill it without
  # letting it clean up, then run any other command and require the services to
  # be back. Where the kill lands is not deterministic, so B7b forges the record
  # to cover the window this one may miss.
  anas backup create -w "$ws" --to "$btrfs_dest" --mode snapshot -y --json \
    >"$REPORT_DIR/backup-killed.json" 2>&1 &
  killed_pid=$!
  sleep 1
  kill -9 "$killed_pid" 2>/dev/null || true
  wait "$killed_pid" 2>/dev/null || true
  # Compensation runs under the exclusive lock, which is what makes it safe
  # against a backup that is still going. Read-only commands do not take that
  # lock, and should not: starting containers is a mutation, and doing it from
  # `snapshot list` would be a surprise. So the command used here is a
  # mutating one, which is the set an operator would actually run next.
  anas snapshot create -w "$ws" --label "trigger compensation" >/dev/null 2>&1 || true
  sleep 3
  survived=$(docker ps --filter "name=anasbk_" -q | wc -l)
  [ "$survived" -gt 0 ] ||
    fail "a killed backup left the services stopped after a later command ran"
  leftover=$(ls "$ws/.anas/state/transactions"/*.yml 2>/dev/null | wc -l)
  [ "$leftover" = "0" ] || fail "$leftover container transaction(s) were not compensated"

  echo "== B7b: a forged crash record is compensated by the next command =="
  # Forge the record a killed backup would have left, then stop a module by hand
  # and check that the next command that takes the exclusive lock starts it.
  active=$(sed -n 's/^active_deployment: //p' "$ws/.anas/state/active.yml")
  txn="$ws/.anas/state/transactions/forged.yml"
  cat >"$txn" <<EOF
api_version: anas.state/v2
id: forged
kind: backup_containers
started_at: 2026-07-31T00:00:00Z
workspace: $ws
deployment_id: $active
modules:
  - traefik
state: stopped
EOF
  docker compose --project-name anas_traefik --env-file .env \
    --project-directory "$ws/.anas/deployments/$active/modules/traefik" \
    -f "$ws/.anas/deployments/$active/modules/traefik/docker-compose.yml" stop >/dev/null 2>&1 ||
    docker stop $(docker ps --filter "name=anasbk_traefik" -q) >/dev/null 2>&1 || true
  sleep 2
  # Any command that takes the exclusive lock must notice and compensate.
  anas snapshot list -w "$ws" >/dev/null 2>&1 || true
  anas backup capabilities -w "$ws" >/dev/null 2>&1 || true
  anas snapshot create -w "$ws" --label "trigger compensation" --json >/dev/null 2>&1 || true
  sleep 3
  if [ -f "$txn" ]; then
    fail "the forged transaction record was not cleared by a later command"
  fi
  resumed=$(docker ps --filter "name=anasbk_" -q | wc -l)
  [ "$resumed" -gt 0 ] || fail "compensation did not start the stopped module"

  echo "== B8: the send modes =="
  if [ "$privileged" = "yes" ]; then
    anas backup create -w "$ws" --to "$ext4_dest" --mode send-file -y --json \
      >"$REPORT_DIR/backup-sendfile.json"
    sf_id=$(json_field "$REPORT_DIR/backup-sendfile.json" backup_id)
    [ -n "$sf_id" ] || fail "send-file create emitted no id"
    # Two channels: the stream and a separate metadata archive. A backup with
    # only one of them is worse than none, because it looks like a backup.
    [ -f "$ext4_dest/$sf_id/data.stream" ] || fail "send-file wrote no data stream"
    [ -f "$ext4_dest/$sf_id/meta.tar" ] || fail "send-file wrote no metadata channel"
    [ -f "$ext4_dest/$sf_id/backup.yml" ] || fail "send-file wrote no manifest"

    anas backup verify --to "$ext4_dest" --backup-id "$sf_id" --json \
      >"$REPORT_DIR/backup-verify-sendfile.json"
    grep -q '"ok": true' "$REPORT_DIR/backup-verify-sendfile.json" ||
      fail "verify found problems in a fresh send-file backup"

    # Truncating the stream must be caught. This is the damage a presence check
    # cannot see, and the reason the manifest records a size at all.
    truncate -s -1024 "$ext4_dest/$sf_id/data.stream"
    anas backup verify --to "$ext4_dest" --backup-id "$sf_id" --json \
      >"$REPORT_DIR/backup-verify-truncated.json" 2>/dev/null || true
    grep -q 'size_mismatch' "$REPORT_DIR/backup-verify-truncated.json" ||
      fail "verify did not catch a truncated send stream"
    rm -rf "$ext4_dest/$sf_id"

    # send into another Btrfs.
    anas backup create -w "$ws" --to "$btrfs_dest" --mode send -y --json \
      >"$REPORT_DIR/backup-send.json"
    s_id=$(json_field "$REPORT_DIR/backup-send.json" backup_id)
    [ -n "$s_id" ] || fail "send create emitted no id"
    is_subvolume "$btrfs_dest/$s_id/data" || fail "send did not receive a subvolume"
    [ -f "$btrfs_dest/$s_id/meta.tar" ] || fail "send wrote no metadata channel"
    [ -f "$btrfs_dest/$s_id/data/marker-before" ] || fail "the received subvolume has no data"
  else
    # Loud, and on stdout as well as stderr, so it lands in the summary rather
    # than only in a log nobody reads.
    echo "SKIP: btrfs send needs CAP_SYS_ADMIN; rerun as root to cover the send and"
    echo "SKIP: send-file modes. Everything above this line ran unprivileged."
    echo "SKIP test-backup.sh B8: btrfs send needs root" >&2
  fi

  echo "backup checks complete"
} >"$log" 2>&1 || {
  status=$?
  cat "$log"
  echo "backup test aborted with status $status" >&2
  exit "$status"
}

cat "$log"

if [ "$failures" -ne 0 ]; then
  echo "$failures backup assertion(s) failed" >&2
  exit 1
fi
echo "backup tests passed"
