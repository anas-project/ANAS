#!/usr/bin/with-contenv bash
set -euo pipefail

# This is an offline, one-time migration for forests created before ANAS was
# assigned IANA PEN 66678.  It deliberately does not start or stop Samba and
# must run in a maintenance container with the DC data volume mounted.

readonly legacy_oid="1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401.2.1"
readonly legacy_guid="7108c5a7-2290-45e0-9eba-eef087be58e3"
readonly final_oid="1.3.6.1.4.1.66678.1.2.1"
readonly final_guid="db3786ae-3261-4d44-a2a1-588bfe3e41c5"
readonly final_guid_b64="roY322EyRE2ioViL/j5BxQ=="
readonly attribute_name="anasIdentityAnchor"
readonly legacy_cn="anasIdentityAnchor-Legacy-GuidRoot"
readonly samdb="${ANAS_SAMDB_PATH:-/var/lib/samba/private/sam.ldb}"

mode=check
snapshot_id=""
backup_dir=""

usage() {
  cat <<'EOF'
Usage:
  migrate-identity-anchor-oid.sh --check
  migrate-identity-anchor-oid.sh --execute --snapshot-id ID --backup-dir PATH

Migrates the active anasIdentityAnchor schema object from the retired
GUID-derived OID to 1.3.6.1.4.1.66678.1.2.1. The command must run as root while
the Samba daemon and every other writer of the DC data volume are stopped.

--check is read-only. --execute requires the identifier of an already verified
ANAS snapshot and a new evidence directory outside /var/lib/samba. The caller
must mount that directory into the maintenance container from storage that is
not replaced when the Samba data snapshot is restored.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --check)
      mode=check
      ;;
    --execute)
      mode=execute
      ;;
    --snapshot-id)
      [ "$#" -ge 2 ] || { echo "--snapshot-id requires a value" >&2; exit 2; }
      snapshot_id=$2
      shift
      ;;
    --backup-dir)
      [ "$#" -ge 2 ] || { echo "--backup-dir requires a value" >&2; exit 2; }
      backup_dir=$2
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

if [ "$(id -u)" -ne 0 ]; then
  echo "the identity-anchor schema migration must run as root" >&2
  exit 1
fi
if [ ! -f "$samdb" ]; then
  echo "Samba database not found: $samdb" >&2
  exit 1
fi
for process_name in samba samba-ad-dc; do
  if pgrep -x "$process_name" >/dev/null 2>&1; then
    echo "Samba is running (${process_name}); stop the DC before touching its local database" >&2
    exit 1
  fi
done
for command_name in flock install ldbadd ldbmodify ldbrename ldbsearch python3 readlink sha256sum sync; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "required command is unavailable: $command_name" >&2
    exit 1
  }
done

if [ "$mode" = execute ]; then
  migration_lock="/var/lib/samba/.anas-identity-anchor-oid-migration.lock"
else
  migration_lock="/run/anas-identity-anchor-oid-migration-check.lock"
fi
exec 9>"$migration_lock"
if ! flock -n 9; then
  echo "another identity-anchor OID migration is already running" >&2
  exit 1
fi

readonly in_progress_marker="/var/lib/samba/.anas-identity-anchor-oid-migration.in-progress"
readonly pending_marker="${in_progress_marker}.new"
readonly completed_marker="/var/lib/samba/.anas-identity-anchor-oid"
if [ -e "$in_progress_marker" ] || [ -L "$in_progress_marker" ] ||
   [ -e "$pending_marker" ] || [ -L "$pending_marker" ]; then
  echo "an earlier identity-anchor OID migration did not finish; keep services stopped and restore its recorded snapshot before retrying" >&2
  exit 1
fi

workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT HUP INT TERM

record_value() {
  printf '%s\n' "$1" \
    | awk '/^ / { line = line substr($0, 2); next } { if (line != "") print line; line = $0 } END { if (line != "") print line }' \
    | sed -n "s/^$2: //p" \
    | head -n 1
}

