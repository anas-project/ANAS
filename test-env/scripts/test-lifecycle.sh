#!/usr/bin/env sh
# Walks a deployment through its whole life in one run, in the order an operator
# would, and checks each step against what the contracts promise.
#
# The per-feature suites each prove one mechanism in isolation. This one exists
# for the failures that only appear in sequence: state left behind by an earlier
# command, a flag that works alone but not after an apply, a restored workspace
# that verifies clean and then will not start. Those are the failures that reach
# users, because users never run one command.
#
# It also covers the parameter combinations that only exist at a particular
# point in the life — `rollback` with nothing to roll back to, `snapshot create`
# before there is a deployment for it to belong to — which a table-driven test
# cannot reach because it has no deployment to be at a point in.
#
# Btrfs is not required. On another filesystem the snapshot steps assert the
# refusal instead of the operation, because refusing correctly is the contract
# there. Likewise a host where no backup mode can run asserts that create says
# so rather than producing something partial.
set -u

. "$(dirname -- "$0")/common.sh"

# common.sh turns on `set -e`, which this suite must not run under: half its
# assertions are that a command fails with a particular exit code, and the
# first of those would end the run. The earlier version of this file hid the
# problem by wrapping the body in `{ ... } || handler`, which suppresses
# `set -e` inside the braces — and with it every genuine failure too, so the
# run continued against a state it was never meant to reach and still reported
# whatever the last assertion said. Each step checks its own outcome instead.
set +e

cd "$ROOT_DIR"

ws=${ANAS_LIFECYCLE_WORKSPACE:-$RUNTIME_DIR/lifecycle}
restored=${ANAS_LIFECYCLE_RESTORED:-$RUNTIME_DIR/lifecycle-restored}
dest=${ANAS_LIFECYCLE_DEST:-$RUNTIME_DIR/lifecycle-backup}
config=${ANAS_LIFECYCLE_CONFIG:-$CONFIG_DIR/lifecycle.yml}
prefix=${ANAS_LIFECYCLE_PREFIX:-anaslc_}
failures=0

# Deliberately not `set -e` plus a `|| { }` wrapper. In POSIX shell, `set -e`
# is suppressed inside any command that is part of an `&&`/`||` list, so a body
# written as `{ ... } || handler` runs with the abort-on-error it appears to
# have switched on. An early failure then leaves every later assertion running
# against a state it was never meant to see, and the run still reports whatever
# the last assertion happened to say. Every step here checks its own outcome.
run() {
  "$@" >>"$log" 2>&1
  status=$?
  # $? has to be captured before anything else runs: inside `if ! cmd`, it is
  # the status of the negation rather than of the command, which reports a
  # failure as "exit 0" and sends the reader looking in the wrong place.
  [ $status -eq 0 ] && return 0
  fail "'$*' failed (exit $status)"
  return 1
}

# The exit-code table is part of the contract, and `go run` collapses every
# non-zero status to 1, so this suite drives a built binary.
anas_bin="$ROOT_DIR/.anas-test/bin/anas"
mkdir -p "$(dirname -- "$anas_bin")"
go build -o "$anas_bin" ./cmd/anas || exit 1
anas() { "$anas_bin" "$@"; }

log="$REPORT_DIR/lifecycle.log"
: >"$log"

# Scratch files go under the report directory, not /tmp. A fixed /tmp name is
# shared across privilege levels, and Linux's protected_regular refuses to let
# even root open a file in a sticky world-writable directory owned by someone
# else — so an unprivileged run leaves a file a later sudo run cannot write,
# and the failure reads as the command under test misbehaving.
scratch="$REPORT_DIR/lifecycle-scratch.$$"
mkdir -p "$scratch"

cleanup() {
  anas stop -w "$ws" >>"$log" 2>&1 || true
  anas stop -w "$restored" >>"$log" 2>&1 || true
  rm -rf "$scratch"
}
trap cleanup EXIT INT TERM

fail() { echo "FAIL: $*" >&2; failures=$((failures + 1)); }
step() { echo; echo "== $* =="; }

