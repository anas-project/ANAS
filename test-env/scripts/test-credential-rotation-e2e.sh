#!/usr/bin/env sh
# End-to-end credential inventory and rotation through a real ANAS process,
# Eturnal Hook, deployment artifacts, Secret Store and Compose command boundary.
#
# This suite is deliberately local and deterministic: it never opens SSH and
# never selects a server Docker socket. The fake Docker boundary models the
# live Eturnal credential projection and supports one-shot activation failure
# injection, allowing compensation to be tested without touching a host.
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

PYTHON=${PYTHON:-$(command -v python3)}
anas_bin="$ROOT_DIR/.anas-test/bin/anas"
base="$RUNTIME_DIR/credential-rotation-e2e"
ws="$base/workspace"
fixture_root="$base/module-root"
config="$CONFIG_DIR/credential-rotation.yml"
docker_log="$base/docker.log"
live_secret_file="$base/live-eturnal-secret"
mkdir -p "$(dirname -- "$anas_bin")"
rm -rf "$base"
mkdir -p "$base" "$fixture_root/modules" "$fixture_root/contracts"
go build -o "$anas_bin" ./cmd/anas

# Keep the production credential declaration and Hook byte-for-byte while
# excluding the reverse-proxy dependency, whose local-admin verification is a
# separate E2E concern and performs a real HTTPS request. A temporary module
# root makes that test scoping explicit without changing the built-in module.
cp "$ROOT_DIR/go.mod" "$ROOT_DIR/go.sum" "$fixture_root/"
cp -R "$ROOT_DIR/modules/eturnal" "$fixture_root/modules/eturnal"
awk '
  /^dependencies:$/ { skipping = 1; next }
  skipping && /^[^[:space:]]/ { skipping = 0 }
  !skipping { print }
' "$ROOT_DIR/modules/eturnal/module.yml" >"$fixture_root/modules/eturnal/module.yml.tmp"
mv "$fixture_root/modules/eturnal/module.yml.tmp" "$fixture_root/modules/eturnal/module.yml"

export PATH="$TEST_ENV_DIR/fakes:$PATH"
export ANAS_FAKE_DOCKER_LOG="$docker_log"
export ANAS_FAKE_DOCKER_ETURNAL_SECRET_FILE="$live_secret_file"
export ANAS_FAKE_DOCKER_ETURNAL_MISSING_ONCE_MARKER="$base/probe-missing-once.marker"
: >"$docker_log"

anas() {
  "$anas_bin" "$@"
}

fail() {
  echo "credential rotation E2E failed: $*" >&2
  exit 1
}

active_deployment() {
  sed -n 's/^active_deployment: //p' "$ws/.anas/state/active.yml"
}

read_secret() {
  secret_document="$base/secret.json"
  anas config secret get TURN_SECRET -w "$ws" --json >"$secret_document" 2>"$base/secret.err"
  "$PYTHON" - "$secret_document" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as stream:
    document = json.load(stream)
value = document.get("value", "")
assert value, document
print(value)
PY
}

capture_exit() {
  label=$1
  expected=$2
  shift 2
  captured_stdout="$base/$label.stdout"
  captured_stderr="$base/$label.stderr"
  set +e
  "$@" >"$captured_stdout" 2>"$captured_stderr" </dev/null
  actual=$?
  set -e
  [ "$actual" -eq "$expected" ] || {
    echo "$label: expected exit $expected, got $actual" >&2
    sed -n '1,160p' "$captured_stdout" >&2
    sed -n '1,160p' "$captured_stderr" >&2
    exit 1
  }
}

assert_no_secret() {
  secret=$1
  shift
  for file in "$@"; do
    [ -f "$file" ] || continue
    if grep -Fq -- "$secret" "$file"; then
      fail "credential plaintext reached $(basename -- "$file")"
    fi
  done
}

