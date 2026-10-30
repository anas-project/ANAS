#!/usr/bin/python3
"""Check or explicitly install the two non-login ANAS mail resource classes.

No directory bootstrap, ACL changes, personnel writes or existing-schema repair
is performed. Default execution is read-only. Business-domain migration remains
separate from this installer and requires a validated Samba snapshot.
"""

import argparse
import base64
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import uuid


SPEC_PATH = Path(__file__).resolve().parents[1] / "share/anas/mail-schema.json"
DATABASE = "/var/lib/samba/private/sam.ldb"
NAME = re.compile(r"^[A-Za-z][A-Za-z0-9]+$")


def load_spec(path=SPEC_PATH):
    spec = json.loads(path.read_text(encoding="utf-8"))
    if spec.get("schema") != "anas.mail-directory/v1":
        raise ValueError("unsupported mail schema definition")
    names, oids, guids = set(), set(), set()
    for section, branch in (("attributes", "2"), ("classes", "1")):
        for item in spec[section]:
            name, oid, guid = item["name"], item["oid"], item["guid"]
            if not NAME.fullmatch(name) or not re.fullmatch(r"1\.3\.6\.1\.4\.1\.66678\.1\." + branch + r"\.[1-9][0-9]*", oid) or str(uuid.UUID(guid)) != guid:
                raise ValueError("invalid mail schema identifier")
            if name.casefold() in names or oid in oids or guid in guids:
                raise ValueError("duplicate mail schema identifier")
            names.add(name.casefold()); oids.add(oid); guids.add(guid)
            if section == "attributes" and (not 0 <= item["min"] <= item["max"] <= 65536 or type(item["single"]) is not bool or item["index"] not in (0, 1)):
                raise ValueError("invalid mail schema attribute definition")
    attributes = {x["name"] for x in spec["attributes"]}
    if {x["name"] for x in spec["classes"]} != {"anasMailbox", "anasMailAddressClaim"}:
        raise ValueError("invalid mail resource classes")
    for item in spec["classes"]:
        fields = item["must"] + item["may"]
        if len(fields) != len(set(fields)) or set(fields) - attributes - {"cn"} or "cn" not in item["must"] or "anasIdentityAnchor" in fields:
            raise ValueError("invalid mailbox resource class fields")
    return spec


def parse_ldif(raw):
    """Read ldbsearch output, including folded lines and base64 values."""
    unfolded = []
    for line in raw.splitlines():
        if line.startswith(" "):
            if not unfolded:
                raise ValueError("invalid directory response")
            unfolded[-1] += line[1:]
        else:
            unfolded.append(line)
    records, record = [], {}
    for line in unfolded + [""]:
        if not line:
            if record:
                records.append(record)
                record = {}
            continue
        if line.startswith("#") or ":" not in line:
            continue
        key, value = line.split(":", 1)
        if key.lower() == "dn" and record:
            records.append(record)
            record = {}
        if value.startswith(":"):
            decoded = base64.b64decode(value[1:].strip(), validate=True)
        elif value.startswith("<"):
            raise ValueError("external LDIF reference rejected")
        else:
            decoded = value.lstrip(" ").encode("utf-8")
        record.setdefault(key.casefold(), []).append(decoded)
    return records


def expected(item, kind, schema_dn):
    name = item["name"]
    out = {
        "dn": [f"CN={name},{schema_dn}"],
        "objectClass": [kind], "cn": [name], "lDAPDisplayName": [name],
        "adminDisplayName": [name], "adminDescription": [item["description"]],
        "schemaIDGUID": [uuid.UUID(item["guid"]).bytes_le],
    }
    if kind == "attributeSchema":
        out.update({"attributeID": [item["oid"]], "attributeSyntax": ["2.5.5.12"], "oMSyntax": ["64"], "isSingleValued": [str(item["single"]).upper()], "rangeLower": [str(item["min"])], "rangeUpper": [str(item["max"])], "searchFlags": [str(item["index"])], "showInAdvancedViewOnly": ["TRUE"]})
    else:
        # OWNER RIGHTS limits the creator's implicit WRITE_DAC permission;
        # directory ACLs still grant administrators/system full control.
        out.update({"governsID": [item["oid"]], "subClassOf": ["top"], "objectClassCategory": ["1"], "rDNAttID": ["cn"], "possSuperiors": ["organizationalUnit"], "mustContain": item["must"], "mayContain": item["may"], "defaultObjectCategory": [f"CN={name},{schema_dn}"], "defaultSecurityDescriptor": [item["security_descriptor"]]})
    return {key.casefold(): [x if isinstance(x, bytes) else x.encode("utf-8") for x in values] for key, values in out.items()}