expect_exit() {
  want=$1
  shift
  "$@" >>"$log" 2>&1
  got=$?
  [ "$got" = "$want" ] || fail "expected exit $want from '$*', got $got"
}

# `env VAR=x anas ...` cannot be used: anas is a shell function here, and env
# only execs external programs, so it would report 127 and look like a failure
# of the command under test.
with_workspace_env() {
  value=$1
  shift
  ANAS_WORKSPACE="$value" "$@"
}

# A JSON document is only useful when it is the only thing on stdout, so the
# check is that it parses whole rather than that it contains some key.
json_ok() {
  out=$("$@" 2>>"$log")
  if [ $? -ne 0 ]; then
    fail "'$*' failed"
    return
  fi
  printf '%s' "$out" | python3 -c 'import json,sys; json.load(sys.stdin)' 2>/dev/null ||
    fail "'$*' did not print exactly one JSON document"
}

active_deployment() { sed -n 's/^active_deployment: //p' "$1/.anas/state/active.yml" 2>/dev/null; }
running() { docker ps --filter "name=^$prefix" --format '{{.Names}}' | sort | tr '\n' ' '; }
container_id() { docker inspect --type container --format '{{.Id}}' "${prefix}$1" 2>/dev/null; }

# Docker's event journal is the observable boundary for lifecycle order. Final
# state alone cannot distinguish "dependent stopped first" from "dependency
# disappeared first and the dependent happened to recover". Timestamps use
# RFC3339 nanoseconds so an event from an earlier operation in the same second
# cannot leak into the assertion.
capture_container_events() {
  since=$1
  until=$2
  output=$3
  docker events --since "$since" --until "$until" --filter type=container \
    --format '{{.Actor.Attributes.name}} {{.Action}}' >"$output" 2>>"$log" ||
    fail "could not read Docker lifecycle events"
}

event_order() {
  events=$1
  action=$2
  awk -v prefix="$prefix" -v action="$action" '
    $2 == action && ($1 == prefix "lego" || $1 == prefix "traefik") {
      printf "%s ", $1
    }
  ' "$events"
}

btrfs_here=no
mkdir -p "$(dirname -- "$ws")"
[ "$(df -T "$(dirname -- "$ws")" 2>/dev/null | awk 'NR==2 {print $2}')" = btrfs ] && btrfs_here=yes
echo "workspace filesystem: $([ $btrfs_here = yes ] && echo btrfs || echo 'not btrfs (snapshot steps assert the refusal)')"

rm -rf "$ws" "$restored" "$dest" 2>/dev/null || true

step "L1 init"
json_ok anas init "$ws" -y --json
for d in .anas data snapshots; do
  [ -d "$ws/$d" ] || fail "init did not create $d/"
done
# Re-initialising an existing workspace is a precondition failure, not a usage
# mistake: the command line was right, the machine was not in the state it needs.
expect_exit 4 anas init "$ws" -y
run anas config import "$config" -w "$ws"

step "L2 before anything is deployed"
expect_exit 4 anas start -w "$ws"
expect_exit 4 anas rollback -w "$ws"
expect_exit 4 anas snapshot create -w "$ws"
# Queries answer rather than fail: "nothing is deployed" is an answer.
json_ok anas status -w "$ws" --json
json_ok anas deployments list -w "$ws" --json

step "L3 inspect the configuration"
json_ok anas config explain nextcloud.domain_prefix --json
json_ok anas config plan -w "$ws" --json
json_ok anas plan -w "$ws" -c "$ws/config.yml" --json
json_ok anas lock -w "$ws" --json

step "L4 first apply"
if run anas apply --build -w "$ws" --update-lock; then
  first=$(active_deployment "$ws")
  [ -n "$first" ] || fail "apply left no active deployment"
  [ -n "$(running)" ] || fail "apply started no containers"
  echo "deployment 1: $first"
else
  echo "apply failed; the rest of the life cannot be exercised" >&2
  cat "$log"
  exit 1
fi

