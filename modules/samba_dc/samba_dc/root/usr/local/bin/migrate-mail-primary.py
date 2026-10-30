#!/usr/bin/python3
"""Explicit offline migration of existing personnel primary-mail resources.

Default --check plans without writes. --execute needs a caller-verified full
Samba snapshot, all writers stopped and a new private evidence directory.
No personnel, alias, password, membership, schema or existing resource is edited.
"""

import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import uuid

SHARE = Path(__file__).resolve().parents[1] / "share/anas"
sys.path.insert(0, str(SHARE))
from mail_policy import default_address, desired_mail, mail_domain, mail_occupants, values

RESOURCE_FIELDS = ["objectClass", "cn", "anasMailboxID", "anasMailOwnerAnchor", "anasMailboxKind", "anasMailDefaultAddress", "anasMailAddress", "anasMailAddressKind", "anasMailState", "anasMailConfigVersion", "anasMailOperationID", "anasMailOperationState", "anasMailOperationData"]
PERSON_FIELDS = ["objectClass", "objectGUID", "objectSid", "userAccountControl", "isCriticalSystemObject", "sAMAccountName", "mail", "proxyAddresses", "mS-DS-ConsistencyGuid", "anasIdentityAnchor"]


def load_bootstrap():
    path = Path(__file__).with_name("install-mail-directory.py")
    loader = importlib.util.spec_from_file_location("mail_directory", path)
    module = importlib.util.module_from_spec(loader); loader.loader.exec_module(module)
    return module


def one(attrs, name):
    found = values(attrs, name)
    if len(found) != 1:
        raise ValueError("mail migration ambiguous or missing resource field")
    return found[0].decode("utf-8")


def canonical_uuid(value):
    if str(uuid.UUID(value)) != value:
        raise ValueError("mail migration noncanonical identity")
    return value


def locator(base, owner=None, address=None):
    if owner is not None:
        return "CN=" + hashlib.sha256(("anas-primary-mailbox\n" + owner).encode()).hexdigest() + ",OU=Mailboxes,OU=Mail," + base
    return "CN=" + hashlib.sha256(address.encode()).hexdigest() + ",OU=Addresses,OU=Mail," + base


def resource(attrs, kind):
    classes = {item.lower() for item in values(attrs, "objectClass")}
    if kind.lower().encode() not in classes or classes & {b"user", b"computer"}:
        raise ValueError("mail migration invalid non-login resource")
    for name in ("anasMailboxID", "anasMailOwnerAnchor", "anasMailConfigVersion", "anasMailOperationID"):
        canonical_uuid(one(attrs, name))
    if one(attrs, "anasMailState") != "active" or one(attrs, "anasMailOperationState") != "complete":
        raise ValueError("mail migration existing operation requires reconciliation")