root_record=$(ldbsearch -H "$samdb" -b '' -s base '(objectClass=*)' defaultNamingContext configurationNamingContext schemaNamingContext 2>/dev/null)
root_base_dn=$(record_value "$root_record" defaultNamingContext)
configuration_dn=$(record_value "$root_record" configurationNamingContext)
schema_dn=$(record_value "$root_record" schemaNamingContext)
if [ -z "$root_base_dn" ] || [ -z "$configuration_dn" ] || [ -z "$schema_dn" ] ||
   printf '%s%s%s' "$root_base_dn" "$configuration_dn" "$schema_dn" | grep -q '[[:cntrl:]]'; then
  echo "could not determine safe directory naming contexts from RootDSE" >&2
  exit 1
fi
if [ -n "${SAMBA_DC_BASE_DN:-}" ] && [ "${SAMBA_DC_BASE_DN,,}" != "${root_base_dn,,}" ]; then
  echo "SAMBA_DC_BASE_DN does not match RootDSE defaultNamingContext; refusing a partial-domain migration" >&2
  exit 1
fi
base_dn=$root_base_dn
readonly base_dn
readonly configuration_dn
readonly schema_dn
readonly legacy_dn="CN=${attribute_name},${schema_dn}"
readonly retired_dn="CN=${legacy_cn},${schema_dn}"

schema_search() {
  local filter=$1
  shift
  ldbsearch -H "$samdb" -b "$schema_dn" -s one "$filter" "$@" 2>/dev/null
}

record_count() {
  printf '%s\n' "$1" | grep -Ec '^dn::? ' || true
}

legacy_active=$(schema_search "(&(objectClass=attributeSchema)(attributeID=${legacy_oid})(!(isDefunct=TRUE)))" dn cn lDAPDisplayName attributeID schemaIDGUID attributeSyntax oMSyntax isSingleValued rangeLower rangeUpper searchFlags isDefunct)
legacy_any=$(schema_search "(&(objectClass=attributeSchema)(attributeID=${legacy_oid}))" dn cn lDAPDisplayName adminDisplayName attributeID schemaIDGUID attributeSyntax oMSyntax isSingleValued rangeLower rangeUpper searchFlags isDefunct)
final_active=$(schema_search "(&(objectClass=attributeSchema)(attributeID=${final_oid})(!(isDefunct=TRUE)))" dn cn lDAPDisplayName attributeID schemaIDGUID attributeSyntax oMSyntax isSingleValued rangeLower rangeUpper searchFlags isDefunct)
final_any=$(schema_search "(|(&(objectClass=attributeSchema)(attributeID=${final_oid}))(&(objectClass=classSchema)(governsID=${final_oid})))" dn objectClass cn lDAPDisplayName attributeID governsID schemaIDGUID isDefunct)
active_name=$(schema_search "(&(lDAPDisplayName=${attribute_name})(!(isDefunct=TRUE)))" dn objectClass cn lDAPDisplayName attributeID governsID schemaIDGUID isDefunct)
all_name=$(schema_search "(lDAPDisplayName=${attribute_name})" dn objectClass cn lDAPDisplayName attributeID governsID schemaIDGUID isDefunct)
final_guid_record=$(schema_search "(schemaIDGUID=${final_guid})" dn objectClass lDAPDisplayName attributeID governsID isDefunct)
legacy_guid_record=$(schema_search "(schemaIDGUID=${legacy_guid})" dn objectClass lDAPDisplayName attributeID governsID isDefunct)
retired_name_record=$(schema_search "(|(cn=${legacy_cn})(lDAPDisplayName=${legacy_cn}))" dn objectClass cn lDAPDisplayName attributeID governsID schemaIDGUID isDefunct)

legacy_active_count=$(record_count "$legacy_active")
legacy_count=$(record_count "$legacy_any")
final_active_count=$(record_count "$final_active")
final_count=$(record_count "$final_any")
active_name_count=$(record_count "$active_name")
all_name_count=$(record_count "$all_name")
final_guid_count=$(record_count "$final_guid_record")
legacy_guid_count=$(record_count "$legacy_guid_record")
retired_name_count=$(record_count "$retired_name_record")

