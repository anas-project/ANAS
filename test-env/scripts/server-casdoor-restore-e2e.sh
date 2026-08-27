#!/usr/bin/env bash
# Casdoor consistent snapshot backup and empty-workspace restore E2E.
set -Eeuo pipefail
umask 077

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
export ANAS_TEST_CONTAINER_PREFIX=${ANAS_TEST_CONTAINER_PREFIX:-anas_casdoor_}
# shellcheck source=server-iam-matrix-common.sh
source "$script_dir/server-iam-matrix-common.sh"

workspace=${ANAS_TEST_WORKSPACE:?ANAS_TEST_WORKSPACE is required}
anas_cmd=${ANAS_TEST_ANAS_CMD:-anas}
restore_root=${ANAS_TEST_RESTORE_ROOT:?ANAS_TEST_RESTORE_ROOT is required}
fixture_bin=${CASDOOR_LOGOUT_FIXTURE_BIN:-/home/whl/anas-casdoor-m3-e2e/casdoor-oidc-logout-consumer}
report_dir=${ANAS_TEST_REPORT_DIR:-$workspace/test-env/reports}
casdoor="${prefix}casdoor"
dirwatch="${prefix}casdoor_dirwatch"
consumer="${prefix}oauth2_proxy"
fixture_container="${prefix}casdoor_restore_consumer"
timeout=${CASDOOR_PROTOCOL_E2E_TIMEOUT:-420}
test_suffix=${ANAS_TEST_MATRIX_SUFFIX:-$(date +%H%M%S)}
test_user="icr${test_suffix}"
test_password=${ANAS_TEST_MATRIX_PASSWORD:-Anas-Iam-${test_suffix}-E2e!}
run_root=
backup_dir=
restore_workspace=
backup_id=
snapshot_id=
source_stopped=false
target_started=false
fixture_started=false
fixture_configured=false