assert_eturnal_remove_before_up() {
  LOG_FILE="$docker_log" PROJECT_NAME=anas_cred_eturnal "$PYTHON" - <<'PY'
import os, re
project = os.environ["PROJECT_NAME"]
operations = []
with open(os.environ["LOG_FILE"], encoding="utf-8") as stream:
    for line in stream:
        if f"--project-name {project}" not in line:
            continue
        if re.search(r"\sdown(?:\s|$)", line):
            operations.append("down")
        elif re.search(r"\sup(?:\s|$)", line):
            operations.append("up")
assert "up" in operations, operations
removed = False
for operation in operations:
    if operation == "down":
        removed = True
    elif not removed:
        raise AssertionError(f"up without preceding down: {operations}")
    else:
        removed = False
PY
  if grep -F -- '--force-recreate' "$docker_log" >/dev/null; then
    fail 'deployment transition used redundant --force-recreate'
  fi
  if grep -Eq 'docker restart .*eturnal|docker compose .* restart( |$)' "$docker_log"; then
    fail 'deployment transition restarted Eturnal instead of replacing its Compose project'
  fi
}

assert_eturnal_artifact_transition() {
  previous_id=$1
  target_id=$2
  LOG_FILE="$docker_log" PROJECT_NAME=anas_cred_eturnal \
    PREVIOUS_ID="$previous_id" TARGET_ID="$target_id" "$PYTHON" - <<'PY'
import os, re

project = os.environ["PROJECT_NAME"]
previous_suffix = f"/.anas/deployments/{os.environ['PREVIOUS_ID']}/modules/eturnal"
target_suffix = f"/.anas/deployments/{os.environ['TARGET_ID']}/modules/eturnal"
events = []
with open(os.environ["LOG_FILE"], encoding="utf-8") as stream:
    for raw_line in stream:
        working_dir, separator, command = raw_line.rstrip("\n").partition("|docker ")
        if not separator or f"--project-name {project}" not in command:
            continue
        if re.search(r"\sdown(?:\s|$)", command):
            events.append(("down", working_dir))
        elif re.search(r"\sup(?:\s|$)", command):
            events.append(("up", working_dir))

previous_down = next(
    (index for index, event in enumerate(events)
     if event[0] == "down" and event[1].endswith(previous_suffix)),
    None,
)
target_up = next(
    (index for index, event in enumerate(events)
     if event[0] == "up" and event[1].endswith(target_suffix)),
    None,
)
assert previous_down is not None, (previous_suffix, events)
assert target_up is not None, (target_suffix, events)
assert previous_down < target_up, events
PY
}

echo '== initialise and activate the real Eturnal credential declaration =='
anas init "$ws" -y >/dev/null
anas config import "$config" -w "$ws" >/dev/null
anas apply -w "$ws" --root "$fixture_root" --update-lock --no-snapshot -y >/dev/null
initial_deployment=$(active_deployment)
[ -n "$initial_deployment" ] || fail 'initial apply produced no active deployment'
[ -f "$ANAS_FAKE_DOCKER_ETURNAL_MISSING_ONCE_MARKER" ] || fail 'ready barrier did not exercise transient missing config'
if grep -Eq 'docker restart .*eturnal' "$docker_log"; then
  fail 'transient startup readiness restarted Eturnal instead of retrying probe'
fi
initial_secret=$(read_secret)
[ "$initial_secret" = "$(cat "$live_secret_file")" ] || fail 'initial ready barrier did not project the Store credential'

echo '== runtime drift uses Eturnal native reload without container restart =='
printf '%s' 'drifted-runtime-value' >"$live_secret_file"
: >"$docker_log"
anas start -w "$ws" >/dev/null
[ "$(cat "$live_secret_file")" = "$initial_secret" ] || fail 'Eturnal reload did not restore the deployment credential'
grep -F 'eturnalctl reload' "$docker_log" >/dev/null || fail 'Eturnal reconcile did not invoke native reload'
if grep -Eq 'docker restart .*eturnal|docker compose .* restart( |$)' "$docker_log"; then
  fail 'Eturnal reconcile restarted the container'
fi
if grep -E 'docker compose .*--project-name anas_cred_eturnal( |$).* down( |$)' "$docker_log" >/dev/null; then
  fail 'same-deployment drift repair removed the Eturnal container'
