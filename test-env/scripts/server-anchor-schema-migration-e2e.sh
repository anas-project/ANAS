#!/usr/bin/env bash
set -euo pipefail

# TEST_CASES: ANCHOR-R-003 ANCHOR-R-004 ANCHOR-R-005 ANCHOR-R-006 ANCHOR-R-007 ANCHOR-R-008 ANCHOR-R-011

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/../.." && pwd)
source "$script_dir/server-require-isolated-docker.sh"

docker_cmd=${DOCKER_CMD:-docker}
image=${ANAS_LEGACY_SAMBA_DC_IMAGE:-docker.cnb.cool/anas.dev/anas/anas-samba-dc:4.23.6-r7}
suffix="$$-$(date +%s)"
container="anas_pen_anchor_e2e_${suffix}"
fresh_container="${container}_fresh"
evidence_root=$(mktemp -d "${TMPDIR:-/tmp}/anas-pen-anchor-e2e.XXXXXX")
migration="$repo_root/modules/samba_dc/samba_dc/root/usr/local/bin/migrate-identity-anchor-oid.sh"
installer="$repo_root/modules/samba_dc/samba_dc/root/usr/local/bin/install-identity-schema.sh"

cleanup() {
  "$docker_cmd" rm -f "$container" "$fresh_container" >/dev/null 2>&1 || true
  rm -rf -- "$evidence_root"
}
trap cleanup EXIT HUP INT TERM

test -f "$migration"
test -f "$installer"
"$docker_cmd" image inspect "$image" >/dev/null

"$docker_cmd" run --rm -i \
  --name "$container" \
  --network none \
  --privileged \
  --entrypoint /bin/bash \
  -v "$migration:/candidate-migrate.sh:ro" \
  -v "$installer:/candidate-install.sh:ro" \
  -v "$evidence_root:/evidence" \
"$image" -s <<'CONTAINER'
set -euo pipefail
trap 'chmod -R a+rwx /evidence >/dev/null 2>&1 || true' EXIT

readonly base_dn='DC=pen-anchor,DC=test'
readonly schema_dn="CN=Schema,CN=Configuration,${base_dn}"
readonly legacy_oid='1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401.2.1'
readonly final_oid='1.3.6.1.4.1.66678.1.2.1'
readonly final_guid='db3786ae-3261-4d44-a2a1-588bfe3e41c5'

expect_failure() {
  local expected=$1
  shift
  local output status
  set +e
  output=$("$@" 2>&1)
  status=$?
  set -e
  if [ "$status" -eq 0 ]; then
    echo "expected command to fail: $*" >&2
    return 1
  fi
  printf '%s\n' "$output" | grep -F -- "$expected" >/dev/null || {
    echo "failure did not contain expected diagnostic: $expected" >&2
    printf '%s\n' "$output" >&2
    return 1
  }
}

rm -f /etc/samba/smb.conf
samba-tool domain provision \
  --server-role=dc \
  --use-rfc2307 \
  --domain=PENANCHOR \
  --realm=PEN-ANCHOR.TEST \
  --adminpass='Fixture-Only-42!' \
  --dns-backend=NONE >/tmp/provision.log

export SAMBA_DC_BASE_DN=$base_dn
export SAMBA_DC_DOMAIN_ACTION=provision
/usr/local/bin/install-identity-schema.sh >/tmp/legacy-install.log

samba-tool user add anchor-user 'Fixture-User-42!' >/dev/null
samba-tool group add anchor-group >/dev/null
cat >/tmp/legacy-values.ldif <<EOF
dn: CN=anchor-user,CN=Users,${base_dn}
changetype: modify
add: mS-DS-ConsistencyGuid
mS-DS-ConsistencyGuid:: EREREREREREREREREREREQ==
-
add: anasIdentityAnchor
anasIdentityAnchor: 11111111-1111-1111-1111-111111111111
-