def plan(snapshot, base, domain):
    """Deterministic inventory; never guess aliases' independent mailbox mapping."""
    domain = mail_domain(domain)
    boxes, claims, box_ids = {}, {}, set()
    for dn, attrs in snapshot["boxes"]:
        key = dn.casefold()
        box_id = one(attrs, "anasMailboxID")
        if key in boxes or box_id in box_ids:
            raise ValueError("mail migration duplicate mailbox identity")
        boxes[key] = attrs; box_ids.add(box_id)
    for dn, attrs in snapshot["claims"]:
        if dn.casefold() in claims:
            raise ValueError("mail migration duplicate claim")
        claims[dn.casefold()] = attrs
    namespace = mail_occupants(snapshot["namespace"])
    # Dirty values are never granted, but surrounding whitespace must not hide
    # a competing legacy address from the migration preflight.
    for _dn, attrs in snapshot["namespace"]:
        addresses = list(values(attrs, "mail"))
        addresses += [raw.strip()[5:] for raw in values(attrs, "proxyAddresses") if raw.strip()[:5].lower() == b"smtp:"]
        for raw in addresses:
            namespace.setdefault(raw.strip().lower(), set()).add(values(attrs, "objectGUID")[0])
    anchors = {}
    for _dn, attrs in snapshot["anchors"]:
        guid = values(attrs, "objectGUID")
        if len(guid) != 1 or len(guid[0]) != 16:
            raise ValueError("mail migration namespace identity unavailable")
        for anchor in values(attrs, "anasIdentityAnchor"):
            anchors.setdefault(anchor.lower(), set()).add(guid[0])
    creates, existing, seen_owners, unmanaged = [], [], set(), set()
    for dn, attrs in sorted(snapshot["people"], key=lambda row: row[0].casefold()):
        if values(attrs, "isCriticalSystemObject") == [b"TRUE"]:
            continue
        address_bytes = desired_mail(attrs, domain)
        if address_bytes is None:
            continue
        sid = values(attrs, "objectSid")
        if not int(one(attrs, "userAccountControl")) & 512 or len(sid) != 1 or len(sid[0]) < 12:
            raise ValueError("mail migration ordinary personnel SID or account type unavailable")
        guid = values(attrs, "objectGUID")
        binary = values(attrs, "mS-DS-ConsistencyGuid")
        if len(guid) != 1 or len(guid[0]) != 16 or len(binary) != 1 or len(binary[0]) != 16:
            raise ValueError("mail migration personnel identity unavailable")
        owner = canonical_uuid(one(attrs, "anasIdentityAnchor"))
        if str(uuid.UUID(bytes_le=binary[0])) != owner or anchors.get(owner.encode()) != {guid[0]} or owner in seen_owners:
            raise ValueError("mail migration personnel anchor conflict")
        seen_owners.add(owner)
        if values(attrs, "mail") != [address_bytes]:
            raise ValueError("mail migration primary policy must converge before migration")
        if namespace.get(address_bytes, set()) != {guid[0]}:
            raise ValueError("mail migration legacy address ownership conflict")
        address = address_bytes.decode("ascii")
        item = {"person_dn": dn, "owner": owner, "object_guid": str(uuid.UUID(bytes_le=guid[0])), "address": address}
        box_dn, claim_dn = locator(base, owner=owner), locator(base, address=address)
        box, claim = boxes.get(box_dn.casefold()), claims.get(claim_dn.casefold())
        matching_claims = {other_dn.casefold() for other_dn, other in snapshot["claims"] if any(raw.strip().lower() == address_bytes for raw in values(other, "anasMailAddress"))}
        if matching_claims - {claim_dn.casefold()}:
            raise ValueError("mail migration noncanonical or duplicate address claim")
        if any(other_dn.casefold() != box_dn.casefold() and any(raw.strip().lower() == address_bytes for raw in values(other, "anasMailDefaultAddress")) for other_dn, other in snapshot["boxes"]):
            raise ValueError("mail migration address referenced by another mailbox")
        # A second primary at another DN is never silently adopted or ignored.
        for other_dn, other in snapshot["boxes"]:
            if values(other, "anasMailOwnerAnchor") == [owner.encode()] and values(other, "anasMailboxKind") == [b"primary"] and other_dn.casefold() != box_dn.casefold():
                raise ValueError("mail migration noncanonical existing primary")
        if box is None:
            if claim is not None:
                raise ValueError("mail migration cannot adopt an existing claim")
            creates.append(item)
        else:
            resource(box, "anasMailbox")
            if one(box, "anasMailboxID") == owner or one(box, "anasMailOwnerAnchor") != owner or one(box, "anasMailboxKind") != "primary" or one(box, "anasMailDefaultAddress") != address:
                raise ValueError("mail migration primary ownership conflict")
            operation = json.loads(one(box, "anasMailOperationData"))
            if not isinstance(operation, dict) or operation.get("type") not in ("person.import", "user.create", "system.primary") or operation.get("object_guid") != item["object_guid"]:
                raise ValueError("mail migration personnel object was replaced or operation unknown")
            if operation["type"] == "system.primary":
                source = operation.get("from", "")
                if set(operation) != {"type", "object_guid", "from", "to"} or operation.get("to") != address or source.split("@", 1)[0] != address.split("@", 1)[0]:
                    raise ValueError("mail migration system primary operation invalid")
                parts = source.split("@")
                if len(parts) != 2 or default_address(*parts).decode() != source:
                    raise ValueError("mail migration system primary source invalid")
            if claim is None:
                raise ValueError("mail migration completed primary claim missing")
            resource(claim, "anasMailAddressClaim")
            if any(one(claim, name) != expected for name, expected in {
                "anasMailboxID": one(box, "anasMailboxID"), "anasMailOwnerAnchor": owner,
                "anasMailAddress": address, "anasMailAddressKind": "primary", "anasMailOperationID": one(box, "anasMailOperationID"),
            }.items()):
                raise ValueError("mail migration primary claim ownership conflict")
            existing.append(dict(item, mailbox_id=one(box, "anasMailboxID")))
        owner_box_ids = {one(b, "anasMailboxID").encode() for _box_dn, b in snapshot["boxes"] if values(b, "anasMailOwnerAnchor") == [owner.encode()]}
        for proxy in values(attrs, "proxyAddresses"):
            if proxy[:5].lower() == b"smtp:" and proxy[5:].lower() != address_bytes:
                raw = proxy[5:].lower()
                # Count only; leave every value and pre-existing mapping intact.
                mapped = any(
                    values(c, "anasMailAddress") == [raw] and values(c, "anasMailOwnerAnchor") == [owner.encode()]
                    and values(c, "anasMailState") == [b"active"] and one(c, "anasMailboxID").encode() in owner_box_ids
                    for _claim_dn, c in snapshot["claims"]
                )
                if not mapped:
                    unmanaged.add((owner, raw))
    return {"creates": creates, "existing": existing, "unmapped_smtp_count": len(unmanaged)}