fi
assert_no_secret "$initial_secret" "$docker_log"

echo '== deployment apply and rollback remove the old container before up =='
: >"$docker_log"
anas config set eturnal.port 43479 -w "$ws" --root "$fixture_root" --json >"$base/config-set.json"
changed_deployment=$(active_deployment)
[ "$changed_deployment" != "$initial_deployment" ] || fail 'config set produced no new deployment'
assert_eturnal_remove_before_up
assert_eturnal_artifact_transition "$initial_deployment" "$changed_deployment"
[ "$(read_secret)" = "$initial_secret" ] || fail 'config-only apply changed the credential Store'
[ "$(cat "$live_secret_file")" = "$initial_secret" ] || fail 'config-only apply did not recreate Eturnal from its target env_file'
: >"$docker_log"
anas rollback "$initial_deployment" -w "$ws" --json >"$base/rollback-config.json"
[ "$(active_deployment)" = "$initial_deployment" ] || fail 'config rollback did not restore the original deployment'
assert_eturnal_remove_before_up
assert_eturnal_artifact_transition "$changed_deployment" "$initial_deployment"
[ "$(cat "$live_secret_file")" = "$initial_secret" ] || fail 'config rollback did not recreate Eturnal from the previous env_file'
assert_no_secret "$initial_secret" "$base/config-set.json" "$base/rollback-config.json" "$docker_log"

echo '== inventory is value-free and reports executable ownership =='
anas credential list -w "$ws" --json >"$base/list-initial.json"
anas credential list -w "$ws" >"$base/list-initial.txt"
INVENTORY="$base/list-initial.json" "$PYTHON" - <<'PY'
import json, os
with open(os.environ["INVENTORY"], encoding="utf-8") as stream:
    document = json.load(stream)
assert document["ok"] is True, document
assert len(document["credentials"]) == 1, document
item = document["credentials"][0]
assert item == {
    "id": "eturnal.secret",
    "owner": "eturnal",
    "consumers": [],
    "kind": "shared_secret",
    "authority": "anas",
    "generation": 1,
    "rotation_mode": "reconcile",
    "status": "rotatable",
}, item
PY
assert_no_secret "$initial_secret" "$base/list-initial.json" "$base/list-initial.txt" "$docker_log"

echo '== single, all, force and missing-target dry-runs share a value-free planner =='
before_dry_id=$(active_deployment)
before_dry_secret=$(read_secret)
before_dry_log_lines=$(wc -l <"$docker_log" | tr -d ' ')
anas credential rotate eturnal.secret -w "$ws" --dry-run --json >"$base/dry-single.json"
anas credential rotate --all -w "$ws" --dry-run --json >"$base/dry-all.json"
anas credential rotate eturnal.secret -w "$ws" --force --dry-run --json >"$base/dry-force.json"
anas credential rotate missing.secret -w "$ws" --dry-run --json >"$base/dry-missing.json"
DRY_SINGLE="$base/dry-single.json" DRY_ALL="$base/dry-all.json" \
DRY_FORCE="$base/dry-force.json" DRY_MISSING="$base/dry-missing.json" "$PYTHON" - <<'PY'
import json, os
def load(name):
    with open(os.environ[name], encoding="utf-8") as stream:
        return json.load(stream)
single = load("DRY_SINGLE")
assert single["dry_run"] is True and single["executable"] is True, single
assert single["plan"]["credential_order"] == ["eturnal.secret"], single
assert single["plan"]["affected_modules"] == ["eturnal"], single
assert single["plan"]["stop_order"] == ["eturnal"], single
assert single["plan"]["activation_order"] == ["eturnal"], single
all_plan = load("DRY_ALL")
assert all_plan["executable"] is True and all_plan["plan"]["all"] is True, all_plan
assert all_plan["plan"]["credential_order"] == ["eturnal.secret"], all_plan
assert all_plan["plan"]["affected_modules"] == ["eturnal"], all_plan
force = load("DRY_FORCE")
assert force["executable"] is True and force["plan"]["force"] is True, force
missing = load("DRY_MISSING")
assert missing["ok"] is True and missing["executable"] is False, missing
assert any(item.get("id") == "missing.secret" for item in missing["plan"]["blockers"]), missing
PY
[ "$(active_deployment)" = "$before_dry_id" ] || fail 'dry-run changed the active deployment'
[ "$(read_secret)" = "$before_dry_secret" ] || fail 'dry-run changed the Secret Store'
[ "$(wc -l <"$docker_log" | tr -d ' ')" = "$before_dry_log_lines" ] || fail 'dry-run called Docker'
assert_no_secret "$initial_secret" "$base/dry-single.json" "$base/dry-all.json" \
  "$base/dry-force.json" "$base/dry-missing.json"

