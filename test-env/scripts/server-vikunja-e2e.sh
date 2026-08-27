#!/usr/bin/env bash
# TEST_CASES: VIK-T-002, VIK-T-003, VIK-T-004, VIK-T-008, VIK-T-009, VIK-T-010, VIK-T-011, VIK-T-012
set -euo pipefail

# Release-gate probes for a Vikunja deployment. The script intentionally
# refuses the host's normal Docker socket; run it in the dedicated test
# network namespace with the ANAS Vikunja test daemon.

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$script_dir/server-require-isolated-docker.sh"

mode=${1:-core}
docker_cmd=${DOCKER_CMD:-docker}
prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_vik_}
domain=${ANAS_TEST_DOMAIN:-vikunja.test}
entry_ip=${ANAS_TEST_ENTRY_IP:-10.253.20.2}
entry_port=${ANAS_TEST_ENTRY_PORT:-9000}
http_timeout=${ANAS_TEST_HTTP_TIMEOUT:-180}
workspace=${ANAS_TEST_WORKSPACE:-}
anas_cmd=${ANAS_TEST_ANAS_CMD:-anas}
db_type=${ANAS_TEST_DB_TYPE:-postgres}
api_token=${ANAS_TEST_API_TOKEN:-}
api_jwt=${ANAS_TEST_JWT:-}
api_username=${ANAS_TEST_USERNAME:-}
api_password=${ANAS_TEST_PASSWORD:-}
api_token_file=${ANAS_TEST_API_TOKEN_FILE:-}
oidc_e2e_cmd=${ANAS_TEST_OIDC_E2E_CMD:-$script_dir/server-vikunja-oidc-e2e.sh}
oidc_session_file=${ANAS_TEST_OIDC_SESSION_FILE:-}
upgrade_deployment=${ANAS_TEST_UPGRADE_DEPLOYMENT:-}
rollback_deployment=${ANAS_TEST_ROLLBACK_DEPLOYMENT:-}
incompatible_rollback_deployment=${ANAS_TEST_INCOMPATIBLE_ROLLBACK_DEPLOYMENT:-}
upgrade_revision=${ANAS_TEST_UPGRADE_REVISION:-4}
rollback_revision=${ANAS_TEST_ROLLBACK_REVISION:-3}
restore_root=${ANAS_TEST_RESTORE_ROOT:-}
report_dir=${ANAS_TEST_REPORT_DIR:-$script_dir/../reports}
netns=${ANAS_TEST_NETNS:-anas-vikunja-test}
webhook_receiver_ip=${ANAS_TEST_WEBHOOK_RECEIVER_IP:-1.1.1.1}
webhook_receiver_port=${ANAS_TEST_WEBHOOK_RECEIVER_PORT:-18080}
webhook_receiver_script=${ANAS_TEST_WEBHOOK_RECEIVER_SCRIPT:-$script_dir/server-vikunja-webhook-receiver.py}
vikunja_container="${prefix}vikunja"
vikunja_host="tasks.$domain"
vikunja_url="https://$vikunja_host:$entry_port"
resolve=(--resolve "$vikunja_host:$entry_port:$entry_ip")
workdir=$(mktemp -d)
webhook_receiver_launcher_pid=
webhook_receiver_pid_file=
webhook_alias_added=false
api_token_id=
api_token_created=false
upgrade_restore_needed=false
restore_run_root=
restore_workspace=
restore_backup_dir=
restore_backup_id=
restore_snapshot_id=
restore_original_stopped=false
restore_workspace_started=false
restore_fixture_created=false
restore_project_id=
restore_task_id=
restore_comment_id=
restore_attachment_id=
restore_webhook_id=
load_project_ids=()
load_baseline_tasks=0
load_hold_file=${ANAS_TEST_LOAD_HOLD_FILE:-}

active_deployment_id() {
  sed -n 's/^active_deployment: //p' "$workspace/.anas/state/active.yml"
}

workspace_active_deployment_id() {
  sed -n 's/^active_deployment: //p' "$1/.anas/state/active.yml"
}

restore_upgrade_deployment() {
  [ "$upgrade_restore_needed" = true ] || return 0
  [ -n "$upgrade_deployment" ] || return 0
  [ -n "$workspace" ] || return 0
  [ "$(active_deployment_id)" != "$upgrade_deployment" ] || return 0
  install -d -m 0700 "$report_dir"
  "$anas_cmd" rollback "$upgrade_deployment" -w "$workspace" --json \
    >"$report_dir/vikunja-upgrade-emergency-restore.json" \
    2>"$report_dir/vikunja-upgrade-emergency-restore.log" || true
  chmod 0600 "$report_dir/vikunja-upgrade-emergency-restore.json" \
    "$report_dir/vikunja-upgrade-emergency-restore.log" 2>/dev/null || true
}

stop_webhook_receiver() {
  local child_pid=
  if [ -n "$webhook_receiver_pid_file" ] && [ -r "$webhook_receiver_pid_file" ]; then
    child_pid=$(tr -d '[:space:]' <"$webhook_receiver_pid_file")
    case "$child_pid" in
      ''|*[!0-9]*) child_pid= ;;
    esac
  fi
  [ -z "$child_pid" ] || kill "$child_pid" 2>/dev/null || true
  [ -z "$webhook_receiver_launcher_pid" ] || \
    kill "$webhook_receiver_launcher_pid" 2>/dev/null || true
  [ -z "$webhook_receiver_launcher_pid" ] || \
    wait "$webhook_receiver_launcher_pid" 2>/dev/null || true
  webhook_receiver_launcher_pid=
  webhook_receiver_pid_file=
}

delete_created_api_token() {
  [ "$api_token_created" = true ] || return 0
  [ -n "$api_token_id" ] || return 0
  [ -n "$api_jwt" ] || return 0
  local status
  status=$(curl_vikunja -o /dev/null -w '%{http_code}' -X DELETE \
    -H "Authorization: Bearer $api_jwt" \
    "$vikunja_url/api/v2/tokens/$api_token_id" 2>/dev/null || true)
  if [ "$status" != 200 ] && [ "$status" != 204 ] && \
    [ -n "$api_password" ] && [ -n "$oidc_session_file" ]; then
    refresh_oidc_session >/dev/null
    status=$(curl_vikunja -o /dev/null -w '%{http_code}' -X DELETE \
      -H "Authorization: Bearer $api_jwt" \
      "$vikunja_url/api/v2/tokens/$api_token_id" 2>/dev/null || true)
  fi
  if [ "$status" != 200 ] && [ "$status" != 204 ]; then
    printf 'failed to delete generated API token id=%s HTTP %s\n' \
      "$api_token_id" "${status:-000}" >&2
    return 1
  fi
  api_token_created=false
  api_token_id=
}

remove_test_subvolume() {
  local path=$1
  [ -n "$path" ] || return 0
  [ -e "$path" ] || return 0
  sudo -n btrfs subvolume show "$path" >/dev/null 2>&1 || return 0
  sudo -n btrfs subvolume delete "$path" >/dev/null 2>&1
}