verify_final_schema() {
  local record=$1
  [ "$(record_count "$record")" -eq 1 ] || return 1
  [ "$(record_value "$record" lDAPDisplayName)" = "$attribute_name" ] || return 1
  [ "$(record_value "$record" attributeID)" = "$final_oid" ] || return 1
  [ "$(record_value "$record" schemaIDGUID)" = "$final_guid" ] || return 1
  [ "$(record_value "$record" attributeSyntax)" = "2.5.5.12" ] || return 1
  [ "$(record_value "$record" oMSyntax)" = "64" ] || return 1
  [ "$(record_value "$record" isSingleValued)" = "TRUE" ] || return 1
  [ "$(record_value "$record" rangeLower)" = "36" ] || return 1
  [ "$(record_value "$record" rangeUpper)" = "36" ] || return 1
  [ "$(record_value "$record" searchFlags)" = "1" ] || return 1
}

verify_legacy_schema() {
  local record=$1
  [ "$(record_count "$record")" -eq 1 ] || return 1
  [ "${legacy_dn,,}" = "$(record_value "$record" dn | tr '[:upper:]' '[:lower:]')" ] || return 1
  [ "$(record_value "$record" cn)" = "$attribute_name" ] || return 1
  [ "$(record_value "$record" lDAPDisplayName)" = "$attribute_name" ] || return 1
  [ "$(record_value "$record" attributeID)" = "$legacy_oid" ] || return 1
  [ "$(record_value "$record" schemaIDGUID)" = "$legacy_guid" ] || return 1
  [ "$(record_value "$record" attributeSyntax)" = "2.5.5.12" ] || return 1
  [ "$(record_value "$record" oMSyntax)" = "64" ] || return 1
  [ "$(record_value "$record" isSingleValued)" = "TRUE" ] || return 1
  [ "$(record_value "$record" rangeLower)" = "36" ] || return 1
  [ "$(record_value "$record" rangeUpper)" = "36" ] || return 1
  [ "$(record_value "$record" searchFlags)" = "1" ] || return 1
}

class_anchor_state() {
  local class_name=$1
  local record matches
  record=$(ldbsearch -H "$samdb" -b "CN=${class_name},${schema_dn}" -s base '(objectClass=classSchema)' mayContain 2>/dev/null) || {
    echo "could not read ${class_name} schema class" >&2
    return 1
  }
  if [ "$(record_count "$record")" -ne 1 ]; then
    echo "expected exactly one ${class_name} schema class" >&2
    return 1
  fi
  matches=$(printf '%s\n' "$record" | grep -Fxic "mayContain: ${attribute_name}" || true)
  case "$matches" in
    0) echo absent ;;
    1) echo present ;;
    *) echo "duplicate ${attribute_name} links on ${class_name}" >&2; return 1 ;;
  esac
}

validate_class_references() {
  local expected_state=$1
  local reference_export="${workdir}/class-references-${expected_state}.ldif"
  schema_search \
    "(&(objectClass=classSchema)(|(mayContain=${attribute_name})(mustContain=${attribute_name})(systemMayContain=${attribute_name})(systemMustContain=${attribute_name})))" \
    dn mayContain mustContain systemMayContain systemMustContain >"$reference_export"
  python3 - "$reference_export" "$schema_dn" "$attribute_name" "$expected_state" <<'PY'
import base64
import pathlib
import sys

source, schema_dn, attribute_name, expected_state = sys.argv[1:]
linked_fields = {"maycontain", "mustcontain", "systemmaycontain", "systemmustcontain"}

def unfold(text):
    result = []
    for line in text.splitlines():
        if line.startswith(" "):
            if not result:
                raise SystemExit("invalid folded class-reference LDIF")
            result[-1] += line[1:]
        else:
            result.append(line)
    return result

def decoded_value(line):
    field, separator, value = line.partition(":")
    if not separator:
        raise SystemExit("invalid class-reference LDIF field")
    if value.startswith(": "):
        try:
            return field, base64.b64decode(value[2:], validate=True).decode("utf-8")
        except (ValueError, UnicodeDecodeError) as exc:
            raise SystemExit(f"invalid class-reference base64: {exc}")
    if value.startswith(" "):
        return field, value[1:]
    raise SystemExit("invalid class-reference LDIF value")

references = []
for block in "\n".join(unfold(pathlib.Path(source).read_text(encoding="utf-8"))).split("\n\n"):
    fields = [line for line in block.splitlines() if line and not line.startswith("#")]
    if not fields:
        continue
    decoded = [decoded_value(line) for line in fields]
    dns = [value for field, value in decoded if field.lower() == "dn"]
    if len(dns) != 1:
        raise SystemExit("every class-reference record must have exactly one DN")
    for field, value in decoded:
        if field.lower() in linked_fields and value.lower() == attribute_name.lower():
            references.append((dns[0].lower(), field.lower(), value.lower()))

expected = []
if expected_state == "present":
    expected = [
        (f"CN=User,{schema_dn}".lower(), "maycontain", attribute_name.lower()),
        (f"CN=Group,{schema_dn}".lower(), "maycontain", attribute_name.lower()),
    ]
elif expected_state != "absent":
    raise SystemExit(f"unsupported expected class-reference state: {expected_state}")

if sorted(references) != sorted(expected):
    raise SystemExit(
        "identity-anchor class references are not the controlled User/Group mayContain set: "
        f"found={references!r} expected={expected!r}"
    )
PY
}