echo '== runtime and confirmation preconditions fail before mutation =='
anas stop -w "$ws" >/dev/null
anas credential rotate eturnal.secret -w "$ws" --dry-run --json >"$base/dry-stopped.json"
DRY_STOPPED="$base/dry-stopped.json" "$PYTHON" - <<'PY'
import json, os
with open(os.environ["DRY_STOPPED"], encoding="utf-8") as stream:
    document = json.load(stream)
assert document["executable"] is False, document
assert any("runtime is not running" in item["reason"] for item in document["plan"]["blockers"]), document
PY
anas start -w "$ws" >/dev/null
capture_exit confirmation 3 anas credential rotate eturnal.secret -w "$ws" --json
CONFIRMATION="$base/confirmation.stdout" "$PYTHON" - <<'PY'
import json, os
with open(os.environ["CONFIRMATION"], encoding="utf-8") as stream:
    document = json.load(stream)
assert document["ok"] is False, document
assert document["error"]["code"] == "confirmation_required", document
PY
[ "$(active_deployment)" = "$initial_deployment" ] || fail 'confirmation refusal changed the active deployment'
[ "$(read_secret)" = "$initial_secret" ] || fail 'confirmation refusal changed the Secret Store'

echo '== candidate activation failure restores the previous runtime and Store =='
: >"$docker_log"
export ANAS_FAKE_DOCKER_FAIL_ONCE_MATCH='--project-name anas_cred_eturnal( |$).* up( |$)'
export ANAS_FAKE_DOCKER_FAIL_ONCE_MARKER="$base/fail-once.marker"
capture_exit candidate-failure 1 anas credential rotate eturnal.secret -w "$ws" -y --json
FAILURE="$base/candidate-failure.stdout" "$PYTHON" - <<'PY'
import json, os
with open(os.environ["FAILURE"], encoding="utf-8") as stream:
    document = json.load(stream)
assert document["ok"] is False, document
assert document["error"]["code"] == "credential_rotation_failed", document
rotation = document["error"]["detail"]["rotation"]
assert rotation["status"] == "previous_restored", rotation
assert rotation["previous_deployment"], rotation
assert rotation["candidate_deployment"], rotation
PY
[ -f "$ANAS_FAKE_DOCKER_FAIL_ONCE_MARKER" ] || fail 'candidate failure injection was not reached'
assert_eturnal_remove_before_up
[ "$(active_deployment)" = "$initial_deployment" ] || fail 'failed rotation did not restore the previous deployment'
[ "$(read_secret)" = "$initial_secret" ] || fail 'failed rotation changed the committed Secret Store'
[ "$(cat "$live_secret_file")" = "$initial_secret" ] || fail 'failed rotation did not restore the live Eturnal credential'
assert_no_secret "$initial_secret" "$base/candidate-failure.stdout" "$base/candidate-failure.stderr" "$docker_log"

