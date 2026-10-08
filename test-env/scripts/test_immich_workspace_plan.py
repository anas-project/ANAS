#!/usr/bin/env python3
"""Plan-evidence driver checks; no real-host acceptance claim."""
import argparse
import contextlib
import importlib.util
import io
import pathlib
import tempfile
import unittest
from unittest import mock

path = pathlib.Path(__file__).with_name("immich-workspace-e2e.py")
spec = importlib.util.spec_from_file_location("immich_workspace_plan", path)
harness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(harness)


class PlanEvidenceTests(unittest.TestCase):
    def fixture(self, directory):
        root = pathlib.Path(directory)
        for name in ("config.yml", ".anas/secrets.yml", ".anas/state/active.yml"):
            path = root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("same managed state")
        with mock.patch.dict(harness.os.environ, {"DOCKER_HOST": "unix:///run/anas-immich-test.sock"}):
            suite = harness.Suite(argparse.Namespace(), root, root / "modules")
        modules = ["samba_dc", "postgres", "authentik", "immich", "traefik"]
        reply = {"modules": modules, "module_plans": {"postgres": {
            "database_consumers": "authentik,immich",
            "extension_upgrade": "provider artifact changes require a full workspace stop, ANAS recovery point, and provider qualification before consumers start; stop/restart scope=" + ",".join(modules),
        }}}
        return suite, reply

    def test_real_cli_plan_records_consumers_and_complete_scope(self):
        with tempfile.TemporaryDirectory() as directory:
            suite, reply = self.fixture(directory)
            runtime = {("immich", "anas_immich"): {"id": "own-runtime", "image": "sha256:same"}}
            with mock.patch.object(suite, "runtime", return_value=runtime), mock.patch.object(suite, "anas", return_value=reply) as anas, contextlib.redirect_stdout(io.StringIO()):
                suite.verify_maintenance_plan("before-upgrade", suite.modules)
            anas.assert_called_once_with("before-upgrade", "plan", "-w", suite.workspace, "--module-root", suite.modules)
            self.assertEqual(suite.report["postgres_maintenance_plans"]["before-upgrade"], {
                "database_consumers": ["authentik", "immich"], "stop_restart_scope": reply["modules"], "managed_state_unchanged": True,
            })

    def test_missing_or_repeated_consumer_and_partial_stop_scope_are_rejected(self):
        for defect in ("missing-consumer", "duplicate-consumer", "partial-scope", "missing-barrier"):
            with self.subTest(defect=defect), tempfile.TemporaryDirectory() as directory:
                suite, reply = self.fixture(directory)
                provider = reply["module_plans"]["postgres"]
                if defect == "missing-consumer":
                    provider["database_consumers"] = "immich"
                elif defect == "duplicate-consumer":
                    provider["database_consumers"] += ",immich"
                elif defect == "partial-scope":
                    provider["extension_upgrade"] = provider["extension_upgrade"].replace(",traefik", "")
                else:
                    provider["extension_upgrade"] = "stop/restart scope=" + ",".join(reply["modules"])
                with mock.patch.object(suite, "runtime", return_value={}), mock.patch.object(suite, "anas", return_value=reply), self.assertRaises(AssertionError):
                    suite.verify_maintenance_plan("invalid-plan", suite.modules)
                self.assertNotIn("postgres_maintenance_plans", suite.report)

    def test_plan_that_mutates_private_managed_state_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            suite, reply = self.fixture(directory)
            def run_plan(*args):
                (suite.workspace / ".anas/secrets.yml").write_text("unexpected secret mutation")
                return reply
            with mock.patch.object(suite, "runtime", return_value={}), mock.patch.object(suite, "anas", side_effect=run_plan), self.assertRaisesRegex(AssertionError, "changed runtime or managed state"):
                suite.verify_maintenance_plan("invalid-plan", suite.modules)
            self.assertNotIn("postgres_maintenance_plans", suite.report)


if __name__ == "__main__":
    unittest.main()