dn: CN=anchor-group,CN=Users,${base_dn}
changetype: modify
add: mS-DS-ConsistencyGuid
mS-DS-ConsistencyGuid:: IiIiIiIiIiIiIiIiIiIiIg==
-
add: anasIdentityAnchor
anasIdentityAnchor: 22222222-2222-2222-2222-222222222222
-
EOF
ldbmodify -H /var/lib/samba/private/sam.ldb /tmp/legacy-values.ldif >/dev/null

echo '== read-only legacy preflight =='
bash /candidate-migrate.sh --check
default_output=$(bash /candidate-migrate.sh)
printf '%s\n' "$default_output" | grep -F 'read-only preflight passed' >/dev/null
test ! -e /var/lib/samba/.anas-identity-anchor-oid-migration.in-progress
test ! -e /var/lib/samba/.anas-identity-anchor-oid-migration.in-progress.new
ldbsearch -H /var/lib/samba/private/sam.ldb -b "$base_dn" \
  '(anasIdentityAnchor=*)' anasIdentityAnchor \
  | grep -F 'anasIdentityAnchor: 11111111-1111-1111-1111-111111111111' >/dev/null

echo '== incomplete-marker and execute argument guards =='
for marker in \
  /var/lib/samba/.anas-identity-anchor-oid-migration.in-progress \
  /var/lib/samba/.anas-identity-anchor-oid-migration.in-progress.new; do
  printf '%s\n' 'snapshot_id=interrupted-fixture' >"$marker"
  expect_failure 'an earlier identity-anchor OID migration did not finish' \
    bash /candidate-migrate.sh --check
  rm -f "$marker"
done
expect_failure '--execute requires a safe --snapshot-id' \
  bash /candidate-migrate.sh --execute
expect_failure '--execute requires --backup-dir on storage outside /var/lib/samba' \
  bash /candidate-migrate.sh --execute --snapshot-id fixture-verified-snapshot
expect_failure 'backup evidence must be outside the Samba data volume' \
  bash /candidate-migrate.sh --execute --snapshot-id fixture-verified-snapshot \
    --backup-dir /var/lib/samba/unsafe-evidence
test ! -e /var/lib/samba/.anas-identity-anchor-oid-migration.in-progress
test ! -e /var/lib/samba/.anas-identity-anchor-oid-migration.in-progress.new
test ! -e /var/lib/samba/unsafe-evidence
legacy_after_guards=$(ldbsearch -H /var/lib/samba/private/sam.ldb -b "$schema_dn" -s one \
  "(&(objectClass=attributeSchema)(attributeID=${legacy_oid})(!(isDefunct=TRUE)))" attributeID)
printf '%s\n' "$legacy_after_guards" | grep -F "attributeID: ${legacy_oid}" >/dev/null
expect_failure 'requires the documented offline PEN 66678 migration' \
  bash /candidate-install.sh

echo '== guarded offline migration =='
bash /candidate-migrate.sh \
  --execute \
  --snapshot-id fixture-verified-snapshot \
  --backup-dir /evidence/pen66678

echo '== schema, class, and value verification =='
echo '-- active schema'
active=$(ldbsearch -H /var/lib/samba/private/sam.ldb -b "$schema_dn" -s one \
  "(&(objectClass=attributeSchema)(lDAPDisplayName=anasIdentityAnchor)(!(isDefunct=TRUE)))" \
  attributeID schemaIDGUID)
printf '%s\n' "$active" | grep -F "attributeID: ${final_oid}" >/dev/null
printf '%s\n' "$active" | grep -F "schemaIDGUID: ${final_guid}" >/dev/null
echo '-- retired schema'
retired=$(ldbsearch -H /var/lib/samba/private/sam.ldb -b "$schema_dn" -s one \
  "(&(objectClass=attributeSchema)(attributeID=${legacy_oid}))" cn lDAPDisplayName schemaIDGUID isDefunct)