case "$restore_root" in
  /*anas*casdoor*e2e*) ;;
  *) printf 'ANAS_TEST_RESTORE_ROOT must be an absolute Casdoor E2E path\n' >&2; exit 2 ;;
esac
test "$restore_root" != "$workspace"

section() { printf '\n== %s ==\n' "$1"; }

remove_test_subvolume() {
  local path=$1
  [ -n "$path" ] || return 0
  [ -e "$path" ] || return 0
  sudo -n btrfs subvolume show "$path" >/dev/null 2>&1 || return 0
  sudo -n btrfs subvolume delete "$path" >/dev/null 2>&1
}

remove_run_root() {
  [ -n "$run_root" ] || return 0
  case "$run_root" in
    "$restore_root"/run.*) ;;
    *) return 2 ;;
  esac
  if [ -n "$restore_workspace" ]; then
    remove_test_subvolume "$restore_workspace/data" || true
  fi
  if [ -n "$backup_id" ] && [ -n "$backup_dir" ]; then
    remove_test_subvolume "$backup_dir/$backup_id/data" || true
  fi
  sudo -n rm -rf -- "$run_root"
}

remove_source_snapshot() {
  local id=$1 path
  [[ "$id" =~ ^[0-9]{8}T[0-9]{6}Z-[a-f0-9]{8}$ ]]
  path=$workspace/snapshots/$id
  case "$path" in
    "$workspace"/snapshots/*) ;;
    *) return 2 ;;
  esac
  remove_test_subvolume "$path/data" || true
  remove_test_subvolume "$path/userdata" || true
  sudo -n rm -rf -- "$path"
}

remove_fixture() {
  "$docker_cmd" rm -f "$fixture_container" >/dev/null 2>&1 || true
  fixture_started=false
}

start_fixture() {
  remove_fixture
  "$docker_cmd" run -d --name "$fixture_container" \
    --network "container:$casdoor" \
    --user "$(id -u):$(id -g)" \
    --entrypoint /fixture \
    -v "$fixture_bin:/fixture:ro" \
    -v "$run_root:/state" \
    -e "CASDOOR_FIXTURE_ISSUER=$issuer" \
    -e CASDOOR_FIXTURE_INTERNAL_ORIGIN=http://127.0.0.1:8000 \
    -e CASDOOR_FIXTURE_APPLICATION=app-anas-oauth2-proxy \
    -e CASDOOR_FIXTURE_ORGANIZATION=anas \
    -e "CASDOOR_FIXTURE_CLIENT_ID=$client_id" \
    -e CASDOOR_FIXTURE_CLIENT_SECRET_FILE=/state/client-secret \
    -e CASDOOR_FIXTURE_REDIRECT_URI=http://127.0.0.1:18081/callback \
    -e CASDOOR_FIXTURE_BACKCHANNEL_URI=http://127.0.0.1:18081/backchannel \
    -e "CASDOOR_FIXTURE_MANAGED_REDIRECT_URIS=$managed_redirect_uris" \
    -e "CASDOOR_FIXTURE_MANAGED_BACKCHANNEL_URI=$managed_backchannel_uri" \
    "$fixture_image" serve >/dev/null
  fixture_started=true
  for _ in $(seq 1 30); do
    if "$docker_cmd" exec "$fixture_container" sh -c \
      'wget -q -O /dev/null http://127.0.0.1:18081/healthz'; then
      return 0
    fi
    sleep 1
  done
  return 1
}

fixture() {
  "$docker_cmd" exec "$fixture_container" /fixture "$@"
}

admin_fixture() {
  "$docker_cmd" exec "$casdoor" sh -c 'cat /run/secrets/casdoor-break-glass-password' |
    "$docker_cmd" exec -i "$fixture_container" /fixture "$@"
}

casdoor_user() {
  "$docker_cmd" exec "$dirwatch" /opt/anas/bin/casdoor-helper directory-watch \
    --get-user "anas/$test_user" 2>/dev/null || printf 'null\n'
}

wait_for_user() {
  local deadline current
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    current=$(casdoor_user)
    if printf '%s' "$current" | jq -e \
      '.name == "'"$test_user"'" and .isForbidden == false and .isDeleted == false and
       (.externalId | length) > 0 and (.groups | index("anas/Admins")) != null' >/dev/null 2>&1; then
      printf '%s\n' "$current"
      return 0
    fi
    sleep 5
  done
  printf 'Casdoor did not converge restore user %s; last state: %s\n' "$test_user" "$current" >&2
  return 1
}

wait_healthy() {
  local container=$1 deadline status
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    status=$("$docker_cmd" inspect --format \
      '{{.State.Status}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$container" 2>/dev/null || true)
    if [ "$status" = 'running|healthy' ] || [ "$status" = 'running|' ]; then
      return 0
    fi
    sleep 5
  done
  printf 'container %s did not become healthy; last state=%s\n' "$container" "$status" >&2
  return 1
}

workspace_active() {
  "$anas_cmd" status -w "$1" --json | jq -er '.active_deployment'
}

cleanup_user() {
  samba_tool user delete "$test_user" >/dev/null 2>&1 || true
}

cleanup() {
  local status=$?
  set +e
  remove_fixture
  if [ "$target_started" = true ] && [ -n "$restore_workspace" ]; then
    "$anas_cmd" stop -w "$restore_workspace" >/dev/null 2>&1
    target_started=false
  fi
  if [ "$source_stopped" = true ]; then
    "$anas_cmd" start -w "$workspace" >/dev/null 2>&1
    source_stopped=false
  fi
  if [ "$fixture_configured" = true ] && [ -n "$run_root" ]; then
    start_fixture >/dev/null 2>&1
    admin_fixture restore --backup /state/application-original.json >/dev/null 2>&1
    remove_fixture
    fixture_configured=false
  fi
  cleanup_user
  if [ -n "$snapshot_id" ]; then
    "$anas_cmd" snapshot delete "$snapshot_id" -w "$workspace" -y --json >/dev/null 2>&1
    [ ! -e "$workspace/snapshots/$snapshot_id" ] || remove_source_snapshot "$snapshot_id" || true
  fi
  if [ -n "$run_root" ]; then
    remove_run_root || true
  fi
  if [ "$status" -ne 0 ]; then
    printf 'FAIL: Casdoor restore E2E\n' >&2
  fi
  exit "$status"
}
trap cleanup EXIT HUP INT TERM

section "preflight and restore identity seed"
for container in "$dc" "$casdoor" "$dirwatch" "$consumer"; do
  wait_healthy "$container"
done
test -x "$fixture_bin"
command -v jq >/dev/null
install -d -m 0700 "$restore_root" "$report_dir"
run_root=$(mktemp -d "$restore_root/run.XXXXXX")
chmod 0700 "$run_root"
backup_dir=$run_root/backup
restore_workspace=$run_root/restored
install -d -m 0700 "$backup_dir"

issuer=$("$docker_cmd" exec "$casdoor" printenv CASDOOR_DOMAIN_FULL)
client_id=$("$docker_cmd" exec "$casdoor" printenv ANAS_IAM_CLIENT__OAUTH2_PROXY__CLIENT_ID)
managed_redirect_uris=$("$docker_cmd" exec "$casdoor" printenv ANAS_IAM_CLIENT__OAUTH2_PROXY__REDIRECT_URIS)
managed_backchannel_uri=$("$docker_cmd" exec "$casdoor" printenv ANAS_IAM_CLIENT__OAUTH2_PROXY__OIDC_LOGOUT_URI 2>/dev/null || true)
"$docker_cmd" exec "$casdoor" printenv ANAS_IAM_CLIENT__OAUTH2_PROXY__CLIENT_SECRET >"$run_root/client-secret"
printf '%s\n' "$test_password" >"$run_root/user-password"
chmod 0600 "$run_root/client-secret" "$run_root/user-password"
fixture_image=$("$docker_cmd" inspect --format '{{.Config.Image}}' "$casdoor")
start_fixture
admin_fixture configure --backup /state/application-original.json >/dev/null
fixture_configured=true

cleanup_user
samba_tool user add "$test_user" "$test_password" --userou='OU=People' \
  --mail-address="${test_user}@m5.nas.test" >/dev/null
samba_tool user setexpiry "$test_user" --noexpiry >/dev/null
samba_tool user rename "$test_user" --display-name="Casdoor restore E2E $test_user" >/dev/null
samba_tool group addmembers Admins "$test_user" >/dev/null
wait_anchor "$test_user"
directory_anchor=$(samba_tool user show "$test_user" --attributes=anasIdentityAnchor |
  sed -n 's/^anasIdentityAnchor: //p')
wait_for_user >"$run_root/source-user.json"
test "$(jq -er '.externalId' "$run_root/source-user.json")" = "$directory_anchor"
fixture login --username "$test_user" --password-file /state/user-password \
  --state-file /state/source-login.json >"$run_root/source-login.out"
source_sub=$(jq -er '.sub' "$run_root/source-login.json")
printf 'restore_seed=ready username=%s anchor=%s sub=%s\n' "$test_user" "$directory_anchor" "$source_sub"

section "consistent snapshot backup"
source_deployment=$(workspace_active "$workspace")
source_store_digest=$(sha256sum "$workspace/.anas/secrets.yml" | awk '{print $1}')
source_admin_digest=$(sha256sum "$workspace/.anas/local-admins.yml" | awk '{print $1}')
source_manifest_digest=$(sha256sum \
  "$workspace/.anas/deployments/$source_deployment/deployment.yml" | awk '{print $1}')
cursor_file=$workspace/data/casdoor/dirwatch/cursor.json
test -s "$cursor_file"
source_cursor_digest=$(sha256sum "$cursor_file" | awk '{print $1}')
source_client_digest=$(sha256sum "$run_root/client-secret" | awk '{print $1}')

"$anas_cmd" backup create -w "$workspace" --to "$backup_dir" --mode snapshot -y --json \
  >"$report_dir/casdoor-restore-backup-create.json" \
  2>"$report_dir/casdoor-restore-backup-create.log"
chmod 0600 "$report_dir"/casdoor-restore-backup-create.*
jq -e '.ok == true and .mode == "snapshot" and (.backup_id | length > 0)' \
  "$report_dir/casdoor-restore-backup-create.json" >/dev/null
backup_id=$(jq -r '.backup_id' "$report_dir/casdoor-restore-backup-create.json")
snapshot_id=$(jq -r '.snapshot_id // empty' "$report_dir/casdoor-restore-backup-create.json")
"$anas_cmd" backup verify --to "$backup_dir" --backup-id "$backup_id" --json \
  >"$report_dir/casdoor-restore-backup-verify.json"
chmod 0600 "$report_dir/casdoor-restore-backup-verify.json"
jq -e '.ok == true and (.problems | length == 0)' \
  "$report_dir/casdoor-restore-backup-verify.json" >/dev/null

section "restore into an empty workspace"
"$anas_cmd" init "$restore_workspace" -y >/dev/null
"$anas_cmd" backup restore --from "$backup_dir" -w "$restore_workspace" \
  --backup-id "$backup_id" --dry-run --json >"$report_dir/casdoor-restore-dry-run.json"
jq -e '.ok == true and .dry_run == true and (.would_replace | length > 0)' \
  "$report_dir/casdoor-restore-dry-run.json" >/dev/null
"$anas_cmd" backup restore --from "$backup_dir" -w "$restore_workspace" \
  --backup-id "$backup_id" -y --json >"$report_dir/casdoor-restore.json" \
  2>"$report_dir/casdoor-restore.log"
chmod 0600 "$report_dir"/casdoor-restore*.json "$report_dir"/casdoor-restore*.log
jq -e '.ok == true and .verify.ok == true and
  ([.restored[]] | index("data") != null) and
  ([.restored[]] | index("secrets") != null) and
  ([.restored[]] | index("active_deployment") != null)' \
  "$report_dir/casdoor-restore.json" >/dev/null

remove_fixture
"$anas_cmd" stop -w "$workspace" >"$report_dir/casdoor-restore-source-stop.log" 2>&1
source_stopped=true
"$anas_cmd" start -w "$restore_workspace" >"$report_dir/casdoor-restore-target-start.log" 2>&1
target_started=true
chmod 0600 "$report_dir"/casdoor-restore-*-start.log "$report_dir"/casdoor-restore-*-stop.log
for container in "$dc" "$casdoor" "$dirwatch" "$consumer"; do
  wait_healthy "$container"
done

section "restored state and protocol validation"
test "$(workspace_active "$restore_workspace")" = "$source_deployment"
test "$(sha256sum "$restore_workspace/.anas/secrets.yml" | awk '{print $1}')" = "$source_store_digest"
test "$(sha256sum "$restore_workspace/.anas/local-admins.yml" | awk '{print $1}')" = "$source_admin_digest"
test "$(sha256sum "$restore_workspace/.anas/deployments/$source_deployment/deployment.yml" | awk '{print $1}')" = "$source_manifest_digest"
test "$(sha256sum "$restore_workspace/data/casdoor/dirwatch/cursor.json" | awk '{print $1}')" = "$source_cursor_digest"
"$docker_cmd" exec "$casdoor" printenv ANAS_IAM_CLIENT__OAUTH2_PROXY__CLIENT_SECRET \
  >"$run_root/restored-client-secret"
test "$(sha256sum "$run_root/restored-client-secret" | awk '{print $1}')" = "$source_client_digest"

start_fixture
wait_for_user >"$run_root/restored-user.json"
test "$(jq -er '.externalId' "$run_root/restored-user.json")" = "$directory_anchor"
fixture login --username "$test_user" --password-file /state/user-password \
  --state-file /state/restored-login.json >"$run_root/restored-login.out"
test "$(jq -er '.sub' "$run_root/restored-login.json")" = "$source_sub"
"$anas_cmd" admin local credential casdoor break_glass -w "$restore_workspace" --json \
  | jq -er '.account.password' >"$run_root/restored-admin-password"
admin_login_output=$(cat "$run_root/restored-admin-password" |
  "$docker_cmd" exec -i "$fixture_container" /fixture admin-login)
printf '%s' "$admin_login_output" | jq -e \
  '.login == "accepted" and .username == "admin_casdoor" and .is_admin == true' >/dev/null
printf 'backup_restore=pass mode=snapshot empty_workspace=true database=true signing=true consumer_secret=true cursor=true inventory=true deployment=true\n'
printf 'restore_login=pass username=%s anchor=unchanged sub=unchanged jwks_signature=verified\n' "$test_user"

section "restore source and clean fixtures"
remove_fixture
"$anas_cmd" stop -w "$restore_workspace" >"$report_dir/casdoor-restore-target-stop.log" 2>&1
target_started=false
"$anas_cmd" start -w "$workspace" >"$report_dir/casdoor-restore-source-start.log" 2>&1
source_stopped=false
for container in "$dc" "$casdoor" "$dirwatch" "$consumer"; do
  wait_healthy "$container"
done
start_fixture
admin_fixture restore --backup /state/application-original.json >/dev/null
fixture_configured=false
remove_fixture
cleanup_user

if [ -n "$snapshot_id" ]; then
  if ! "$anas_cmd" snapshot delete "$snapshot_id" -w "$workspace" -y --json \
    >"$report_dir/casdoor-restore-snapshot-delete.json"; then
    remove_source_snapshot "$snapshot_id"
  fi
  chmod 0600 "$report_dir/casdoor-restore-snapshot-delete.json"
  snapshot_id=
fi

saved_run_root=$run_root
remove_run_root
run_root=
test ! -e "$saved_run_root"
printf '\nCasdoor backup/restore E2E tests passed\n'
