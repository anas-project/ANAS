#!/usr/bin/env bash
# Casdoor pinned-version multi-architecture, restart, upgrade, and rollback E2E.
set -Eeuo pipefail
umask 077

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
export ANAS_TEST_CONTAINER_PREFIX=${ANAS_TEST_CONTAINER_PREFIX:-anas_casdoor_}
# shellcheck source=server-iam-matrix-common.sh
source "$script_dir/server-iam-matrix-common.sh"

workspace=${ANAS_TEST_WORKSPACE:?ANAS_TEST_WORKSPACE is required}
anas_cmd=${ANAS_TEST_ANAS_CMD:-anas}
repo_root=${ANAS_TEST_REPO_ROOT:?ANAS_TEST_REPO_ROOT is required}
lifecycle_root=${ANAS_TEST_LIFECYCLE_ROOT:?ANAS_TEST_LIFECYCLE_ROOT is required}
fixture_bin=${CASDOOR_LOGOUT_FIXTURE_BIN:-/home/whl/anas-casdoor-m3-e2e/casdoor-oidc-logout-consumer}
report_dir=${ANAS_TEST_REPORT_DIR:-$workspace/test-env/reports}
casdoor="${prefix}casdoor"
dirwatch="${prefix}casdoor_dirwatch"
consumer="${prefix}oauth2_proxy"
timeout=${CASDOOR_PROTOCOL_E2E_TIMEOUT:-420}
test_suffix=${ANAS_TEST_MATRIX_SUFFIX:-$(date +%H%M%S)}
test_user="icl${test_suffix}"
test_password=${ANAS_TEST_MATRIX_PASSWORD:-Anas-Iam-${test_suffix}-E2e!}
arm64_image=${ANAS_TEST_ARM64_IMAGE:-anas-casdoor-e2e:3.143.0-r8-arm64}
qemu_image=${ANAS_TEST_QEMU_IMAGE:-m.daocloud.io/docker.io/tonistiigi/binfmt@sha256:465d3fdd28d0f2b871ba4b4ec98bd183292e96167f00d9fd40bd249f8632d705}
qemu_container="${prefix}binfmt_extract_${test_suffix}"
run_root=
original_deployment=
test_revision_deployment=
failure_line=
original_lock_backup=
qemu_binary=
qemu_extract_created=false