printf '%s\n' "$retired" | grep -F 'cn: anasIdentityAnchor-Legacy-GuidRoot' >/dev/null
printf '%s\n' "$retired" | grep -F 'lDAPDisplayName: anasIdentityAnchor-Legacy-GuidRoot' >/dev/null
printf '%s\n' "$retired" | grep -F 'schemaIDGUID: 7108c5a7-2290-45e0-9eba-eef087be58e3' >/dev/null
printf '%s\n' "$retired" | grep -F 'isDefunct: TRUE' >/dev/null
echo '-- User/Group class links'
for class_name in User Group; do
  ldbsearch -H /var/lib/samba/private/sam.ldb \
    -b "CN=${class_name},${schema_dn}" -s base mayContain \
    | grep -F 'mayContain: anasIdentityAnchor' >/dev/null
done
echo '-- restored anchor values'
for value in \
  11111111-1111-1111-1111-111111111111 \
  22222222-2222-2222-2222-222222222222; do
  value_result=$(ldbsearch -H /var/lib/samba/private/sam.ldb -b "$base_dn" \
    "(anasIdentityAnchor=${value})" dn)
  test "$(printf '%s\n' "$value_result" | grep -Ec '^dn::? ')" -eq 1
done
echo '-- external evidence permissions'
test -s /evidence/pen66678/legacy-values.ldif
test -s /evidence/pen66678/final-values.ldif
test "$(stat -c '%a' /evidence/pen66678)" = 700
sha256sum -c /evidence/pen66678/SHA256SUMS >/dev/null

echo '== completed-state no-op and new installer idempotence =='
bash /candidate-migrate.sh --check
completed_before=$(ldbsearch -H /var/lib/samba/private/sam.ldb -b "$schema_dn" -s one \
  '(|(attributeID=1.3.6.1.4.1.66678.1.2.1)(attributeID=1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401.2.1)(cn=User)(cn=Group))' \
  dn cn lDAPDisplayName attributeID schemaIDGUID isDefunct mayContain)
marker_before=$(sha256sum /var/lib/samba/.anas-identity-anchor-oid)
completed_execute_output=$(bash /candidate-migrate.sh --execute \
  --snapshot-id ignored-completed-state --backup-dir /evidence/must-not-be-created)
printf '%s\n' "$completed_execute_output" | grep -F 'no migration is needed' >/dev/null
test ! -e /evidence/must-not-be-created
test "$marker_before" = "$(sha256sum /var/lib/samba/.anas-identity-anchor-oid)"
completed_after=$(ldbsearch -H /var/lib/samba/private/sam.ldb -b "$schema_dn" -s one \
  '(|(attributeID=1.3.6.1.4.1.66678.1.2.1)(attributeID=1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401.2.1)(cn=User)(cn=Group))' \
  dn cn lDAPDisplayName attributeID schemaIDGUID isDefunct mayContain)
test "$completed_before" = "$completed_after"
bash /candidate-install.sh >/tmp/final-install-1.log
bash /candidate-install.sh >/tmp/final-install-2.log

echo '== unmarked partial schema fails closed =='
cat >/tmp/remove-final-group-link.ldif <<EOF
dn: CN=Group,${schema_dn}
changetype: modify
delete: mayContain
mayContain: anasIdentityAnchor
-
EOF
ldbmodify -H /var/lib/samba/private/sam.ldb \
  --option='dsdb:schema update allowed=true' /tmp/remove-final-group-link.ldif >/dev/null
expect_failure 'the final schema is not linked from both User and Group' \
  bash /candidate-migrate.sh --check

echo 'identity-anchor PEN migration E2E passed'
CONTAINER

"$docker_cmd" run --rm -i \
  --name "$fresh_container" \
  --network none \
  --privileged \
  --entrypoint /bin/bash \
  -v "$installer:/candidate-install.sh:ro" \
"$image" -s <<'FRESH_CONTAINER'
set -euo pipefail

