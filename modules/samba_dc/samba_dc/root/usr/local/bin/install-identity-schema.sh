#!/usr/bin/with-contenv bash
set -euo pipefail

# IANA assigned PEN 66678 to the ANAS maintainer.  Child assignments are
# governed by the repository OID registry and are never recycled.
readonly ANAS_SCHEMA_OID_ROOT="1.3.6.1.4.1.66678.1"
readonly ANAS_IDENTITY_ANCHOR_OID="${ANAS_SCHEMA_OID_ROOT}.2.1"
readonly ANAS_IDENTITY_ANCHOR_NAME="anasIdentityAnchor"
readonly ANAS_IDENTITY_ANCHOR_SCHEMA_GUID="db3786ae-3261-4d44-a2a1-588bfe3e41c5"
readonly ANAS_IDENTITY_ANCHOR_SCHEMA_GUID_B64="roY322EyRE2ioViL/j5BxQ=="
readonly ANAS_IDENTITY_ANCHOR_LEGACY_OID="1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401.2.1"

readonly samdb="/var/lib/samba/private/sam.ldb"
readonly schema_dn="CN=Schema,CN=Configuration,${SAMBA_DC_BASE_DN}"

for migration_marker in \
  /var/lib/samba/.anas-identity-anchor-oid-migration.in-progress \
  /var/lib/samba/.anas-identity-anchor-oid-migration.in-progress.new; do
  if [ -e "$migration_marker" ] || [ -L "$migration_marker" ]; then
    echo "an identity-anchor OID migration is incomplete; keep Samba stopped and restore the snapshot recorded in ${migration_marker}" >&2
    exit 1
  fi
done

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

schema_search() {
  local filter=$1
  shift
  ldbsearch -H "$samdb" -b "$schema_dn" -s one "$filter" "$@" 2>/dev/null
}

record_value() {
  # LDIF folds long values by starting continuation lines with one space.
  # Unfold first, otherwise a long OID is silently truncated during checks.
  printf '%s\n' "$1" \
    | awk '/^ / { line = line substr($0, 2); next } { if (line != "") print line; line = $0 } END { if (line != "") print line }' \
    | sed -n "s/^$2: //p" \
    | head -n 1
}

record_count() {
  printf '%s\n' "$1" | grep -Ec '^dn::? ' || true
}

name_record=$(schema_search "(&(lDAPDisplayName=${ANAS_IDENTITY_ANCHOR_NAME})(!(isDefunct=TRUE)))" dn objectClass attributeID governsID lDAPDisplayName attributeSyntax oMSyntax isSingleValued rangeLower rangeUpper searchFlags schemaIDGUID isDefunct)
all_name_record=$(schema_search "(lDAPDisplayName=${ANAS_IDENTITY_ANCHOR_NAME})" dn objectClass attributeID governsID lDAPDisplayName schemaIDGUID isDefunct)
oid_record=$(schema_search "(|(&(objectClass=attributeSchema)(attributeID=${ANAS_IDENTITY_ANCHOR_OID}))(&(objectClass=classSchema)(governsID=${ANAS_IDENTITY_ANCHOR_OID})))" dn objectClass attributeID governsID lDAPDisplayName)
guid_record=$(schema_search "(schemaIDGUID=${ANAS_IDENTITY_ANCHOR_SCHEMA_GUID})" dn objectClass attributeID governsID lDAPDisplayName)
legacy_record=$(schema_search "(&(objectClass=attributeSchema)(attributeID=${ANAS_IDENTITY_ANCHOR_LEGACY_OID}))" dn lDAPDisplayName isDefunct)
name_display=$(record_value "$name_record" lDAPDisplayName)
oid_display=$(record_value "$oid_record" lDAPDisplayName)
guid_display=$(record_value "$guid_record" lDAPDisplayName)
legacy_display=$(record_value "$legacy_record" lDAPDisplayName)

name_count=$(record_count "$name_record")
all_name_count=$(record_count "$all_name_record")
oid_count=$(record_count "$oid_record")
guid_count=$(record_count "$guid_record")
legacy_count=$(record_count "$legacy_record")

if [ -z "$name_display" ]; then
  if [ "$name_count" -ne 0 ] || [ "$all_name_count" -ne 0 ]; then
    echo "schema name ${ANAS_IDENTITY_ANCHOR_NAME} exists only in an inactive or ambiguous state; refusing automatic repair" >&2
    exit 1
  fi
  if [ "$legacy_count" -ne 0 ] || [ -n "$legacy_display" ]; then
    echo "legacy ${ANAS_IDENTITY_ANCHOR_NAME} schema ${ANAS_IDENTITY_ANCHOR_LEGACY_OID} requires the documented offline PEN 66678 migration before this release can start" >&2
    exit 1
  fi
  if [ "$oid_count" -ne 0 ] || [ -n "$oid_display" ]; then
    echo "schema OID ${ANAS_IDENTITY_ANCHOR_OID} is already assigned to ${oid_display}" >&2
    exit 1
  fi
  if [ "$guid_count" -ne 0 ] || [ -n "$guid_display" ]; then
    echo "schemaIDGUID ${ANAS_IDENTITY_ANCHOR_SCHEMA_GUID} is already assigned to ${guid_display}" >&2
    exit 1
  fi
  if [ "${SAMBA_DC_DOMAIN_ACTION:-provision}" = "join" ]; then
    echo "${ANAS_IDENTITY_ANCHOR_NAME} is absent from the joined forest; install the ANAS schema on its schema master before joining this DC" >&2
    exit 1
  fi

  cat >"$tmpdir/attribute.ldif" <<EOF
