import copy
import importlib.util
import json
from pathlib import Path
import sys
import unittest
import uuid

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "anchor_worker"))
SOURCE = ROOT / "samba_dc/root/usr/local/bin/migrate-mail-primary.py"
loader = importlib.util.spec_from_file_location("mail_primary_migration", SOURCE)
migration = importlib.util.module_from_spec(loader); loader.loader.exec_module(migration)
BASE = "DC=example,DC=test"
CONFIG = {"base_dn": BASE, "mail_domain": "example.test"}


def person(name):
    anchor, guid = uuid.uuid4(), uuid.uuid4()
    return ("CN=" + name + ",OU=People," + BASE, {
        "objectClass": [b"top", b"person", b"user"], "objectGUID": [guid.bytes_le],
        "mS-DS-ConsistencyGuid": [anchor.bytes_le], "anasIdentityAnchor": [str(anchor).encode()],
        "sAMAccountName": [name.encode()], "mail": [(name + "@example.test").encode()],
        "userAccountControl": [b"512"], "objectSid": [b"\x01\x02\x00\x00\x00\x00\x00\x05" + (21).to_bytes(4, "little") + (1100).to_bytes(4, "little")],
    })


class FakeDirectory:
    def __init__(self):
        people = [person("alice"), person("bob")]
        people[0][1]["proxyAddresses"] = [b"smtp:alias@example.test", b"X500:/old"]
        self.data = {"people": people, "namespace": copy.deepcopy(people), "anchors": copy.deepcopy(people), "boxes": [], "claims": []}
        self.saved = None
        self.writes = 0
        self.fail_after = None
        self.fail_readback = False

    def begin(self): self.saved = copy.deepcopy(self.data)
    def commit(self): self.saved = None
    def cancel(self):
        if self.saved is not None:
            self.data = self.saved; self.saved = None
    def validate_migration(self, _config): pass
    def inventory(self, _config):
        result = copy.deepcopy(self.data)
        if self.fail_readback and self.writes:
            result["claims"] = []
        return result
    def add(self, record):
        if self.writes == self.fail_after:
            raise OSError("injected write interruption")
        self.writes += 1
        target = "boxes" if record["objectClass"] == "anasMailbox" else "claims"
        self.data[target].append((record["dn"], {name: [str(value).encode()] for name, value in record.items() if name != "dn"}))