step "L5 query the running deployment"
json_ok anas status -w "$ws" --json
json_ok anas deployments list -w "$ws" --json
json_ok anas deployments inspect "$first" -w "$ws" --json
json_ok anas config secret list -w "$ws" --json
expect_exit 4 anas deployments inspect no-such-id -w "$ws"

step "L6 the artifact carries what a restore needs"
[ -f "$ws/.anas/deployments/$first/config.source.yml" ] ||
  fail "no config.source.yml; a snapshot of this deployment could not be restored on its own"
[ -z "$(find "$ws/.anas/deployments/$first" -type f -perm -u=w | head -1)" ] ||
  fail "the artifact was not sealed read-only"

step "L7 write data that must survive everything below"
echo lifecycle-marker >"$ws/data/marker"

step "L8 config set applies the running deployment"
traefik_before=$(container_id traefik)
[ -n "$traefik_before" ] || fail "Traefik has no container before config set"
run anas config set global.timezone Europe/Berlin -w "$ws"
second=$(active_deployment "$ws")
[ "$second" != "$first" ] || fail "config set produced no new deployment"
traefik_after=$(container_id traefik)
[ -n "$traefik_after" ] || fail "Traefik has no container after config set"
[ "$traefik_after" != "$traefik_before" ] ||
  fail "container_recreate kept the same Traefik container id"
echo "deployment 2: $second"

step "L9 rollback keeps the data"
run anas rollback "$first" -w "$ws"
[ "$(active_deployment "$ws")" = "$first" ] || fail "rollback did not restore deployment 1"
[ -f "$ws/data/marker" ] || fail "rollback destroyed data written after the previous apply"
[ -n "$(running)" ] || fail "rollback left no containers running"

step "L10 snapshots"
if [ $btrfs_here = yes ]; then
  json_ok anas snapshot create -w "$ws" --label lifecycle --json
  json_ok anas snapshot list -w "$ws" --json
  id=$(anas snapshot list -w "$ws" --json 2>/dev/null | sed -n 's/^ *"id": "\(.*\)",$/\1/p' | head -1)
  if [ -n "$id" ]; then
    json_ok anas snapshot show "$id" -w "$ws" --json
    json_ok anas snapshot verify -w "$ws" --json
    # restore must refuse an inferred workspace even when one is inferable.
    expect_exit 2 with_workspace_env "$ws" anas snapshot restore "$id"
  else
    fail "snapshot create produced nothing listable"
  fi
else
  expect_exit 4 anas snapshot create -w "$ws"
  echo "snapshot steps: refusal asserted (not btrfs)"
fi

step "L11 backup capabilities"
mkdir -p "$dest"
json_ok anas backup capabilities -w "$ws" --to "$dest" --json

step "L12 backup create"
# Whether any mode can run is a property of the host, so this reads the answer
# from `capabilities` rather than pattern-matching failure text. Every
# unavailable mode must give an enumerated reason: a caller that cannot back up
# needs to know whether it is looking at the wrong filesystem or at data it
# cannot read, because those have different remedies.
anas backup capabilities -w "$ws" --to "$dest" --json >"$scratch/caps.json" 2>>"$log" ||
  fail "backup capabilities failed"
available=$(CAPS="$scratch/caps.json" python3 -c "
import json, os
d = json.load(open(os.environ['CAPS']))
known = {'dest_not_specified','dest_not_exist','dest_not_writable','dest_not_btrfs',
         'dest_not_same_filesystem','source_not_btrfs','data_not_subvolume',
         'data_is_mountpoint','btrfs_tool_missing','insufficient_privilege',
         'insufficient_space'}
bad = [m['id'] + ':' + str(m.get('reason')) for m in d['modes']
       if not m['available'] and m.get('reason') not in known]
print('UNKNOWN_REASON ' + ' '.join(bad) if bad
      else ' '.join(m['id'] for m in d['modes'] if m['available']))
" 2>/dev/null)
case "$available" in
  UNKNOWN_REASON*) fail "capabilities gave an unenumerated reason: $available" ;;
esac

backup_made=no
if [ -n "$available" ]; then
  echo "available modes: $available"
  if run anas backup create -w "$ws" --to "$dest" -y; then
    json_ok anas backup list --to "$dest" --json
    json_ok anas backup verify --to "$dest" --json
    backup_made=yes
  fi
