#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"
PYTHON=${PYTHON:-$(command -v python3)}
ws="$RUNTIME_DIR/managed-config-e2e"
source_config="$RUNTIME_DIR/managed-config-source.yml"
log="$REPORT_DIR/managed-config-e2e.log"

file_digest() {
  "$PYTHON" - "$1" <<'PY'
import hashlib, sys
print(hashlib.sha256(open(sys.argv[1], "rb").read()).hexdigest())
PY
}

# The external source may carry a one-time lifecycle password. Controlled
# import must remove it from workspace config and persist it in secrets.yml.
sed '/^global:/i\
    administration:\
      local_accounts:\
        primary:\
          password: Import-Only-Local-Password-1!\
' "$CONFIG_DIR/local-admin.yml" >"$source_config"

rm -rf "$ws"
run_anas init "$ws" -y >/dev/null
run_anas config import "$source_config" -w "$ws" >/dev/null

{
  echo "== controlled import splits lifecycle credentials =="
  if grep -q 'Import-Only-Local-Password-1!' "$ws/config.yml"; then
    echo "lifecycle password remained in managed config" >&2
    exit 1
  fi
  grep -q 'Import-Only-Local-Password-1!' "$ws/.anas/secrets.yml"

  echo "== local administrator usernames cannot be imported =="
  rejected="$RUNTIME_DIR/managed-config-rejected.yml"
  sed '/password: Import-Only-Local-Password-1!/i\
          username: custom_admin' "$source_config" >"$rejected"
  before=$(file_digest "$ws/config.yml")
  if run_anas config import "$rejected" -w "$ws" >/dev/null 2>&1; then
    echo "username override import unexpectedly succeeded" >&2
    exit 1
  fi
  after=$(file_digest "$ws/config.yml")
  [ "$before" = "$after" ]

  echo "== config set reports a real execution state =="
  result=$(run_anas config set global.timezone UTC -w "$ws" --root "$ROOT_DIR" --json)
  RESULT_JSON=$result "$PYTHON" - <<'PY'
import json, os
doc = json.loads(os.environ["RESULT_JSON"])
assert doc["execution"]["status"] == "pending_initial_apply", doc
assert doc["setting"]["executor"] == "deployment_apply_fallback", doc
assert doc["setting"]["editable"] is True, doc
PY

  echo "== unsafe effects are rejected before config changes =="
  before=$(file_digest "$ws/config.yml")
  if run_anas config set global.base_domain changed.example -w "$ws" --root "$ROOT_DIR" >/dev/null 2>&1; then
    echo "immutable config set unexpectedly succeeded" >&2
    exit 1
  fi
  after=$(file_digest "$ws/config.yml")
  [ "$before" = "$after" ]

  echo "== OIDC is the default for capable modules =="
  oidc_source="$RUNTIME_DIR/managed-config-oidc.yml"
  cat >"$oidc_source" <<'YAML'
modules:
  authentik: {}
  meshcentral: {}
  nextcloud: {}
  netbird: {}
global:
  base_domain: test.example
  email: admin@test.example
identity:
  directory:
    provider: samba_dc
  iam:
    provider: authentik
YAML
  oidc_ws="$RUNTIME_DIR/managed-config-oidc"
  rm -rf "$oidc_ws"
  run_anas init "$oidc_ws" -y >/dev/null
  run_anas config import "$oidc_source" -w "$oidc_ws" >/dev/null
  plan=$(run_anas plan -w "$oidc_ws" --root "$ROOT_DIR" --json)
  PLAN_JSON=$plan "$PYTHON" - <<'PY'
import json, os
doc = json.loads(os.environ["PLAN_JSON"])
protocols = {item["module"]: item["interface"] for item in doc["iam"]["consumers"]}
assert protocols["netbird"] == "oidc", protocols
assert protocols["nextcloud"] == "oidc", protocols
assert protocols["meshcentral"] == "oidc", protocols
PY
} >"$log" 2>&1

cat "$log"
echo "PASS: managed config, local admin and OIDC defaults end to end"