validate_anchor_export() {
  local source=$1
  local delete_path=${2:-}
  local restore_path=${3:-}
  python3 - "$source" "$delete_path" "$restore_path" "$attribute_name" <<'PY'
import base64
import pathlib
import re
import sys
import uuid

source, delete_path, restore_path, attribute = sys.argv[1:]
binary_attribute = "mS-DS-ConsistencyGuid"
object_guid_attribute = "objectGUID"

def unfold(text):
    result = []
    for line in text.splitlines():
        if line.startswith(" "):
            if not result:
                raise SystemExit("invalid folded LDIF before first line")
            result[-1] += line[1:]
        else:
            result.append(line)
    return result

def values(lines, name):
    prefix = name.lower() + ":"
    return [line for line in lines if line.lower().startswith(prefix)]

def decode(line, name, binary=False):
    prefix = name + ":"
    if line[: len(prefix)].lower() != prefix.lower():
        raise SystemExit(f"invalid LDIF field for {name}")
    encoded = line[len(prefix):]
    if encoded.startswith(": "):
        try:
            return base64.b64decode(encoded[2:], validate=True)
        except ValueError as exc:
            raise SystemExit(f"invalid base64 {name}: {exc}")
    if encoded.startswith(" ") and not binary:
        return encoded[1:].encode("utf-8")
    raise SystemExit(f"{name} must use {'base64' if binary else 'valid LDIF'} encoding")

def decode_guid_syntax(line, name):
    raw = decode(line, name)
    if len(raw) == 16:
        return raw
    try:
        return uuid.UUID(raw.decode("ascii")).bytes_le
    except (UnicodeDecodeError, ValueError):
        raise SystemExit(f"{name} must be UUID text or 16 raw bytes")

entries = []
for block in "\n".join(unfold(pathlib.Path(source).read_text(encoding="utf-8"))).split("\n\n"):
    lines = [line for line in block.splitlines() if line and not line.startswith("#")]
    if not lines:
        continue
    dn_lines = values(lines, "dn")
    if not dn_lines:
        continue
    text_lines = values(lines, attribute)
    binary_lines = values(lines, binary_attribute)
    object_guid_lines = values(lines, object_guid_attribute)
    if len(dn_lines) != 1 or len(text_lines) != 1 or len(binary_lines) != 1 or len(object_guid_lines) != 1:
        raise SystemExit(
            "every exported entry must have exactly one DN, objectGUID, "
            f"{binary_attribute}, and {attribute}"
        )
    dn_key = decode(dn_lines[0], "dn")
    text_value = decode(text_lines[0], attribute).decode("ascii")
    binary_value = decode_guid_syntax(binary_lines[0], binary_attribute)
    object_guid = decode_guid_syntax(object_guid_lines[0], object_guid_attribute)
    if len(binary_value) != 16 or len(object_guid) != 16:
        raise SystemExit("objectGUID and mS-DS-ConsistencyGuid must each contain exactly 16 bytes")
    try:
        parsed = uuid.UUID(text_value)
    except ValueError as exc:
        raise SystemExit(f"invalid identity anchor {text_value!r}: {exc}")
    if str(parsed) != text_value or not re.fullmatch(
        r"[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}", text_value
    ):
        raise SystemExit(f"identity anchor is not lowercase canonical UUID text: {text_value!r}")
    if uuid.UUID(bytes_le=binary_value) != parsed:
        raise SystemExit(f"text and binary identity anchors disagree for DN {dn_key!r}")
    entries.append((dn_key, dn_lines[0], text_value))

dn_keys = [dn for dn, _, _ in entries]
anchors = [value for _, _, value in entries]
if len(dn_keys) != len(set(dn_keys)):
    raise SystemExit("duplicate DNs exist in the identity-anchor export")
if len(anchors) != len(set(anchors)):
    raise SystemExit("duplicate identity-anchor values exist; repair directory integrity before migration")

if bool(delete_path) != bool(restore_path):
    raise SystemExit("delete and restore output paths must be supplied together")
if delete_path:
    with pathlib.Path(delete_path).open("x", encoding="utf-8") as output:
        for _, dn_line, _ in entries:
            output.write(f"{dn_line}\nchangetype: modify\ndelete: {attribute}\n-\n\n")
    with pathlib.Path(restore_path).open("x", encoding="utf-8") as output:
        for _, dn_line, value in entries:
            output.write(
                f"{dn_line}\nchangetype: modify\nadd: {attribute}\n"
                f"{attribute}: {value}\n-\n\n"
            )
print(len(entries))
PY
}