dn: CN=${ANAS_IDENTITY_ANCHOR_NAME},${schema_dn}
objectClass: attributeSchema
cn: ${ANAS_IDENTITY_ANCHOR_NAME}
attributeID: ${ANAS_IDENTITY_ANCHOR_OID}
lDAPDisplayName: ${ANAS_IDENTITY_ANCHOR_NAME}
adminDisplayName: ${ANAS_IDENTITY_ANCHOR_NAME}
adminDescription: Printable UUID projection of mS-DS-ConsistencyGuid
attributeSyntax: 2.5.5.12
oMSyntax: 64
isSingleValued: TRUE
rangeLower: 36
rangeUpper: 36
searchFlags: 1
showInAdvancedViewOnly: TRUE
schemaIDGUID:: ${ANAS_IDENTITY_ANCHOR_SCHEMA_GUID_B64}
EOF
  echo "Install AD schema attribute ${ANAS_IDENTITY_ANCHOR_NAME} (${ANAS_IDENTITY_ANCHOR_OID})"
  ldbadd -H "$samdb" --option="dsdb:schema update allowed=true" "$tmpdir/attribute.ldif"
  name_record=$(schema_search "(&(lDAPDisplayName=${ANAS_IDENTITY_ANCHOR_NAME})(!(isDefunct=TRUE)))" dn attributeID lDAPDisplayName attributeSyntax oMSyntax isSingleValued rangeLower rangeUpper searchFlags schemaIDGUID isDefunct)
  all_name_record=$(schema_search "(lDAPDisplayName=${ANAS_IDENTITY_ANCHOR_NAME})" dn objectClass attributeID governsID lDAPDisplayName schemaIDGUID isDefunct)
fi

if [ "$(record_count "$name_record")" -ne 1 ] || [ "$(record_count "$all_name_record")" -ne 1 ]; then
  echo "identity-anchor schema name is not uniquely assigned to one active object" >&2
  exit 1
fi
test "$(record_value "$name_record" attributeID)" = "$ANAS_IDENTITY_ANCHOR_OID"
test "$(record_value "$name_record" schemaIDGUID)" = "$ANAS_IDENTITY_ANCHOR_SCHEMA_GUID"
test "$(record_value "$name_record" attributeSyntax)" = "2.5.5.12"
test "$(record_value "$name_record" oMSyntax)" = "64"
test "$(record_value "$name_record" isSingleValued)" = "TRUE"
test "$(record_value "$name_record" rangeLower)" = "36"
test "$(record_value "$name_record" rangeUpper)" = "36"
test "$(record_value "$name_record" searchFlags)" = "1"

# Re-query collision keys after a fresh add. Schema OIDs and GUIDs are global
# across attributeSchema and classSchema, not merely unique among attributes.
name_dn=$(record_value "$name_record" dn | tr '[:upper:]' '[:lower:]')
oid_record=$(schema_search "(|(&(objectClass=attributeSchema)(attributeID=${ANAS_IDENTITY_ANCHOR_OID}))(&(objectClass=classSchema)(governsID=${ANAS_IDENTITY_ANCHOR_OID})))" dn objectClass lDAPDisplayName)
guid_record=$(schema_search "(schemaIDGUID=${ANAS_IDENTITY_ANCHOR_SCHEMA_GUID})" dn objectClass lDAPDisplayName)
if [ "$(record_count "$oid_record")" -ne 1 ] ||
   [ "$(record_count "$guid_record")" -ne 1 ] ||
   [ "$(record_value "$oid_record" dn | tr '[:upper:]' '[:lower:]')" != "$name_dn" ] ||
   [ "$(record_value "$guid_record" dn | tr '[:upper:]' '[:lower:]')" != "$name_dn" ]; then
  echo "identity-anchor schema OID or schemaIDGUID collides with another schema object" >&2
  exit 1
fi

for class_name in User Group; do
  class_dn="CN=${class_name},${schema_dn}"
  class_record=$(ldbsearch -H "$samdb" -b "$class_dn" -s base "(objectClass=classSchema)" mayContain 2>/dev/null)
  if ! printf '%s\n' "$class_record" | sed -n 's/^mayContain: //p' | grep -Fxiq "$ANAS_IDENTITY_ANCHOR_NAME"; then
    if [ "${SAMBA_DC_DOMAIN_ACTION:-provision}" = "join" ]; then
      echo "$class_dn does not permit ${ANAS_IDENTITY_ANCHOR_NAME}; update the schema master before joining this DC" >&2
      exit 1
    fi
    cat >"$tmpdir/class.ldif" <<EOF
dn: ${class_dn}
changetype: modify
add: mayContain
mayContain: ${ANAS_IDENTITY_ANCHOR_NAME}
EOF
    echo "Allow ${ANAS_IDENTITY_ANCHOR_NAME} on ${class_name} objects"
    ldbmodify -H "$samdb" --option="dsdb:schema update allowed=true" "$tmpdir/class.ldif"
  fi
done

touch /run/anas-identity-schema.ready
echo "ANAS identity schema is ready: attribute=${ANAS_IDENTITY_ANCHOR_NAME} oid=${ANAS_IDENTITY_ANCHOR_OID} schemaIDGUID=${ANAS_IDENTITY_ANCHOR_SCHEMA_GUID}"
