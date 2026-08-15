#!/usr/bin/env sh
# End-to-end checks for the runtime behavior behind every change-effect class.
# The CLI, config importer, module hooks, renderer, deployment manifests and
# activation diff are real. Docker/Compose is a logged command boundary so the
# suite is deterministic, offline, and safe to run on a developer workstation.
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

anas_bin="$ROOT_DIR/.anas-test/bin/anas"
fake_bin="$TEST_ENV_DIR/fakes"
light_ws="$RUNTIME_DIR/parameter-effects-light"
samba_ws="$RUNTIME_DIR/parameter-effects-samba"
full_ws="$RUNTIME_DIR/parameter-effects-full"
llng_ws="$RUNTIME_DIR/parameter-effects-llng"
light_source="$RUNTIME_DIR/parameter-effects-light.yml"
samba_source="$RUNTIME_DIR/parameter-effects-samba.yml"
samba_data_change="$RUNTIME_DIR/parameter-effects-samba-data-change.yml"
samba_domain_change="$RUNTIME_DIR/parameter-effects-samba-domain-change.yml"
samba_credential_change="$RUNTIME_DIR/parameter-effects-samba-credential-change.yml"
log="$REPORT_DIR/parameter-effects.log"
command_log="$REPORT_DIR/parameter-effects-docker.log"

mkdir -p "$(dirname -- "$anas_bin")"
go build -o "$anas_bin" ./cmd/anas

export PATH="$fake_bin:$PATH"
export ANAS_FAKE_DOCKER_LOG="$command_log"

anas() { "$anas_bin" "$@"; }
fail() { echo "FAIL: $*" >&2; exit 1; }
active_deployment() { sed -n 's/^active_deployment: //p' "$1/.anas/state/active.yml"; }
reset_commands() { : >"$command_log"; }
file_digest() {
  python3 - "$1" <<'PY'
import hashlib, sys
print(hashlib.sha256(open(sys.argv[1], "rb").read()).hexdigest())
PY
}

assert_json_effect() {
  document=$1
  effect=$2
  executor=$3
  RESULT_JSON=$document EXPECT_EFFECT=$effect EXPECT_EXECUTOR=$executor python3 - <<'PY'
import json, os
doc = json.loads(os.environ["RESULT_JSON"])
assert doc["execution"]["status"] == "applied", doc
assert doc["setting"]["effect"] == os.environ["EXPECT_EFFECT"], doc
assert doc["setting"]["executor"] == os.environ["EXPECT_EXECUTOR"], doc
assert doc["execution"]["deployment_id"] != doc["execution"]["previous_deployment"], doc
PY
}

assert_active_env() {
  workspace=$1
  module=$2
  key=$3
  value=$4
  deployment=$(active_deployment "$workspace")
  [ -n "$deployment" ] || fail "$workspace has no active deployment"
  env_file="$workspace/.anas/deployments/$deployment/modules/$module/.env"
  [ -f "$env_file" ] || fail "$module has no rendered environment in $deployment"
  if [ "$value" = __ABSENT__ ]; then
    if grep -q "^$key=" "$env_file"; then
      fail "$key should be omitted for $module"
    fi
    return
  fi
  grep -Fqx "$key=$value" "$env_file" || fail "$key did not render as $value for $module"
}

assert_compose_action() {
  module=$1
  action=$2
  grep -E "docker compose .*--project-name anas_${module}( |$).* ${action}( |$)" "$command_log" >/dev/null ||
    fail "Compose $action was not invoked for $module"
}

assert_no_compose_action() {
  action=$1
  if grep -E "docker compose .* (${action})( |$)" "$command_log" >/dev/null; then
    fail "unexpected Compose $action action"
  fi
}

assert_no_runtime_mutation() {
  if grep -E 'docker compose .* (build|up|down|start|stop|restart)( |$)|docker network (create|rm)( |$)|^.*\|sudo ' "$command_log" >/dev/null; then
    fail "a guarded parameter reached the runtime boundary"
  fi
}

assert_no_container_mutation() {
  if grep -E 'docker compose .* (build|up|down|start|stop|restart)( |$)|docker network (create|rm)( |$)' "$command_log" >/dev/null; then
    fail "an unchanged parameter mutated containers or Docker networks"
  fi
}