def records(base, item):
    box_id, operation_id = str(uuid.uuid4()), str(uuid.uuid4())
    operation = json.dumps({"type": "person.import", "object_guid": item["object_guid"], "person_dn": item["person_dn"]}, separators=(",", ":"))
    common = {"anasMailboxID": box_id, "anasMailOwnerAnchor": item["owner"], "anasMailState": "active", "anasMailOperationID": operation_id, "anasMailOperationState": "complete", "anasMailOperationData": operation}
    box_dn, claim_dn = locator(base, owner=item["owner"]), locator(base, address=item["address"])
    box = dict(common, dn=box_dn, objectClass="anasMailbox", cn=box_dn[3:].split(",", 1)[0], anasMailConfigVersion=str(uuid.uuid4()), anasMailboxKind="primary", anasMailDefaultAddress=item["address"])
    claim = dict(common, dn=claim_dn, objectClass="anasMailAddressClaim", cn=claim_dn[3:].split(",", 1)[0], anasMailConfigVersion=str(uuid.uuid4()), anasMailAddress=item["address"], anasMailAddressKind="primary")
    return box, claim


def migrate(directory, config, write=False, before=None):
    directory.begin()
    try:
        directory.validate_migration(config)
        proposed = plan(directory.inventory(config), config["base_dn"], config["mail_domain"])
        result = {"created": 0, "planned": len(proposed["creates"]), "existing": len(proposed["existing"]), "unmapped_smtp_count": proposed["unmapped_smtp_count"], "mailboxes": list(proposed["existing"])}
        if not write:
            directory.cancel()
            return result
        additions = [records(config["base_dn"], item) for item in proposed["creates"]]
        result["mailboxes"] += [dict(item, mailbox_id=box["anasMailboxID"]) for item, (box, _claim) in zip(proposed["creates"], additions)]
        if before:
            before(result)
        for box, claim in additions:
            directory.add(box); directory.add(claim)
        confirmed = plan(directory.inventory(config), config["base_dn"], config["mail_domain"])
        expected = {(x["owner"], x["mailbox_id"], x["address"]) for x in result["mailboxes"]}
        actual = {(x["owner"], x["mailbox_id"], x["address"]) for x in confirmed["existing"]}
        if confirmed["creates"] or actual != expected:
            raise ValueError("mail migration read-back mismatch")
        directory.commit()
        result["created"] = len(additions)
        return result
    except BaseException:
        directory.cancel()
        raise


