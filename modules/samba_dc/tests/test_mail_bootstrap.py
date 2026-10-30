import copy
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest

SOURCE = Path(__file__).resolve().parents[1] / "samba_dc/root/usr/local/bin/bootstrap-mail-directory.py"
spec = importlib.util.spec_from_file_location("mail_bootstrap", SOURCE)
bootstrap = importlib.util.module_from_spec(spec); spec.loader.exec_module(bootstrap)
CONFIG = {"base_dn":"DC=example,DC=test", "people_dn":"OU=People,DC=example,DC=test", "mail_domain":"old.example.test", "snapshot_id":"snapshot-verified-1", "roles":{role:"CN="+role+",OU=Service Accounts,DC=example,DC=test" for role in ("reader","creator","address_writer","primary_writer")}}


class Backend:
    def __init__(self, fail=None):
        self.calls=[]; self.fail=fail
        self.sids={role:"S-1-5-21-1-2-3-"+str(1100+i) for i,role in enumerate(CONFIG["roles"])}
    def step(self, name):
        self.calls.append(name)
        if self.fail == name: raise ValueError("injected interruption")
    def preflight(self): self.step("preflight")
    def schema(self, write): self.step("schema-write" if write else "schema-check")
    def ensure_services(self): self.step("services")
    def prepare(self, write): self.step("acl-write" if write else "acl-check")
    def roles(self): self.step("roles"); return self.sids
    def migrate(self): self.step("migrate")


class MailBootstrapTests(unittest.TestCase):
    def test_restart_checks_without_reimport_or_role_recreation(self):
        with tempfile.TemporaryDirectory() as folder:
            marker=Path(folder)/"completed.json"
            first=Backend()
            self.assertTrue(bootstrap.execute(CONFIG,first,marker))
            self.assertEqual(first.calls,["preflight","schema-write","services","acl-write","roles","migrate","acl-check"])
            second=Backend()
            self.assertFalse(bootstrap.execute(CONFIG,second,marker))
            self.assertEqual(second.calls,["preflight","schema-check","acl-check","roles"])

    def test_every_interruption_keeps_incomplete_and_retry_can_finish(self):
        for failure in ["preflight","schema-write","services","acl-write","roles","migrate","acl-check"]:
            with self.subTest(failure=failure),tempfile.TemporaryDirectory() as folder:
                marker=Path(folder)/"completed.json"
                with self.assertRaises(ValueError): bootstrap.execute(CONFIG,Backend(failure),marker)
                self.assertFalse(marker.exists())
                self.assertTrue(bootstrap.execute(CONFIG,Backend(),marker))

    def test_completed_baseline_and_role_identity_cannot_change(self):
        with tempfile.TemporaryDirectory() as folder:
            marker=Path(folder)/"completed.json"
            bootstrap.execute(CONFIG,Backend(),marker)
            replacement=Backend(); replacement.sids["reader"]="S-1-5-21-1-2-3-9999"
            with self.assertRaises(ValueError): bootstrap.execute(CONFIG,replacement,marker)
            self.assertNotIn("services",replacement.calls)
            self.assertNotIn("migrate",replacement.calls)
            changed=copy.deepcopy(CONFIG);changed["mail_domain"]="new.example.test"
            with self.assertRaises(ValueError): bootstrap.execute(changed,Backend(),marker)
            marker.chmod(0o644)
            denied=Backend()
            with self.assertRaises(ValueError): bootstrap.execute(CONFIG,denied,marker)
            self.assertEqual(denied.calls,[])


if __name__ == "__main__": unittest.main()
