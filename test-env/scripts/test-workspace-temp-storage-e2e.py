#!/usr/bin/env python3
# TEST_CASES: TEMP-T-011
"""Counterexamples for remote probes; no SSH, Docker, or host mutations."""
import importlib.util
import copy
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("temp_suite", Path(__file__).with_name("server-workspace-temp-storage-e2e.py"))
suite = importlib.util.module_from_spec(spec)
spec.loader.exec_module(suite)


class ProbeCounterexamples(unittest.TestCase):
    @staticmethod
    def stop_payload(project):
        return {"ok": False, "error": {"code": "stop_failed", "detail": {
            "primary": {"code": "stop_failed", "message": "synthetic-stop-failure",
                        "command": {"phase": "down", "exit_code": 98, "project": project}},
            "recovery": [{"phase": "previous_restore", "status": "succeeded"}]}}}

    @staticmethod
    def local_suite(root):
        probe = suite.Suite.__new__(suite.Suite)
        probe.root = root
        probe.report_dir = root / "reports"
        probe.report_dir.mkdir(mode=0o700)
        probe.anas, probe.docker = "/synthetic/anas", "/synthetic/docker"
        probe.workspace = root / "workspace"
        probe.failure_detail = None
        probe.cleanup_state, probe.cleanup_errors = "not_attempted", []
        return probe

    def test_requires_exact_run_scope_and_dedicated_daemon(self):
        root = Path("/home/whl/anas-temp-storage-e2e/run-one")
        host = "unix:///run/anas-temp-e2e-one.sock"
        suite.require_isolation(root, "run-one", host, str(root / "docker"))
        variants = [
            (root, "run-one", "unix:///var/run/docker.sock", str(root / "docker")),
            (root, "run-one", host, "/var/lib/docker"),
            (root, "../run-one", host, str(root / "docker")),
            (Path("/home/whl/anas-temp-storage-e2e/../run-one"), "run-one", host, str(root / "docker")),
            (Path("/tmp/anas-temp-storage-e2e/run-one"), "run-one", host, str(root / "docker")),
        ]
        for values in variants:
            with self.subTest(values=values), self.assertRaises(suite.ProbeFailure):
                suite.require_isolation(*values)

    def test_container_recreation_requires_every_module(self):
        before = {"first": "old-a", "second": "old-b"}
        suite.require_changed(before, {"first": "new-a", "second": "new-b"}, tuple(before))
        for after in ({"first": "new-a", "second": "old-b"}, {"first": "new-a"}):
            with self.assertRaises(suite.ProbeFailure):
                suite.require_changed(before, after, tuple(before))

    def test_historical_rollback_invokes_supported_cli_and_proves_fresh_immutable_result(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            probe = object.__new__(suite.Suite)
            probe.workspace, probe.root_a = root / "workspace", root / "temp-a"
            artifact = probe.workspace / ".anas/deployments/history"
            artifact.mkdir(parents=True)
            original = artifact / "frozen"
            original.write_text("immutable")
            previous = [probe.root_a / ("old-" + str(index)) for index in range(3)]
            fresh = [probe.root_a / ("new-" + str(index)) for index in range(3)]
            for path in [*previous, *fresh]:
                path.mkdir(parents=True)
            before = suite.tree_digest(artifact)
            for changed in (None, "old-deployment", "old-paths", "changed-history"):
                with self.subTest(changed=changed):
                    original.write_text("immutable")
                    probe.cli = mock.Mock()
                    if changed == "changed-history":
                        probe.cli.side_effect = lambda *args: original.write_text("rewritten")
                    probe.active = mock.Mock(return_value="history" if changed == "old-deployment" else "fresh-deployment")
                    paths = previous if changed == "old-paths" else fresh
                    probe.inspect = mock.Mock(return_value={str(index): {"Mounts": [{"Destination": "/runtime", "Source": str(path)}]} for index, path in enumerate(paths)})
                    if changed:
                        with self.assertRaises(suite.ProbeFailure):
                            probe.rollback_history("history", previous, before, probe.root_a)
                    else:
                        self.assertEqual(probe.rollback_history("history", previous, before, probe.root_a), fresh)
                    probe.cli.assert_called_once_with("rollback", "history", "-w", str(probe.workspace), "--json")

    def test_mount_oracle_rejects_shared_or_wrong_root(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            paths = [root / str(i) for i in range(3)]
            for path in paths:
                path.mkdir()
            containers = {str(i): {"Mounts": [{"Destination": "/runtime", "Source": str(path)}]} for i, path in enumerate(paths)}
            self.assertEqual(suite.require_mounts(containers, root), paths)
            containers["1"]["Mounts"][0]["Source"] = str(paths[0])
            with self.assertRaises(suite.ProbeFailure):
                suite.require_mounts(containers, root)
            containers["1"]["Mounts"][0]["Source"] = "/outside"
            with self.assertRaises(suite.ProbeFailure):
                suite.require_mounts(containers, root)

    def test_dependency_order_must_match_real_events(self):
        def event(name, action):
            return {"Action": action, "Actor": {"Attributes": {"com.docker.compose.project": "prefix_" + name}}}
        events = [event("second", "destroy"), event("first", "destroy"), event("first", "start"), event("second", "start")]
        suite.require_event_order(events, "prefix_", ("first", "second"))
        for changed in (events[1:], list(reversed(events))):
            with self.assertRaises(suite.ProbeFailure):
                suite.require_event_order(changed, "prefix_", ("first", "second"))

    def test_stop_failure_requires_primary_and_independent_restore(self):
        with tempfile.TemporaryDirectory() as directory:
            marker = Path(directory) / "fault"
            marker.touch()
            project = "prefix_consumer"
            payload = self.stop_payload(project)
            suite.require_stop_failure(1, payload, marker, project)
            variants = []
            for key, value in (("code", "start_failed"), ("message", "recovery succeeded")):
                changed = copy.deepcopy(payload)
                changed["error"]["detail"]["primary"][key] = value
                variants.append(changed)
            for key, value in (("phase", "up"), ("exit_code", 0), ("project", "another_workspace")):
                changed = copy.deepcopy(payload)
                changed["error"]["detail"]["primary"]["command"][key] = value
                variants.append(changed)
            for recovery in ([], [{"phase": "candidate_stop", "status": "succeeded"}],
                             [{"phase": "previous_restore", "status": "failed"}],
                             [{"phase": "previous_restore", "status": "succeeded"}] * 2):
                changed = copy.deepcopy(payload)
                changed["error"]["detail"]["recovery"] = recovery
                variants.append(changed)
            for changed in variants:
                with self.subTest(changed=changed), self.assertRaises(suite.ProbeFailure):
                    suite.require_stop_failure(1, changed, marker, project)
            with self.assertRaises(suite.ProbeFailure):
                suite.require_stop_failure(0, payload, marker, project)
            marker.unlink()
            with self.assertRaises(suite.ProbeFailure):
                suite.require_stop_failure(1, payload, marker, project)

    def test_real_down_shim_fails_once_and_old_actual_mounts_must_survive(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            probe = self.local_suite(root)
            probe.root_a, probe.root_b, probe.prefix = root / "a", root / "b", "synthetic_"
            paths = [probe.root_b / str(i) for i in range(3)]
            for path in paths:
                path.mkdir(parents=True)
            containers = {str(i): {"State": {"Running": True}, "Mounts": [{"Destination": "/runtime", "Source": str(path)}]}
                          for i, path in enumerate(paths)}
            probe.inspect = lambda: containers
            probe.active = lambda: "old-deployment"
            real_docker = root / "fake-docker"
            real_docker.write_text("#!" + sys.executable + "\nimport sys\nsys.exit(0)\n")
            real_docker.chmod(0o700)
            probe.docker = str(real_docker)
            observed = []

            def switch(target, expected=0, env=None):
                if env is None:
                    return 0, b"", b""
                shim = root / "stop-fault-bin/docker"
                for arguments, wanted in ((["compose", "-p", "other_workspace", "down"], 0),
                                          (["compose", "-p", probe.prefix + "consumer", "up"], 0),
                                          (["compose", "-p", probe.prefix + "consumer", "down"], 98),
                                          (["compose", "--project-name", probe.prefix + "consumer", "down"], 0)):
                    result = subprocess.run([str(shim), *arguments], env=env, capture_output=True, timeout=10)
                    self.assertEqual(result.returncode, wanted)
                    observed.append(result)
                return 1, json.dumps(self.stop_payload(probe.prefix + "consumer")).encode(), b""

            probe.set_root = switch
            probe.fail_once_stop_switch()
            self.assertIn(b"synthetic-stop-failure", observed[2].stderr)
            self.assertTrue(all((path / "stop-failure-sentinel").read_text() == "preserve-after-down-failure" for path in paths))
            # Reporting success cannot compensate for wrong actual mount content.
            probe.root.joinpath("stop-fault-bin").rename(root / "previous-fault")
            probe.inspect = lambda: {**containers, "0": {"State": {"Running": False}, "Mounts": containers["0"]["Mounts"]}}
            with self.assertRaises(suite.ProbeFailure):
                probe.fail_once_stop_switch()

    def test_host_mount_requires_explicit_blocker_owner_and_preserved_content(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            lease, marker = root / "lease", root / "outside-source-sentinel"
            lease.mkdir()
            owner = lease / ".anas-temp-owner.yml"
            owner.touch()
            marker.write_text("preserve-host-mount")
            status = {"directories": [{"path": str(lease), "blockers": ["host_mount_reference"], "reclaimable": False}]}
            suite.require_host_mount_retained(status, lease, marker)
            for change in ({"blockers": []}, {"blockers": ["container_reference:old"]}, {"reclaimable": True}, {"path": str(root / "wrong")}):
                changed = copy.deepcopy(status)
                changed["directories"][0].update(change)
                with self.assertRaises(suite.ProbeFailure):
                    suite.require_host_mount_retained(changed, lease, marker)
            marker.write_text("altered")
            with self.assertRaises(suite.ProbeFailure):
                suite.require_host_mount_retained(status, lease, marker)
            marker.write_text("preserve-host-mount")
            owner.unlink()
            with self.assertRaises(suite.ProbeFailure):
                suite.require_host_mount_retained(status, lease, marker)

    def test_host_stop_accepts_only_the_specific_release_guard(self):
        message = "release producer temporary storage: temporary module producer still has references: [host_mount_reference]"
        payload = {"ok": False, "error": {"code": "stop_failed", "message": message}}
        suite.require_host_stop_refusal(1, payload, "producer")
        for code, response in (
            (0, payload), (1, {}), (1, {"ok": True, "error": payload["error"]}),
            (1, {"ok": False, "error": {"code": "usage", "message": message}}),
            (1, {"ok": False, "error": {"code": "stop_failed", "message": message.replace("producer", "consumer")}}),
            (1, {"ok": False, "error": {"code": "stop_failed", "message": message + "\nstop consumer: failure"}}),
            (1, {"ok": False, "error": {"code": "stop_failed", "message": "Docker query failed: host_mount_reference"}}),
        ):
            with self.subTest(code=code, response=response), self.assertRaises(suite.ProbeFailure):
                suite.require_host_stop_refusal(code, response, "producer")
    def test_same_filesystem_mount_oracle_uses_mountinfo_and_exact_inode(self):
        raw = "12 1 0:3 /source /run/with\\040space\\134slash rw - btrfs /dev/x rw\n"
        with mock.patch.object(Path, "read_text", return_value=raw):
            self.assertEqual(suite.mount_points(), {Path("/run/with space\\slash")})
        source, target = Path("/synthetic/source"), Path("/synthetic/target")
        with mock.patch.object(suite, "mount_points", return_value={target}), mock.patch.object(Path, "stat", return_value=mock.Mock(st_dev=2, st_ino=42)):
            suite.require_bind_mount(source, target)
        with mock.patch.object(suite, "mount_points", return_value=set()):
            with self.assertRaises(suite.ProbeFailure):
                suite.require_bind_mount(source, target)
        with mock.patch.object(suite, "mount_points", return_value={target}), mock.patch.object(Path, "stat", side_effect=[mock.Mock(st_dev=2, st_ino=42)] * 2 + [mock.Mock(st_dev=2, st_ino=43)] * 2):
            with self.assertRaises(suite.ProbeFailure):
                suite.require_bind_mount(source, target)

    def test_host_probe_requires_empty_daemon_and_unmount_before_reclamation(self):
        for inventory in (b"", b"remaining-container\n"):
            with self.subTest(inventory=inventory), tempfile.TemporaryDirectory() as directory:
                root = Path(directory).resolve()
                probe = self.local_suite(root)
                probe.workspace, probe.host_bind_mounts = root / "workspace", set()
                leases = [root / "leases" / str(i) for i in range(3)]
                for lease in leases:
                    lease.mkdir(parents=True)
                    (lease / ".anas-temp-owner.yml").touch()
                target, source = leases[0] / "remaining-host-bind", root / "core-host-bind-source"
                mounted, calls, released = set(), [], []

                def command(arguments):
                    calls.append(arguments)
                    if "mount" in arguments:
                        mounted.add(target)
                        (target / "host-bind-sentinel").write_text("preserve-host-mount")
                    elif "umount" in arguments:
                        mounted.remove(target)
                    elif "ps" in arguments:
                        return 0, inventory, b""
                    return 0, b"", b""

                def cli(*arguments, **kwargs):
                    calls.append(list(arguments))
                    if arguments[0] == "stop":
                        if mounted:
                            self.assertIsNone(kwargs.get("expected"))
                            return 1, json.dumps({"ok": False, "error": {"code": "stop_failed", "message": "release producer temporary storage: temporary module producer still has references: [host_mount_reference]"}}).encode(), b""
                        released.append(True)
                    if arguments[:2] == ("temp", "gc") and not mounted:
                        self.assertTrue(released)
                        for lease in leases:
                            shutil.rmtree(lease)
                    return 0, b"", b""

                probe.command, probe.cli = command, cli
                probe.status = lambda: {"directories": [{"path": str(leases[0]), "state": "released" if released else "active", "blockers": [] if released else ["host_mount_reference"], "reclaimable": bool(released)}]}
                with mock.patch.object(suite, "private_mount_helpers") as guard, mock.patch.object(suite, "require_bind_mount") as bind, mock.patch.object(suite, "mount_points", side_effect=lambda: set(mounted)):
                    if inventory:
                        with self.assertRaises(suite.ProbeFailure):
                            probe.host_mount_reference_gc(leases)
                        self.assertTrue(leases[0].exists())
                        self.assertIn(target, mounted)
                        self.assertFalse(any(arguments[:2] == ["temp", "gc"] for arguments in calls))
                        self.assertEqual(guard.call_count, 1)
                    else:
                        probe.host_mount_reference_gc(leases)
                        self.assertFalse(leases[0].exists())
                        self.assertEqual((source / "host-bind-sentinel").read_text(), "preserve-host-mount")
                        self.assertEqual(mounted, set())
                        self.assertEqual(probe.host_bind_mounts, set())
                        self.assertEqual(guard.call_count, 2)
                        self.assertEqual(bind.call_count, 2)
                        stops = [index for index, arguments in enumerate(calls) if arguments[0] == "stop"]
                        unmount = next(index for index, arguments in enumerate(calls) if "umount" in arguments)
                        self.assertEqual(len(stops), 2)
                        self.assertLess(stops[0], unmount)
                        self.assertGreater(stops[1], unmount)

    def test_private_diagnostics_keep_timeout_and_malformed_cli_error(self):
        with tempfile.TemporaryDirectory() as directory:
            probe = self.local_suite(Path(directory).resolve())
            with mock.patch.object(suite.subprocess, "run", side_effect=subprocess.TimeoutExpired(["raw argv"], 4, output=b"partial raw", stderr=b"partial stderr")):
                with self.assertRaises(suite.ProbeFailure) as failure:
                    probe.command([probe.anas, "apply"], timeout=4)
            self.assertEqual(failure.exception.code, "external_command_timeout")
            private = json.loads(next((probe.report_dir / "private-cli").glob("*.json")).read_text())
            self.assertEqual(private["stdout"], "partial raw")
            self.assertNotIn("partial", json.dumps(suite.public_error(failure.exception)))
            raw = json.dumps({"error": {"code": {"untrusted": "raw"}}}).encode()
            with mock.patch.object(suite.subprocess, "run", return_value=subprocess.CompletedProcess([], 9, raw, b"")):
                with self.assertRaises(suite.ProbeFailure):
                    probe.command([probe.anas, "apply"])
            self.assertEqual(probe.failure_detail["error_code"], "command_failed")
    def test_private_diagnostics_preserve_raw_failure_but_public_report_is_allowlisted(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            probe = self.local_suite(root)
            raw = b'{"error":{"code":"unexpected raw sensitive value"},"synthetic":"private raw output"}'
            with mock.patch.object(suite.subprocess, "run", return_value=subprocess.CompletedProcess([], 7, raw, b"private stderr")):
                with self.assertRaises(suite.ProbeFailure) as failure:
                    probe.command([probe.anas, "apply", "private argument"])
            files = list((probe.report_dir / "private-cli").glob("*.json"))
            self.assertEqual(len(files), 1)
            self.assertEqual(files[0].stat().st_mode & 0o777, 0o600)
            self.assertEqual(files[0].parent.stat().st_mode & 0o777, 0o700)
            private = json.loads(files[0].read_text())
            self.assertEqual(private["stdout"], raw.decode())
            self.assertEqual(private["argv"][-1], "private argument")
            public = json.dumps({"error": suite.public_error(failure.exception), "detail": probe.failure_detail})
            for text in ("private raw output", "private stderr", "private argument", "unexpected raw sensitive value"):
                self.assertNotIn(text, public)
            self.assertEqual(probe.failure_detail["error_code"], "command_failed")
            for malformed in ({"code": {}}, {"code": "private message"}, {"message": "private message"}):
                self.assertEqual(suite.public_error(malformed), suite.public_error(RuntimeError("private message")))

    def test_diagnostic_scope_refuses_symlink_and_write_failure_preserves_primary(self):
        with tempfile.TemporaryDirectory() as directory, tempfile.TemporaryDirectory() as outside:
            root = Path(directory).resolve()
            probe = self.local_suite(root)
            (probe.report_dir / "private-cli").symlink_to(outside, target_is_directory=True)
            with self.assertRaises(suite.ProbeFailure):
                probe.private_diagnostic("command-failure", {"raw": "private"})
            self.assertEqual(list(Path(outside).iterdir()), [])
            with mock.patch.object(suite.subprocess, "run", return_value=subprocess.CompletedProcess([], 98, b"raw", b"stderr")):
                with self.assertRaises(suite.ProbeFailure) as failure:
                    probe.command([probe.anas, "apply"])
            self.assertEqual(failure.exception.code, "external_command_failed")
            self.assertTrue(probe.diagnostics_failed)

    def test_failed_scoped_stop_continues_and_reports_cleanup_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            probe = self.local_suite(Path(directory).resolve())
            probe.prefix = "synthetic_"
            calls = []

            def stop_owned(prefix, module, workspace):
                calls.append(prefix + module)
                self.assertEqual(workspace, probe.workspace)
                if module == "consumer":
                    raise RuntimeError("synthetic private stop exception")
                if module == "passive":
                    raise suite.ProbeFailure("synthetic verified stop failure")

            probe.stop_owned_container = stop_owned
            with self.assertRaises(suite.ProbeFailure):
                probe.stop_for_inspection()
            self.assertEqual(calls, [probe.prefix + name for name in reversed(probe.modules)])
            self.assertEqual(probe.cleanup_state, "failed")
            self.assertEqual(len(probe.cleanup_errors), 2)
            self.assertNotIn("private", json.dumps(probe.cleanup_errors))

    @staticmethod
    def cleanup_container(probe, running=True):
        return {"Id": "a" * 64, "Name": "/owned_producer", "State": {"Running": running}, "Config": {"Labels": {
            "com.docker.compose.project": "owned_producer",
            "com.docker.compose.project.working_dir": str(probe.workspace / ".anas/deployments/frozen/modules/producer")}}}

    def test_inspection_cleanup_skips_only_proven_absence_and_stopped_owned_containers(self):
        with tempfile.TemporaryDirectory() as directory:
            probe = self.local_suite(Path(directory).resolve())
            probe.command, probe.docker_json = mock.Mock(return_value=(0, b"", b"")), mock.Mock()
            probe.stop_owned_container("owned_", "producer", probe.workspace)
            probe.docker_json.assert_not_called()
            probe.command.assert_called_once_with([probe.docker, "ps", "-aq", "--no-trunc", "--filter", "name=^/owned_producer$"])
            probe.command = mock.Mock(return_value=(0, ("a" * 64).encode(), b""))
            probe.docker_json = mock.Mock(return_value=[self.cleanup_container(probe, False)])
            probe.stop_owned_container("owned_", "producer", probe.workspace)
            self.assertEqual(probe.command.call_count, 1)
            probe.docker_json.assert_called_once_with("inspect", "a" * 64)

    def test_inspection_cleanup_rejects_unknown_inventory_and_foreign_owner_before_stop(self):
        with tempfile.TemporaryDirectory() as directory:
            probe = self.local_suite(Path(directory).resolve())
            for code, body in ((1, b""), (0, b"short-id"), (0, (("a" * 64) + "\n" + ("b" * 64)).encode())):
                with self.subTest(code=code, body=body):
                    probe.command, probe.docker_json = mock.Mock(return_value=(code, body, b"")), mock.Mock()
                    with self.assertRaises(suite.ProbeFailure):
                        probe.stop_owned_container("owned_", "producer", probe.workspace)
                    probe.docker_json.assert_not_called()
                    self.assertEqual(probe.command.call_count, 1)
            original = self.cleanup_container(probe)
            for field in ("id", "name", "project", "workspace", "module", "state"):
                changed = copy.deepcopy(original)
                if field == "id": changed["Id"] = "b" * 64
                if field == "name": changed["Name"] = "/foreign_producer"
                if field == "project": changed["Config"]["Labels"]["com.docker.compose.project"] = "foreign_producer"
                if field == "workspace": changed["Config"]["Labels"]["com.docker.compose.project.working_dir"] = str(probe.root / "other/.anas/deployments/frozen/modules/producer")
                if field == "module": changed["Config"]["Labels"]["com.docker.compose.project.working_dir"] = str(probe.workspace / ".anas/deployments/frozen/modules/consumer")
                if field == "state": changed["State"]["Running"] = None
                with self.subTest(field=field):
                    probe.command = mock.Mock(return_value=(0, ("a" * 64).encode(), b""))
                    probe.docker_json = mock.Mock(return_value=[changed])
                    with self.assertRaises(suite.ProbeFailure):
                        probe.stop_owned_container("owned_", "producer", probe.workspace)
                    self.assertEqual(probe.command.call_count, 1)

    def test_inspection_cleanup_stops_by_verified_id_and_checks_release_or_stopped_state(self):
        with tempfile.TemporaryDirectory() as directory:
            probe = self.local_suite(Path(directory).resolve())
            identity = "a" * 64
            for stop_code, query_code, remaining, final_running, accepted in (
                (0, 0, identity.encode(), False, True), (3, 0, b"", True, True),
                (3, 0, identity.encode(), True, False), (0, 1, b"", False, False),
                (0, 0, identity.encode(), True, False),
            ):
                with self.subTest(stop_code=stop_code, query_code=query_code, remaining=remaining, final_running=final_running):
                    probe.command = mock.Mock(side_effect=[(0, identity.encode(), b""), (stop_code, b"", b""), (query_code, remaining, b"")])
                    probe.docker_json = mock.Mock(side_effect=[[self.cleanup_container(probe, True)], [self.cleanup_container(probe, final_running)]])
                    if accepted:
                        probe.stop_owned_container("owned_", "producer", probe.workspace)
                    else:
                        with self.assertRaises(suite.ProbeFailure):
                            probe.stop_owned_container("owned_", "producer", probe.workspace)
                    self.assertEqual(probe.command.call_args_list[1], mock.call([probe.docker, "stop", identity], expected=None))

    def test_fixture_health_probes_succeed_except_passive(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            probe = self.local_suite(root)
            probe.fixture, probe.workspace = root / "fixture", root / "workspace"
            probe.root_a, probe.root_b, probe.prefix = root / "a", root / "b", "synthetic_"
            probe.cli = mock.Mock()
            probe.apply = mock.Mock()
            probe.inspect = lambda: {name: {"State": {"Running": True}} for name in probe.modules}
            probe.prepare()
            for name in probe.modules:
                compose = (probe.fixture / "modules" / name / "docker-compose.yml").read_text()
                self.assertIn('test: ["CMD", "' + ("false" if name == "passive" else "true") + '"]', compose)
                manifest = (probe.fixture / "modules" / name / "module.yml").read_text()
                self.assertIn("status: release", manifest)
                self.assertIn("anas.module-hook/v1", manifest)


if __name__ == "__main__":
    unittest.main()
