import copy
import importlib.util
from pathlib import Path
import unittest


SOURCE = Path(__file__).resolve().parents[1] / "samba_dc/root/usr/local/bin/install-mail-directory.py"
loader = importlib.util.spec_from_file_location("mail_directory", SOURCE)
directory = importlib.util.module_from_spec(loader)
loader.loader.exec_module(directory)

BASE = "DC=example,DC=test"
CONFIG = {"base_dn": BASE, "people_dn": "OU=People," + BASE,
          "roles": {role: "CN=" + role + ",OU=Service Accounts," + BASE for role in directory.ROLES}}
SIDS = {role: "S-1-5-21-1-2-3-" + str(1100 + i) for i, role in enumerate(sorted(directory.ROLES))}
FIELDS = set(directory.COMMIT_FIELDS) | {field for fields in directory.WRITER_FIELDS.values() for field in fields} | set(directory.ANCHOR_FIELDS) | {"anasMailbox", "anasMailAddressClaim", "mail", "proxyAddresses", "user"}
GUIDS = {name: "guid-" + name for name in FIELDS}


class FakeDirectory:
    def __init__(self):
        self.ous, self.grants = {}, {}
        self.snapshot = None
        self.writes = 0
        self.fail_after = None
        self.invalid = False
        self.readback_failure = False

    def begin(self):
        self.snapshot = copy.deepcopy((self.ous, self.grants))

    def commit(self):
        self.snapshot = None

    def cancel(self):
        if self.snapshot is not None:
            self.ous, self.grants = self.snapshot
            self.snapshot = None

    def validate(self, _config):
        if self.invalid:
            raise ValueError("mail directory invalid service role")
        return SIDS, GUIDS

    def check_ou(self, dn, descriptor):
        if dn not in self.ous:
            return False
        if self.ous[dn] != descriptor:
            raise ValueError("mail directory OU ownership or ACL drift")
        if self.readback_failure and self.writes:
            return False
        return True

    def missing_grants(self, dn, aces, _sids):
        return [ace for ace in aces if ace not in self.grants.get(dn, [])]

    def write(self):
        if self.writes == self.fail_after:
            raise OSError("injected interruption")
        self.writes += 1

    def add_ou(self, dn, descriptor):
        self.write(); self.ous[dn] = descriptor

    def add_grants(self, dn, aces):
        self.write(); self.grants.setdefault(dn, []).extend(aces)


class MailDirectoryTests(unittest.TestCase):
    def test_check_never_writes_explicit_prepare_and_repeat_noop(self):
        db = FakeDirectory()
        with self.assertRaisesRegex(ValueError, "incomplete"):
            directory.prepare(db, CONFIG)
        self.assertEqual(db.writes, 0)
        self.assertEqual(directory.prepare(db, CONFIG, True), 21)
        self.assertEqual(directory.prepare(db, CONFIG), 0)
        self.assertEqual(directory.prepare(db, CONFIG, True), 0)
        self.assertEqual(db.writes, 5)

    def test_every_write_interruption_rolls_back_and_resume(self):
        for fail_after in range(5):
            with self.subTest(fail_after=fail_after):
                db = FakeDirectory(); db.fail_after = fail_after
                with self.assertRaises(OSError):
                    directory.prepare(db, CONFIG, True)
                self.assertEqual((db.ous, db.grants), ({}, {}))
                db.fail_after = None
                self.assertEqual(directory.prepare(db, CONFIG, True), 21)

    def test_service_or_existing_ou_drift_fails_before_writes(self):
        for invalid in (True, False):
            db = FakeDirectory(); db.invalid = invalid
            if not invalid:
                db.ous["OU=Addresses,OU=Mail," + BASE] = "unowned existing OU"
            before = copy.deepcopy((db.ous, db.grants))
            with self.assertRaises(ValueError):
                directory.prepare(db, CONFIG, True)
            self.assertEqual(db.writes, 0)
            self.assertEqual((db.ous, db.grants), before)

    def test_readback_failure_rolls_back(self):
        db = FakeDirectory(); db.readback_failure = True
        with self.assertRaisesRegex(ValueError, "read-back"):
            directory.prepare(db, CONFIG, True)
        self.assertEqual((db.ous, db.grants), ({}, {}))

    def test_fixed_scope_role_separation_and_no_delete_or_acl_delegation(self):
        ous, grants = directory.policy(CONFIG, SIDS, GUIDS)
        self.assertEqual(len(ous), 3)
        self.assertTrue(all(text.startswith("O:DAG:DAD:P") for text in ous.values()))
        for dn, text in ous.items():
            self.assertIn("(A;CI;LCRPLORC;;;" + SIDS["reader"] + ")", text)
            self.assertNotIn(";GA;", text)
            self.assertNotIn(";GR;", text)
            self.assertNotIn("WP;guid-anasMailboxID;guid-anasMailbox;", text)
            self.assertNotIn("WP;guid-anasMailOwnerAnchor;guid-anasMailbox;", text)
            self.assertNotIn("WP;guid-anasMailAddress;", text)
            self.assertNotIn(";DC;", text)
            self.assertNotIn(";WD;", text)
            self.assertNotIn(";WO;", text)
        self.assertNotIn(SIDS["creator"], next(ace for ace in grants[CONFIG["people_dn"]] if ";WP;" in ace))
        self.assertEqual(set(grants), {BASE, CONFIG["people_dn"]})

    def test_role_aliasing_unknown_config_and_custom_role_rejected(self):
        for variant in ("same_sid", "extra_role", "sddl"):
            config, sids = copy.deepcopy(CONFIG), dict(SIDS)
            if variant == "same_sid": sids["reader"] = sids["creator"]
            if variant == "extra_role": config["roles"]["admin"] = "CN=Administrator," + BASE
            if variant == "sddl": config["sddl"] = "D:(A;;GA;;;AU)"
            with self.subTest(variant=variant), self.assertRaises(ValueError):
                directory.policy(config, sids, GUIDS)


if __name__ == "__main__":
    unittest.main()
