#!/usr/bin/env sh
# The CLI contract, asserted against a real process.
#
# internal/runner/contract_test.go covers the same ground in-process, which is
# where the breadth lives. This suite exists for the one thing a Go test cannot
# observe: the number the process actually exits with. `go run` collapses every
# non-zero status to 1, so this drives a built binary — the same reason
# test-snapshot.sh does.
#
# C1  stdout carries exactly one JSON document, parseable without filtering.
# C2  the exit code is the number from the table, not merely non-zero.
# C3  progress and the workspace announcement go to stderr, never stdout.
# C4  a command needing confirmation with no tty returns 3 at once.
# C5  every document carries api_version, and every failure a snake_case code.
#
# C2 is the point of the whole file. A command that returns 1 where the table
# says 4 still looks like it works from a prompt, and gives an external
# non-interactive caller nothing to branch on.
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

log="$REPORT_DIR/contract.log"
failures=0
: >"$log"

anas_bin="$ROOT_DIR/.anas-test/bin/anas"
mkdir -p "$(dirname -- "$anas_bin")"
go build -o "$anas_bin" ./cmd/anas

work=$(mktemp -d "${TMPDIR:-/tmp}/anas-contract.XXXXXX")
cleanup() {
  rm -rf "$work" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}

# expect_exit <code> <label> -- <command...>
#
# Asserts the exact status, and that stdout held one parseable JSON document
# carrying the envelope. Both halves matter: a command can return the right
# code while printing something no caller can read.
expect_exit() {
  want=$1
  label=$2
  shift 3
  out="$work/stdout"
  err="$work/stderr"
  set +e
  "$@" >"$out" 2>"$err" </dev/null
  got=$?
  set -e
  {
    echo "--- $label (want exit $want, got $got)"
    echo "stdout:"
    cat "$out"
    echo "stderr:"
    cat "$err"
  } >>"$log"
  [ "$got" = "$want" ] || fail "$label: expected exit $want, got $got"
  assert_document "$want" "$label" "$out"
}

# assert_document checks C1 and C5 with python3, which is the only dependency
# here that is not POSIX. Parsing with it rather than with grep is the whole
# assertion: "JSON.parse(stdout) succeeds" cannot be checked by pattern.
assert_document() {
  want=$1
  label=$2
  file=$3
  if [ -z "${PYTHON:-}" ]; then
    PYTHON=$(command -v python3 || true)
  fi
  if [ -z "$PYTHON" ]; then
    echo "SKIP: no python3; cannot verify that stdout parses as JSON" >&2
    return 0
  fi
  "$PYTHON" - "$want" "$label" "$file" <<'PY' || fail "$label: stdout is not one contract document"
import json, re, sys

want, label, path = int(sys.argv[1]), sys.argv[2], sys.argv[3]
raw = open(path).read()

# Exactly one document: decode one value, then require nothing but whitespace.
decoder = json.JSONDecoder()
try:
    document, end = decoder.raw_decode(raw.lstrip())
except ValueError as exc:
    print(f"{label}: stdout is not JSON ({exc}): {raw!r}", file=sys.stderr)
    sys.exit(1)
if raw.lstrip()[end:].strip():
    print(f"{label}: stdout carries more than one document: {raw!r}", file=sys.stderr)
    sys.exit(1)

if document.get("api_version") != "anas.dev/cli/v1":
    print(f"{label}: api_version = {document.get('api_version')!r}", file=sys.stderr)
    sys.exit(1)
if not isinstance(document.get("ok"), bool):
    print(f"{label}: ok is missing or not a boolean", file=sys.stderr)
    sys.exit(1)
if want == 0:
    if not document["ok"]:
        print(f"{label}: ok = false on a successful command", file=sys.stderr)
        sys.exit(1)
    sys.exit(0)

if document["ok"]:
    print(f"{label}: ok = true on a failing command", file=sys.stderr)
    sys.exit(1)
error = document.get("error")
if not isinstance(error, dict):
    print(f"{label}: no error object", file=sys.stderr)
    sys.exit(1)
# A code is an enumeration a caller switches on. Spaces or capitals in it mean
# free text wearing a code's clothes.
if not re.fullmatch(r"[a-z][a-z0-9_]*", error.get("code", "")):
    print(f"{label}: error.code = {error.get('code')!r} is not snake_case", file=sys.stderr)
    sys.exit(1)
if not error.get("message"):
    print(f"{label}: error.message is empty", file=sys.stderr)
    sys.exit(1)
PY
}