else
  echo "no mode available here; asserting the refusal"
  expect_exit 4 anas backup create -w "$ws" --to "$dest" -y
  [ -z "$(ls -A "$dest" 2>/dev/null)" ] || fail "a refused backup still wrote to $dest"
fi

step "L13 restore into a second workspace and start it"
if [ "$backup_made" = yes ]; then
  # The original stops first. Leaving it running would let its containers
  # satisfy the "did the restored workspace start" check, which is the whole
  # point of the step.
  run anas stop -w "$ws"
  [ -z "$(running)" ] || fail "stop left containers running"

  # restore needs an existing workspace, and refuses one it had to infer.
  expect_exit 2 anas backup restore --from "$dest" -w "$restored" -y
  json_ok anas init "$restored" -y --json
  expect_exit 2 with_workspace_env "$restored" anas backup restore --from "$dest" -y

  if run anas backup restore --from "$dest" -w "$restored" -y; then
    [ -f "$restored/data/marker" ] || fail "the restored workspace lost the data marker"
    [ -f "$restored/config.yml" ] || fail "the restored workspace has no config"
    [ -n "$(active_deployment "$restored")" ] || fail "the restored workspace has no active deployment"
    if run anas start -w "$restored"; then
      [ -n "$(running)" ] || fail "the restored workspace did not start"
      run anas stop -w "$restored"
      echo "restored workspace started from the backup alone"
    fi
  fi
  run anas start -w "$ws"
else
  echo "restore step skipped: no backup to restore"
fi

step "L14 stop, and confirm the workspace is still usable afterwards"
run anas stop -w "$ws"
[ -z "$(running)" ] || fail "stop left containers running"
json_ok anas status -w "$ws" --json
run anas start -w "$ws"
[ -n "$(running)" ] || fail "the deployment did not start again after a stop"
run anas stop -w "$ws"

step "L15 named lifecycle commands preserve dependency chains"
run anas start -w "$ws"
all_running=$(running)
[ -n "$all_running" ] || fail "the deployment did not start"
# A module this deployment does not have is a usage error, not a silent no-op
# that reports success having restarted nothing.
expect_exit 2 anas restart nosuchmodule -w "$ws" --json
# Traefik depends on Lego. Restarting Lego must recreate both the dependency
# and its dependent, stopping Traefik first and starting Lego first.
lego_before=$(container_id lego)
traefik_before=$(container_id traefik)
run anas restart lego -w "$ws"
[ "$(running)" = "$all_running" ] || fail "restarting one module changed which containers run: '$all_running' -> '$(running)'"
[ -n "$(container_id lego)" ] && [ "$(container_id lego)" != "$lego_before" ] ||
  fail "restart lego did not recreate Lego"
[ -n "$(container_id traefik)" ] && [ "$(container_id traefik)" != "$traefik_before" ] ||
  fail "restart lego did not include dependent Traefik"
# Multiple explicit targets are deliberately written in the opposite order.
# The actual Docker journal, rather than only the final state, must show the
# dependent destroyed first and the dependency started first.
events="$scratch/restart-multiple.events"
since=$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)
run anas restart traefik lego -w "$ws"
until=$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)
capture_container_events "$since" "$until" "$events"
[ "$(event_order "$events" destroy)" = "${prefix}traefik ${prefix}lego " ] ||
  fail "multiple restart destroy order was '$(event_order "$events" destroy)'"
[ "$(event_order "$events" start)" = "${prefix}lego ${prefix}traefik " ] ||
  fail "multiple restart start order was '$(event_order "$events" start)'"
# Stop receives dependency order on the command line but must still execute in
# reverse dependency order. Starting receives reverse input and must execute in
# forward dependency order.
events="$scratch/stop-multiple.events"
since=$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)
run anas stop lego traefik -w "$ws"
until=$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)
capture_container_events "$since" "$until" "$events"
[ "$(event_order "$events" destroy)" = "${prefix}traefik ${prefix}lego " ] ||
  fail "multiple stop destroy order was '$(event_order "$events" destroy)'"
