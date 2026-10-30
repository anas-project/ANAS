#!/usr/bin/python3
"""Explicit, transactional OU/ACL preparation after mail schema installation.

Read a non-secret JSON configuration on stdin. Default/--check never writes.
No schema, personnel, password, membership or service-account creation occurs.
This development tool is not wired into startup or business-domain migration.
"""

import argparse
import importlib.util
import json
import os
from pathlib import Path
import sys


MARKER = "ANAS mail directory/v1"
ROLES = {"reader", "creator", "address_writer", "primary_writer"}
COMMIT_FIELDS = ("anasMailState", "anasMailConfigVersion", "anasMailOperationID", "anasMailOperationState", "anasMailOperationData")
WRITER_FIELDS = {
    "anasMailbox": COMMIT_FIELDS + ("anasMailDefaultAddress", "anasMailQuotaState"),
    "anasMailAddressClaim": COMMIT_FIELDS + ("anasMailboxID", "anasMailOwnerAnchor", "anasMailAddressKind", "anasMailOwnershipHistory"),
}
PRIMARY_FIELDS = {
    "anasMailbox": COMMIT_FIELDS + ("anasMailDefaultAddress",),
    "anasMailAddressClaim": COMMIT_FIELDS + ("anasMailAddressKind",),
}
ANCHOR_FIELDS = ("mS-DS-ConsistencyGuid", "anasIdentityAnchor")


def policy(config, sids, guids):
    """Fixed scopes; callers cannot submit SDDL or choose writable fields."""
    if set(config) != {"base_dn", "people_dn", "roles"} or set(config["roles"]) != ROLES or set(sids) != ROLES or len(set(sids.values())) != len(ROLES):
        raise ValueError("mail directory invalid roles or configuration")
    base, people = config["base_dn"], config["people_dn"]
    mail = "OU=Mail," + base
    # Use concrete AD rights. Samba splits inheritable GA/GR into an
    # inherit-only generic ACE plus an expanded ACE on the object itself.
    # Concrete masks retain the same rights and round-trip as one exact ACE.
    common = "O:DAG:DAD:P(A;CI;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;DA)(A;CI;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;SY)"
    common += "".join("(A;CI;LCRPLORC;;;" + sids[role] + ")" for role in sorted(ROLES))
    ous = {mail: common}
    for ou, kind in (("Mailboxes", "anasMailbox"), ("Addresses", "anasMailAddressClaim")):
        aces = ""
        for role in ("creator", "address_writer", "primary_writer"):
            aces += "(OA;;CC;" + guids[kind] + ";;" + sids[role] + ")"
            fields = COMMIT_FIELDS if role == "creator" else PRIMARY_FIELDS[kind] if role == "primary_writer" else WRITER_FIELDS[kind]
            aces += "".join("(OA;CI;WP;" + guids[field] + ";" + guids[kind] + ";" + sids[role] + ")" for field in fields)
        ous["OU=" + ou + "," + mail] = common + aces
    grants = {base: [], people: []}
    # Current-domain collision queries need mail/proxy visibility outside People.
    # These ACEs grant no read of password secrets and no write outside People.
    for role in sorted(ROLES):
        sid = sids[role]
        grants[base] += ["(A;CI;LC;;;" + sid + ")"]
        grants[base] += ["(OA;CI;RP;" + guids[attr] + ";;" + sid + ")" for attr in ("mail", "proxyAddresses")]
    grants[people] += ["(OA;CI;WP;" + guids["proxyAddresses"] + ";" + guids["user"] + ";" + sids["address_writer"] + ")"]
    # The existing Anchor Worker retains its exact anchor/mail ACEs. Add only
    # personnel proxy writes; no new sAMAccountName/password/owner authority.
    grants[people] += ["(OA;CI;WP;" + guids[attr] + ";;" + sids["primary_writer"] + ")" for attr in ANCHOR_FIELDS + ("mail",)]
    grants[people] += ["(OA;CI;WP;" + guids["proxyAddresses"] + ";" + guids["user"] + ";" + sids["primary_writer"] + ")"]
    # Same fixed ordinary-user creation right as portal provisioning. No reset,
    # existing-user mail/sAMAccountName write or group/computer creation right.
    grants[people] += ["(OA;CI;CC;" + guids["user"] + ";;" + sids["creator"] + ")"]
    return ous, grants


