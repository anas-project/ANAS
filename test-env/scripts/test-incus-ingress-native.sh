#!/usr/bin/env bash
# Runs an explicitly precompiled test binary, never installs a daemon or reads
# Docker/Incus sockets. A fresh temporary kernel netns is required, not skipped.
set -euo pipefail

if [[ "$(uname -s)" != Linux ]] || [[ "$#" != 1 ]] || [[ ! -x "$1" ]]; then
  printf '%s\n' 'Usage: test-incus-ingress-native.sh /absolute/precompiled.test (Linux with CAP_SYS_ADMIN required)' >&2
  exit 2
fi
case "$1" in /*) ;; *) printf '%s\n' 'The test binary path must be absolute.' >&2; exit 2 ;; esac
command -v go >/dev/null
command -v python3 >/dev/null
report="$(mktemp)"
trap 'rm -f "$report"' EXIT
package='github.com/anas-project/ANAS/internal/incusingresshost'

if ! ANAS_REQUIRE_INGRESS_NATIVE=1 "$1" -test.v=test2json -test.count=1 -test.timeout=90s \
  -test.run='^(TestNativeNamespaceKernelIdentityAndCookie|TestNativeCommandPinsExecutableDescriptor|TestNativeNamespaceSwitchRestoresOriginal|TestNativeNFTScriptAndReadback|TestNativeAddressRoutingCannotFollowDeviceReuse|TestNativeReplyOriginRejectsSpoofAndDeviceReuse|TestNativeConntrackBidirectionalCleanup)$' \
  | go tool test2json -t -p "$package" >"$report"; then
  cat "$report" >&2
  exit 1
fi

python3 - "$report" "$package" <<'PY'
import json
import sys

required = {
    'TestNativeNamespaceKernelIdentityAndCookie',
    'TestNativeCommandPinsExecutableDescriptor',
    'TestNativeNamespaceSwitchRestoresOriginal',
    'TestNativeNFTScriptAndReadback',
    'TestNativeAddressRoutingCannotFollowDeviceReuse',
    'TestNativeReplyOriginRejectsSpoofAndDeviceReuse',
    'TestNativeConntrackBidirectionalCleanup',
    *('TestNativeNamespaceSwitchRestoresOriginal/' + name for name in ('success', 'error', 'panic', 'cancel')),
}
passed = set()
with open(sys.argv[1], encoding='utf-8') as report:
    for line in report:
        event = json.loads(line)
        if event.get('Package') != sys.argv[2]:
            continue
        name = event.get('Test', '')
        if event.get('Action') == 'skip':
            sys.exit('Native ingress case skipped; no acceptance evidence.')
        if event.get('Action') == 'pass' and name in required:
            passed.add(name)
if passed != required:
    sys.exit('Native ingress evidence missing: ' + ', '.join(sorted(required - passed)))
print('Native namespace/FD/nft/FIB/reply-packet/conntrack regressions passed; Docker, Incus, full TCP sessions, HTTP and IPv6 acceptance are separate.')
PY