def native_directory():
    bootstrap = load_bootstrap()

    class Directory(bootstrap.Directory):
        def validate_migration(self, config):
            if not isinstance(config, dict) or set(config) != {"base_dn", "people_dn", "roles", "mail_domain"}:
                raise ValueError("mail migration invalid configuration")
            mail_domain(config["mail_domain"])
            scoped = {key: value for key, value in config.items() if key != "mail_domain"}
            sids, guids = self.validate(scoped)
            ous, grants = bootstrap.policy(scoped, sids, guids)
            if any(not self.check_ou(dn, descriptor) for dn, descriptor in ous.items()) or any(self.missing_grants(dn, aces, sids) for dn, aces in grants.items()):
                raise ValueError("mail migration directory preparation incomplete")

        def inventory(self, config):
            def search(base, expression, attrs):
                rows = self.db.search(base=base, scope=self.ldb.SCOPE_SUBTREE, expression=expression, attrs=attrs)
                return [(str(row.dn), {str(name): [bytes(value) for value in row[name]] for name in row.keys() if str(name) != "dn"}) for row in rows]
            base = config["base_dn"]
            return {
                "people": search(config["people_dn"], "(&(objectClass=user)(!(objectClass=computer)))", PERSON_FIELDS),
                "namespace": search(base, "(|(mail=*)(proxyAddresses=*))", ["objectGUID", "mail", "proxyAddresses"]),
                "anchors": search(base, "(anasIdentityAnchor=*)", ["objectGUID", "anasIdentityAnchor"]),
                "boxes": search("OU=Mailboxes,OU=Mail," + base, "(objectClass=anasMailbox)", RESOURCE_FIELDS),
                "claims": search("OU=Addresses,OU=Mail," + base, "(objectClass=anasMailAddressClaim)", RESOURCE_FIELDS),
            }

        def add(self, record):
            self.db.add(record)

    return Directory()


def save(path, value):
    with path.open("x", encoding="utf-8") as stream:
        os.chmod(path, 0o600)
        json.dump(value, stream, ensure_ascii=True, indent=2)
        stream.write("\n"); stream.flush(); os.fsync(stream.fileno())


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--check", action="store_true")
    mode.add_argument("--execute", action="store_true")
    parser.add_argument("--snapshot-id")
    parser.add_argument("--evidence-dir")
    args = parser.parse_args()
    try:
        raw = sys.stdin.buffer.read(65537)
        if len(raw) > 65536:
            raise ValueError("mail migration configuration too large")
        config = json.loads(raw)
        if args.execute:
            if os.geteuid() != 0 or not args.snapshot_id or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._:-]{0,127}", args.snapshot_id) or not args.evidence_dir or os.environ.get("SAMBA_DC_DOMAIN_ACTION") == "join":
                raise ValueError("mail migration requires root, verified snapshot reference and explicit evidence destination")
            # Like the existing schema migration, this cannot prove host-wide
            # quiescence. The operator must also stop external LDAP writers.
            for process in ("samba", "samba-ad-dc"):
                checked = subprocess.run(["pgrep", "-x", process], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                if checked.returncode != 1:
                    raise ValueError("mail migration requires a stopped DC")
            evidence = Path(args.evidence_dir)
            parent = evidence.parent.resolve(strict=True)
            protected = Path("/var/lib/samba").resolve()
            if not evidence.is_absolute() or evidence.parent != parent or parent == protected or protected in parent.parents or stat.S_IMODE(parent.stat().st_mode) & 0o077:
                raise ValueError("mail migration evidence must use a private parent outside Samba data")
            evidence.mkdir(mode=0o700)
        loader = importlib.util.spec_from_file_location("mail_schema", Path(__file__).with_name("install-mail-schema.py"))
        schema = importlib.util.module_from_spec(loader); loader.loader.exec_module(schema)
        schema.install(schema.Directory(), schema.load_spec())
        def before(result):
            save(evidence / "intent.json", {"snapshot_id": args.snapshot_id, "source_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), "result": result})
        result = migrate(native_directory(), config, args.execute, before if args.execute else None)
        if args.execute:
            save(evidence / "result.json", result)
        print(json.dumps({key: value for key, value in result.items() if key != "mailboxes"}, sort_keys=True))
    except Exception as exc:
        message = str(exc) if type(exc) is ValueError and str(exc).startswith(("mail migration ", "mail directory ", "mail schema ")) else "mail migration operation failed; inspect directory before retrying"
        print(message, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
