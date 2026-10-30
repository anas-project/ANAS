#!/usr/bin/env bash
# TEST_CASES: MAILMOD-T-014
# Internal M3 subset; not the formal mail Module's full acceptance suite.
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/../.." && pwd)
source "$script_dir/server-require-isolated-docker.sh"

docker_cmd=${DOCKER_CMD:-docker}
root=${MAIL_SCHEMA_TEST_ROOT:?an explicit new private mail-schema test directory is required}
image=${ANAS_SAMBA_DC_IMAGE:-ghcr.io/anas-project/anas-samba-dc:4.23.6-r13}
scope=${1:-${MAIL_SCHEMA_SCOPE:-full}}
case "$scope" in
  full) ;;
  addresses) test -f "${MAIL_PRIMARY_TEST_BINARY:?address scope requires the current portal/LDAP test binary}" ;;
  *) echo 'unknown mail schema test scope' >&2; exit 2 ;;
esac
case "$root" in
  /*anas-mail-schema-test-*) ;;
  *) echo 'refusing an unscoped mail schema test directory' >&2; exit 2 ;;
esac
test ! -e "$root"
umask 077
mkdir -p "$root/candidate" "$root/data/samba" "$root/data/etc" "$root/evidence" "$root/input"
container="anas_mail_schema_${BASHPID}"
probe_container="${container}_context"
probe_image="anas-mail-context-test:${BASHPID}"
cleanup() {
  status=$?
  trap - EXIT
  "$docker_cmd" logs "$container" >"$root/evidence/container.log" 2>&1 || true
  "$docker_cmd" rm -f -v "$container" >"$root/evidence/container-remove.log" 2>&1 || status=1
  "$docker_cmd" rm -f -v "$probe_container" >/dev/null 2>&1 || true
  "$docker_cmd" image rm "$probe_image" >"$root/evidence/context-image-remove.log" 2>&1 || true
  rm -f -- "$root/input/secrets.json" "$root/input/primary-live.json"
  printf '%s\n' "$status" >"$root/evidence/exit-code"
  exit "$status"
}
trap cleanup EXIT

# Exercise the actual context allowlist without rebuilding Ubuntu/Samba or
# uploading runtime .env files. The scratch probe never runs a process.
if [ "$scope" = full ]; then
mkdir -p "$root/context"
cp -R -- "$repo_root/modules/samba_dc/." "$root/context/"
printf '%s\n' 'ANAS_CONTEXT_CANARY=not-a-real-secret' >"$root/context/.env"
cat >"$root/input/context-probe.Dockerfile" <<'DOCKERFILE'
FROM scratch
COPY . /context/
DOCKERFILE
"$docker_cmd" build --network none --label anas.mail-context-test=true -t "$probe_image" \
  -f "$root/input/context-probe.Dockerfile" "$root/context" >"$root/evidence/context-build.log" 2>&1
"$docker_cmd" image inspect --format '{{.Id}}' "$probe_image" >"$root/evidence/context-image-id"
"$docker_cmd" create --name "$probe_container" "$probe_image" /not-executed >"$root/evidence/context-container-id"
"$docker_cmd" cp "$probe_container:/context/." - >"$root/context-output.tar"
python3 - "$root/context-output.tar" "$repo_root/modules/samba_dc/anchor_worker/mail_policy.py" <<'PY' >"$root/evidence/context-check.json"
import hashlib,json,sys,tarfile
from pathlib import Path
archive,policy=Path(sys.argv[1]),Path(sys.argv[2])
with tarfile.open(archive) as tf:
    contents={str(Path(m.name)):tf.extractfile(m).read() for m in tf.getmembers() if m.isfile()}
files=sorted(contents)
assert '.env' not in files
assert 'anchor_worker/mail_policy.py' in files,files
assert 'samba_dc/root/usr/local/bin/migrate-mail-primary.py' in files
assert all(name=='anchor_worker/mail_policy.py' or name=='samba_dc/Dockerfile' or name.startswith('samba_dc/root/') for name in files),files
assert contents['anchor_worker/mail_policy.py']==policy.read_bytes()
print(json.dumps({'passed':True,'files':len(files),'policy_sha256':hashlib.sha256(policy.read_bytes()).hexdigest(),'runtime_env_excluded':True}))
PY
"$docker_cmd" rm -v "$probe_container" >"$root/evidence/context-container-remove.log"
fi

for relative in \
  modules/samba_dc/samba_dc/root/usr/local/bin/install-mail-schema.py \
  modules/samba_dc/samba_dc/root/usr/local/bin/install-mail-directory.py \
  modules/samba_dc/samba_dc/root/usr/local/bin/migrate-mail-primary.py \
  modules/samba_dc/samba_dc/root/usr/local/bin/install-identity-schema.sh \
  modules/samba_dc/samba_dc/root/usr/local/share/anas/mail-schema.json \
  test-env/scripts/mail-directory-schema-fixture.py \
  test-env/scripts/mail-primary-worker-live.py; do
  mkdir -p "$root/candidate/$(dirname -- "$relative")"
  cp -- "$repo_root/$relative" "$root/candidate/$relative"
done
mkdir -p "$root/candidate/modules/samba_dc/anchor_worker"
cp -- "$repo_root/modules/samba_dc/anchor_worker/"*.py "$root/candidate/modules/samba_dc/anchor_worker/"
cp -- "$repo_root/modules/samba_dc/anchor_worker/mail_policy.py" "$root/candidate/modules/samba_dc/samba_dc/root/usr/local/share/anas/mail_policy.py"
python3 - "$root/input/secrets.json" <<'PY'
from pathlib import Path
import json,secrets,sys
path=Path(sys.argv[1])
path.write_text(json.dumps({name:secrets.token_urlsafe(30)+'aA9!' for name in ('Administrator','alice','bob','reader','portal','address','primary','outsider')}))
path.chmod(0o600)
PY
if [ -n "${MAIL_PRIMARY_TEST_BINARY:-}" ]; then
  test -f "$MAIL_PRIMARY_TEST_BINARY"
  cp -- "$MAIL_PRIMARY_TEST_BINARY" "$root/input/portal-primary.test"
  chmod 700 "$root/input/portal-primary.test"
  python3 - "$root/input/secrets.json" "$root/input/primary-live.json" <<'PY'
from pathlib import Path
import json,sys
secret=json.loads(Path(sys.argv[1]).read_text())
path=Path(sys.argv[2])
path.write_text(json.dumps({'URL':'ldaps://127.0.0.1:636','BindDN':'CN=portal,OU=Service Accounts,DC=mailschema,DC=invalid','Password':secret['portal'],'ReaderPassword':secret['reader'],'AddressPassword':secret['address'],'CAFile':'/var/lib/samba/private/tls/ca.pem'}))
path.chmod(0o600)
PY
fi
cat >"$root/input/start.sh" <<'SH'
#!/bin/bash
set -euo pipefail
python3 /candidate/test-env/scripts/mail-directory-schema-fixture.py provision
SAMBA_DC_BASE_DN=DC=mailschema,DC=invalid SAMBA_DC_DOMAIN_ACTION=provision bash /candidate/modules/samba_dc/samba_dc/root/usr/local/bin/install-identity-schema.sh
python3 /candidate/test-env/scripts/mail-directory-schema-fixture.py prepare
python3 /candidate/test-env/scripts/mail-directory-schema-fixture.py migrate
exec samba -i --no-process-group --option='tls enabled=yes' \
  --option='tls cafile=/var/lib/samba/private/tls/ca.pem' \
  --option='tls certfile=/var/lib/samba/private/tls/cert.pem' \
  --option='tls keyfile=/var/lib/samba/private/tls/key.pem'
SH
"$docker_cmd" image inspect --format '{{.Id}}' "$image" >"$root/evidence/image-id"
"$docker_cmd" run -d --name "$container" --label anas.mail-schema-test=true \
  --hostname dc.mailschema.invalid --memory 768m --cpus 2 --pids-limit 256 \
  --network bridge --entrypoint /bin/bash \
  -v "$root/candidate:/candidate:ro" -v "$root/input:/input:ro" \
  -v "$root/data/samba:/var/lib/samba" -v "$root/data/etc:/etc/samba" \
  -v "$root/evidence:/evidence" \
  "$image" /input/start.sh >"$root/evidence/container-id"

ready=false
for ((attempt=0; attempt<240; attempt++)); do
  if "$docker_cmd" exec "$container" ldbsearch -H ldap://127.0.0.1 -b '' -s base '(objectClass=*)' defaultNamingContext >"$root/evidence/readiness.log" 2>&1; then
    ready=true
    break
  fi
  if [ "$("$docker_cmd" inspect --format '{{.State.Running}}' "$container")" != true ]; then
    echo 'isolated schema fixture stopped; inspect private evidence' >&2
    exit 1
  fi
  sleep 1
done
test "$ready" = true
"$docker_cmd" exec "$container" samba --version >"$root/evidence/samba-version"
timeout 120 "$docker_cmd" exec "$container" python3 /candidate/test-env/scripts/mail-directory-schema-fixture.py verify >"$root/evidence/verify.log" 2>&1
if [ -n "${MAIL_PRIMARY_TEST_BINARY:-}" ]; then
  sha256sum "$root/input/portal-primary.test" >"$root/evidence/primary-test-binary.sha256"
  timeout 270 "$docker_cmd" exec -e ANAS_MAIL_PRIMARY_TEST_CONFIG=/input/primary-live.json "$container" \
    /input/portal-primary.test -test.v -test.run '^TestLDAP(PrimaryCreation|MailManagement)' -test.timeout 240s >"$root/evidence/primary-live.log" 2>&1
fi
if [ "$scope" = addresses ]; then
  printf '%s\n' 'isolated schema, ACL, portal creation and Mail address adapter passed; downstream worker and IAM excluded' >"$root/evidence/scope.txt"
  cat "$root/evidence/scope.txt"
  exit 0
fi
anchor_image=${ANAS_ANCHOR_IMAGE:?explicit cached Anchor Worker image required}
"$docker_cmd" image inspect --format '{{.Id}}' "$anchor_image" >"$root/evidence/anchor-image-id"
"$docker_cmd" exec "$container" cat /var/lib/samba/private/tls/ca.pem >"$root/input/anchor-ca.pem"
timeout 180 "$docker_cmd" run --rm --user "$(id -u):$(id -g)" --network "container:$container" \
  --read-only --cap-drop ALL --security-opt no-new-privileges:true \
  --entrypoint python3 -v "$root/candidate:/candidate:ro" -v "$root/input:/input:ro" \
  -v "$root/input/anchor-ca.pem:/test-ca.pem:ro" -v "$root/evidence:/evidence" \
  "$anchor_image" -B /candidate/test-env/scripts/mail-primary-worker-live.py >"$root/evidence/primary-worker-live.log" 2>&1
echo 'isolated mail schema, ACL and LDAP reservation subset passed'
