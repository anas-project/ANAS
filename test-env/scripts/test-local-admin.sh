#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"
PYTHON=${PYTHON:-$(command -v python3)}

ws="$RUNTIME_DIR/local-admin-e2e"
config="$CONFIG_DIR/local-admin.yml"
log="$REPORT_DIR/local-admin-e2e.log"
make_workspace "$ws" "$config"

{
  echo "== plan does not pull an IAM gate =="
  plan=$(run_anas plan -c "$ws/config.yml")
  printf '%s\n' "$plan"
  if printf '%s\n' "$plan" | grep -q '^oauth2_proxy$'; then
    echo "ddns_go unexpectedly pulled oauth2_proxy" >&2
    exit 1
  fi

  echo "== render creates one managed local credential =="
  run_anas render -w "$ws" --update-lock
  deployment=$(find "$(ws_deployments "$ws")" -mindepth 1 -maxdepth 1 -type d | sort | tail -1)
  env_file="$deployment/casks/ddns_go/.env"
  grep -q '^DDNS_GO_LOCAL_ADMIN_USERNAME=admin_ddns_go$' "$env_file"
  grep -q '^DDNS_GO_PASSWORD_HASH=' "$env_file"
  if grep -q '^DDNS_GO_LOCAL_ADMIN_PASSWORD=' "$env_file"; then
    echo "local administrator plaintext leaked into the rendered env" >&2
    exit 1
  fi
  if grep -q '^ANAS_TRAEFIK_ROUTE__DDNS_GO__MIDDLEWARES=' "$env_file"; then
    echo "ddns_go route still carries an external authentication middleware" >&2
    exit 1
  fi

  echo "== inventory hides the password =="
  inventory=$(run_anas admin local list -w "$ws" --json)
  printf '%s\n' "$inventory"
  printf '%s\n' "$inventory" | grep -q 'admin_ddns_go'
  if printf '%s\n' "$inventory" | grep -q '"password"'; then
    echo "local administrator inventory exposed a password" >&2
    exit 1
  fi

  echo "== explicit credential lookup returns the managed password =="
  credential=$(run_anas admin local credential ddns_go -w "$ws" --json)
  CREDENTIAL_JSON=$credential "$PYTHON" - <<'PY'
import json, os
doc = json.loads(os.environ["CREDENTIAL_JSON"])["account"]
assert doc["username"] == "admin_ddns_go", doc
assert doc["purpose"] == "primary", doc
assert len(doc["password"]) == 24, doc
PY
  echo "credential lookup validated without writing the password to the report"
} >"$log" 2>&1

cat "$log"
echo "PASS: managed local administrator end to end"
