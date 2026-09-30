#!/usr/bin/env bash
# Local native regression gate; no daemon installation, sudo, Incus, systemd
# activation, package operation, firewall rule or production socket is used.
set -euo pipefail

if [[ "$(uname -s)" != Linux ]] || [[ "$(id -u)" == 0 ]] || [[ "$(id -g)" == 0 ]]; then
  printf '%s\n' 'Host action native tests require Linux and a non-root UID/primary GID; no tests were accepted.' >&2
  exit 2
fi
root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"
command -v go >/dev/null
command -v python3 >/dev/null
report="$(mktemp)"
trap 'rm -f "$report"' EXIT

if ! go test -json -count=1 -timeout=90s ./internal/hostaction -run '^(TestFileLedger|TestActivation|TestReceive|TestRejectedActivation)' >"$report"; then
  cat "$report" >&2
  exit 1
fi

# go test exits successfully when all relevant cases skip or a regex finds no
# tests. This gate requires evidence that the exact native cases ran.
python3 - "$report" <<'PY'
import json
import sys

native = {
    "TestFileLedgerRecordsOneInvocationAndItsTerminal",
    "TestFileLedgerReportsAnExecutorThatDiedAsLost",
    "TestFileLedgerPrunesOnlyExpiredFreeRecords",
    "TestActivationServesOneBoundAuditedRequest",
    "TestActivationDisconnectDoesNotCancelExecutionAudit",
    "TestActivationCleanupFailureCannotReportCleanCompletion",
    "TestActivationPinsFileAndSocketOwnership",
    "TestActivationRejectsWrongDescriptorKinds",
    "TestRejectedActivationInputIsAuditedWithoutPayload",
    "TestReceiveUsesKernelCredentialsAndStrictEOF",
    "TestReceiveDeniesUnauthorizedPeerWithoutReadingPayload",
}
package = "github.com/anas-project/ANAS/internal/hostaction"
required = {(package, name) for name in native}
passed = set()
with open(sys.argv[1], encoding="utf-8") as report:
    for line in report:
        event = json.loads(line)
        name = event.get("Test", "")
        case = (event.get("Package", ""), name)
        parent = (case[0], name.split("/")[0])
        if event.get("Action") == "skip" and parent in required:
            sys.exit("Native host action test skipped; no acceptance evidence.")
        if event.get("Action") == "pass" and case in required:
            passed.add(case)
if passed != required:
    sys.exit("Native host action evidence is incomplete: " + ", ".join(package + ":" + name for package, name in sorted(required-passed)))
print("Host action native socket/ledger regressions passed; this is not systemd/root/Incus acceptance.")
PY