def matches(row, wanted):
    if row.get("isdefunct", [b"FALSE"]) != [b"FALSE"]:
        return False
    for name, values in wanted.items():
        actual = row.get(name, [])
        if name == "objectclass":
            if not {v.lower() for v in values}.issubset({v.lower() for v in actual}):
                return False
        elif name == "schemaidguid":
            # Samba's ldbsearch LDIF handler prints GUID-syntax attributes as
            # UUID text, whereas raw LDIF/LDAP can return their 16 wire bytes.
            try:
                canonical = [v if len(v) == 16 else uuid.UUID(v.decode("ascii")).bytes_le for v in actual]
            except (ValueError, UnicodeError):
                return False
            if canonical != values:
                return False
        elif sorted(v.lower() for v in actual) != sorted(v.lower() for v in values):
            return False
    return True


def ldif(values):
    lines = []
    for key, items in values.items():
        for value in items:
            lines.append(key + ":: " + base64.b64encode(value).decode("ascii"))
    return "\n".join(lines) + "\n\n"


class Directory:
    def run(self, program, *args, data=None):
        result = subprocess.run([program, "-H", DATABASE, *args], input=data, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        if result.returncode:
            # Tool output may contain directory values: report only the action.
            raise ValueError("mail schema directory operation failed: " + program)
        return result.stdout

    def context(self):
        rows = parse_ldif(self.run("ldbsearch", "-b", "", "-s", "base", "(objectClass=*)", "schemaNamingContext"))
        if len(rows) != 1 or len(rows[0].get("schemanamingcontext", [])) != 1:
            raise ValueError("schema naming context unavailable")
        return rows[0]["schemanamingcontext"][0].decode("utf-8")

    def find(self, item, schema_dn):
        query = f'(|(lDAPDisplayName={item["name"]})(attributeID={item["oid"]})(governsID={item["oid"]})(schemaIDGUID={item["guid"]}))'
        return parse_ldif(self.run("ldbsearch", "-b", schema_dn, "-s", "one", query, "*"))

    def add(self, values):
        self.run("ldbadd", "--option=dsdb:schema update allowed=true", data=ldif(values))


def install(directory, spec, write=False):
    schema_dn = directory.context()
    missing = []
    # Validate every existing name/OID/GUID before writing the first object.
    for section, kind in (("attributes", "attributeSchema"), ("classes", "classSchema")):
        for item in spec[section]:
            wanted = expected(item, kind, schema_dn)
            rows = directory.find(item, schema_dn)
            if not rows:
                missing.append((item, wanted))
            elif len(rows) != 1 or not matches(rows[0], wanted):
                raise ValueError("mail schema collision or drift: " + item["name"])
    if missing and not write:
        raise ValueError("mail schema incomplete; explicit installation required")
    for item, wanted in missing:
        directory.add(wanted)
        rows = directory.find(item, schema_dn)
        if len(rows) != 1 or not matches(rows[0], wanted):
            raise ValueError("mail schema installation not confirmed: " + item["name"])
    return len(missing)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--check", action="store_true", help="read-only check (default)")
    mode.add_argument("--install", action="store_true", help="explicitly add missing definitions; never overwrite existing schema")
    args = parser.parse_args()
    try:
        # A joined DC only verifies definitions installed on its schema master.
        write = args.install and os.environ.get("SAMBA_DC_DOMAIN_ACTION") != "join"
        count = install(Directory(), load_spec(), write)
    except (OSError, ValueError, KeyError, TypeError) as exc:
        # Definition identifiers are public; arbitrary OS/JSON details are not.
        message = str(exc) if type(exc) is ValueError and str(exc).startswith("mail schema ") else "mail schema operation failed"
        print(message, file=sys.stderr)
        return 1
    print(f"mail schema verified: 2 non-login classes; installed={count}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
