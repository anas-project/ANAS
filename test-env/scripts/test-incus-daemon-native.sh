#!/usr/bin/env bash
# Explicit lab entry point: no package installation, host daemon, default
# socket, guest launch, loop device, or host network mutation. Dependencies
# are an operator-provided extracted distribution package tree.
set -euo pipefail
umask 077

if [[ ${1:-} != --inside ]]; then
  if [[ $(uname -s) != Linux || $(id -u) != 0 || ( $# != 5 && $# != 6 ) ]]; then
    echo 'Usage (authorized Linux lab): sudo bash test-incus-daemon-native.sh NEW_RUN_ROOT EXTRACTED_DEPS HOST_TEST_BINARY PROVIDER_BINARY TEST2JSON [CLIENT_TEST_BINARY]' >&2
    exit 2
  fi
  for path in "$@"; do
    case "$path" in /*) ;; *) echo 'All paths must be absolute.' >&2; exit 2 ;; esac
    [[ "$path" != *:* && "$path" != *','* && "$path" != *$'\n'* ]]
  done
  root=$1 deps=$2 tests=$3 provider=$4 decoder=$5
  client=${6:-}
  [[ -z "$client" || -x "$client" ]]
  [[ $(realpath -m -- "$root") == "$root" && $(realpath -- "$deps") == "$deps" ]]
  [[ ! -e "$root" && ! -L "$root" && -d "$deps/usr" && -x "$tests" && -x "$provider" && -x "$decoder" ]]
  for program in unshare timeout mount ip nft python3; do command -v "$program" >/dev/null; done
  script=$(realpath "$0")
  mkdir -m 0700 "$root"
  net=$(stat -Lc %i /proc/self/ns/net)
  mnt=$(stat -Lc %i /proc/self/ns/mnt)
  pid=$(stat -Lc %i /proc/self/ns/pid)
  # The PID namespace kills remaining descendants even on timeout/failure;
  # private mounts and state vanish when it exits. Reports alone persist.
  timeout -k 10s 150s unshare --mount --net --pid --fork --mount-proc --kill-child=SIGKILL \
    bash "$script" --inside "$root" "$deps" "$tests" "$provider" "$decoder" "$client" "$net" "$mnt" "$pid"
  exit $?
fi

[[ $# == 10 && $(id -u) == 0 && $$ == 1 ]]
root=$2 deps=$3 tests=$4 provider=$5 decoder=$6 client=$7 net=$8 mnt=$9 pid=${10}
[[ $(stat -Lc %i /proc/self/ns/net) != "$net" && $(stat -Lc %i /proc/self/ns/mnt) != "$mnt" && $(stat -Lc %i /proc/self/ns/pid) != "$pid" ]]
[[ $(ip -j link show | python3 -c 'import json,sys;print(",".join(x["ifname"] for x in json.load(sys.stdin)))') == lo ]]
[[ -z $(nft list tables) ]]
mount --make-rprivate /
mount -t overlay overlay -o "lowerdir=$deps/usr:/usr,ro" /usr
mount -t tmpfs -o nosuid,nodev,noexec,mode=0755,size=128m tmpfs /run
ip link set lo up
fixture=/run/anas-incus-native
mkdir -m 0700 "$fixture" "$fixture/state" "$fixture/client" "$fixture/keys" "$root/logs"
printf '{"schema":"anas.incus-native-isolation/v1","parent_netns":%s,"parent_mntns":%s,"parent_pidns":%s}\n' "$net" "$mnt" "$pid" > "$fixture/isolation.json"
export INCUS_DIR="$fixture/state" INCUS_CONF="$fixture/client" INCUS_SOCKET="$fixture/state/unix.socket" INCUS_LOGS="$root/logs"
/usr/libexec/incus/incusd --group root --logfile "$root/logs/daemon.log" > "$root/logs/stderr.log" 2>&1 &
daemon=$!
cleanup() {
  original=$?
  trap - EXIT
  if kill -0 "$daemon" 2>/dev/null; then
    kill -TERM "$daemon" || true
    for _ in $(seq 1 40); do kill -0 "$daemon" 2>/dev/null || break; sleep .25; done
    if kill -0 "$daemon" 2>/dev/null; then
      echo 'Daemon shutdown required forced containment; not a clean shutdown.' >&2
      kill -KILL "$daemon" || true
      original=1
    fi
  fi
  wait_status=0
  wait "$daemon" 2>/dev/null || wait_status=$?
  if [[ $wait_status != 0 ]]; then
    echo 'Daemon did not report a successful shutdown.' >&2
    original=1
  fi
  exit "$original"
}
trap cleanup EXIT

python3 "$(dirname "$0")/incus-provider-native.py" --ready
python3 "$(dirname "$0")/incus-provider-native.py" --cli-control > "$root/cli-control.jsonl"
set +e
ANAS_REQUIRE_INCUS_DAEMON_NATIVE=1 GOMAXPROCS=2 "$decoder" -t -p github.com/anas-project/ANAS/internal/incusprovision \
  "$tests" -test.v=test2json -test.run='^TestNativeIncusUnixStorageLifecycle$' -test.count=1 -test.timeout=60s > "$root/host-client.jsonl" 2>&1
native_status=$?
python3 "$(dirname "$0")/incus-provider-native.py" --provider "$provider" > "$root/provider.jsonl" 2>&1
provider_status=$?
client_status=0
if [[ -n "$client" ]]; then
  python3 "$(dirname "$0")/incus-provider-native.py" --client "$client" --test2json "$decoder" --report-root "$root" > "$root/client.jsonl" 2>&1
  client_status=$?
fi
set -e
[[ $client_status == 0 ]] || { echo 'Native shared-client checks failed; inspect private reports.' >&2; exit 1; }
python3 - "$root/host-client.jsonl" "$native_status" "$provider_status" <<'PY'
import json,sys
events=[json.loads(line) for line in open(sys.argv[1],encoding='utf-8')]
name='TestNativeIncusUnixStorageLifecycle'
package='github.com/anas-project/ANAS/internal/incusprovision'
passed=any(e.get('Package')==package and e.get('Test')==name and e.get('Action')=='pass' for e in events)
terminal=any(e.get('Package')==package and not e.get('Test') and e.get('Action')=='pass' for e in events)
bad=any(e.get('Action') in ('skip','fail') for e in events)
if sys.argv[2:] != ['0','0'] or not passed or not terminal or bad:
    sys.exit('Native daemon validation failed or was skipped; inspect private per-case reports.')
print('Native Unix client storage lifecycle and Provider rejection/pinning checks passed; guest/VM/quota/production installation are separate.')
PY
