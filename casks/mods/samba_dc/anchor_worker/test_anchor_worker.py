import json
import tempfile
import time
import unittest
from pathlib import Path

from anchor_worker import (
    AuditFollower,
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


class AuditFollowerRotationTest(unittest.TestCase):
    """The reader's half of the contract with Samba's `max log size`.

    Samba caps each log file itself: past the limit it renames the file to
    <path>.old and reopens the original name. The follower has to cross that
    boundary without dropping a record and without replaying one, so these
    tests reproduce the rename rather than trusting that it is harmless.
    """

    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.path = Path(directory.name) / "dsdb.json"
        self.rotated = self.path.with_name(self.path.name + ".old")

    def follow(self) -> AuditFollower:
        follower = AuditFollower(self.path)
        self.assertTrue(follower.open_at_end())
        self.addCleanup(follower.close)
        return follower

    def append(self, text: str) -> None:
        with self.path.open("a", encoding="utf-8") as handle:
            handle.write(text)

    def test_rename_rotation_loses_no_line_and_repeats_none(self):
        self.path.write_text("already-consumed\n", encoding="utf-8")
        follower = self.follow()
        # The cursor starts at the end, so what predates it is not re-read.
        self.assertEqual(follower.lines(), [])

        self.append("before-rotation\n")
        self.path.rename(self.rotated)
        self.path.write_text("after-rotation\n", encoding="utf-8")

        # The poll that notices the inode changed drains the renamed file,
        # and only the next one reads the replacement -- from its start, not
        # from the offset the follower held in the file that was rotated away.
        self.assertEqual(follower.lines(), ["before-rotation\n"])
        self.assertEqual(follower.lines(), ["after-rotation\n"])
        self.assertEqual(follower.lines(), [])

    def test_rotation_drains_a_straggler_writing_through_the_old_descriptor(self):
        self.path.touch()
        follower = self.follow()

        # Samba is multi-process. One process renames and reopens; the others
        # keep appending through the descriptor they already hold until their
        # own check_log_size() sees the path's inode has changed. Those writes
        # land in the rotated file *after* the replacement exists, so noticing
        # the rename is not enough -- the follower has to drain again before
        # letting go of the old descriptor.
        straggler = self.path.open("a", encoding="utf-8")
        self.addCleanup(straggler.close)

        self.path.rename(self.rotated)
        self.path.write_text("from-the-reopened-file\n", encoding="utf-8")
        straggler.write("from-the-old-descriptor\n")
        straggler.flush()

        self.assertEqual(follower.lines(), ["from-the-old-descriptor\n"])
        self.assertEqual(follower.lines(), ["from-the-reopened-file\n"])

    def test_truncation_in_place_skips_records_which_is_why_we_do_not_use_it(self):
        """The case for leaving rotation to Samba instead of adding logrotate.

        copytruncate keeps the inode, so the follower has nothing to detect;
        it can only compare its offset against the size. That works while the
        file is still shorter than the offset, but a writer that refills past
        the old offset first makes the truncation invisible: the follower
        resumes at a byte position that now falls in the middle of the new
        content, skipping every record below it and tearing the one it lands
        in. Samba's rename changes the inode and has no such window.
        """
        self.path.write_text("x" * 200 + "\n", encoding="utf-8")
        follower = self.follow()
        self.assertEqual(follower.lines(), [])

        with self.path.open("w", encoding="utf-8") as handle:
            handle.truncate(0)
        self.append("refilled\n" * 40)

        recovered = follower.lines()
        self.assertLess(len(recovered), 40)
        # Resumed mid-record: the first thing read is not a whole line.
        self.assertEqual(recovered[0], "illed\n")


if __name__ == "__main__":
    unittest.main()