anas() {
  "$anas_bin" "$@"
}

ws="$work/ws"
echo "== C1/C5: a fresh workspace, reported as one document =="
expect_exit 0 "init" -- anas init "$ws" -y --json
expect_exit 4 "init over an existing workspace" -- anas init "$ws" -y --json

echo "== C2: usage mistakes exit 2 =="
expect_exit 2 "unknown command" -- anas frobnicate --json
expect_exit 2 "config with no subcommand" -- anas config --json
expect_exit 2 "deployments with no subcommand" -- anas deployments --json
expect_exit 2 "snapshot with no subcommand" -- anas snapshot --json
expect_exit 2 "backup with no subcommand" -- anas backup --json
expect_exit 2 "plan with no config" -- anas plan --root "$ROOT_DIR" --json
expect_exit 2 "apply with conflicting snapshot flags" -- anas apply -w "$ws" --snapshot --no-snapshot --json
expect_exit 2 "rollback without -w" -- anas rollback --json
expect_exit 2 "an unrecognised flag" -- anas status -w "$ws" --nope --json

echo "== C2: unmet preconditions exit 4 =="
expect_exit 4 "plan with a missing config" -- anas plan -w "$ws" -c "$work/absent.yml" --root "$ROOT_DIR" --json
expect_exit 4 "start with no active deployment" -- anas start -w "$ws" --json
expect_exit 4 "stop with no active deployment" -- anas stop -w "$ws" --json
expect_exit 4 "restart with no active deployment" -- anas restart -w "$ws" --json
expect_exit 4 "rollback with no previous deployment" -- anas rollback -w "$ws" --json
expect_exit 4 "inspect a missing deployment" -- anas deployments inspect nosuch -w "$ws" --json
expect_exit 4 "get a missing secret" -- anas config secret get NO_SUCH_SECRET -w "$ws" --json
expect_exit 4 "apply an unknown deployment" -- anas apply -w "$ws" --deployment nosuch --json

echo "== C2: queries answer, they do not fail =="
expect_exit 0 "status on an undeployed workspace" -- anas status -w "$ws" --json
expect_exit 0 "deployments list when there are none" -- anas deployments list -w "$ws" --json
expect_exit 0 "snapshot list when there are none" -- anas snapshot list -w "$ws" --json
expect_exit 0 "config secret list" -- anas config secret list -w "$ws" --json
expect_exit 0 "config plan" -- anas config plan -w "$ws" --root "$ROOT_DIR" --json
expect_exit 0 "config explain" -- anas config explain global.base_domain --root "$ROOT_DIR" --json
expect_exit 0 "backup capabilities" -- anas backup capabilities -w "$ws" --json

echo "== C3: the workspace announcement is on stderr, not in the document =="
anas start -w "$ws" --json >"$work/stdout" 2>"$work/stderr" </dev/null || true
if ! grep -q "^workspace: " "$work/stderr"; then
  fail "start did not announce the workspace on stderr"
fi
if grep -q "^workspace: " "$work/stdout"; then
  fail "the workspace announcement reached stdout, where only the document belongs"
fi

echo "== C4: -w is honoured wherever it appears =="
# `config secret get KEY -w <workspace>` parsed with the standard flag package
# stopped at KEY and silently ignored -w, reading another deployment's secrets.
# The two orders must agree.
#
# Both orders must reach the workspace named by -w. Before the fix, the
# trailing form ignored -w, resolved from the current directory instead, and
# so failed with a usage error about not finding a workspace at all rather
# than with secret_missing inside the one that was asked for.
before=$(anas config secret get NO_SUCH -w "$ws" --json 2>/dev/null || true)
after=$(anas config secret get -w "$ws" NO_SUCH --json 2>/dev/null || true)
if [ "$before" != "$after" ]; then
  fail "config secret get gives different answers depending on where -w appears"
fi
if ! printf '%s' "$before" | grep -q '"code": "secret_missing"'; then
  fail "config secret get -w did not reach the named workspace; got: $before"
fi

echo "== C1: human output is not JSON, and JSON output is only JSON =="
anas status -w "$ws" >"$work/human" 2>/dev/null </dev/null || true
if head -c 1 "$work/human" | grep -q '{'; then
  fail "status without --json emitted JSON; the human form is not a contract but must not impersonate one"
fi

if [ "$failures" -ne 0 ]; then
  echo "FAILURES: $failures (see $log)" >&2
  exit 1
fi
echo "PASS: contract conformance ($(grep -c '^--- ' "$log") assertions)"