export_anchor_set() {
  local destination=$1
  ldbsearch -H "$samdb" -b "$base_dn" -s sub "(${attribute_name}=*)" \
    dn objectGUID mS-DS-ConsistencyGuid "$attribute_name" >"$destination"
}

verify_retired_schema() {
  local record=$1
  [ "$(record_count "$record")" -eq 1 ] || return 1
  [ "${retired_dn,,}" = "$(record_value "$record" dn | tr '[:upper:]' '[:lower:]')" ] || return 1
  [ "$(record_value "$record" cn)" = "$legacy_cn" ] || return 1
  [ "$(record_value "$record" lDAPDisplayName)" = "$legacy_cn" ] || return 1
  [ "$(record_value "$record" attributeID)" = "$legacy_oid" ] || return 1
  [ "$(record_value "$record" schemaIDGUID)" = "$legacy_guid" ] || return 1
  printf '%s\n' "$record" | grep -Fq 'isDefunct: TRUE'
}

if [ "$final_active_count" -eq 1 ] && [ "$active_name_count" -eq 1 ]; then
  if [ "$final_count" -ne 1 ] || [ "$final_guid_count" -ne 1 ] || [ "$all_name_count" -ne 1 ] || ! verify_final_schema "$final_active"; then
    echo "the PEN 66678 schema object exists but its immutable definition is incompatible" >&2
    exit 1
  fi
  if [ "$legacy_count" -gt 1 ] ||
     { [ "$legacy_count" -eq 1 ] && { [ "$legacy_guid_count" -ne 1 ] || ! verify_retired_schema "$legacy_any"; }; }; then
    echo "the final schema is active but the legacy OID is not a single defunct history record" >&2
    exit 1
  fi
  if [ "$legacy_count" -eq 1 ] && [ "$retired_name_count" -ne 1 ]; then
    echo "the final schema is active but the retired legacy schema name is ambiguous" >&2
    exit 1
  fi
  completed_user_link=$(class_anchor_state User)
  completed_group_link=$(class_anchor_state Group)
  if [ "$completed_user_link" != present ] || [ "$completed_group_link" != present ]; then
    echo "the final schema is not linked from both User and Group" >&2
    exit 1
  fi
  validate_class_references present
  completed_values="${workdir}/completed-values.ldif"
  export_anchor_set "$completed_values"
  completed_anchor_count=$(validate_anchor_export "$completed_values")
  echo "identity-anchor schema already uses ${final_oid}; verified ${completed_anchor_count} values; no migration is needed"
  exit 0
fi

legacy_user_link=$(class_anchor_state User)
legacy_group_link=$(class_anchor_state Group)

