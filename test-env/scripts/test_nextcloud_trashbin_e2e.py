#!/usr/bin/env python3
# TEST_CASES: NCT-T-003
"""Counterexamples for the E2E oracle, isolation and cleanup."""
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("trashbin_probe", Path(__file__).with_name("server-nextcloud-trashbin-e2e.py"))
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)


class OracleTests(unittest.TestCase):
    def test_authentication_failure_is_not_deletion_protection(self):
        for status in (200, 204, 401, 404, 500):
            with self.subTest(status=status), self.assertRaises(probe.ProbeFailure):
                probe.require_status(status, (403,), "delete")

    def test_trash_listing_requires_successful_properties_and_own_user(self):
        xml = b'''<d:multistatus xmlns:d="DAV:" xmlns:nc="http://nextcloud.org/ns">
<d:response><d:href>/remote.php/dav/trashbin/test-user/trash/file.txt.d123</d:href>
<d:propstat><d:prop><nc:trashbin-filename>file.txt</nc:trashbin-filename></d:prop>
<d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>'''
        self.assertEqual(probe.trash_entries(xml, "test-user"), {"file.txt": "/remote.php/dav/trashbin/test-user/trash/file.txt.d123"})
        self.assertEqual(probe.trash_entries(xml.replace(b"200 OK", b"404 Not Found"), "test-user"), {})
        with self.assertRaises(probe.ProbeFailure):
            probe.trash_entries(xml, "other-user")

    def test_cleanup_attempts_both_restores_and_user_deletion_after_a_failure(self):
        instance = probe.Probe.__new__(probe.Probe)
        instance.original = {"files.trash.delete": False, "trashbin_retention_obligation": "60,365"}
        instance.user_created = True
        instance.user = "temporary-user"
        instance.events = []
        attempts = []

        def set_config(key, value):
            attempts.append(key)
            if key == "files.trash.delete":
                raise probe.ProbeFailure("injected write failure")

        instance.set_config = set_config
        instance.occ = lambda *args: attempts.append(args)
        instance.command = lambda *args: b"false"
        with self.assertRaisesRegex(probe.ProbeFailure, "cleanup failed"):
            instance.cleanup()
        self.assertEqual(attempts, ["files.trash.delete", "trashbin_retention_obligation", ("user:delete", "temporary-user")])

    def test_assertion_failure_still_cleans_up(self):
        instance = probe.Probe.__new__(probe.Probe)
        calls = []
        instance.prepare = lambda: calls.append("prepare")
        instance.create_user = lambda: calls.append("create")
        instance.cleanup = lambda: calls.append("cleanup")

        def exercise():
            raise probe.ProbeFailure("injected HTTP assertion failure")

        instance.exercise = exercise
        with self.assertRaises(probe.ProbeFailure):
            instance.run()
        self.assertEqual(calls, ["prepare", "create", "cleanup"])

    def test_http_credentials_are_stdin_only(self):
        instance = probe.Probe.__new__(probe.Probe)
        instance.user, instance.password = "test-user", "synthetic-secret"
        instance.url, instance.resolve = "https://nc.test:9000", "nc.test:9000:127.0.0.1"
        calls = []
        instance.command = lambda args, data: (calls.append((args, data)) or b"ok\n200")
        self.assertEqual(instance.http("GET", "/status.php"), (200, b"ok"))
        self.assertNotIn(instance.password, " ".join(calls[0][0]))
        self.assertIn(instance.password.encode(), calls[0][1])

    def test_production_socket_is_rejected_before_docker_commands(self):
        instance = probe.Probe.__new__(probe.Probe)
        instance.command = lambda *args: self.fail("Docker must not be touched")
        with patch.dict(os.environ, {"ANAS_TEST_DOCKER_SOCKET": "/run/docker.sock", "DOCKER_HOST": "unix:///run/docker.sock"}):
            with self.assertRaises(probe.ProbeFailure):
                instance.prepare()

    def test_production_data_root_is_rejected(self):
        instance = probe.Probe.__new__(probe.Probe)
        instance.docker = "docker"
        calls = []
        instance.command = lambda *args: (calls.append(args) or b"/var/lib/docker\n")
        with patch.dict(os.environ, {"ANAS_TEST_DOCKER_SOCKET": "/run/anas-e2e.sock", "DOCKER_HOST": "unix:///run/anas-e2e.sock"}):
            with self.assertRaisesRegex(probe.ProbeFailure, "data root"):
                instance.prepare()
        self.assertEqual(len(calls), 1)

    def test_container_from_another_workspace_is_rejected(self):
        instance = probe.Probe.__new__(probe.Probe)
        instance.docker, instance.container = "docker", "anas_test_nextcloud"
        instance.workspace = Path("/tmp/own-workspace")
        metadata = [{"Config": {"Labels": {"com.docker.compose.project.working_dir": "/tmp/another-workspace/.anas/deployments/run/modules/nextcloud"}}}]
        calls = []

        def command(args):
            calls.append(args)
            return b"/tmp/anas-e2e-data\n" if args[1] == "info" else json.dumps(metadata).encode()

        instance.command = command
        with patch.dict(os.environ, {"ANAS_TEST_DOCKER_SOCKET": "/run/anas-e2e.sock", "DOCKER_HOST": "unix:///run/anas-e2e.sock"}):
            with self.assertRaisesRegex(probe.ProbeFailure, "workspace"):
                instance.prepare()
        self.assertEqual([args[1] for args in calls], ["info", "inspect"])

    def test_report_is_private_even_with_permissive_umask(self):
        with tempfile.TemporaryDirectory() as root:
            previous = os.umask(0)
            try:
                report = Path(probe.write_report(Path(root), {"passed": False, "failure": "injected failure"}))
            finally:
                os.umask(previous)
            self.assertEqual(report.stat().st_mode & 0o777, 0o600)
            self.assertNotIn("synthetic-secret", report.read_text())


if __name__ == "__main__":
    unittest.main()