run_fallback_case() {
  workspace=$1
  path=$2
  value=$3
  effect=$4
  module=$5
  env_key=$6
  rendered_value=$7
  reset_commands
  result=$(anas config set "$path" "$value" -w "$workspace" --root "$ROOT_DIR" --json)
  assert_json_effect "$result" "$effect" deployment_apply_fallback
  assert_active_env "$workspace" "$module" "$env_key" "$rendered_value"
  assert_compose_action "$module" up
  assert_no_compose_action build
}

# Large application fixtures have local-administrator verification that must
# talk to a real service after the first start. This suite deliberately fakes
# Docker, so establish the rendered baseline as the already-running deployment
# and let the parameter changes exercise only their real activation diff.
# Real local-account startup is covered by test-local-admin.sh and the server
# E2Es; pretending an HTTPS dashboard exists here would weaken both tests.
mark_rendered_running() {
  workspace=$1
  deployment=$(ls -1dt "$workspace/.anas/deployments"/* | head -1)
  deployment=$(basename "$deployment")
  mkdir -p "$workspace/.anas/state"
  cat >"$workspace/.anas/state/active.yml" <<EOF
api_version: anas.state/v2
active_deployment: $deployment
runtime_status: running
previous_deployments: []
EOF
}

cat >"$light_source" <<'YAML'
modules:
  lego:
    config:
      dns_provider: cloudflare
global:
  base_domain: effects.test
  email: admin@effects.test
  timezone: Asia/Shanghai
  virtual_domain: true
env:
  CONTAINER_PREFIX: anaspe_
  NETWORK_PREFIX: anaspe_
  HOST_IP: 192.0.2.10
  INTERFACE: anas-test0
  HOST_SUBNET_MASK: "24"
  DEFAULT_GATEWAY_IP: 192.0.2.1
  HOST_DNS_SERVER: 192.0.2.1
secrets:
  cloudflare_dns_api_token: test-cloudflare-token
  tencentcloud_secret_id: test-tencentcloud-id
  tencentcloud_secret_key: test-tencentcloud-key
YAML

cat >"$samba_source" <<'YAML'
modules:
  samba_dc:
    config:
      ldap_bind_password: Initial-LDAP-Password-1!
  samba_fs: {}
global:
  base_domain: effects.test
  email: admin@effects.test
  timezone: Asia/Shanghai
  virtual_domain: true
env:
  CONTAINER_PREFIX: anaspes_
  NETWORK_PREFIX: anaspes_
  HOST_IP: 192.0.2.10
  INTERFACE: anas-test0
  HOST_SUBNET_MASK: "24"
  DEFAULT_GATEWAY_IP: 192.0.2.1
  HOST_DNS_SERVER: 192.0.2.1
YAML

rm -rf "$light_ws" "$samba_ws" "$full_ws" "$llng_ws"
anas init "$light_ws" -y >/dev/null
anas config import "$light_source" -w "$light_ws" >/dev/null
reset_commands
anas apply -w "$light_ws" --root "$ROOT_DIR" --update-lock >/dev/null
assert_compose_action lego up

{
  echo "== container_recreate changes the artifact and reaches Compose up =="
  reset_commands
  result=$(anas config set global.timezone UTC -w "$light_ws" --root "$ROOT_DIR" --json)
  assert_json_effect "$result" container_recreate deployment_apply_fallback
  assert_active_env "$light_ws" lego TZ UTC
  assert_compose_action lego up
  assert_no_compose_action build

  echo "== reconcile uses the declared deployment fallback and renders the value =="
  reset_commands
  result=$(anas config set global.default_language zh-Hans -w "$light_ws" --root "$ROOT_DIR" --json)
  assert_json_effect "$result" reconcile deployment_apply_fallback
  assert_active_env "$light_ws" lego DEFAULT_LANGUAGE zh-Hans
  assert_compose_action lego up
  assert_no_compose_action build

  echo "== image_rebuild runs Compose build before activating the new artifact =="
  reset_commands
  result=$(anas config set global.chinese_build_speedup true -w "$light_ws" --root "$ROOT_DIR" --json)
  assert_json_effect "$result" image_rebuild deployment_build_apply
  assert_active_env "$light_ws" lego CHINESE_BUILD_SPEEDUP true
  assert_compose_action lego build
  assert_compose_action lego up
  build_line=$(grep -nE 'docker compose .*--project-name anas_lego( |$).* build( |$)' "$command_log" | head -1 | cut -d: -f1)
  up_line=$(grep -nE 'docker compose .*--project-name anas_lego( |$).* up( |$)' "$command_log" | head -1 | cut -d: -f1)
  [ "$build_line" -lt "$up_line" ] || fail "Compose up ran before image build"
  deployment=$(active_deployment "$light_ws")
  inspect=$(anas deployments inspect "$deployment" -w "$light_ws" --json)
  INSPECT_JSON=$inspect python3 - <<'PY'
import json, os
doc = json.loads(os.environ["INSPECT_JSON"])
assert doc["deployment"]["images_built"] is True, doc
PY

  echo "== hot_reload currently performs its documented deployment fallback =="
  anas init "$samba_ws" -y >/dev/null
  anas config import "$samba_source" -w "$samba_ws" >/dev/null
  reset_commands
  anas apply -w "$samba_ws" --root "$ROOT_DIR" --update-lock >/dev/null
  assert_compose_action samba_dc up
  assert_compose_action samba_fs up

  reset_commands
  result=$(anas config set samba_dc.user_min_pass_length 10 -w "$samba_ws" --root "$ROOT_DIR" --json)
  assert_json_effect "$result" hot_reload deployment_apply_fallback
  assert_active_env "$samba_ws" samba_dc SAMBA_DC_USER_MIN_PASS_LENGTH 10
  assert_compose_action samba_dc up
  if grep -E 'docker compose .*--project-name anas_samba_fs( |$).* up( |$)' "$command_log" >/dev/null; then
    fail "hot_reload restarted unchanged samba_fs"
  fi
  assert_no_compose_action build

  echo "== every hot_reload parameter reaches the current fallback =="
  run_fallback_case "$samba_ws" samba_dc.user_complex_pass false hot_reload samba_dc SAMBA_DC_USER_COMPLEX_PASS false
  run_fallback_case "$samba_ws" samba_dc.user_min_pass_length 9 hot_reload samba_dc SAMBA_DC_USER_MIN_PASS_LENGTH 9
  run_fallback_case "$samba_ws" samba_dc.user_password_history 5 hot_reload samba_dc SAMBA_DC_USER_PASSWORD_HISTORY 5
  run_fallback_case "$samba_ws" samba_dc.user_max_pass_age 91 hot_reload samba_dc SAMBA_DC_USER_MAX_PASS_AGE 91
  run_fallback_case "$samba_ws" samba_dc.user_min_pass_age 0 hot_reload samba_dc SAMBA_DC_USER_MIN_PASS_AGE 0
  run_fallback_case "$samba_ws" samba_dc.user_lockout_threshold 11 hot_reload samba_dc SAMBA_DC_USER_LOCKOUT_THRESHOLD 11
  run_fallback_case "$samba_ws" samba_dc.user_lockout_duration 31 hot_reload samba_dc SAMBA_DC_USER_LOCKOUT_DURATION 31
  run_fallback_case "$samba_ws" samba_dc.user_lockout_reset_after 31 hot_reload samba_dc SAMBA_DC_USER_LOCKOUT_RESET_AFTER 31

  echo "== setting the same value is runtime-idempotent =="
  reset_commands
  export ANAS_FAKE_DOCKER_NETWORK_EXISTS=true
  result=$(anas config set samba_dc.user_lockout_reset_after 31 -w "$samba_ws" --root "$ROOT_DIR" --json)
  unset ANAS_FAKE_DOCKER_NETWORK_EXISTS
  assert_json_effect "$result" hot_reload deployment_apply_fallback
  assert_active_env "$samba_ws" samba_dc SAMBA_DC_USER_LOCKOUT_RESET_AFTER 31
  assert_no_container_mutation

  echo "== a failed fallback restores the active deployment and managed config =="
  before=$(active_deployment "$samba_ws")
  before_config=$(file_digest "$samba_ws/config.yml")
  before_managed=$(file_digest "$samba_ws/.anas/config-managed.yml")
  failure_marker="$RUNTIME_DIR/parameter-effects-fallback-failed"
  rm -f "$failure_marker"
  export ANAS_FAKE_DOCKER_FAIL_ONCE_MATCH='--project-name anas_samba_dc .* up'
  export ANAS_FAKE_DOCKER_FAIL_ONCE_MARKER="$failure_marker"
  reset_commands
  if anas config set samba_dc.user_lockout_reset_after 32 -w "$samba_ws" --root "$ROOT_DIR" \
    >"$RUNTIME_DIR/parameter-effects-fallback-failure.out" 2>&1; then
    unset ANAS_FAKE_DOCKER_FAIL_ONCE_MATCH ANAS_FAKE_DOCKER_FAIL_ONCE_MARKER
    fail "fault-injected fallback unexpectedly succeeded"
  fi
  unset ANAS_FAKE_DOCKER_FAIL_ONCE_MATCH ANAS_FAKE_DOCKER_FAIL_ONCE_MARKER
  [ -f "$failure_marker" ] || fail "fault injection did not reach samba_dc Compose up"
  grep -q 'exit status 97' "$RUNTIME_DIR/parameter-effects-fallback-failure.out" ||
    fail "fallback failure did not report the failed runtime action"
  [ "$(active_deployment "$samba_ws")" = "$before" ] ||
    fail "failed fallback changed the active deployment"
  [ "$(file_digest "$samba_ws/config.yml")" = "$before_config" ] ||
    fail "failed fallback did not restore config.yml"
  [ "$(file_digest "$samba_ws/.anas/config-managed.yml")" = "$before_managed" ] ||
    fail "failed fallback did not restore the managed config digest"
  assert_active_env "$samba_ws" samba_dc SAMBA_DC_USER_LOCKOUT_RESET_AFTER 31
  samba_up_count=$(grep -Ec 'docker compose .*--project-name anas_samba_dc( |$).* up( |$)' "$command_log")
  [ "$samba_up_count" -ge 2 ] || fail "failed fallback did not restart the prior deployment"

  echo "== every reconcile parameter reaches the current fallback =="
  run_fallback_case "$samba_ws" samba_fs.share_access_mode all_rw reconcile samba_fs SHARE_ACCESS_MODE all_rw
  run_fallback_case "$samba_ws" samba_fs.share_guest_read_only Yes reconcile samba_fs SHARE_GUEST_READ_ONLY Yes
  run_fallback_case "$light_ws" global.default_language de-DE reconcile lego DEFAULT_LANGUAGE de-DE
  run_fallback_case "$light_ws" global.default_locale fr-FR reconcile lego DEFAULT_LOCALE fr-FR
  run_fallback_case "$light_ws" lego.dns_provider tencentcloud reconcile lego LEGO_DNS_PROVIDER tencentcloud
  run_fallback_case "$light_ws" global.virtual_domain false reconcile lego VIRTUAL_DOMAIN __ABSENT__

  anas init "$full_ws" -y >/dev/null
  anas config import "$CONFIG_DIR/matrix-apps.yml" -w "$full_ws" >/dev/null
  anas render -w "$full_ws" --update-lock >/dev/null
  mark_rendered_running "$full_ws"
  run_fallback_case "$full_ws" authentik.domain_prefix auth2 reconcile authentik AUTHENTIK_DOMAIN_PREFIX auth2
  run_fallback_case "$full_ws" nextcloud.domain_prefix files2 reconcile nextcloud NEXTCLOUD_DOMAIN_PREFIX files2
  run_fallback_case "$full_ws" nextcloud.language de-DE reconcile nextcloud NEXTCLOUD_LANGUAGE de_DE
  run_fallback_case "$full_ws" nextcloud.locale de-DE reconcile nextcloud NEXTCLOUD_LOCALE de_DE
  run_fallback_case "$full_ws" nextcloud.memories_enabled false reconcile nextcloud NEXTCLOUD_MEMORIES_ENABLED false

  anas init "$llng_ws" -y >/dev/null
  anas config import "$CONFIG_DIR/matrix-auth.yml" -w "$llng_ws" >/dev/null
  anas render -w "$llng_ws" --update-lock >/dev/null
  mark_rendered_running "$llng_ws"
  run_fallback_case "$llng_ws" llng.domain_prefix login2 reconcile llng LLNG_DOMAIN_PREFIX login2

  echo "== credential_rotate refuses replacement import without touching runtime =="
  sed 's/Initial-LDAP-Password-1!/Replacement-LDAP-Password-2!/' \
    "$samba_source" >"$samba_credential_change"
  before_secret=$(file_digest "$samba_ws/.anas/secrets.yml")
  before_config=$(file_digest "$samba_ws/config.yml")
  reset_commands
  if anas config set samba_dc.ldap_bind_password Replacement-LDAP-Password-2! \
    -w "$samba_ws" --root "$ROOT_DIR" >"$RUNTIME_DIR/parameter-effects-credential-set.out" 2>&1; then
    fail "credential config set unexpectedly succeeded"
  fi
  grep -q 'lifecycle-managed' "$RUNTIME_DIR/parameter-effects-credential-set.out" ||
    fail "credential config-set refusal did not name the lifecycle constraint"
  [ "$(file_digest "$samba_ws/config.yml")" = "$before_config" ] ||
    fail "rejected credential config set changed managed config"
  [ "$(file_digest "$samba_ws/.anas/secrets.yml")" = "$before_secret" ] ||
    fail "rejected credential config set changed the Secret Store"

  if anas config import "$samba_credential_change" -w "$samba_ws" >"$RUNTIME_DIR/parameter-effects-credential.out" 2>&1; then
    fail "credential replacement import unexpectedly succeeded"
  fi
  grep -q 'declared credential rotation command' "$RUNTIME_DIR/parameter-effects-credential.out" ||
    fail "credential refusal did not name the lifecycle operation"
  after_secret=$(file_digest "$samba_ws/.anas/secrets.yml")
  [ "$before_secret" = "$after_secret" ] || fail "rejected credential import changed the Secret Store"
  assert_no_runtime_mutation

  echo "== data_migrate materializes a candidate but blocks activation =="
  awk '
    /ldap_bind_password:/ { next }
    /^  samba_fs: \{\}$/ {
      print "  samba_fs:"
      print "    config:"
      print "      hostname: ChangedFS"
      next
    }
    { print }
  ' "$samba_source" >"$samba_data_change"
  reset_commands
  if anas config set samba_fs.hostname ChangedFS -w "$samba_ws" --root "$ROOT_DIR" \
    >"$RUNTIME_DIR/parameter-effects-data-set.out" 2>&1; then
    fail "data_migrate config set unexpectedly succeeded"
  fi
  grep -q 'requires data migration (rejoin-samba-member)' "$RUNTIME_DIR/parameter-effects-data-set.out" ||
    fail "data migration config-set refusal did not name the migration"
  assert_no_runtime_mutation

  anas config import "$samba_data_change" -w "$samba_ws" >/dev/null
  before=$(active_deployment "$samba_ws")
  reset_commands
  if anas apply -w "$samba_ws" --root "$ROOT_DIR" --update-lock >"$RUNTIME_DIR/parameter-effects-data.out" 2>&1; then
    fail "data_migrate change unexpectedly activated"
  fi
  grep -q 'modules.samba_fs.config.hostname (data_migrate; rejoin-samba-member)' "$RUNTIME_DIR/parameter-effects-data.out" ||
    fail "data migration guard did not report the changed parameter"
  [ "$(active_deployment "$samba_ws")" = "$before" ] || fail "data migration guard changed the active deployment"
  assert_no_runtime_mutation

  echo "== immutable materializes a candidate but blocks activation =="
  sed '/ldap_bind_password:/d' "$samba_source" |
    sed 's/base_domain: effects.test/base_domain: replacement.test/' >"$samba_domain_change"
  anas config import "$samba_domain_change" -w "$samba_ws" >/dev/null
  before=$(active_deployment "$samba_ws")
  reset_commands
  if anas apply -w "$samba_ws" --root "$ROOT_DIR" --update-lock >"$RUNTIME_DIR/parameter-effects-immutable.out" 2>&1; then
    fail "immutable change unexpectedly activated"
  fi
  grep -q 'global.base_domain (immutable; migrate-domain)' "$RUNTIME_DIR/parameter-effects-immutable.out" ||
    fail "immutable guard did not report the changed parameter"
  [ "$(active_deployment "$samba_ws")" = "$before" ] || fail "immutable guard changed the active deployment"
  assert_no_runtime_mutation
} >"$log" 2>&1

cat "$log"
echo "PASS: parameter runtime effects reach or stop before the expected execution boundary"