if [ "$legacy_active_count" -ne 1 ] || [ "$legacy_count" -ne 1 ] ||
   [ "$final_count" -ne 0 ] || [ "$final_guid_count" -ne 0 ] ||
   [ "$active_name_count" -ne 1 ] || [ "$all_name_count" -ne 1 ] || [ "$legacy_guid_count" -ne 1 ] ||
   [ "$retired_name_count" -ne 0 ] ||
   ! verify_legacy_schema "$legacy_active" ||
   [ "$legacy_user_link" != present ] || [ "$legacy_group_link" != present ]; then
  echo "schema is neither the exact supported legacy state nor the completed PEN 66678 state; refusing migration" >&2
  exit 1
fi
validate_class_references present

dc_records=$(ldbsearch -H "$samdb" -b "$configuration_dn" -s sub '(objectClass=nTDSDSA)' dn msDS-isRODC 2>/dev/null)
dc_count=$(record_count "$dc_records")
if [ "$dc_count" -ne 1 ]; then
  echo "migration supports exactly one domain controller; found ${dc_count}" >&2
  exit 1
fi
if printf '%s\n' "$dc_records" | grep -Fqi 'msDS-isRODC: TRUE'; then
  echo "migration cannot run against a read-only domain controller" >&2
  exit 1
fi
local_dsa_dn=$(record_value "$dc_records" dn)
schema_record=$(ldbsearch -H "$samdb" -b "$schema_dn" -s base '(objectClass=dMD)' fSMORoleOwner 2>/dev/null)
schema_owner_dn=$(record_value "$schema_record" fSMORoleOwner)
if [ -z "$local_dsa_dn" ] || [ -z "$schema_owner_dn" ] || [ "${local_dsa_dn,,}" != "${schema_owner_dn,,}" ]; then
  echo "the only DC is not the verified schema FSMO owner; refusing migration" >&2
  exit 1
fi

preflight_values="${workdir}/legacy-values.ldif"
export_anchor_set "$preflight_values"
preflight_anchor_count=$(validate_anchor_export "$preflight_values")

echo "legacy identity-anchor schema is eligible for migration:"
echo "  old OID: ${legacy_oid}"
echo "  new OID: ${final_oid}"
echo "  domain controllers: ${dc_count}"
echo "  validated anchors: ${preflight_anchor_count}"

if [ "$mode" = check ]; then
  echo "read-only preflight passed; create and verify an ANAS snapshot, then rerun with --execute --snapshot-id ID"
  exit 0
fi

if [ -z "$snapshot_id" ] || ! printf '%s' "$snapshot_id" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'; then
  echo "--execute requires a safe --snapshot-id for an already verified ANAS snapshot" >&2
  exit 2
fi

umask 077
if [ -z "$backup_dir" ]; then
  echo "--execute requires --backup-dir on storage outside /var/lib/samba" >&2
  exit 2
