import json
import tempfile
import time
import unittest
from pathlib import Path

from anchor_worker import (
    Settings,
    Worker,
    extract_audit_event,
    healthcheck,
    printable_anchor,
    split_bases,
)


class AnchorWorkerHelpersTest(unittest.TestCase):
    def test_printable_anchor_uses_active_directory_guid_byte_order(self):
        raw = bytes.fromhex("e324e6032304d349943a576652f81031")
        self.assertEqual(
            printable_anchor(raw),
            "03e624e3-0423-49d3-943a-576652f81031",
        )
        with self.assertRaises(ValueError):
            printable_anchor(b"short")

    def test_extracts_successful_add_with_samba_prefix(self):
        record = {
            "type": "dsdbChange",
            "dsdbChange": {
                "statusCode": 0,
                "status": "Success",
                "operation": "Add",
                "dn": "CN=Alice,OU=People,DC=example,DC=test",
            },
        }
        line = "[2026/08/05] " + json.dumps(record)
        self.assertEqual(
            extract_audit_event(line),
            "CN=Alice,OU=People,DC=example,DC=test",
        )

    def test_ignores_modify_and_failed_add(self):
        for operation, status in (("Modify", "Success"), ("Add", "Failure")):
            line = json.dumps(
                {
                    "type": "dsdbChange",
                    "dsdbChange": {
                        "status": status,
                        "operation": operation,
                        "dn": "CN=Alice,DC=example,DC=test",
                    },
                }
            )
            self.assertIsNone(extract_audit_event(line))

    def test_semicolon_separated_search_bases_preserve_dn_commas(self):
        self.assertEqual(
            split_bases("OU=People,DC=a,DC=test; OU=Admins,DC=a,DC=test"),
            ["OU=People,DC=a,DC=test", "OU=Admins,DC=a,DC=test"],
        )

    def test_healthcheck_requires_recent_successful_reconciliation(self):
        with tempfile.TemporaryDirectory() as directory:
            health_file = Path(directory) / "health.json"
            settings = Settings(
                ldap_url="ldaps://dc:636",
                bind_dn="CN=svc,DC=example,DC=test",
                bind_password="secret",
                user_bases=("OU=People,DC=example,DC=test",),
                group_bases=("OU=Groups,DC=example,DC=test",),
                binary_anchor_attribute="mS-DS-ConsistencyGuid",
                anchor_attribute="anasIdentityAnchor",
                audit_file=Path(directory) / "audit.json",
                health_file=health_file,
                tls_cacert="/certs/ca.crt",
                scan_interval=300,
                page_size=500,
            )
            health_file.write_text(
                json.dumps({"ready": True, "last_scan_at": int(time.time())}),
                encoding="utf-8",
            )
            self.assertEqual(healthcheck(settings), 0)
            health_file.write_text(
                json.dumps({"ready": False, "last_scan_at": int(time.time())}),
                encoding="utf-8",
            )
            self.assertEqual(healthcheck(settings), 1)

    def test_scope_check_rejects_computers_container_without_ldap_retry(self):
        settings = Settings(
            ldap_url="ldaps://dc:636",
            bind_dn="CN=svc,DC=example,DC=test",
            bind_password="secret",
            user_bases=("OU=People,DC=example,DC=test",),
            group_bases=("OU=Groups,DC=example,DC=test",),
            binary_anchor_attribute="mS-DS-ConsistencyGuid",
            anchor_attribute="anasIdentityAnchor",
            audit_file=Path("/tmp/audit.json"),
            health_file=Path("/tmp/health.json"),
            tls_cacert="/certs/ca.crt",
            scan_interval=300,
            page_size=500,
        )
        worker = object.__new__(Worker)
        worker.settings = settings
        self.assertTrue(worker.dn_in_scope("CN=Alice,OU=People,DC=example,DC=test"))
        self.assertFalse(worker.dn_in_scope("CN=PC1,CN=Computers,DC=example,DC=test"))


if __name__ == "__main__":
    unittest.main()