remove_source_snapshot() {
  local snapshot_id=$1 snapshot_path
  [[ "$snapshot_id" =~ ^[0-9]{8}T[0-9]{6}Z-[a-f0-9]{8}$ ]]
  snapshot_path=$workspace/snapshots/$snapshot_id
  case "$snapshot_path" in
    "$workspace"/snapshots/*) ;;
    *) return 2 ;;
  esac
  remove_test_subvolume "$snapshot_path/data"
  remove_test_subvolume "$snapshot_path/userdata"
  sudo -n rm -rf -- "$snapshot_path"
}

cleanup_restore_fixture() {
  [ "$restore_fixture_created" = true ] || return 0
  [ -z "$restore_webhook_id" ] || \
    api_delete_jwt "/projects/$restore_project_id/webhooks/$restore_webhook_id" || true
  [ -z "$restore_attachment_id" ] || \
    api_delete_jwt "/tasks/$restore_task_id/attachments/$restore_attachment_id" || true
  [ -z "$restore_comment_id" ] || \
    api_delete_jwt "/tasks/$restore_task_id/comments/$restore_comment_id" || true
  [ -z "$restore_task_id" ] || api_delete_jwt "/tasks/$restore_task_id" || true
  [ -z "$restore_project_id" ] || api_delete_jwt "/projects/$restore_project_id" || true
  restore_fixture_created=false
}

cleanup_restore_test() {
  [ -n "$restore_run_root" ] || return 0
  if [ "$restore_workspace_started" = true ] && [ -n "$restore_workspace" ]; then
    "$anas_cmd" stop -w "$restore_workspace" >/dev/null 2>&1 || true
    restore_workspace_started=false
  fi
  if [ "$restore_original_stopped" = true ]; then
    "$anas_cmd" start -w "$workspace" >/dev/null 2>&1 || true
    wait_healthy "$vikunja_container" >/dev/null 2>&1 || true
    wait_http_and_auth_policy >/dev/null 2>&1 || true
    restore_original_stopped=false
  fi
  refresh_oidc_session >/dev/null 2>&1 || true
  cleanup_restore_fixture
  if [ -n "$restore_snapshot_id" ]; then
    "$anas_cmd" snapshot delete "$restore_snapshot_id" -w "$workspace" -y --json \
      >/dev/null 2>&1 || true
    [ ! -e "$workspace/snapshots/$restore_snapshot_id" ] || \
      remove_source_snapshot "$restore_snapshot_id" || true
    restore_snapshot_id=
  fi
  if [ -n "$restore_workspace" ]; then
    remove_test_subvolume "$restore_workspace/data" || true
  fi
  if [ -n "$restore_backup_id" ] && [ -n "$restore_backup_dir" ]; then
    remove_test_subvolume "$restore_backup_dir/$restore_backup_id/data" || true
  fi
  case "$restore_run_root" in
    "$restore_root"/run.*) sudo -n rm -rf -- "$restore_run_root" ;;
  esac
  restore_run_root=
}

cleanup() {
  stop_webhook_receiver
  cleanup_restore_test
  if [ "${#load_project_ids[@]}" -gt 0 ] && [ -n "$api_token" ]; then
    local project_id
    for project_id in "${load_project_ids[@]}"; do
      api_delete "/projects/$project_id" >/dev/null 2>&1 || true
    done
    load_project_ids=()
  fi
  if [ -n "$load_hold_file" ]; then
    rm -f "$load_hold_file" "$load_hold_file.release"
  fi
  delete_created_api_token
  restore_upgrade_deployment
  if [ "$webhook_alias_added" = true ]; then
    sudo -n ip netns exec "$netns" ip addr del "$webhook_receiver_ip/32" dev lo \
      2>/dev/null || true
    webhook_alias_added=false
  fi
  rm -rf "$workdir"
}

trap cleanup EXIT HUP INT TERM
trap 'printf "FAIL: Vikunja E2E mode=%s line=%s\n" "$mode" "$LINENO" >&2' ERR

section() {
  printf '\n== %s ==\n' "$1"
}

curl_vikunja() {
  curl -skS --connect-timeout 10 --max-time "$http_timeout" "${resolve[@]}" "$@"
}

wait_healthy() {
  local container=$1 attempt state health
  for attempt in $(seq 1 90); do
    state=$($docker_cmd inspect --format '{{.State.Status}}' "$container" 2>/dev/null || true)
    health=$($docker_cmd inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$container" 2>/dev/null || true)
    if [ "$state" = running ] && { [ -z "$health" ] || [ "$health" = healthy ]; }; then
      return 0
    fi
    if [ "$state" = exited ] || [ "$state" = dead ]; then
      $docker_cmd logs --tail 80 "$container" >&2 || true
      return 1
    fi
    sleep 2
  done
  $docker_cmd inspect --format '{{json .State}}' "$container" >&2 || true
  $docker_cmd logs --tail 80 "$container" >&2 || true
  return 1
}

info_json() {
  curl_vikunja "$vikunja_url/api/v1/info"
}

assert_http_and_auth_policy() {
  local info=$workdir/info.json status
  status=$(curl_vikunja -o "$info" -w '%{http_code}' "$vikunja_url/api/v1/info")
  test "$status" = 200
  # During a stack transition the proxy may briefly return a non-JSON error
  # body. Treat that as "not ready" without flooding the E2E log; the caller
  # owns the bounded retry and final failure message.
  jq -e '.version | type == "string" and length > 0' "$info" >/dev/null 2>&1
  jq -e '.auth.local.enabled == false' "$info" >/dev/null 2>&1
  jq -e '.auth.openid_connect.enabled == true' "$info" >/dev/null 2>&1
  jq -e '.auth.openid_connect.providers | any(.key == "anas" and .client_id == "vikunja")' \
    "$info" >/dev/null 2>&1
  jq -e '.auth.local.registration_enabled == false' "$info" >/dev/null 2>&1
  printf 'http=ready local_auth=disabled registration=disabled oidc_provider=anas\n'
}

wait_http_and_auth_policy() {
  local attempt
  for attempt in $(seq 1 60); do
    if assert_http_and_auth_policy; then
      return 0
    fi
    sleep 2
  done
  printf 'Vikunja HTTP policy did not become ready after container health passed\n' >&2
  return 1
}

refresh_oidc_session() {
  : "${api_username:?ANAS_TEST_USERNAME is required to refresh the OIDC session}"
  : "${api_password:?ANAS_TEST_PASSWORD is required to refresh the OIDC session}"
  : "${oidc_session_file:?ANAS_TEST_OIDC_SESSION_FILE is required to refresh the OIDC session}"
  test -x "$oidc_e2e_cmd"
  ANAS_TEST_USERNAME=$api_username \
    ANAS_TEST_PASSWORD=$api_password \
    ANAS_TEST_SECRET_FILE=$oidc_session_file \
    ANAS_TEST_PRESERVE_USERS=true \
    "$oidc_e2e_cmd" refresh
  # The file uses shell %q escaping and is mode 0600. Source it; never parse it
  # as a Docker env file or print any value.
  source "$oidc_session_file"
  api_jwt=$ANAS_TEST_JWT
  api_username=$ANAS_TEST_USERNAME
  api_password=$ANAS_TEST_PASSWORD
  test -n "$api_jwt"
}

assert_runtime() {
  local state health restarts readonly ports process_uid image_arch env_json logs
  state=$($docker_cmd inspect --format '{{.State.Status}}' "$vikunja_container")
  health=$($docker_cmd inspect --format '{{.State.Health.Status}}' "$vikunja_container")
  restarts=$($docker_cmd inspect --format '{{.RestartCount}}' "$vikunja_container")
  readonly=$($docker_cmd inspect --format '{{.HostConfig.ReadonlyRootfs}}' "$vikunja_container")
  ports=$($docker_cmd inspect --format '{{json .HostConfig.PortBindings}}' "$vikunja_container")
  image_arch=$($docker_cmd image inspect --format '{{.Architecture}}' \
    "$($docker_cmd inspect --format '{{.Image}}' "$vikunja_container")")
  # Docker requires the PID column to remain present in custom `top` output.
  process_uid=$($docker_cmd top "$vikunja_container" -eo pid,uid,comm | awk 'NR == 2 { print $2 }')
  test "$state" = running
  test "$health" = healthy
  test "$restarts" = 0
  test "$readonly" = true
  { [ "$ports" = null ] || [ "$ports" = '{}' ]; }
  test "$image_arch" = amd64
  { [ "$process_uid" = 1000 ] || [ "$process_uid" = '#1000' ]; }

  env_json=$($docker_cmd inspect --format '{{json .Config.Env}}' "$vikunja_container")
  printf '%s' "$env_json" | jq -e '
    any(startswith("VIKUNJA_DATABASE_TYPE=")) and
    any(startswith("VIKUNJA_DATABASE_PASSWORD=")) and
    any(startswith("VIKUNJA_AUTH_OPENID_PROVIDERS_ANAS_CLIENTSECRET=")) and
    any(startswith("VIKUNJA_SERVICE_SECRET=")) and
    any(. == "VIKUNJA_AUTH_LOCAL_ENABLED=false") and
    any(. == "VIKUNJA_SERVICE_ENABLEREGISTRATION=false")
  ' >/dev/null

  # Compare in memory only. Never emit either managed secret.
  local service_secret oidc_secret
  service_secret=$(printf '%s' "$env_json" | jq -r '.[] | select(startswith("VIKUNJA_SERVICE_SECRET=")) | split("=")[1]')
  oidc_secret=$(printf '%s' "$env_json" | jq -r '.[] | select(startswith("VIKUNJA_AUTH_OPENID_PROVIDERS_ANAS_CLIENTSECRET=")) | split("=")[1]')
  test -n "$service_secret"
  test -n "$oidc_secret"
  logs=$workdir/vikunja.log
  $docker_cmd logs "$vikunja_container" >"$logs" 2>&1
  ! grep -Fq -- "$service_secret" "$logs"
  ! grep -Fq -- "$oidc_secret" "$logs"
  printf 'runtime=healthy arch=%s uid=%s read_only=%s restarts=%s published_ports=none secret_log_leak=none\n' \
    "$image_arch" "$process_uid" "$readonly" "$restarts"
}

database_row_count() {
  case "$db_type" in
    postgres)
      $docker_cmd exec "${prefix}postgres" sh -lc \
        'psql -U "$POSTGRES_USER" -d vikunja -A -t -c "select count(*) from users"' | tr -d '[:space:]'
      ;;
    mariadb)
      $docker_cmd exec "${prefix}mariadb" sh -lc \
        'mariadb -u root -p"$MARIADB_ROOT_PASSWORD" -N -s -e "select count(*) from vikunja.users"' | tr -d '[:space:]'
      ;;
    *) printf 'unsupported ANAS_TEST_DB_TYPE: %s\n' "$db_type" >&2; return 2 ;;
  esac
}

database_object_counts() {
  case "$db_type" in
    postgres)
      $docker_cmd exec "${prefix}postgres" sh -lc \
        'psql -U "$POSTGRES_USER" -d vikunja -A -t -c "select (select count(*) from users),(select count(*) from projects),(select count(*) from tasks),(select count(*) from task_comments),(select count(*) from task_attachments),(select count(*) from files),(select count(*) from api_tokens),(select count(*) from webhooks)"'
      ;;
    mariadb)
      $docker_cmd exec "${prefix}mariadb" sh -lc \
        'mariadb -u root -p"$MARIADB_ROOT_PASSWORD" -N -s -e "select (select count(*) from vikunja.users),(select count(*) from vikunja.projects),(select count(*) from vikunja.tasks),(select count(*) from vikunja.task_comments),(select count(*) from vikunja.task_attachments),(select count(*) from vikunja.files),(select count(*) from vikunja.api_tokens),(select count(*) from vikunja.webhooks)"'
      ;;
    *) printf 'unsupported ANAS_TEST_DB_TYPE: %s\n' "$db_type" >&2; return 2 ;;
  esac
}

database_oidc_binding() {
  [[ "$api_username" =~ ^[A-Za-z0-9._-]+$ ]]
  case "$db_type" in
    postgres)
      $docker_cmd exec "${prefix}postgres" sh -lc \
        'psql -U "$POSTGRES_USER" -d vikunja -A -t -F "|" -c "select username,issuer,subject from users"' |
        awk -F '|' -v username="$api_username" '$1 == username { print $2 "|" $3; exit }'
      ;;
    mariadb)
      $docker_cmd exec "${prefix}mariadb" sh -lc \
        'mariadb -u root -p"$MARIADB_ROOT_PASSWORD" -N -s -e "select username,issuer,subject from vikunja.users"' |
        awk -F '\t' -v username="$api_username" '$1 == username { print $2 "|" $3; exit }'
      ;;
    *) printf 'unsupported ANAS_TEST_DB_TYPE: %s\n' "$db_type" >&2; return 2 ;;
  esac
}

deployment_revision() {
  local deployment=$1
  sed -n 's/^revision: //p' \
    "$workspace/.anas/deployments/$deployment/modules/vikunja/module.yml" | head -n 1
}

assert_active_deployment() {
  local expected_deployment=$1 expected_revision=$2
  test "$(active_deployment_id)" = "$expected_deployment"
  test "$(deployment_revision "$expected_deployment")" = "$expected_revision"
  wait_healthy "$vikunja_container"
  wait_http_and_auth_policy
}

assert_runtime_revision_image() {
  local expected_revision=$1 image_ref image_id
  image_ref=$($docker_cmd inspect --format '{{.Config.Image}}' "$vikunja_container")
  image_id=$($docker_cmd inspect --format '{{.Image}}' "$vikunja_container")
  case "$image_ref" in
    *"/anas-vikunja:2.4.0-r$expected_revision") ;;
    *) printf 'unexpected Vikunja runtime image for revision %s: %s\n' \
         "$expected_revision" "$image_ref" >&2; return 1 ;;
  esac
  test -n "$image_id"
  printf 'runtime_revision_image=pass revision=%s image_ref=%s image_id=%s\n' \
    "$expected_revision" "$image_ref" "$image_id"
}

assert_database_mapping() {
  local expected actual rows
  case "$db_type" in
    postgres) expected=postgres ;;
    mariadb) expected=mysql ;;
    *) printf 'unsupported ANAS_TEST_DB_TYPE: %s\n' "$db_type" >&2; return 2 ;;
  esac
  actual=$($docker_cmd inspect --format '{{json .Config.Env}}' "$vikunja_container" |
    jq -r '.[] | select(startswith("VIKUNJA_DATABASE_TYPE=")) | split("=")[1]')
  test "$actual" = "$expected"
  rows=$(database_row_count)
  test "$rows" -ge 0
  printf 'database_binding=%s upstream_type=%s user_rows=%s\n' "$db_type" "$actual" "$rows"
}

restart_probe() {
  local db_container before after
  before=$(database_row_count)
  case "$db_type" in
    postgres) db_container="${prefix}postgres" ;;
    mariadb) db_container="${prefix}mariadb" ;;
  esac
  section 'application restart'
  $docker_cmd restart "$vikunja_container" >/dev/null
  wait_healthy "$vikunja_container"
  wait_http_and_auth_policy
  section 'database restart'
  $docker_cmd restart "$db_container" >/dev/null
  wait_healthy "$db_container"
  wait_healthy "$vikunja_container"
  wait_http_and_auth_policy
  after=$(database_row_count)
  test "$after" = "$before"
  printf 'restart_persistence=pass rows_before=%s rows_after=%s\n' "$before" "$after"
}

assert_api_token() {
  local status
  test -n "$api_token"
  # The user-profile route is deliberately JWT-only and is not present in the
  # API-token /routes inventory. Validate the token on an explicitly granted
  # endpoint instead of misclassifying that route-level 401 as token failure.
  status=$(curl_vikunja -o "$workdir/projects.json" -w '%{http_code}' \
    -H "Authorization: Bearer $api_token" "$vikunja_url/api/v2/projects?per_page=1")
  test "$status" = 200
  jq -e '.items | type == "array"' "$workdir/projects.json" >/dev/null
  printf 'api_token=accepted route=projects.read_all\n'
}

create_api_token() {
  [ -n "$api_token" ] && return 0
  : "${api_jwt:?ANAS_TEST_JWT is required when ANAS_TEST_API_TOKEN is absent}"
  local response=$workdir/api-token.json payload status expires_at
  expires_at=$(python3 - <<'PY'
from datetime import datetime, timedelta, timezone
print((datetime.now(timezone.utc) + timedelta(days=1)).isoformat().replace("+00:00", "Z"))
PY
)
  payload=$(jq -cn --arg expires_at "$expires_at" '{
    title:"ANAS Vikunja E2E",
    expires_at:$expires_at,
    permissions:{
      projects:["create","delete","read_all","read_one","update","tasks_by_index"],
      tasks:["create","delete","read","read_all","read_one","update"],
      tasks_comments:["create","delete","read_all","read_one","update"],
      tasks_attachments:["create","delete","read_all","read_one"],
      projects_webhooks:["create","delete","read_all","update"],
      caldav:["access"]
    }
  }')
  : >"$response"
  chmod 0600 "$response"
  status=$(curl_vikunja -o "$response" -w '%{http_code}' \
    -H "Authorization: Bearer $api_jwt" -H 'Content-Type: application/json' \
    --data "$payload" "$vikunja_url/api/v2/tokens")
  if [ "$status" != 201 ]; then
    printf 'create API token returned HTTP %s\n' "$status" >&2
    jq '{code,message,title,detail}' "$response" >&2 || true
    return 1
  fi
  api_token=$(jq -r '.token // empty' "$response")
  api_token_id=$(jq -r '.id // empty' "$response")
  test -n "$api_token"
  test "$api_token_id" -gt 0
  api_token_created=true
  if [ -n "$api_token_file" ]; then
    install -d -m 0700 "$(dirname -- "$api_token_file")"
    umask 077
    printf '%s' "$api_token" >"$api_token_file"
    chmod 0600 "$api_token_file"
    printf 'api_token_artifact=stored mode=0600\n'
  fi
  printf 'api_token=created permissions=projects,tasks,comments,attachments,webhooks,caldav\n'
}

api_json() {
  local method=$1 path=$2 payload=$3 expected=$4 output=$5 status
  status=$(curl_vikunja -o "$output" -w '%{http_code}' -X "$method" \
    -H "Authorization: Bearer $api_token" -H 'Content-Type: application/json' \
    --data "$payload" "$vikunja_url/api/v2$path")
  if [ "$status" != "$expected" ]; then
    printf '%s %s returned HTTP %s, want %s\n' "$method" "$path" "$status" "$expected" >&2
    jq '{code,message,title,detail}' "$output" >&2 || true
    return 1
  fi
}

api_delete() {
  local path=$1 status
  status=$(curl_vikunja -o "$workdir/delete.json" -w '%{http_code}' -X DELETE \
    -H "Authorization: Bearer $api_token" "$vikunja_url/api/v2$path")
  case "$status" in 200|204) return 0 ;; esac
  printf 'DELETE %s returned HTTP %s\n' "$path" "$status" >&2
  return 1
}

api_delete_jwt() {
  local path=$1 status
  test -n "$api_jwt"
  status=$(curl_vikunja -o "$workdir/delete-jwt.json" -w '%{http_code}' -X DELETE \
    -H "Authorization: Bearer $api_jwt" "$vikunja_url/api/v2$path")
  case "$status" in 200|204) return 0 ;; esac
  printf 'DELETE %s with OIDC session returned HTTP %s\n' "$path" "$status" >&2
  return 1
}

api_v2_crud_probe() {
  : "${api_username:?ANAS_TEST_USERNAME is required for API/CalDAV mode}"
  local stamp project_id task_id comment_id attachment_id status
  stamp=$(date +%s)

  api_json POST /projects \
    "$(jq -cn --arg title "ANAS E2E $stamp" '{title:$title,description:"restore/API fixture"}')" \
    201 "$workdir/project.json"
  project_id=$(jq -r '.id' "$workdir/project.json")
  test "$project_id" -gt 0
  api_json GET "/projects/$project_id" '{}' 200 "$workdir/project-read.json"
  api_json PATCH "/projects/$project_id" \
    ' {"description":"updated through API v2"}' 200 "$workdir/project-updated.json"
  test "$(jq -r '.description' "$workdir/project-updated.json")" = 'updated through API v2'

  api_json POST "/projects/$project_id/tasks" \
    "$(jq -cn --arg title "Task $stamp" '{title:$title,description:"attachment target"}')" \
    201 "$workdir/task.json"
  task_id=$(jq -r '.id' "$workdir/task.json")
  test "$task_id" -gt 0
  api_json GET "/tasks/$task_id" '{}' 200 "$workdir/task-read.json"
  api_json PATCH "/tasks/$task_id" '{"priority":3}' 200 "$workdir/task-updated.json"
  test "$(jq -r '.priority' "$workdir/task-updated.json")" = 3

  api_json POST "/tasks/$task_id/comments" '{"comment":"ANAS E2E comment"}' \
    201 "$workdir/comment.json"
  comment_id=$(jq -r '.id' "$workdir/comment.json")
  test "$comment_id" -gt 0
  api_json GET "/tasks/$task_id/comments/$comment_id" '{}' 200 "$workdir/comment-read.json"
  api_json PATCH "/tasks/$task_id/comments/$comment_id" \
    '{"comment":"ANAS E2E comment updated"}' 200 "$workdir/comment-updated.json"

  printf 'Vikunja attachment E2E %s\n' "$stamp" >"$workdir/attachment.txt"
  status=$(curl_vikunja -o "$workdir/attachment.json" -w '%{http_code}' \
    -H "Authorization: Bearer $api_token" \
    -F "files=@$workdir/attachment.txt;type=text/plain" \
    "$vikunja_url/api/v2/tasks/$task_id/attachments")
  test "$status" = 201
  test "$(jq '.errors | length' "$workdir/attachment.json")" = 0
  attachment_id=$(jq -r '.success[0].id' "$workdir/attachment.json")
  test "$attachment_id" -gt 0
  status=$(curl_vikunja -o "$workdir/attachment-download.txt" -w '%{http_code}' \
    -H "Authorization: Bearer $api_token" \
    "$vikunja_url/api/v2/tasks/$task_id/attachments/$attachment_id")
  test "$status" = 200
  cmp "$workdir/attachment.txt" "$workdir/attachment-download.txt"

  status=$(curl_vikunja -o "$workdir/caldav.xml" -w '%{http_code}' -X PROPFIND \
    -u "$api_username:$api_token" -H 'Depth: 0' \
    "$vikunja_url/dav/principals/$api_username/")
  test "$status" = 207
  grep -Fq 'multistatus' "$workdir/caldav.xml"

  api_delete "/tasks/$task_id/attachments/$attachment_id"
  api_delete "/tasks/$task_id/comments/$comment_id"
  api_delete "/tasks/$task_id"
  api_delete "/projects/$project_id"
  printf 'api_v2_crud=pass project,task,comment,attachment caldav=207 cleanup=pass\n'
}

webhook_probe() {
  local stamp project_id webhook_id task_id receiver_dir receiver_url
  local webhook_secret bad_signature status attempt logs
  stamp=$(date +%s)
  receiver_dir=$workdir/webhook
  receiver_url="http://$webhook_receiver_ip:$webhook_receiver_port/vikunja"
  webhook_secret=$(openssl rand -hex 32)
  bad_signature=$(printf '0%.0s' $(seq 1 64))
  install -d -m 0700 "$receiver_dir"

  # 1.1.1.1 is an alias on loopback inside the disposable network namespace,
  # not the Internet address. It avoids weakening Vikunja's private-IP SSRF
  # protection while keeping every packet inside the isolated server fixture.
  sudo -n ip netns exec "$netns" ip addr replace "$webhook_receiver_ip/32" dev lo
  webhook_alias_added=true
  sudo -n ip netns exec "$netns" sudo -n -u "$(id -un)" \
    env ANAS_WEBHOOK_SECRET="$webhook_secret" \
    python3 "$webhook_receiver_script" --bind "$webhook_receiver_ip" \
    --port "$webhook_receiver_port" --output-dir "$receiver_dir" &
  webhook_receiver_launcher_pid=$!
  webhook_receiver_pid_file=$receiver_dir/receiver.pid

  for attempt in $(seq 1 30); do
    if sudo -n ip netns exec "$netns" curl -fs --max-time 2 \
      "http://$webhook_receiver_ip:$webhook_receiver_port/health" >/dev/null; then
      break
    fi
    sleep 1
  done
  test "$attempt" -lt 30

  status=$(sudo -n ip netns exec "$netns" curl -sS -o /dev/null -w '%{http_code}' \
    -H 'Content-Type: application/json' -H "X-Vikunja-Signature: $bad_signature" \
    --data '{"event_name":"task.created"}' "$receiver_url")
  test "$status" = 401
  test -f "$receiver_dir/invalid-signature.json"

  api_json POST /projects \
    "$(jq -cn --arg title "ANAS webhook E2E $stamp" '{title:$title}')" \
    201 "$workdir/webhook-project.json"
  project_id=$(jq -r '.id' "$workdir/webhook-project.json")
  api_json POST "/projects/$project_id/webhooks" \
    "$(jq -cn --arg target "$receiver_url" --arg secret "$webhook_secret" \
      '{target_url:$target,secret:$secret,events:["task.created"]}')" \
    201 "$workdir/webhook-created.json"
  webhook_id=$(jq -r '.id' "$workdir/webhook-created.json")
  test "$webhook_id" -gt 0
  ! jq -e --arg secret "$webhook_secret" '.. | strings | select(. == $secret)' \
    "$workdir/webhook-created.json" >/dev/null

  api_json POST "/projects/$project_id/tasks" \
    "$(jq -cn --arg title "Webhook task $stamp" '{title:$title}')" \
    201 "$workdir/webhook-task.json"
  task_id=$(jq -r '.id' "$workdir/webhook-task.json")
  test "$task_id" -gt 0

  for attempt in $(seq 1 30); do
    [ -f "$receiver_dir/verified.json" ] && break
    sleep 1
  done
  test -f "$receiver_dir/verified.json"
  jq -e '.status == "verified" and .event_name == "task.created"' \
    "$receiver_dir/verified.json" >/dev/null
  jq -e --argjson project_id "$project_id" --argjson task_id "$task_id" '
    .event_name == "task.created" and
    .data.task.id == $task_id and
    .data.task.project_id == $project_id
  ' "$receiver_dir/payload.json" >/dev/null

  logs=$workdir/vikunja-webhook.log
  $docker_cmd logs "$vikunja_container" >"$logs" 2>&1
  ! grep -Fq -- "$webhook_secret" "$logs"
  api_delete "/projects/$project_id/webhooks/$webhook_id"
  api_delete "/tasks/$task_id"
  api_delete "/projects/$project_id"
  stop_webhook_receiver
  sudo -n ip netns exec "$netns" ip addr del "$webhook_receiver_ip/32" dev lo \
    2>/dev/null || true
  webhook_alias_added=false
  printf 'webhook=pass event=task.created hmac=sha256 wrong_signature=rejected secret_log_leak=none cleanup=pass\n'
}

rotation_dry_run_probe() {
  : "${workspace:?ANAS_TEST_WORKSPACE is required for rotation mode}"
  local dry=$workdir/dry-run.json
  "$anas_cmd" credential rotate vikunja.service_secret --dry-run --json -w "$workspace" >"$dry"
  "$anas_cmd" credential rotate vikunja.oidc_client_secret --dry-run --json -w "$workspace" >>"$dry"
  "$anas_cmd" credential rotate --module vikunja --dry-run --json -w "$workspace" >>"$dry"
  "$anas_cmd" credential rotate --all --dry-run --json -w "$workspace" >>"$dry"
  ! grep -Eqi '(service_secret|client_secret)[[:space:]]*[:=][[:space:]]*[a-f0-9]{32,}' "$dry"
  printf 'credential_rotation_dry_run=pass targets=single-service,single-oidc,module,all\n'
}

rotation_execute() {
  local label=$1 expected_credentials=$2 report=$report_dir/vikunja-rotation-$label.json
  shift 2
  "$anas_cmd" credential rotate "$@" -w "$workspace" -y --json >"$report"
  chmod 0600 "$report"
  jq -e --argjson credentials "$expected_credentials" '
    .ok == true and .rotation.status == "complete" and
    .rotation.previous_deployment != .rotation.candidate_deployment and
    .rotation.credentials == $credentials
  ' "$report" >/dev/null
  wait_healthy "$vikunja_container"
  printf 'credential_rotation=pass scope=%s credentials=%s candidate=promoted runtime=healthy\n' \
    "$label" "$(jq -r '.rotation.credentials | join(",")' "$report")"
}

rotation_probe() {
  : "${workspace:?ANAS_TEST_WORKSPACE is required for rotation mode}"
  install -d -m 0700 "$report_dir"
  rotation_dry_run_probe
  rotation_execute single-service '["vikunja.service_secret"]' vikunja.service_secret
  if [ -n "$api_jwt" ]; then
    local status
    status=$(curl_vikunja -o /dev/null -w '%{http_code}' \
      -H "Authorization: Bearer $api_jwt" "$vikunja_url/api/v1/user")
    test "$status" = 401
    printf 'service_secret_session_effect=pass old_jwt=invalidated\n'
  fi
  rotation_execute single-oidc '["vikunja.oidc_client_secret"]' vikunja.oidc_client_secret
  rotation_execute module '["vikunja.oidc_client_secret","vikunja.service_secret"]' --module vikunja
  rotation_execute deployment '["vikunja.oidc_client_secret","vikunja.service_secret"]' --all
  printf 'credential_rotation_success_paths=pass oidc_relogin=required failure_compensation=separate-gate\n'
}

database_load_task_count() {
  case "$db_type" in
    postgres)
      "$docker_cmd" exec "${prefix}postgres" sh -lc \
        'psql -U "$POSTGRES_USER" -d vikunja -A -t -c "select count(*) from tasks"' |
        tr -d '[:space:]'
      ;;
    mariadb)
      "$docker_cmd" exec "${prefix}mariadb" sh -lc \
        'mariadb -u root -p"$MARIADB_ROOT_PASSWORD" -N -s -e "select count(*) from vikunja.tasks"' |
        tr -d '[:space:]'
      ;;
    *) printf 'unsupported ANAS_TEST_DB_TYPE: %s\n' "$db_type" >&2; return 2 ;;
  esac
}

seed_load_tasks() {
  local first=$1 last=$2 concurrency=${ANAS_TEST_LOAD_CONCURRENCY:-8}
  export ANAS_LOAD_API_TOKEN=$api_token
  export ANAS_LOAD_HOST=$vikunja_host
  export ANAS_LOAD_PORT=$entry_port
  export ANAS_LOAD_IP=$entry_ip
  printf '%s\n' "${load_project_ids[@]}" | xargs -P "$concurrency" -n 1 sh -ceu '
    first=$1
    last=$2
    project_id=$3
    index=$first
    while [ "$index" -le "$last" ]; do
      payload=$(printf "{\"title\":\"ANAS load task %s-%s\"}" "$project_id" "$index")
      for attempt in 1 2 3 4 5; do
        status=$(curl -skS --connect-timeout 10 --max-time 60 \
          --resolve "$ANAS_LOAD_HOST:$ANAS_LOAD_PORT:$ANAS_LOAD_IP" \
          -o /dev/null -w "%{http_code}" \
          -H "Authorization: Bearer $ANAS_LOAD_API_TOKEN" \
          -H "Content-Type: application/json" --data "$payload" \
          "https://$ANAS_LOAD_HOST:$ANAS_LOAD_PORT/api/v2/projects/$project_id/tasks" || true)
        [ -n "$status" ] || status=000
        [ "$status" = 201 ] && break
        case "$status" in 000|429|5??) sleep "$attempt" ;; *) break ;; esac
      done
      if [ "$status" != 201 ]; then
        printf "load seed project=%s index=%s failed HTTP %s after %s attempts\n" \
          "$project_id" "$index" "$status" "$attempt" >&2
        exit 1
      fi
      index=$((index + 1))
    done
  ' sh "$first" "$last"
  unset ANAS_LOAD_API_TOKEN ANAS_LOAD_HOST ANAS_LOAD_PORT ANAS_LOAD_IP
}

report_load_sample() {
  local label=$1 samples=${ANAS_TEST_LOAD_LATENCY_SAMPLES:-20}
  local timings=$workdir/load-timings-$label stats status p50 p95 maximum
  : >"$timings"
  for _ in $(seq 1 "$samples"); do
    status=$(curl_vikunja -o /dev/null -w '%{http_code}|%{time_starttransfer}|%{time_total}' \
      -H "Authorization: Bearer $api_token" \
      "$vikunja_url/api/v2/tasks?per_page=50&page=1")
    test "${status%%|*}" = 200
    printf '%s\n' "${status#*|}" >>"$timings"
  done
  p50=$(cut -d'|' -f2 "$timings" | sort -n | awk -v count="$samples" 'NR == int((count + 1) * 0.50) { print; exit }')
  p95=$(cut -d'|' -f2 "$timings" | sort -n | awk -v count="$samples" 'NR == int((count + 1) * 0.95) { print; exit }')
  maximum=$(cut -d'|' -f2 "$timings" | sort -n | tail -n 1)
  stats=$($docker_cmd stats --no-stream --format '{{.CPUPerc}}|{{.MemUsage}}' "$vikunja_container")
  printf 'load_sample=%s tasks=%s api_total_p50_s=%s api_total_p95_s=%s api_total_max_s=%s cpu_mem=%s\n' \
    "$label" "$(( $(database_load_task_count) - load_baseline_tasks ))" "$p50" "$p95" "$maximum" "$stats"
}

load_probe() {
  local stamp count started elapsed rate project_index project_id
  local hold_timeout=${ANAS_TEST_LOAD_HOLD_TIMEOUT:-1200}
  stamp=$(date +%s)
  create_api_token
  assert_api_token
  load_baseline_tasks=$(database_load_task_count)
  for project_index in $(seq 1 8); do
    api_json POST /projects \
      "$(jq -cn --arg title "ANAS load E2E $stamp-$project_index" \
        '{title:$title,description:"temporary 1k/10k task fixture"}')" \
      201 "$workdir/load-project.json"
    project_id=$(jq -r '.id' "$workdir/load-project.json")
    test "$project_id" -gt 0
    load_project_ids+=("$project_id")
  done
  test "$(database_load_task_count)" = "$load_baseline_tasks"
  printf 'server_cpu=%s server_memory_kib=%s concurrency=%s projects=%s\n' \
    "$(getconf _NPROCESSORS_ONLN)" "$(awk '/^MemTotal:/ { print $2 }' /proc/meminfo)" \
    "${ANAS_TEST_LOAD_CONCURRENCY:-8}" "${#load_project_ids[@]}"
  report_load_sample idle

  started=$(date +%s)
  seed_load_tasks 1 125
  elapsed=$(( $(date +%s) - started ))
  test "$elapsed" -gt 0 || elapsed=1
  rate=$(awk -v total=1000 -v seconds="$elapsed" 'BEGIN { printf "%.2f", total / seconds }')
  count=$(( $(database_load_task_count) - load_baseline_tasks ))
  test "$count" = 1000
  printf 'load_seed=1k created=1000 elapsed_s=%s rate_tasks_s=%s\n' "$elapsed" "$rate"
  report_load_sample 1k

  started=$(date +%s)
  seed_load_tasks 126 1250
  elapsed=$(( $(date +%s) - started ))
  test "$elapsed" -gt 0 || elapsed=1
  rate=$(awk -v total=9000 -v seconds="$elapsed" 'BEGIN { printf "%.2f", total / seconds }')
  count=$(( $(database_load_task_count) - load_baseline_tasks ))
  test "$count" = 10000
  printf 'load_seed=10k incremental_created=9000 elapsed_s=%s rate_tasks_s=%s\n' "$elapsed" "$rate"
  report_load_sample 10k

  if [ -n "$load_hold_file" ]; then
    install -d -m 0700 "$(dirname -- "$load_hold_file")"
    umask 077
    printf 'project_id=%s\ntasks=10000\n' "${load_project_ids[0]}" >"$load_hold_file"
    chmod 0600 "$load_hold_file"
    printf 'load_hold=ready file=%s timeout_s=%s\n' "$load_hold_file" "$hold_timeout"
    for _ in $(seq 1 "$((hold_timeout / 2))"); do
      [ -e "$load_hold_file.release" ] && break
      sleep 2
    done
    test -e "$load_hold_file.release"
    rm -f "$load_hold_file" "$load_hold_file.release"
    printf 'load_hold=released\n'
  fi

  for project_id in "${load_project_ids[@]}"; do
    api_delete "/projects/$project_id"
  done
  load_project_ids=()
  test "$(database_load_task_count)" = "$load_baseline_tasks"
  printf 'load_fixture=pass samples=idle,1k,10k cleanup=pass\n'
}

upgrade_probe() {
  : "${workspace:?ANAS_TEST_WORKSPACE is required for upgrade mode}"
  : "${upgrade_deployment:?ANAS_TEST_UPGRADE_DEPLOYMENT is required for upgrade mode}"
  : "${rollback_deployment:?ANAS_TEST_ROLLBACK_DEPLOYMENT is required for upgrade mode}"
  install -d -m 0700 "$report_dir"

  local before_counts after_counts before_active rc
  local incompatible_report=$report_dir/vikunja-upgrade-incompatible-rollback.json
  local incompatible_log=$report_dir/vikunja-upgrade-incompatible-rollback.log
  local rollback_report=$report_dir/vikunja-upgrade-compatible-rollback.json
  local rollback_log=$report_dir/vikunja-upgrade-compatible-rollback.log
  local return_report=$report_dir/vikunja-upgrade-return.json
  local return_log=$report_dir/vikunja-upgrade-return.log

  assert_active_deployment "$upgrade_deployment" "$upgrade_revision"
  assert_runtime_revision_image "$upgrade_revision"
  before_counts=$(database_object_counts)
  before_active=$(active_deployment_id)

  if [ -n "$incompatible_rollback_deployment" ]; then
    set +e
    "$anas_cmd" rollback "$incompatible_rollback_deployment" -w "$workspace" --json \
      >"$incompatible_report" 2>"$incompatible_log"
    rc=$?
    set -e
    chmod 0600 "$incompatible_report" "$incompatible_log"
    test "$rc" = 4
    jq -e '.ok == false and .error.code == "credential_store_mismatch"' \
      "$incompatible_report" >/dev/null
    test "$(active_deployment_id)" = "$before_active"
    test "$(database_object_counts)" = "$before_counts"
    printf 'upgrade_rollback_boundary=pass incompatible_credentials=rejected active_unchanged=true data_unchanged=true\n'
  fi

  "$anas_cmd" rollback "$rollback_deployment" -w "$workspace" --json \
    >"$rollback_report" 2>"$rollback_log"
  chmod 0600 "$rollback_report" "$rollback_log"
  upgrade_restore_needed=true
  jq -e --arg deployment "$rollback_deployment" '
    .ok == true and .deployment_id == $deployment and .data_touched == false
  ' "$rollback_report" >/dev/null
  assert_active_deployment "$rollback_deployment" "$rollback_revision"
  assert_runtime_revision_image "$rollback_revision"
  after_counts=$(database_object_counts)
  test "$after_counts" = "$before_counts"

  "$anas_cmd" rollback "$upgrade_deployment" -w "$workspace" --json \
    >"$return_report" 2>"$return_log"
  chmod 0600 "$return_report" "$return_log"
  jq -e --arg deployment "$upgrade_deployment" '
    .ok == true and .deployment_id == $deployment and .data_touched == false
  ' "$return_report" >/dev/null
  assert_active_deployment "$upgrade_deployment" "$upgrade_revision"
  assert_runtime_revision_image "$upgrade_revision"
  test "$(database_object_counts)" = "$before_counts"
  upgrade_restore_needed=false
  printf 'upgrade_round_trip=pass rollback_revision=%s upgrade_revision=%s data_touched=false object_counts=%s\n' \
    "$rollback_revision" "$upgrade_revision" "$before_counts"
}

restore_seed_fixture() {
  local stamp webhook_secret status
  stamp=$(date +%s)
  webhook_secret=$(openssl rand -hex 32)
  create_api_token

  api_json POST /projects \
    "$(jq -cn --arg title "ANAS restore E2E $stamp" '{title:$title,description:"backup restore fixture"}')" \
    201 "$workdir/restore-project.json"
  restore_project_id=$(jq -r '.id' "$workdir/restore-project.json")
  test "$restore_project_id" -gt 0
  restore_fixture_created=true

  api_json POST "/projects/$restore_project_id/tasks" \
    "$(jq -cn --arg title "Restore task $stamp" '{title:$title,description:"restore target"}')" \
    201 "$workdir/restore-task.json"
  restore_task_id=$(jq -r '.id' "$workdir/restore-task.json")
  test "$restore_task_id" -gt 0

  api_json POST "/tasks/$restore_task_id/comments" \
    '{"comment":"ANAS restore E2E comment"}' 201 "$workdir/restore-comment.json"
  restore_comment_id=$(jq -r '.id' "$workdir/restore-comment.json")
  test "$restore_comment_id" -gt 0

  printf 'Vikunja restore attachment %s\n' "$stamp" >"$workdir/restore-attachment.txt"
  status=$(curl_vikunja -o "$workdir/restore-attachment.json" -w '%{http_code}' \
    -H "Authorization: Bearer $api_token" \
    -F "files=@$workdir/restore-attachment.txt;type=text/plain" \
    "$vikunja_url/api/v2/tasks/$restore_task_id/attachments")
  test "$status" = 201
  restore_attachment_id=$(jq -r '.success[0].id' "$workdir/restore-attachment.json")
  test "$restore_attachment_id" -gt 0

  api_json POST "/projects/$restore_project_id/webhooks" \
    "$(jq -cn --arg secret "$webhook_secret" \
      '{target_url:"https://restore.invalid/vikunja",secret:$secret,events:["task.created"]}')" \
    201 "$workdir/restore-webhook.json"
  restore_webhook_id=$(jq -r '.id' "$workdir/restore-webhook.json")
  test "$restore_webhook_id" -gt 0
  ! jq -e --arg secret "$webhook_secret" '.. | strings | select(. == $secret)' \
    "$workdir/restore-webhook.json" >/dev/null
  printf 'restore_seed=ready project=%s task=%s comment=%s attachment=%s webhook=%s api_token=created\n' \
    "$restore_project_id" "$restore_task_id" "$restore_comment_id" \
    "$restore_attachment_id" "$restore_webhook_id"
}

restore_validate_fixture() {
  local status issuer subject
  assert_api_token

  status=$(curl_vikunja -o "$workdir/restored-project.json" -w '%{http_code}' \
    -H "Authorization: Bearer $api_token" \
    "$vikunja_url/api/v2/projects/$restore_project_id")
  test "$status" = 200
  test "$(jq -r '.id' "$workdir/restored-project.json")" = "$restore_project_id"
  printf 'restore_object=pass kind=project id=%s\n' "$restore_project_id"

  status=$(curl_vikunja -o "$workdir/restored-task.json" -w '%{http_code}' \
    -H "Authorization: Bearer $api_token" \
    "$vikunja_url/api/v2/tasks/$restore_task_id")
  test "$status" = 200
  test "$(jq -r '.id' "$workdir/restored-task.json")" = "$restore_task_id"
  printf 'restore_object=pass kind=task id=%s\n' "$restore_task_id"

  status=$(curl_vikunja -o "$workdir/restored-comment.json" -w '%{http_code}' \
    -H "Authorization: Bearer $api_token" \
    "$vikunja_url/api/v2/tasks/$restore_task_id/comments/$restore_comment_id")
  test "$status" = 200
  test "$(jq -r '.id' "$workdir/restored-comment.json")" = "$restore_comment_id"
  printf 'restore_object=pass kind=comment id=%s\n' "$restore_comment_id"

  status=$(curl_vikunja -o "$workdir/restored-attachment.txt" -w '%{http_code}' \
    -H "Authorization: Bearer $api_token" \
    "$vikunja_url/api/v2/tasks/$restore_task_id/attachments/$restore_attachment_id")
  test "$status" = 200
  cmp "$workdir/restore-attachment.txt" "$workdir/restored-attachment.txt"
  printf 'restore_object=pass kind=attachment id=%s content=matched\n' "$restore_attachment_id"

  status=$(curl_vikunja -o "$workdir/restored-webhooks.json" -w '%{http_code}' \
    -H "Authorization: Bearer $api_token" \
    "$vikunja_url/api/v2/projects/$restore_project_id/webhooks")
  test "$status" = 200
  jq -e --argjson id "$restore_webhook_id" '
    (if type == "array" then . else .items end) | any(.id == $id)
  ' "$workdir/restored-webhooks.json" >/dev/null
  printf 'restore_object=pass kind=webhook id=%s\n' "$restore_webhook_id"

  issuer=${restore_oidc_binding%%|*}
  subject=${restore_oidc_binding#*|}
  test -n "$issuer"
  test -n "$subject"
  test "$(database_oidc_binding)" = "$restore_oidc_binding"
  printf 'restore_fixture_validation=pass project,task,comment,attachment,oidc-binding,api-token,webhook\n'
}

restore_delete_fixture_strict() {
  api_delete_jwt "/projects/$restore_project_id/webhooks/$restore_webhook_id"
  restore_webhook_id=
  api_delete_jwt "/tasks/$restore_task_id/attachments/$restore_attachment_id"
  restore_attachment_id=
  api_delete_jwt "/tasks/$restore_task_id/comments/$restore_comment_id"
  restore_comment_id=
  api_delete_jwt "/tasks/$restore_task_id"
  restore_task_id=
  api_delete_jwt "/projects/$restore_project_id"
  restore_project_id=
  restore_fixture_created=false
  delete_created_api_token
}

restore_probe() {
  : "${workspace:?ANAS_TEST_WORKSPACE is required for restore mode}"
  : "${api_jwt:?ANAS_TEST_JWT is required for restore mode}"
  : "${api_username:?ANAS_TEST_USERNAME is required for restore mode}"
  : "${api_password:?ANAS_TEST_PASSWORD is required for restore mode}"
  : "${oidc_session_file:?ANAS_TEST_OIDC_SESSION_FILE is required for restore mode}"
  : "${restore_root:?ANAS_TEST_RESTORE_ROOT is required for restore mode}"
  case "$restore_root" in
    /*anas*vikunja*test*|*/anas-vikunja-e2e/*) ;;
    *) printf 'ANAS_TEST_RESTORE_ROOT must be an absolute Vikunja test path\n' >&2; return 2 ;;
  esac
  test "$restore_root" != "$workspace"
  install -d -m 0700 "$restore_root" "$report_dir"
  restore_run_root=$(mktemp -d "$restore_root/run.XXXXXX")
  chmod 0700 "$restore_run_root"
  restore_backup_dir=$restore_run_root/backup
  restore_workspace=$restore_run_root/restored
  install -d -m 0700 "$restore_backup_dir"
  umask 077

  local baseline_counts seeded_counts restored_counts source_deployment
  local source_secret_digest restored_secret_digest saved_run_root
  local create_report=$report_dir/vikunja-restore-backup-create.json
  local create_log=$report_dir/vikunja-restore-backup-create.log
  local verify_report=$report_dir/vikunja-restore-backup-verify.json
  local restore_dry_report=$report_dir/vikunja-restore-dry-run.json
  local restore_report=$report_dir/vikunja-restore.json
  local restore_log=$report_dir/vikunja-restore.log
  local target_stop_log=$report_dir/vikunja-restore-target-stop.log
  local source_start_log=$report_dir/vikunja-restore-source-start.log

  baseline_counts=$(database_object_counts)
  restore_seed_fixture
  seeded_counts=$(database_object_counts)
  test "$seeded_counts" != "$baseline_counts"
  restore_oidc_binding=$(database_oidc_binding)
  test -n "${restore_oidc_binding%%|*}"
  test -n "${restore_oidc_binding#*|}"
  source_deployment=$(workspace_active_deployment_id "$workspace")
  source_secret_digest=$(sha256sum "$workspace/.anas/secrets.yml" | awk '{print $1}')

  "$anas_cmd" backup create -w "$workspace" --to "$restore_backup_dir" \
    --mode snapshot -y --json >"$create_report" 2>"$create_log"
  chmod 0600 "$create_report" "$create_log"
  jq -e '.ok == true and .mode == "snapshot" and (.backup_id | length > 0)' \
    "$create_report" >/dev/null
  restore_backup_id=$(jq -r '.backup_id' "$create_report")
  restore_snapshot_id=$(jq -r '.snapshot_id // empty' "$create_report")
  wait_healthy "$vikunja_container"
  wait_http_and_auth_policy

  "$anas_cmd" backup verify --to "$restore_backup_dir" --backup-id "$restore_backup_id" \
    --json >"$verify_report"
  chmod 0600 "$verify_report"
  jq -e '.ok == true and (.problems | length == 0)' "$verify_report" >/dev/null

  "$anas_cmd" init "$restore_workspace" -y >/dev/null
  "$anas_cmd" backup restore --from "$restore_backup_dir" -w "$restore_workspace" \
    --backup-id "$restore_backup_id" --dry-run --json >"$restore_dry_report"
  chmod 0600 "$restore_dry_report"
  jq -e '.ok == true and .dry_run == true and (.would_replace | length > 0)' \
    "$restore_dry_report" >/dev/null
  "$anas_cmd" backup restore --from "$restore_backup_dir" -w "$restore_workspace" \
    --backup-id "$restore_backup_id" -y --json >"$restore_report" 2>"$restore_log"
  chmod 0600 "$restore_report" "$restore_log"
  jq -e '.ok == true and .verify.ok == true and
    ([.restored[]] | index("data") != null) and
    ([.restored[]] | index("secrets") != null) and
    ([.restored[]] | index("active_deployment") != null)
  ' "$restore_report" >/dev/null

  restore_original_stopped=true
  "$anas_cmd" stop -w "$workspace" >"$report_dir/vikunja-restore-source-stop.log" 2>&1
  chmod 0600 "$report_dir/vikunja-restore-source-stop.log"
  restore_workspace_started=true
  "$anas_cmd" start -w "$restore_workspace" >"$report_dir/vikunja-restore-target-start.log" 2>&1
  chmod 0600 "$report_dir/vikunja-restore-target-start.log"
  wait_healthy "$vikunja_container"
  wait_http_and_auth_policy

  test "$(workspace_active_deployment_id "$restore_workspace")" = "$source_deployment"
  restored_secret_digest=$(sha256sum "$restore_workspace/.anas/secrets.yml" | awk '{print $1}')
  test "$restored_secret_digest" = "$source_secret_digest"
  restored_counts=$(database_object_counts)
  test "$restored_counts" = "$seeded_counts"
  restore_validate_fixture
  refresh_oidc_session
  local status
  status=$(curl_vikunja -o "$workdir/restored-user.json" -w '%{http_code}' \
    -H "Authorization: Bearer $api_jwt" "$vikunja_url/api/v1/user")
  test "$status" = 200
  test "$(jq -r '.username' "$workdir/restored-user.json")" = "$api_username"
  test "$(database_oidc_binding)" = "$restore_oidc_binding"
  printf 'restore_oidc_relogin=pass username=%s association=unchanged\n' "$api_username"

  "$anas_cmd" stop -w "$restore_workspace" >"$target_stop_log" 2>&1
  chmod 0600 "$target_stop_log"
  restore_workspace_started=false
  "$anas_cmd" start -w "$workspace" >"$source_start_log" 2>&1
  chmod 0600 "$source_start_log"
  wait_healthy "$vikunja_container"
  wait_http_and_auth_policy
  restore_original_stopped=false
  test "$(database_object_counts)" = "$seeded_counts"
  refresh_oidc_session
  restore_delete_fixture_strict
  test "$(database_object_counts)" = "$baseline_counts"

  if [ -n "$restore_snapshot_id" ]; then
    if ! "$anas_cmd" snapshot delete "$restore_snapshot_id" -w "$workspace" -y --json \
      >"$report_dir/vikunja-restore-source-snapshot-delete.json"; then
      remove_source_snapshot "$restore_snapshot_id"
    fi
    chmod 0600 "$report_dir/vikunja-restore-source-snapshot-delete.json"
    restore_snapshot_id=
  fi
  remove_test_subvolume "$restore_workspace/data"
  remove_test_subvolume "$restore_backup_dir/$restore_backup_id/data"
  saved_run_root=$restore_run_root
  sudo -n rm -rf -- "$saved_run_root"
  test ! -e "$saved_run_root"
  restore_run_root=
  printf 'backup_restore=pass mode=snapshot empty_workspace=true deployment=%s secret_store=matched object_counts=%s cleanup=pass\n' \
    "$source_deployment" "$seeded_counts"
}

build_runtime_probe() {
  local source_dir=${ANAS_TEST_REPO_ROOT:-$(cd -- "$script_dir/../.." && pwd)}
  local tag=${ANAS_TEST_ARM64_IMAGE:-anas-vikunja-e2e:2.4.0-arm64}
  local docker_hub_registry=${ANAS_TEST_DOCKER_HUB_REGISTRY:-docker.io}
  local go_builder_registry=${ANAS_TEST_GO_BUILDER_REGISTRY:-$docker_hub_registry}
  local ghcr_registry=${ANAS_TEST_GHCR_REGISTRY:-ghcr.io}
  local chinese_build_speedup=${ANAS_TEST_CHINESE_BUILD_SPEEDUP:-false}
  local apk_mirror_url=${ANAS_TEST_APK_MIRROR_URL:-}
  local npm_registry_url=${ANAS_TEST_NPM_REGISTRY_URL:-https://registry.npmjs.org}
  local goproxy_url=${ANAS_TEST_GOPROXY_URL:-}
  local github_proxy_prefix=${ANAS_TEST_BUILD_GITHUB_DOWNLOAD_PROXY_PREFIX:-}
  section 'arm64 cross-build'
  "$docker_cmd" build --platform linux/arm64 \
    --build-arg DOCKER_HUB_REGISTRY="$docker_hub_registry" \
    --build-arg GO_BUILDER_REGISTRY="$go_builder_registry" \
    --build-arg GHCR_REGISTRY="$ghcr_registry" \
    --build-arg CHINESE_BUILD_SPEEDUP="$chinese_build_speedup" \
    --build-arg APK_MIRROR_URL="$apk_mirror_url" \
    --build-arg NPM_REGISTRY_URL="$npm_registry_url" \
    --build-arg GOPROXY_URL="$goproxy_url" \
    --build-arg BUILD_GITHUB_DOWNLOAD_PROXY_PREFIX="$github_proxy_prefix" \
    --build-arg VIKUNJA_VERSION=2.4.0 \
    --tag "$tag" "$source_dir/modules/vikunja/vikunja"
  test "$($docker_cmd image inspect --format '{{.Architecture}}' "$tag")" = arm64
  printf 'cross_build=pass platform=linux/arm64 image=%s\n' "$tag"
  section 'amd64 runtime'
  wait_healthy "$vikunja_container"
  assert_runtime
  wait_http_and_auth_policy
}

case "$mode" in
  core|postgres|mariadb)
    section 'runtime and public policy'
    wait_healthy "$vikunja_container"
    assert_runtime
    wait_http_and_auth_policy
    section 'database contract and restart persistence'
    assert_database_mapping
    restart_probe
    ;;
  api)
    create_api_token
    assert_api_token
    api_v2_crud_probe
    ;;
  webhook)
    create_api_token
    assert_api_token
    webhook_probe
    ;;
  build-runtime)
    build_runtime_probe
    ;;
  rotation)
    rotation_probe
    ;;
  load)
    load_probe
    ;;
  upgrade)
    upgrade_probe
    ;;
  restore)
    restore_probe
    ;;
  rotation-dry)
    rotation_dry_run_probe
    ;;
  *)
    printf 'usage: %s {build-runtime|core|postgres|mariadb|api|webhook|rotation|rotation-dry|upgrade|restore|load}\n' "$0" >&2
    exit 2
    ;;
esac

printf '\nPASS: Vikunja E2E mode=%s domain=%s\n' "$mode" "$domain"