[ -z "$(running)" ] || fail "multiple stop left containers running: '$(running)'"
events="$scratch/start-multiple.events"
since=$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)
run anas start traefik lego -w "$ws"
until=$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)
capture_container_events "$since" "$until" "$events"
[ "$(event_order "$events" start)" = "${prefix}lego ${prefix}traefik " ] ||
  fail "multiple start order was '$(event_order "$events" start)'"
[ "$(running)" = "$all_running" ] || fail "multiple start did not restore the deployment"
# Stopping a dependency also stops every dependent.
run anas stop lego -w "$ws"
[ -z "$(running)" ] || fail "stop lego left its dependency chain running: '$(running)'"
# Starting the leaf application from a cold state pulls in its prerequisite.
run anas start traefik -w "$ws"
[ "$(running)" = "$all_running" ] || fail "starting the module back did not restore the deployment"
# Stopping a leaf does not stop the prerequisite, which remains valid on its
# own; only dependents are expanded by stop/restart.
run anas stop traefik -w "$ws"
[ -n "$(container_id lego)" ] || fail "stop traefik incorrectly stopped prerequisite Lego"
[ -z "$(container_id traefik)" ] || fail "stop traefik left Traefik running"
run anas start traefik -w "$ws"
[ "$(running)" = "$all_running" ] || fail "start traefik did not restore its dependency chain"
run anas stop -w "$ws"

step "L16 a flag after a module name is still a flag"
# The standard Go flag parser stops at the first positional argument, so
# `anas restart lego -w <workspace>` dropped -w and fell back to the current
# directory or ANAS_WORKSPACE -- acting on whichever deployment that named,
# without saying so. Running from a directory that is not a workspace is what
# makes the difference observable: the command must still find the workspace
# the flag names.
outside=$(mktemp -d)
( cd "$outside" && ANAS_WORKSPACE= "$anas_bin" restart lego -w "$ws" ) \
  >>"$log" 2>&1 || fail "a flag placed after a module name was dropped"
# --root as well as -w: build needs the module registry, which it otherwise
# locates from the current directory. Both flags sit after the module name, which
# is the thing being tested.
( cd "$outside" && ANAS_WORKSPACE= "$anas_bin" build lego -w "$ws" --root "$ROOT_DIR" ) \
  >>"$log" 2>&1 || fail "build dropped a flag placed after a module name"
rmdir "$outside" 2>/dev/null || true
run anas stop -w "$ws"

step "L17 a deployment does not depend on any particular Docker subnet"
# Traefik used to pin its subnet, so a host already using that range could not
# deploy at all -- and a replacement pin collided with whatever Docker handed a
# sibling network of the same deployment. Holding the old pinned range hostage
# is the direct test that nothing depends on it any more.
decoy=anas_subnet_decoy
docker network rm "$decoy" >/dev/null 2>&1 || true
held=no
if docker network create --subnet 172.28.0.0/16 "$decoy" >/dev/null 2>&1; then
  held=yes
else
  # Already taken by something else on this host, which is the same condition
  # under a different owner and needs no decoy of ours.
  echo "172.28.0.0/16 is already held by another network; using it as the decoy" >&2
fi
run anas start -w "$ws"
[ -n "$(running)" ] || fail "the deployment could not start while 172.28.0.0/16 was taken"
run anas stop -w "$ws"
[ "$held" = yes ] && docker network rm "$decoy" >/dev/null 2>&1
true

step "L18 a malformed setting is refused when the config is read"
# A checked scalar: `ipv6: flase` used to be stored verbatim and read as true by
# a module testing != "false", so the setting was written, accepted, and reversed.
bad=$(mktemp)
sed 's/^  email:/  ipv6: flase\n  email:/' "$config" > "$bad"
expect_exit 4 anas plan -c "$bad" -w "$ws"
rm -f "$bad"

echo
if [ "$failures" -ne 0 ]; then
  echo "$failures lifecycle assertion(s) failed" >&2
  echo "full output: $log" >&2
  exit 1
fi
echo "lifecycle passed"
