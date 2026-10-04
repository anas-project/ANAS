#!/usr/bin/env bash
# Real issued grants and cookies for two clients. Isolated Docker only.
set -Eeuo pipefail
umask 077
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$script_dir/server-iam-matrix-common.sh"
casdoor="${prefix}casdoor"
dirwatch="${prefix}casdoor_dirwatch"
nextcloud="${prefix}nextcloud"
fixture_a="${prefix}casdoor_scope_a"
fixture_b="${prefix}casdoor_scope_b"
fixture_bin=${CASDOOR_LOGOUT_FIXTURE_BIN:?}
application_b="app-anas-e2e-peer-${matrix_suffix}"
workdir=$(mktemp -d)
configured=false
peer_created=false
policy_changed=false
compose_dir=$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' "$dirwatch")
compose_project=$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project"}}' "$dirwatch")
issuer=$(docker exec "$casdoor" printenv CASDOOR_DOMAIN_FULL)
image=$(docker inspect --format '{{.Config.Image}}' "$casdoor")
client_id=$(docker exec "$casdoor" printenv ANAS_IAM_CLIENT__NEXTCLOUD__CLIENT_ID)
redirects=$(docker exec "$casdoor" printenv ANAS_IAM_CLIENT__NEXTCLOUD__REDIRECT_URIS)
backchannel=$(docker exec "$casdoor" printenv ANAS_IAM_CLIENT__NEXTCLOUD__OIDC_LOGOUT_URI)
docker exec "$nextcloud" printenv NEXTCLOUD_OIDC_CLIENT_SECRET > "$workdir/client-a"
openssl rand -hex 24 > "$workdir/client-b"
printf '%s\n' "$matrix_password" > "$workdir/user-password"
cp "$compose_dir/.env" "$workdir/override.env"
chmod 0600 "$workdir/override.env"
admin() { docker exec "$casdoor" cat /run/secrets/casdoor-break-glass-password | docker exec -i "$1" /fixture "${@:2}"; }
fixture() { docker exec "$1" /fixture "${@:2}"; }
restart_watcher() { (cd "$compose_dir" && docker compose -p "$compose_project" --env-file "${1:-$compose_dir/.env}" up -d --no-deps --force-recreate anas_casdoor_dirwatch >/dev/null 2>&1); }
cleanup() {
  for state in a.json b.json other-a.json other-b.json; do
    target=$fixture_a
    [[ "$state" == *b.json ]] && target=$fixture_b
    fixture "$target" user-logout --state-file "/state/$state" >/dev/null 2>&1 || true
  done
  if [ "$configured" = true ]; then admin "$fixture_a" restore --backup /state/application-original.json >/dev/null 2>&1 || true; fi
  if [ "$policy_changed" = true ]; then restart_watcher || true; fi
  if [ "$peer_created" = true ]; then admin "$fixture_b" delete-application >/dev/null 2>&1 || true; fi
  docker rm -f "$fixture_a" "$fixture_b" >/dev/null 2>&1 || true
  cleanup_matrix_users
  rm -rf "$workdir"
}
trap cleanup EXIT HUP INT TERM
trap 'printf "FAIL: two-client scope E2E line=%s\n" "$LINENO" >&2' ERR
for item in a b; do
  app=app-anas-nextcloud; peer_client_id=$client_id; port=18081; container=$fixture_a
  if [ "$item" = b ]; then app=$application_b; peer_client_id="anas-e2e-peer-$matrix_suffix"; port=18082; container=$fixture_b; fi
  docker run -d --name "$container" --network "container:$casdoor" --user "$(id -u):$(id -g)" --entrypoint /fixture \
    -v "$fixture_bin:/fixture:ro" -v "$workdir:/state" \
    -e "CASDOOR_FIXTURE_ISSUER=$issuer" -e CASDOOR_FIXTURE_INTERNAL_ORIGIN=http://127.0.0.1:8000 \
    -e "CASDOOR_FIXTURE_APPLICATION=$app" -e "CASDOOR_FIXTURE_CLIENT_ID=$peer_client_id" \
    -e "CASDOOR_FIXTURE_CLIENT_SECRET_FILE=/state/client-$item" \
    -e "CASDOOR_FIXTURE_REDIRECT_URI=http://127.0.0.1:$port/callback" \
    -e "CASDOOR_FIXTURE_BACKCHANNEL_URI=http://127.0.0.1:$port/backchannel" \
    -e "CASDOOR_FIXTURE_MANAGED_REDIRECT_URIS=$redirects" -e "CASDOOR_FIXTURE_MANAGED_BACKCHANNEL_URI=$backchannel" \
    "$image" serve --listen "127.0.0.1:$port" >/dev/null
done
sleep 2
admin "$fixture_a" configure --backup /state/application-original.json
configured=true
admin "$fixture_b" clone-application
peer_created=true
docker exec "$dirwatch" printenv CASDOOR_DIRWATCH_APPLICATIONS > "$workdir/policy.json"
python3 - "$workdir/override.env" "$workdir/policy.json" "$application_b" <<'PY'
import json, pathlib, sys
path, policy, app = sys.argv[1:]
value = json.loads(pathlib.Path(policy).read_text())
value.append({'application':app,'groups':[],'protocol':'oidc'})
key = 'CASDOOR_DIRWATCH_APPLICATIONS='
lines = pathlib.Path(path).read_text().splitlines()
assert sum(line.startswith(key) for line in lines) == 1
pathlib.Path(path).write_text('\n'.join(key + "'" + json.dumps(value, separators=(',', ':')) + "'" if line.startswith(key) else line for line in lines) + '\n')
PY
policy_changed=true
restart_watcher "$workdir/override.env"
create_matrix_users
deadline=$(( $(date +%s) + 420 ))
until docker exec "$casdoor" /opt/anas/bin/casdoor-helper directory-watch --get-user "anas/$direct_user" | jq -e '(.groups | index("anas/APP_nextcloud")) != null' >/dev/null; do
  test "$(date +%s)" -lt "$deadline"; sleep 2
done
fixture "$fixture_a" login --username "$direct_user" --password-file /state/user-password --state-file /state/a.json
fixture "$fixture_b" login --username "$direct_user" --password-file /state/user-password --state-file /state/b.json
fixture "$fixture_a" login --username "$all_user" --password-file /state/user-password --state-file /state/other-a.json
fixture "$fixture_b" login --username "$all_user" --password-file /state/user-password --state-file /state/other-b.json
test "$(jq -r '.sub' "$workdir/a.json")" = "$(jq -r '.sub' "$workdir/b.json")"
samba_tool group removemembers APP_nextcloud "$direct_user" >/dev/null
deadline=$(( $(date +%s) + 420 ))
until fixture "$fixture_a" probe --state-file /state/a.json --expect revoked >/dev/null 2>&1; do
  test "$(date +%s)" -lt "$deadline"; sleep 2
done
fixture "$fixture_a" refresh --state-file /state/a.json --expect rejected
fixture "$fixture_b" probe --state-file /state/b.json --expect active
fixture "$fixture_b" refresh --state-file /state/b.json --expect accepted
fixture "$fixture_a" probe --state-file /state/other-a.json --expect active
fixture "$fixture_b" probe --state-file /state/other-b.json --expect active
printf 'two_client_scope=passed affected_client=revoked same_user_peer_client=active other_user_both_clients=active\n'