class MailPrimaryMigrationTests(unittest.TestCase):
    def test_completed_system_primary_can_be_verified_without_reimport(self):
        db = FakeDirectory()
        migration.migrate(db, CONFIG, True)
        for _dn, row in db.data['boxes']:
            previous = json.loads(row['anasMailOperationData'][0])
            target = row['anasMailDefaultAddress'][0].decode()
            row['anasMailOperationData'] = [json.dumps({'type':'system.primary', 'object_guid':previous['object_guid'], 'from':target.split('@')[0]+'@old.test', 'to':target}).encode()]
        before = copy.deepcopy(db.data)
        self.assertEqual(migration.migrate(db, CONFIG)['existing'], 2)
        self.assertEqual(db.data, before)
        for source in ('other@old.test', 'alice@ bad.test', 'alice@old.test@other.test'):
            broken = copy.deepcopy(before)
            row = broken['boxes'][0][1]
            op = json.loads(row['anasMailOperationData'][0]); op['from'] = source
            row['anasMailOperationData'] = [json.dumps(op).encode()]
            db.data = broken
            with self.subTest(source=source), self.assertRaises(ValueError):
                migration.migrate(db, CONFIG)

    def test_check_is_deterministic_and_never_writes_or_changes_personnel(self):
        db = FakeDirectory(); before = copy.deepcopy(db.data)
        first = migration.migrate(db, CONFIG)
        self.assertEqual(first, migration.migrate(db, CONFIG))
        self.assertEqual(first["planned"], 2)
        self.assertEqual(first["unmapped_smtp_count"], 1)
        self.assertEqual(db.writes, 0)
        self.assertEqual(db.data, before)

    def test_import_and_repeat_preserve_distinct_ids_personnel_and_aliases(self):
        db = FakeDirectory(); people = copy.deepcopy(db.data["people"])
        first = migration.migrate(db, CONFIG, True)
        self.assertEqual((first["created"], first["existing"], first["unmapped_smtp_count"]), (2, 0, 1))
        self.assertEqual(db.data["people"], people)
        for entry in first["mailboxes"]:
            self.assertNotEqual(entry["mailbox_id"], entry["owner"])
            self.assertNotEqual(entry["owner"], entry["object_guid"])
        again = migration.migrate(db, CONFIG, True)
        self.assertEqual((again["created"], again["existing"]), (0, 2))
        self.assertEqual(first["mailboxes"], again["mailboxes"])
        self.assertEqual(db.writes, 4)
        self.assertEqual(len(db.data["claims"]), 2)

    def test_every_write_interruption_and_readback_failure_roll_back(self):
        for fail_after in range(4):
            with self.subTest(fail_after=fail_after):
                db = FakeDirectory(); db.fail_after = fail_after
                before = copy.deepcopy(db.data)
                with self.assertRaises(OSError): migration.migrate(db, CONFIG, True)
                self.assertEqual(db.data, before)
                db.fail_after = None
                self.assertEqual(migration.migrate(db, CONFIG, True)["created"], 2)
        db = FakeDirectory(); db.fail_readback = True
        before = copy.deepcopy(db.data)
        with self.assertRaises(ValueError): migration.migrate(db, CONFIG, True)
        self.assertEqual(db.data, before)

    def test_evidence_failure_precedes_writes_and_lost_result_does_not_recreate(self):
        db = FakeDirectory()
        def failed(_result): raise OSError("evidence unavailable")
        with self.assertRaises(OSError): migration.migrate(db, CONFIG, True, failed)
        self.assertEqual(db.writes, 0)
        committed = migration.migrate(db, CONFIG, True)
        # Emulate process/response failure after commit; the next process sees
        # the same object IDs, not a new import or rewritten personnel.
        restarted = FakeDirectory(); restarted.data = copy.deepcopy(db.data)
        again = migration.migrate(restarted, CONFIG, True)
        self.assertEqual(committed["mailboxes"], again["mailboxes"])
        self.assertEqual(restarted.writes, 0)

    def test_anchor_policy_and_legacy_collisions_fail_before_any_write(self):
        for kind in ("missing_binary", "anchor_mismatch", "duplicate_anchor", "old_suffix", "missing_mail", "missing_sid", "bad_type", "other_primary", "other_proxy", "other_proxy_space"):
            with self.subTest(kind=kind):
                db = FakeDirectory(); attrs = db.data["people"][0][1]
                if kind == "missing_binary": attrs.pop("mS-DS-ConsistencyGuid")
                if kind == "anchor_mismatch": attrs["anasIdentityAnchor"] = [str(uuid.uuid4()).encode()]
                if kind == "duplicate_anchor":
                    other = person("clone"); other[1]["anasIdentityAnchor"] = attrs["anasIdentityAnchor"]
                    db.data["anchors"].append(other)
                if kind == "old_suffix": attrs["mail"] = [b"alice@old.test"]
                if kind == "missing_mail": attrs.pop("mail")
                if kind == "missing_sid": attrs.pop("objectSid")
                if kind == "bad_type": attrs["userAccountControl"] = [b"0"]
                if kind.startswith("other_"):
                    other = person("foreign"); other[1].pop("mail")
                    other[1]["mail" if kind == "other_primary" else "proxyAddresses"] = [b"alice@example.test" if kind == "other_primary" else b" SMTP: ALICE@EXAMPLE.TEST " if kind == "other_proxy_space" else b"SMTP:ALICE@EXAMPLE.TEST"]
                    db.data["namespace"].append(other)
                with self.assertRaises(ValueError): migration.migrate(db, CONFIG, True)
                self.assertEqual(db.writes, 0)

    def test_existing_claims_pending_operations_deleted_claims_and_recreated_people_are_not_adopted(self):
        for kind in ("pending", "missing_claim", "wrong_owner", "wrong_box", "wrong_operation", "wrong_kind", "recreated_person", "unpaired_claim", "second_primary", "noncanonical_claim", "other_box_reference"):
            with self.subTest(kind=kind):
                db = FakeDirectory(); migration.migrate(db, CONFIG, True)
                box, claim = db.data["boxes"][0][1], db.data["claims"][0][1]
                if kind == "pending": box["anasMailState"] = [b"pending"]
                if kind == "missing_claim": db.data["claims"].pop(0)
                if kind == "wrong_owner": claim["anasMailOwnerAnchor"] = [str(uuid.uuid4()).encode()]
                if kind == "wrong_box": claim["anasMailboxID"] = [str(uuid.uuid4()).encode()]
                if kind == "wrong_operation": claim["anasMailOperationID"] = [str(uuid.uuid4()).encode()]
                if kind == "wrong_kind": claim["anasMailAddressKind"] = [b"alias"]
                if kind == "recreated_person":
                    new_guid = uuid.uuid4().bytes_le
                    for key in ("people", "namespace", "anchors"): db.data[key][0][1]["objectGUID"] = [new_guid]
                if kind == "unpaired_claim": db.data["boxes"].pop(0)
                if kind == "second_primary": db.data["boxes"][0] = ("CN=unexpected,OU=Mailboxes,OU=Mail," + BASE, box)
                if kind == "noncanonical_claim": db.data["claims"][0] = ("CN=unexpected,OU=Addresses,OU=Mail," + BASE, claim)
                if kind == "other_box_reference":
                    db.data["boxes"][1][1]["anasMailDefaultAddress"] = box["anasMailDefaultAddress"]
                writes = db.writes
                with self.assertRaises(ValueError): migration.migrate(db, CONFIG, True)
                self.assertEqual(db.writes, writes)

    def test_completed_portal_creation_is_reused_after_profile_change(self):
        db = FakeDirectory(); first = migration.migrate(db, CONFIG, True)
        box = db.data["boxes"][0][1]
        op = json.loads(box["anasMailOperationData"][0]); op["type"] = "user.create"
        op["account"] = {"person_dn": "CN=old-label," + BASE, "profile_hash": "old"}
        box["anasMailOperationData"] = [json.dumps(op).encode()]
        db.data["people"][0][1]["displayName"] = [b"New label"]
        again = migration.migrate(db, CONFIG, True)
        self.assertEqual(first["mailboxes"], again["mailboxes"])
        self.assertEqual(db.writes, 4)


if __name__ == "__main__": unittest.main()