echo '== successful single rotation commits generation and promotes candidate =='
unset ANAS_FAKE_DOCKER_FAIL_ONCE_MATCH ANAS_FAKE_DOCKER_FAIL_ONCE_MARKER
: >"$docker_log"
anas credential rotate eturnal.secret -w "$ws" -y --json >"$base/rotate-single.json" 2>"$base/rotate-single.stderr"
single_deployment=$(active_deployment)
single_secret=$(read_secret)
[ "$single_deployment" != "$initial_deployment" ] || fail 'successful rotation did not promote a candidate deployment'
[ "$single_secret" != "$initial_secret" ] || fail 'successful rotation reused the previous credential'
[ "$single_secret" = "$(cat "$live_secret_file")" ] || fail 'successful rotation did not reconcile the live Eturnal credential'
assert_eturnal_remove_before_up
assert_eturnal_artifact_transition "$initial_deployment" "$single_deployment"
ROTATION="$base/rotate-single.json" "$PYTHON" - <<'PY'
import json, os
with open(os.environ["ROTATION"], encoding="utf-8") as stream:
    document = json.load(stream)
assert document["ok"] is True, document
assert document["rotation"]["status"] == "complete", document
assert document["plan"]["credential_order"] == ["eturnal.secret"], document
PY
anas credential list -w "$ws" --json >"$base/list-generation-2.json"
GENERATION_TWO="$base/list-generation-2.json" "$PYTHON" - <<'PY'
import json, os
with open(os.environ["GENERATION_TWO"], encoding="utf-8") as stream:
    document = json.load(stream)
item = document["credentials"][0]
assert item["generation"] == 2 and item["status"] == "rotatable", item
PY
assert_no_secret "$initial_secret" "$base/rotate-single.json" "$base/rotate-single.stderr" "$docker_log"
assert_no_secret "$single_secret" "$base/rotate-single.json" "$base/rotate-single.stderr" "$base/list-generation-2.json" "$docker_log"

echo '== old deployment cannot be reactivated across a committed generation =='
capture_exit rollback-old 4 anas rollback "$initial_deployment" -w "$ws" --allow-risky --json
ROLLBACK="$base/rollback-old.stdout" "$PYTHON" - <<'PY'
import json, os
with open(os.environ["ROLLBACK"], encoding="utf-8") as stream:
    document = json.load(stream)
assert document["ok"] is False, document
assert document["error"]["code"] == "credential_store_mismatch", document
PY
[ "$(active_deployment)" = "$single_deployment" ] || fail 'blocked rollback changed the active deployment'
[ "$(read_secret)" = "$single_secret" ] || fail 'blocked rollback changed the Secret Store'

echo '== rotate --all uses the full deployment closure and advances again =='
: >"$docker_log"
anas credential rotate --all -w "$ws" -y --json >"$base/rotate-all.json" 2>"$base/rotate-all.stderr"
all_deployment=$(active_deployment)
all_secret=$(read_secret)
[ "$all_deployment" != "$single_deployment" ] || fail 'rotate --all did not promote a candidate deployment'
[ "$all_secret" != "$single_secret" ] || fail 'rotate --all reused the previous credential'
[ "$all_secret" = "$(cat "$live_secret_file")" ] || fail 'rotate --all did not update live Eturnal state'
assert_eturnal_remove_before_up
assert_eturnal_artifact_transition "$single_deployment" "$all_deployment"
anas credential list -w "$ws" --json >"$base/list-generation-3.json"
ROTATE_ALL="$base/rotate-all.json" INVENTORY="$base/list-generation-3.json" "$PYTHON" - <<'PY'
import json, os
with open(os.environ["ROTATE_ALL"], encoding="utf-8") as stream:
    rotation = json.load(stream)
with open(os.environ["INVENTORY"], encoding="utf-8") as stream:
    inventory = json.load(stream)
assert rotation["rotation"]["status"] == "complete", rotation
assert rotation["plan"]["all"] is True, rotation
assert rotation["plan"]["affected_modules"] == ["eturnal"], rotation
item = inventory["credentials"][0]
assert item["generation"] == 3 and item["status"] == "rotatable", item
PY
assert_no_secret "$single_secret" "$base/rotate-all.json" "$base/rotate-all.stderr" "$docker_log"
assert_no_secret "$all_secret" "$base/rotate-all.json" "$base/rotate-all.stderr" "$base/list-generation-3.json" "$docker_log"

anas stop -w "$ws" >/dev/null
echo 'PASS: credential inventory and rotation end to end'