readonly base_dn='DC=pen-fresh,DC=test'
readonly schema_dn="CN=Schema,CN=Configuration,${base_dn}"
readonly legacy_oid='1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401.2.1'
readonly final_oid='1.3.6.1.4.1.66678.1.2.1'
readonly final_guid='db3786ae-3261-4d44-a2a1-588bfe3e41c5'

expect_failure() {
  local expected=$1
  shift
  local output status
  set +e
  output=$("$@" 2>&1)
  status=$?
  set -e
  test "$status" -ne 0
  printf '%s\n' "$output" | grep -F -- "$expected" >/dev/null
}

rm -f /etc/samba/smb.conf
samba-tool domain provision \
  --server-role=dc \
  --use-rfc2307 \
  --domain=PENFRESH \
  --realm=PEN-FRESH.TEST \
  --adminpass='Fixture-Only-42!' \
  --dns-backend=NONE >/tmp/fresh-provision.log

export SAMBA_DC_BASE_DN=$base_dn
export SAMBA_DC_DOMAIN_ACTION=join
expect_failure 'is absent from the joined forest; install the ANAS schema on its schema master' \
  bash /candidate-install.sh
legacy_count_before_install=$(ldbsearch -H /var/lib/samba/private/sam.ldb -b "$schema_dn" -s one \
  "(|(&(objectClass=attributeSchema)(attributeID=${final_oid}))(&(objectClass=attributeSchema)(attributeID=${legacy_oid})))" dn \
  | grep -Ec '^dn::? ' || true)
test "$legacy_count_before_install" -eq 0

export SAMBA_DC_DOMAIN_ACTION=provision
bash /candidate-install.sh >/tmp/fresh-install-1.log

fresh_schema=$(ldbsearch -H /var/lib/samba/private/sam.ldb -b "$schema_dn" -s one \
  '(&(objectClass=attributeSchema)(lDAPDisplayName=anasIdentityAnchor)(!(isDefunct=TRUE)))' \
  attributeID schemaIDGUID attributeSyntax oMSyntax isSingleValued rangeLower rangeUpper searchFlags)
for expected in \
  "attributeID: ${final_oid}" \
  "schemaIDGUID: ${final_guid}" \
  'attributeSyntax: 2.5.5.12' \
  'oMSyntax: 64' \
  'isSingleValued: TRUE' \
  'rangeLower: 36' \
  'rangeUpper: 36' \
  'searchFlags: 1'; do
  printf '%s\n' "$fresh_schema" | grep -F "$expected" >/dev/null
done
legacy_count=$(ldbsearch -H /var/lib/samba/private/sam.ldb -b "$schema_dn" -s one \
  "(&(objectClass=attributeSchema)(attributeID=${legacy_oid}))" dn \
  | grep -Ec '^dn::? ' || true)
test "$legacy_count" -eq 0
for class_name in User Group; do
  ldbsearch -H /var/lib/samba/private/sam.ldb \
    -b "CN=${class_name},${schema_dn}" -s base mayContain \
    | grep -F 'mayContain: anasIdentityAnchor' >/dev/null
done

fresh_before=$(ldbsearch -H /var/lib/samba/private/sam.ldb -b "$schema_dn" -s one \
  '(|(attributeID=1.3.6.1.4.1.66678.1.2.1)(cn=User)(cn=Group))' \
  dn cn lDAPDisplayName attributeID schemaIDGUID mayContain)
bash /candidate-install.sh >/tmp/fresh-install-2.log
fresh_after=$(ldbsearch -H /var/lib/samba/private/sam.ldb -b "$schema_dn" -s one \
  '(|(attributeID=1.3.6.1.4.1.66678.1.2.1)(cn=User)(cn=Group))' \
  dn cn lDAPDisplayName attributeID schemaIDGUID mayContain)
test "$fresh_before" = "$fresh_after"

echo 'identity-anchor fresh-install E2E passed'
FRESH_CONTAINER
