#!/usr/bin/with-contenv bash
set -euo pipefail

# This root was generated once from UUID 43555bcd-c93f-4508-acbf-12be8d710729
# with Microsoft's documented AD schema OID generator. Do not regenerate it.
# Existing schema OIDs are permanent replication identifiers.
readonly ANAS_SCHEMA_OID_ROOT="1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401"
readonly ANAS_IDENTITY_ANCHOR_OID="${ANAS_SCHEMA_OID_ROOT}.2.1"
readonly ANAS_IDENTITY_ANCHOR_NAME="anasIdentityAnchor"
readonly ANAS_IDENTITY_ANCHOR_SCHEMA_GUID="7108c5a7-2290-45e0-9eba-eef087be58e3"
readonly ANAS_IDENTITY_ANCHOR_SCHEMA_GUID_B64="p8UIcZAi4EWeuu7wh75Y4w=="

readonly samdb="/var/lib/samba/private/sam.ldb"
readonly schema_dn="CN=Schema,CN=Configuration,${SAMBA_DC_BASE_DN}"

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

name_record=$(schema_search "(lDAPDisplayName=${ANAS_IDENTITY_ANCHOR_NAME})" dn attributeID lDAPDisplayName attributeSyntax oMSyntax isSingleValued rangeLower rangeUpper schemaIDGUID || true)
oid_record=$(schema_search "(attributeID=${ANAS_IDENTITY_ANCHOR_OID})" dn lDAPDisplayName || true)
name_display=$(record_value "$name_record" lDAPDisplayName)
oid_display=$(record_value "$oid_record" lDAPDisplayName)

if [ -z "$name_display" ]; then
  if [ -n "$oid_display" ]; then
    echo "schema OID ${ANAS_IDENTITY_ANCHOR_OID} is already assigned to ${oid_display}" >&2
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
  name_record=$(schema_search "(lDAPDisplayName=${ANAS_IDENTITY_ANCHOR_NAME})" dn attributeID lDAPDisplayName attributeSyntax oMSyntax isSingleValued rangeLower rangeUpper schemaIDGUID)
fi

test "$(record_value "$name_record" attributeID)" = "$ANAS_IDENTITY_ANCHOR_OID"
test "$(record_value "$name_record" attributeSyntax)" = "2.5.5.12"
test "$(record_value "$name_record" oMSyntax)" = "64"
test "$(record_value "$name_record" isSingleValued)" = "TRUE"
test "$(record_value "$name_record" rangeLower)" = "36"
test "$(record_value "$name_record" rangeUpper)" = "36"

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