case "$lifecycle_root" in
  /*anas*casdoor*e2e*) ;;
  *) printf 'ANAS_TEST_LIFECYCLE_ROOT must be an absolute Casdoor E2E path\n' >&2; exit 2 ;;
esac
test "$repo_root" != "$lifecycle_root"

section() { printf '\n== %s ==\n' "$1"; }

wait_ready() {
  local container=$1 deadline status
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    status=$("$docker_cmd" inspect --format \
      '{{.State.Status}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$container" 2>/dev/null || true)
    if [ "$status" = 'running|healthy' ] || [ "$status" = 'running|' ]; then
      if [ "$container" = "$casdoor" ] &&
        ! "$docker_cmd" exec "$casdoor" /opt/anas/bin/casdoor-helper healthcheck \
          >/dev/null 2>&1; then
        sleep 5
        continue
      fi
      return 0
    fi
    sleep 5
  done
  printf 'container %s did not become ready; last state=%s\n' "$container" "$status" >&2
  return 1
}

workspace_active() {
  "$anas_cmd" status -w "$workspace" --json | jq -er '.active_deployment'
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
  printf 'Casdoor did not converge lifecycle user %s; last state: %s\n' "$test_user" "$current" >&2
  return 1
}

refresh_consumer_material() {
  issuer=$("$docker_cmd" exec "$casdoor" printenv CASDOOR_DOMAIN_FULL)
  client_id=$("$docker_cmd" exec "$casdoor" printenv ANAS_IAM_CLIENT__OAUTH2_PROXY__CLIENT_ID)
  redirect_uri=$("$docker_cmd" exec "$casdoor" printenv ANAS_IAM_CLIENT__OAUTH2_PROXY__REDIRECT_URIS |
    awk -F, '{print $1}')
  fixture_image=$("$docker_cmd" inspect --format '{{.Config.Image}}' "$casdoor")
  "$docker_cmd" exec "$casdoor" printenv ANAS_IAM_CLIENT__OAUTH2_PROXY__CLIENT_SECRET \
    >"$run_root/client-secret"
  chmod 0600 "$run_root/client-secret"
  test "$("$docker_cmd" inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$consumer" |
    sed -n 's/^OAUTH2_PROXY_CLIENT_SECRET=//p' | sha256sum | awk '{print $1}')" = \
    "$(sha256sum "$run_root/client-secret" | awk '{print $1}')"
}

token_login() {
  local state_file=$1
  "$docker_cmd" run --rm --network "container:$casdoor" \
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
    -e "CASDOOR_FIXTURE_REDIRECT_URI=$redirect_uri" \
    "$fixture_image" token-login --username "$test_user" \
      --password-file /state/user-password --state-file "/state/$state_file"
}

admin_login() {
  "$anas_cmd" admin local credential casdoor break_glass -w "$workspace" --json \
    | jq -er '.account.password' \
    | "$docker_cmd" run --rm -i --network "container:$casdoor" \
      --entrypoint /fixture -v "$fixture_bin:/fixture:ro" \
      -e "CASDOOR_FIXTURE_ISSUER=$issuer" \
      -e CASDOOR_FIXTURE_INTERNAL_ORIGIN=http://127.0.0.1:8000 \
      "$fixture_image" admin-login
}

jwks_digest() {
  "$docker_cmd" exec "$casdoor" sh -c \
    'wget -q -O - http://127.0.0.1:8000/.well-known/jwks' |
    jq -cS . | sha256sum | awk '{print $1}'
}

validate_state() {
  local label=$1 expected_deployment=$2 current
  local login_file="${label}-login.json"
  for container in "$dc" "$casdoor" "$dirwatch" "$consumer"; do
    wait_ready "$container"
  done
  test "$(workspace_active)" = "$expected_deployment"
  refresh_consumer_material
  test "$(sha256sum "$run_root/client-secret" | awk '{print $1}')" = "$source_client_digest"
  test "$(jwks_digest)" = "$source_jwks_digest"
  wait_for_user >"$run_root/${label}-user.json"
  test "$(jq -er '.id' "$run_root/${label}-user.json")" = "$source_user_id"
  test "$(jq -er '.externalId' "$run_root/${label}-user.json")" = "$source_anchor"
  token_login "$login_file" >"$run_root/${label}-login.out"
  test "$(jq -er '.sub' "$run_root/$login_file")" = "$source_sub"
  admin_login >"$run_root/${label}-admin.json"
  jq -e '.login == "accepted" and .username == "admin_casdoor" and .is_admin == true' \
    "$run_root/${label}-admin.json" >/dev/null
  printf 'lifecycle_state=pass phase=%s deployment=%s user=true client=true signing=true cursor=true admin=true oidc=true\n' \
    "$label" "$expected_deployment"
}

cleanup_user() {
  samba_tool user delete "$test_user" >/dev/null 2>&1 || true
}

restore_original_lock() {
  if [ -n "$original_lock_backup" ] && [ -f "$original_lock_backup" ]; then
    install -m 0600 "$original_lock_backup" "$workspace/config.lock.yml"
  fi
}

cleanup() {
  local status=$?
  set +e
  if [ "$qemu_extract_created" = true ]; then
    "$docker_cmd" rm -f "$qemu_container" >/dev/null 2>&1 || true
  fi
  if [ -n "$original_deployment" ] && [ "$(workspace_active 2>/dev/null)" != "$original_deployment" ]; then
    "$anas_cmd" rollback "$original_deployment" -w "$workspace" --json >/dev/null 2>&1 || true
  fi
  restore_original_lock
  cleanup_user
  if [ -n "$run_root" ]; then
    case "$run_root" in
      "$lifecycle_root"/run.*) rm -rf -- "$run_root" ;;
    esac
  fi
  if [ "$status" -ne 0 ]; then
    printf 'FAIL: Casdoor lifecycle E2E line=%s\n' "${failure_line:-unknown}" >&2
  fi
  exit "$status"
}
trap 'failure_line=$LINENO' ERR
trap cleanup EXIT HUP INT TERM

section "preflight and stable identity seed"
test -x "$fixture_bin"
test -f "$repo_root/modules/casdoor/casdoor/Dockerfile"
grep -Fq 'ARG CASDOOR_SOURCE_URL=https://github.com/casdoor/casdoor/archive/1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e.tar.gz' \
  "$repo_root/modules/casdoor/casdoor/Dockerfile"
grep -Fq 'ARG CASDOOR_SOURCE_SHA256=365d61c7e8cae30a6b1a135204c74145c9ce6c692068d3fc044404703c0f9460' \
  "$repo_root/modules/casdoor/casdoor/Dockerfile"
install -d -m 0700 "$lifecycle_root" "$report_dir"
run_root=$(mktemp -d "$lifecycle_root/run.XXXXXX")
chmod 0700 "$run_root"
original_lock_backup=$run_root/original-config.lock.yml
install -m 0600 "$workspace/config.lock.yml" "$original_lock_backup"
printf '%s\n' "$test_password" >"$run_root/user-password"
chmod 0600 "$run_root/user-password"
original_deployment=$(workspace_active)
for container in "$dc" "$casdoor" "$dirwatch" "$consumer"; do
  wait_ready "$container"
done
cleanup_user
samba_tool user add "$test_user" "$test_password" --userou='OU=People' \
  --mail-address="${test_user}@m5.nas.test" >/dev/null
samba_tool user setexpiry "$test_user" --noexpiry >/dev/null
samba_tool user rename "$test_user" --display-name="Casdoor lifecycle E2E $test_user" >/dev/null
samba_tool group addmembers Admins "$test_user" >/dev/null
wait_anchor "$test_user"
wait_for_user >"$run_root/source-user.json"
source_anchor=$(jq -er '.externalId' "$run_root/source-user.json")
source_user_id=$(jq -er '.id' "$run_root/source-user.json")
refresh_consumer_material
source_client_digest=$(sha256sum "$run_root/client-secret" | awk '{print $1}')
source_jwks_digest=$(jwks_digest)
source_cursor_digest=$(sha256sum "$workspace/data/casdoor/dirwatch/cursor.json" | awk '{print $1}')
token_login source-login.json >"$run_root/source-login.out"
source_sub=$(jq -er '.sub' "$run_root/source-login.json")

section "amd64 cold start and process restart"
amd64_image=$("$docker_cmd" inspect --format '{{.Config.Image}}' "$casdoor")
test "$("$docker_cmd" image inspect --format '{{.Architecture}}' "$amd64_image")" = amd64
"$anas_cmd" stop -w "$workspace" >"$report_dir/casdoor-lifecycle-cold-stop.log" 2>&1
"$anas_cmd" start -w "$workspace" >"$report_dir/casdoor-lifecycle-cold-start.log" 2>&1
validate_state cold-start "$original_deployment"
"$docker_cmd" restart "$casdoor" >"$report_dir/casdoor-lifecycle-container-restart.log"
validate_state restart "$original_deployment"
test "$(sha256sum "$workspace/data/casdoor/dirwatch/cursor.json" | awk '{print $1}')" = "$source_cursor_digest"
printf 'amd64_lifecycle=pass build=true cold_start=true restart=true\n'

section "fixed 3.143.0 packaging-revision upgrade"
test_modules=$run_root/modules-r9
cp -a "$repo_root/modules" "$test_modules"
cp -a "$repo_root/contracts" "$run_root/contracts"
cp -a "$repo_root/internal" "$repo_root/go.mod" "$repo_root/go.sum" "$run_root/"
sed -i 's/^revision: 8$/revision: 9/' "$test_modules/casdoor/module.yml"
sed -i 's/anas-casdoor:3\.143\.0-r8/anas-casdoor:3.143.0-r9/g' \
  "$test_modules/casdoor/docker-compose.yml"
grep -Fq 'version: 3.143.0' "$test_modules/casdoor/module.yml"
grep -Fq 'revision: 9' "$test_modules/casdoor/module.yml"
"$anas_cmd" lock -w "$workspace" --module-root "$test_modules" --json \
  >"$report_dir/casdoor-lifecycle-r9-lock.json"
"$anas_cmd" apply --build --no-snapshot -w "$workspace" --module-root "$test_modules" -y --json \
  >"$report_dir/casdoor-lifecycle-r9-apply.json" \
  2>"$report_dir/casdoor-lifecycle-r9-apply.log"
test_revision_deployment=$(workspace_active)
test "$test_revision_deployment" != "$original_deployment"
test "$(awk '/^    casdoor:/{found=1} found && /revision:/{print $2; exit}' \
  "$workspace/.anas/deployments/$test_revision_deployment/deployment.yml")" = 9
validate_state fixed-version-upgrade "$test_revision_deployment"

section "safe artifact rollback"
"$anas_cmd" rollback "$original_deployment" -w "$workspace" --json \
  >"$report_dir/casdoor-lifecycle-rollback.json" \
  2>"$report_dir/casdoor-lifecycle-rollback.log"
validate_state rollback "$original_deployment"
jq -e '.ok == true and .data_touched == false' \
  "$report_dir/casdoor-lifecycle-rollback.json" >/dev/null
restore_original_lock
cmp -s "$original_lock_backup" "$workspace/config.lock.yml"
printf 'fixed_version_upgrade=pass from=3.143.0-r8 to=3.143.0-r9-test data_preserved=true\n'
printf 'safe_rollback=pass target=3.143.0-r8 data_touched=false state_preserved=true\n'

section "arm64 pinned-source build and execution"
"$docker_cmd" buildx build --platform linux/arm64 --load \
  --build-arg DOCKER_HUB_REGISTRY=m.daocloud.io/docker.io \
  --build-arg CHINESE_BUILD_SPEEDUP=true \
  --build-arg BUILD_GITHUB_DOWNLOAD_PROXY_PREFIX=https://files.m.daocloud.io \
  --build-arg GOPROXY_URL=https://goproxy.cn,direct \
  -t "$arm64_image" "$repo_root/modules/casdoor/casdoor" \
  >"$report_dir/casdoor-lifecycle-arm64-build.log" 2>&1
test "$("$docker_cmd" image inspect --format '{{.Architecture}}' "$arm64_image")" = arm64
"$docker_cmd" pull "$qemu_image" >"$report_dir/casdoor-lifecycle-qemu-pull.log" 2>&1
test "$("$docker_cmd" image inspect --format '{{.Architecture}}' "$qemu_image")" = amd64
if "$docker_cmd" container inspect "$qemu_container" >/dev/null 2>&1; then
  printf 'temporary QEMU extraction container already exists: %s\n' "$qemu_container" >&2
  exit 1
fi
"$docker_cmd" create --name "$qemu_container" "$qemu_image" \
  >"$report_dir/casdoor-lifecycle-qemu-create.log"
qemu_extract_created=true
qemu_binary=$run_root/qemu-aarch64
"$docker_cmd" cp "$qemu_container:/usr/bin/qemu-aarch64" "$qemu_binary"
"$docker_cmd" rm "$qemu_container" >/dev/null
qemu_extract_created=false
chmod 0555 "$qemu_binary"
test -x "$qemu_binary"
"$docker_cmd" run --rm --platform linux/arm64 \
  --entrypoint /opt/anas/bin/qemu-aarch64 \
  -v "$qemu_binary:/opt/anas/bin/qemu-aarch64:ro" \
  "$arm64_image" -0 uname /bin/uname uname -m \
  >"$report_dir/casdoor-lifecycle-arm64-run.log" 2>&1
test "$(tr -d '\r\n' <"$report_dir/casdoor-lifecycle-arm64-run.log")" = aarch64
chmod 0600 "$report_dir"/casdoor-lifecycle-*
printf 'multi_arch=pass amd64=running arm64=build-and-run pinned_source=true\n'

cleanup_user
saved_run_root=$run_root
run_root=
rm -rf -- "$saved_run_root"
printf '\nCasdoor lifecycle E2E tests passed\n'