fi
case "$backup_dir" in
  /*) ;;
  *) echo "--backup-dir must be absolute" >&2; exit 2 ;;
esac
if [ -e "$backup_dir" ] || [ -L "$backup_dir" ]; then
  echo "backup directory already exists: $backup_dir" >&2
  exit 1
fi
backup_parent=$(dirname "$backup_dir")
if [ ! -d "$backup_parent" ] || [ -L "$backup_parent" ]; then
  echo "backup directory parent must be an existing real directory: $backup_parent" >&2
  exit 1
fi
resolved_backup_parent=$(readlink -f "$backup_parent")
resolved_samba_root=$(readlink -f /var/lib/samba)
case "${resolved_backup_parent}/" in
  "${resolved_samba_root}/"*)
    echo "backup evidence must be outside the Samba data volume so snapshot restore cannot erase it" >&2
    exit 1
    ;;
esac
backup_dir="${resolved_backup_parent}/$(basename "$backup_dir")"
mkdir -m 0700 "$backup_dir"

values_export="${backup_dir}/legacy-values.ldif"
delete_ldif="${backup_dir}/delete-legacy-values.ldif"
restore_ldif="${backup_dir}/restore-final-values.ldif"
schema_export="${backup_dir}/legacy-schema.ldif"

install -m 0600 "$preflight_values" "$values_export"
schema_search "(|(attributeID=${legacy_oid})(cn=User)(cn=Group))" '*' >"$schema_export"
anchor_count=$(validate_anchor_export "$values_export" "$delete_ldif" "$restore_ldif")
[ "$anchor_count" -eq "$preflight_anchor_count" ] || {
  echo "identity-anchor set changed between preflight and protected export" >&2
  exit 1
}

cat >"${backup_dir}/migration-metadata.txt" <<EOF
snapshot_id=${snapshot_id}
base_dn=${base_dn}
legacy_oid=${legacy_oid}
legacy_schema_guid=${legacy_guid}
final_oid=${final_oid}
final_schema_guid=${final_guid}
anchor_count=${anchor_count}
created_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
sha256sum "$values_export" "$delete_ldif" "$restore_ldif" "$schema_export" >"${backup_dir}/SHA256SUMS"
sync -f "$backup_dir"

if [ -e "$completed_marker" ] || [ -L "$completed_marker" ]; then
  echo "legacy schema is active but a completed-migration marker already exists; refusing an ambiguous state" >&2
  exit 1
fi
cat >"$pending_marker" <<EOF
snapshot_id=${snapshot_id}
evidence_dir=${backup_dir}
legacy_oid=${legacy_oid}
final_oid=${final_oid}
started_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
chmod 0600 "$pending_marker"
sync -f "$pending_marker"
mv "$pending_marker" "$in_progress_marker"
sync -f /var/lib/samba

if [ "$anchor_count" -gt 0 ]; then
  ldbmodify -H "$samdb" "$delete_ldif"
fi
remaining_values=$(ldbsearch -H "$samdb" -b "$base_dn" -s sub "(${attribute_name}=*)" dn 2>/dev/null)
if [ "$(record_count "$remaining_values")" -ne 0 ]; then
  echo "legacy identity-anchor values were not cleared atomically; keep services stopped and restore snapshot ${snapshot_id}" >&2
  exit 1
fi

cat >"${backup_dir}/remove-class-links.ldif" <<EOF
dn: CN=User,${schema_dn}
changetype: modify
delete: mayContain
mayContain: ${attribute_name}
-

dn: CN=Group,${schema_dn}
changetype: modify
delete: mayContain
mayContain: ${attribute_name}
-
EOF
ldbmodify -H "$samdb" --option="dsdb:schema update allowed=true" "${backup_dir}/remove-class-links.ldif"
removed_user_link=$(class_anchor_state User)
removed_group_link=$(class_anchor_state Group)
if [ "$removed_user_link" != absent ] || [ "$removed_group_link" != absent ]; then
  echo "legacy class links were not removed; keep services stopped and restore snapshot ${snapshot_id}" >&2
  exit 1
fi
validate_class_references absent

cat >"${backup_dir}/defunct-legacy-schema.ldif" <<EOF
dn: ${legacy_dn}
changetype: modify
replace: isDefunct
isDefunct: TRUE
-
EOF
ldbmodify -H "$samdb" --option="dsdb:schema update allowed=true" "${backup_dir}/defunct-legacy-schema.ldif"

# Samba enforces lDAPDisplayName uniqueness across the schema container even
# for defunct objects.  RDN rename alone is insufficient: release the LDAP
# name explicitly before creating the replacement object.
cat >"${backup_dir}/rename-legacy-display-name.ldif" <<EOF
dn: ${legacy_dn}
changetype: modify
replace: lDAPDisplayName
lDAPDisplayName: ${legacy_cn}
-
replace: adminDisplayName
adminDisplayName: ${legacy_cn}
-
EOF
ldbmodify -H "$samdb" --option="dsdb:schema update allowed=true" "${backup_dir}/rename-legacy-display-name.ldif"
ldbrename -H "$samdb" --option="dsdb:schema update allowed=true" "$legacy_dn" "$retired_dn"

cat >"${backup_dir}/add-final-schema.ldif" <<EOF
dn: ${legacy_dn}
objectClass: attributeSchema
cn: ${attribute_name}
attributeID: ${final_oid}
lDAPDisplayName: ${attribute_name}
adminDisplayName: ${attribute_name}
adminDescription: Printable UUID projection of mS-DS-ConsistencyGuid
attributeSyntax: 2.5.5.12
oMSyntax: 64
isSingleValued: TRUE
rangeLower: 36
rangeUpper: 36
searchFlags: 1
showInAdvancedViewOnly: TRUE
schemaIDGUID:: ${final_guid_b64}
EOF
ldbadd -H "$samdb" --option="dsdb:schema update allowed=true" "${backup_dir}/add-final-schema.ldif"

cat >"${backup_dir}/add-class-links.ldif" <<EOF
dn: CN=User,${schema_dn}
changetype: modify
add: mayContain
mayContain: ${attribute_name}
-

dn: CN=Group,${schema_dn}
changetype: modify
add: mayContain
mayContain: ${attribute_name}
-
EOF
ldbmodify -H "$samdb" --option="dsdb:schema update allowed=true" "${backup_dir}/add-class-links.ldif"
final_user_link=$(class_anchor_state User)
final_group_link=$(class_anchor_state Group)
if [ "$final_user_link" != present ] || [ "$final_group_link" != present ]; then
  echo "replacement class links failed verification; keep services stopped and restore snapshot ${snapshot_id}" >&2
  exit 1
fi
validate_class_references present

if [ "$anchor_count" -gt 0 ]; then
  ldbmodify -H "$samdb" "$restore_ldif"
fi

final_active=$(schema_search "(&(objectClass=attributeSchema)(attributeID=${final_oid})(!(isDefunct=TRUE)))" dn cn lDAPDisplayName attributeID schemaIDGUID attributeSyntax oMSyntax isSingleValued rangeLower rangeUpper searchFlags isDefunct)
if ! verify_final_schema "$final_active"; then
  echo "final schema verification failed; keep services stopped and restore snapshot ${snapshot_id}" >&2
  exit 1
fi
legacy_any=$(schema_search "(&(objectClass=attributeSchema)(attributeID=${legacy_oid}))" dn cn lDAPDisplayName attributeID schemaIDGUID isDefunct)
if ! verify_retired_schema "$legacy_any"; then
  echo "legacy schema history verification failed; keep services stopped and restore snapshot ${snapshot_id}" >&2
  exit 1
fi

final_values="${backup_dir}/final-values.ldif"
export_anchor_set "$final_values"
final_anchor_count=$(validate_anchor_export "$final_values")
[ "$final_anchor_count" -eq "$anchor_count" ] || {
  echo "identity-anchor count changed during migration" >&2
  exit 1
}
python3 - "$values_export" "$final_values" "$attribute_name" <<'PY'
import base64
import pathlib
import sys

def normalized(path, attribute):
    lines = []
    for line in pathlib.Path(path).read_text(encoding="utf-8").splitlines():
        if line.startswith(" ") and lines:
            lines[-1] += line[1:]
        else:
            lines.append(line)
    result = []
    for block in "\n".join(lines).split("\n\n"):
        fields = [line for line in block.splitlines() if line and not line.startswith("#")]
        dn = next((line for line in fields if line.startswith(("dn: ", "dn:: "))), None)
        value_line = next((line for line in fields if line.startswith(attribute + ":")), None)
        if dn is None or value_line is None:
            continue
        if value_line.startswith(attribute + ":: "):
            value = base64.b64decode(value_line.split(":: ", 1)[1], validate=True).decode("ascii")
        else:
            value = value_line.split(": ", 1)[1]
        result.append((dn, value))
    return sorted(result)

before = normalized(sys.argv[1], sys.argv[3])
after = normalized(sys.argv[2], sys.argv[3])
if before != after:
    raise SystemExit("identity-anchor DN/value set changed during migration")
print(f"verified {len(after)} unchanged identity-anchor values")
PY

sha256sum "$final_values" >>"${backup_dir}/SHA256SUMS"
sync -f "$backup_dir"
cat >>"$in_progress_marker" <<EOF
completed_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
sync -f "$in_progress_marker"
mv "$in_progress_marker" "$completed_marker"
sync -f /var/lib/samba

echo "identity-anchor schema migration completed: ${legacy_oid} -> ${final_oid}"
echo "evidence directory: ${backup_dir}"
echo "start the upgraded Samba DC, let structure.sh replace the attribute ACL, then run the documented verification"