def prepare(directory, config, write=False):
    """All preflight, additions and read-back share one local AD transaction."""
    directory.begin()
    try:
        sids, guids = directory.validate(config)
        ous, grants = policy(config, sids, guids)
        missing = []
        for dn, descriptor in ous.items():
            if not directory.check_ou(dn, descriptor):
                missing.append((dn, descriptor))
        additions = {dn: directory.missing_grants(dn, aces, sids) for dn, aces in grants.items()}
        count = len(missing) + sum(len(aces) for aces in additions.values())
        if count and not write:
            raise ValueError("mail directory incomplete; explicit preparation required")
        for dn, descriptor in missing:
            directory.add_ou(dn, descriptor)
        for dn, aces in additions.items():
            if aces:
                directory.add_grants(dn, aces)
        for dn, descriptor in ous.items():
            if not directory.check_ou(dn, descriptor):
                raise ValueError("mail directory OU read-back failed")
        for dn, aces in grants.items():
            if directory.missing_grants(dn, aces, sids):
                raise ValueError("mail directory ACL read-back failed")
        if write:
            directory.commit()
        else:
            directory.cancel()
        return count
    except BaseException:
        directory.cancel()
        raise


class Directory:
    def __init__(self):
        import ldb
        from samba.auth import system_session
        from samba.dcerpc import security
        from samba.ndr import ndr_pack, ndr_unpack
        from samba.param import LoadParm
        from samba.samdb import SamDB
        self.ldb, self.security = ldb, security
        self.pack, self.unpack = ndr_pack, ndr_unpack
        lp = LoadParm(); lp.load("/etc/samba/smb.conf")
        self.db = SamDB(url="/var/lib/samba/private/sam.ldb", session_info=system_session(), lp=lp)
        self.sid = security.dom_sid(self.db.get_domain_sid())
        self.active = False

    def begin(self):
        self.db.transaction_start(); self.active = True

    def commit(self):
        self.db.transaction_commit(); self.active = False

    def cancel(self):
        if self.active:
            self.db.transaction_cancel(); self.active = False

    def read(self, dn, attrs, controls=None):
        try:
            rows = self.db.search(base=dn, scope=self.ldb.SCOPE_BASE, attrs=attrs, controls=controls or [])
        except self.ldb.LdbError as exc:
            if exc.args[0] == self.ldb.ERR_NO_SUCH_OBJECT:
                return None
            raise
        if len(rows) != 1:
            raise ValueError("mail directory ambiguous object")
        return rows[0]

    def dn(self, value):
        if not isinstance(value, str) or len(value) > 2048 or not value or any(ord(c) < 32 for c in value):
            raise ValueError("mail directory invalid DN")
        return self.ldb.Dn(self.db, value)

    def validate(self, config):
        if not isinstance(config, dict) or set(config) != {"base_dn", "people_dn", "roles"} or not isinstance(config["roles"], dict) or set(config["roles"]) != ROLES:
            raise ValueError("mail directory invalid configuration")
        base = self.dn(config["base_dn"])
        people = self.dn(config["people_dn"])
        service = self.dn("OU=Service Accounts," + str(base))
        mail = self.dn("OU=Mail," + str(base))
        if base != self.dn(str(self.db.domain_dn())) or str(base).casefold() != os.environ.get("SAMBA_DC_BASE_DN", "").casefold():
            raise ValueError("mail directory provider domain mismatch")
        if not people.is_child_of(base) or people == service or people.is_child_of(service) or service.is_child_of(people) or people == mail or people.is_child_of(mail) or mail.is_child_of(people):
            raise ValueError("mail directory overlapping or invalid personnel scope")
        for dn in (people, service):
            row = self.read(str(dn), ["objectClass"])
            if row is None or b"organizationalUnit" not in row["objectClass"]:
                raise ValueError("mail directory required provider OU missing")
        # Claims rely on one writable DC; do not imply multi-DC uniqueness.
        root = self.read("", ["configurationNamingContext", "dsServiceName"])
        nodes = self.db.search(base=str(root["configurationNamingContext"][0]), expression="(objectClass=nTDSDSA)", attrs=["msDS-isRODC"])
        schema = str(self.db.get_schema_basedn())
        owner = self.read(schema, ["fSMORoleOwner"])
        if len(nodes) != 1 or str(nodes[0].get("msDS-isRODC", ["FALSE"])[0]).upper() != "FALSE" or self.dn(str(owner["fSMORoleOwner"][0])) != nodes[0].dn or self.dn(str(root["dsServiceName"][0])) != nodes[0].dn:
            raise ValueError("mail directory requires the single writable schema-master DC")
        sids = {}
        for role, value in config["roles"].items():
            dn = self.dn(value)
            if dn.parent() != service:
                raise ValueError("mail directory role must be a direct service-OU account")
            row = self.read(str(dn), ["objectClass", "objectSid", "userAccountControl", "primaryGroupID", "memberOf", "adminCount", "sIDHistory"])
            if row is None or b"user" not in row["objectClass"] or b"computer" in row["objectClass"] or row.get("memberOf") or row.get("sIDHistory") or str(row.get("adminCount", ["0"])[0]) != "0" or str(row.get("primaryGroupID", [""])[0]) != "513" or int(str(row.get("userAccountControl", ["0"])[0])) not in (512, 66048):
                raise ValueError("mail directory role must be an enabled ordinary unprivileged service account")
            sid = str(self.unpack(self.security.dom_sid, bytes(row["objectSid"][0])))
            if not sid.startswith(str(self.sid) + "-") or int(sid.rsplit("-", 1)[1]) < 1000:
                raise ValueError("mail directory built-in role account rejected")
            sids[role] = sid
        # memberOf excludes the primary group. Check its transitive parents too.
        groups = self.db.search(base=str(base), expression="(objectClass=group)", attrs=["objectSid", "member"])
        pending = [g.dn for g in groups if str(self.unpack(self.security.dom_sid, bytes(g["objectSid"][0]))) == str(self.sid) + "-513"]
        if len(pending) != 1:
            raise ValueError("mail directory primary group unavailable")
        seen = set()
        privileged = {512, 516, 518, 519, 520, 521, 526, 527, 544, 548, 549, 550, 551, 552}
        while pending:
            dn = pending.pop()
            if str(dn).casefold() in seen:
                continue
            seen.add(str(dn).casefold())
            for group in groups:
                if group.dn == dn and int(str(self.unpack(self.security.dom_sid, bytes(group["objectSid"][0]))).rsplit("-", 1)[1]) in privileged:
                    raise ValueError("mail directory service primary group inherits privileged roles")
                if any(self.dn(str(member)) == dn for member in group.get("member", [])):
                    pending.append(group.dn)
        from samba.dcerpc import misc
        names = set(COMMIT_FIELDS) | {name for fields in WRITER_FIELDS.values() for name in fields} | set(ANCHOR_FIELDS) | {"anasMailbox", "anasMailAddressClaim", "mail", "proxyAddresses", "user"}
        guids = {}
        for name in names:
            rows = self.db.search(base=schema, expression="(lDAPDisplayName=" + name + ")", attrs=["schemaIDGUID"])
            if len(rows) != 1:
                raise ValueError("mail directory schema attribute unavailable")
            guids[name] = str(self.unpack(misc.GUID, bytes(rows[0]["schemaIDGUID"][0])))
        return sids, guids

    def descriptor(self, dn):
        row = self.read(dn, ["objectClass", "description", "nTSecurityDescriptor"], ["sd_flags:1:7"])
        return row, None if row is None else self.unpack(self.security.descriptor, bytes(row["nTSecurityDescriptor"][0]))

    def check_ou(self, dn, text):
        row, actual = self.descriptor(dn)
        if row is None:
            return False
        wanted = self.security.descriptor.from_sddl(text, self.sid)
        # Samba can reorder equivalent ACEs; compare owner/group/protection and
        # ACE bytes, rejecting additional inherited or explicit permissions.
        if b"organizationalUnit" not in row["objectClass"] or list(row.get("description", [])) != [MARKER.encode()] or str(actual.owner_sid) != str(wanted.owner_sid) or str(actual.group_sid) != str(wanted.group_sid) or not actual.type & self.security.SEC_DESC_DACL_PROTECTED or sorted(self.pack(a) for a in actual.dacl.aces) != sorted(self.pack(a) for a in wanted.dacl.aces):
            raise ValueError("mail directory OU ownership or ACL drift")
        return True

    def add_ou(self, dn, text):
        self.db.add({"dn": dn, "objectClass": "organizationalUnit", "description": MARKER, "nTSecurityDescriptor": self.pack(self.security.descriptor.from_sddl(text, self.sid))})

    def missing_grants(self, dn, texts, sids):
        _, descriptor = self.descriptor(dn)
        if descriptor is None or descriptor.dacl is None:
            raise ValueError("mail directory ACL target missing")
        wanted = [self.security.descriptor.from_sddl("D:" + text, self.sid).dacl.aces[0] for text in texts]
        allowed = {self.pack(ace) for ace in wanted}
        # Preserve all unrelated ACEs. An explicit grant for a managed service
        # that exceeds this tool's policy requires operator review, not repair.
        for ace in descriptor.dacl.aces:
            if str(ace.trustee) in sids.values() and not ace.flags & self.security.SEC_ACE_FLAG_INHERITED_ACE and self.pack(ace) not in allowed:
                raise ValueError("mail directory unexpected existing service ACL")
        existing = {self.pack(ace) for ace in descriptor.dacl.aces}
        return [text for text, ace in zip(texts, wanted) if self.pack(ace) not in existing]

    def add_grants(self, dn, texts):
        _, descriptor = self.descriptor(dn)
        for text in texts:
            descriptor.dacl_add(self.security.descriptor.from_sddl("D:" + text, self.sid).dacl.aces[0])
        message = self.ldb.Message(); message.dn = self.dn(dn)
        message["nTSecurityDescriptor"] = self.ldb.MessageElement(self.pack(descriptor), self.ldb.FLAG_MOD_REPLACE, "nTSecurityDescriptor")
        self.db.modify(message, controls=["sd_flags:1:4"])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--check", action="store_true")
    mode.add_argument("--prepare", action="store_true")
    args = parser.parse_args()
    try:
        raw = sys.stdin.buffer.read(65537)
        if len(raw) > 65536:
            raise ValueError("mail directory configuration too large")
        config = json.loads(raw)
        if args.prepare and os.environ.get("SAMBA_DC_DOMAIN_ACTION") == "join":
            raise ValueError("mail directory joined DC is check-only")
        loader = importlib.util.spec_from_file_location("mail_schema", Path(__file__).with_name("install-mail-schema.py"))
        schema = importlib.util.module_from_spec(loader); loader.loader.exec_module(schema)
        schema.install(schema.Directory(), schema.load_spec())
        count = prepare(Directory(), config, args.prepare)
    except Exception as exc:
        # Do not emit directory objects or configuration, even for native errors.
        message = str(exc) if type(exc) is ValueError and str(exc).startswith(("mail directory ", "mail schema ")) else "mail directory operation failed"
        print(message, file=sys.stderr)
        return 1
    print("mail directory verified: 3 managed OUs; changes=" + str(count))
    return 0


if __name__ == "__main__":
    sys.exit(main())
