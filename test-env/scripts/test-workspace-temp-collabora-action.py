#!/usr/bin/env python3
# TEST_CASES: TEMP-T-023
"""Counterexamples for the fixed browser action boundary; no host operations."""
import importlib.util
import io
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import Mock, patch

_spec = importlib.util.spec_from_file_location("collabora_actions", Path(__file__).with_name("server-workspace-temp-collabora-action.py"))
actions = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(actions)


class FixedActionBoundary(unittest.TestCase):
    def runner(self, directory):
        runner = object.__new__(actions.Actions)
        runner.report = Path(directory)
        runner.run_id, runner.source_digest, runner.binary_digest = "synthetic-only", "sha256:" + "a" * 64, "sha256:" + "b" * 64
        runner.anas, runner.workspace, runner.prefix = Path(directory) / "anas", Path(directory) / "workspace", "owned_edit_"
        runner.failure = None
        return runner

    def test_protocol_rejects_commands_extra_parameters_and_shutdown(self):
        for action in actions.ACTIONS:
            self.assertEqual(actions.fixed_action({"action": action}), action)
        for message in ({"action": "shutdown"}, {"action": "stop-start", "command": "arbitrary"},
                        {"action": "sh -c arbitrary"}, {"action": ["stop-start"]}, ["stop-start"], {}):
            with self.subTest(message=message), self.assertRaises(actions.core.ProbeFailure):
                actions.fixed_action(message)

    def test_messages_cannot_smuggle_second_request_or_exceed_limit(self):
        for body in (b'{"action":"stop-start"}\n{"action":"rebuild"}\n', b"x" * 1025,
                     b'{"action":"stop-start"}\ntrailing-data'):
            sender, receiver = socket.socketpair()
            try:
                sender.sendall(body)
                with self.assertRaises(actions.core.ProbeFailure):
                    actions.receive(receiver)
            finally:
                sender.close()
                receiver.close()
        sender, receiver = socket.socketpair()
        try:
            sender.sendall(json.dumps({"action": "rebuild"}).encode() + b"\n")
            self.assertEqual(actions.receive(receiver), {"action": "rebuild"})
        finally:
            sender.close()
            receiver.close()

    def test_scoped_paths_reject_parent_escape_and_symlink(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            self.assertEqual(actions.scoped_path(root, root / "fixture"), root / "fixture")
            for path in (root, root.parent / "outside", root / ".." / "outside", Path("relative")):
                with self.subTest(path=path), self.assertRaises(actions.core.ProbeFailure):
                    actions.scoped_path(root, path)
            link = root / "link"
            link.symlink_to(root.parent, target_is_directory=True)
            with self.assertRaises(actions.core.ProbeFailure):
                actions.scoped_path(root, link / "socket")

    def test_client_socket_cannot_be_redirected_to_another_run(self):
        root = "/home/whl/anas-temp-storage-e2e/browser-test"
        # Validate the configured socket shape without depending on macOS's
        # /home automount alias; symlink behavior has its own real-disk case.
        with patch.dict(os.environ, {"ANAS_TEST_WORK_ROOT": root, "ANAS_TEST_RUN_ID": "browser-test"}, clear=True), \
                patch.object(Path, "resolve", lambda path: path):
            self.assertEqual(actions.action_socket_path(), Path(root) / "collabora-actions.sock")
            for value in ("/var/run/docker.sock", "/home/whl/anas-temp-storage-e2e/other/collabora-actions.sock",
                          root + "/../other/collabora-actions.sock"):
                with patch.dict(os.environ, {"ANAS_TEST_COLLABORA_ACTION_SOCKET": value}), self.assertRaises(actions.core.ProbeFailure):
                    actions.action_socket_path()

    def test_cli_action_failure_writes_bound_private_receipt_and_never_repeats(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner = self.runner(temporary)
            secret = "synthetic-password"
            document = {"api_version": "anas.dev/cli/v1", "ok": False, "error": {"code": "stop_failed", "message": secret,
                        "detail": {"primary": {"code": "stop_failed", "message": secret,
                                   "command": {"phase": "down", "exit_code": 98, "project": runner.prefix + "collabora", "summary": secret, "argv": [secret]}},
                                   "recovery": [{"phase": "previous_restore", "status": "failed", "message": secret}]}}}
            runner.anas.write_text("#!" + sys.executable + "\nimport sys\nsys.stdout.write(" + repr(json.dumps(document)) + ")\n"
                                   "sys.stderr.write(" + repr(secret) + ")\nsys.exit(1)\n")
            runner.anas.chmod(0o700)
            runner.run_action = Mock(side_effect=lambda *_: runner.cli("stop", "collabora"))
            with self.assertRaises(actions.core.ProbeFailure):
                runner.run("stop-start")
            path = runner.report / "collabora-lifecycle-stop-start.json"
            original = path.read_bytes()
            report = json.loads(original)
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            self.assertEqual({key: report[key] for key in ("run_id", "source_digest", "binary_digest", "action", "status")},
                             {"run_id": runner.run_id, "source_digest": runner.source_digest, "binary_digest": runner.binary_digest, "action": "stop-start", "status": "failed"})
            self.assertEqual(report["failure"], {"operation": "normal_stop_collabora", "exit_code": 1, "error": {"code": "stop_failed", "detail": {
                             "primary": {"code": "stop_failed", "command": {"phase": "down", "exit_code": 98, "module": "collabora"}},
                             "recovery": [{"phase": "previous_restore", "status": "failed"}]}}})
            self.assertNotIn(secret, original.decode())
            self.assertNotIn(runner.prefix, original.decode())
            with self.assertRaises(actions.core.ProbeFailure):
                runner.run("stop-start")
            self.assertEqual(runner.run_action.call_count, 1)
            self.assertEqual(path.read_bytes(), original)

    def test_malformed_or_unknown_cli_failure_never_forges_a_cause(self):
        for raw in (b"synthetic-password", b"\xff", b"[]", b"[" * 2000 + b"]" * 2000,
                    b'{"api_version":"anas.dev/cli/v1","ok":false,"error":{"code":"synthetic-password"}}'):
            with self.subTest(raw=raw), tempfile.TemporaryDirectory() as temporary:
                runner = self.runner(temporary)
                runner.run_action = Mock(side_effect=lambda *_: runner.cli("apply"))
                failed = subprocess.CompletedProcess([str(runner.anas)], 98, stdout=raw, stderr=b"synthetic-password")
                with patch.object(actions.subprocess, "run", return_value=failed), self.assertRaises(actions.core.ProbeFailure):
                    runner.run("switch-a-to-b")
                report = json.loads((runner.report / "collabora-lifecycle-switch-a-to-b.json").read_text())
                self.assertEqual(report["failure"], {"operation": "switch_temporary_root", "exit_code": 98})
                self.assertNotIn("synthetic-password", json.dumps(report))

    def test_non_cli_action_exception_and_receipt_write_failure_preserve_first_cause(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner = self.runner(temporary)
            cause = ValueError("synthetic-password")
            runner.run_action = Mock(side_effect=cause)
            with self.assertRaises(ValueError) as raised:
                runner.run("rebuild")
            self.assertIs(raised.exception, cause)
            report = json.loads((runner.report / "collabora-lifecycle-rebuild.json").read_text())
            self.assertEqual(report["failure"], {"operation": "validate_lifecycle_action", "reason": "ValueError"})
            self.assertNotIn("synthetic-password", json.dumps(report))
            runner.write_report = Mock(side_effect=PermissionError("other-secret"))
            with self.assertRaises(ValueError) as raised:
                runner.run("stop-start")
            self.assertIs(raised.exception, cause)
            self.assertEqual(runner.failure["receipt_write"], "failed")

    def test_broken_receipt_symlink_cannot_repeat_an_action_or_write_outside_report(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner = self.runner(temporary)
            (runner.report / "collabora-lifecycle-rebuild.json").symlink_to(runner.report / "absent")
            runner.run_action = Mock()
            with self.assertRaises(actions.core.ProbeFailure):
                runner.run("rebuild")
            runner.run_action.assert_not_called()

    def ready_fixture(self, directory):
        runner = self.runner(directory)
        runner.root, runner.docker = Path(directory).resolve(), "docker"
        runner.workspace = runner.root / "workspace"
        source = runner.root / "temp/lease"
        source.mkdir(parents=True)
        for name in ("systemplate", "office", "child-roots", "cache"):
            (source / name).mkdir()
        (source / ".templates-version").write_text("26.04.2.4.1\n")
        (source / ".anas-temp-owner.yml").write_bytes(b"synthetic-owner-marker")
        archive = io.BytesIO()
        with tarfile.open(fileobj=archive, mode="w") as output:
            entry = tarfile.TarInfo(".anas-temp-owner.yml")
            entry.size = len(b"synthetic-owner-marker")
            output.addfile(entry, io.BytesIO(b"synthetic-owner-marker"))
        item = {"Id": "c" * 64, "RestartCount": 0, "State": {"Running": True, "Restarting": False,
                "OOMKilled": False, "Pid": 12345, "Health": {"Status": "starting"}},
                "Mounts": [{"Destination": actions.TARGET, "Source": str(source), "Type": "bind", "RW": True}]}
        inventory = {(runner.prefix + "collabora", "anas_collabora"): item}
        runner.inventory = Mock(return_value=inventory)
        runner.cli = Mock(return_value=json.dumps({"directories": [{"module": "collabora", "name": "runtime",
                            "state": "active", "path": str(source)}]}).encode())
        runner.command = Mock(return_value=(0, archive.getvalue()))
        runner.discovery_ready = Mock(return_value=True)
        runner.office_ready = Mock(return_value=True)
        return runner, inventory, source

    def process_reads(self, commands, uid="1001"):
        original_bytes, original_text = Path.read_bytes, Path.read_text
        values = iter(commands)
        def read_bytes(path):
            return next(values) if str(path) == "/proc/12345/cmdline" else original_bytes(path)
        def read_text(path, *args, **kwargs):
            if str(path) == "/proc/12345/status":
                return "Uid:\t" + "\t".join([uid] * 4) + "\nGid:\t1001\t1001\t1001\t1001\n"
            return original_text(path, *args, **kwargs)
        return patch.object(Path, "read_bytes", read_bytes), patch.object(Path, "read_text", read_text)

    def test_ready_wait_allows_exact_wrapper_then_proves_actual_mount_and_discovery(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner, inventory, source = self.ready_fixture(temporary)
            runner.discovery_ready = Mock(side_effect=[False, True])
            reads = self.process_reads([b"/usr/local/bin/anas-collabora-start\0--container-start\0",
                                       b"/usr/bin/coolwsd\0", b"/usr/bin/coolwsd\0", b"/usr/bin/coolwsd\0", b"/usr/bin/coolwsd\0"])
            with reads[0], reads[1], patch.object(actions.time, "sleep") as pause:
                observed, mounted = runner.wait_ready(source.parent, inventory)
            self.assertIs(observed, inventory)
            self.assertEqual(mounted, source)
            self.assertEqual(pause.call_count, 2)
            self.assertEqual(runner.discovery_ready.call_count, 2)
            self.assertEqual(runner.cli.call_count, 2)
            self.assertEqual(runner.command.call_count, 2)
            self.assertIsNone(runner.failure)

    def test_ready_wait_rejects_oom_exit_restart_and_unhealthy_immediately(self):
        variants = [("container_oom", {"OOMKilled": True}), ("container_exited", {"Running": False}),
                    ("container_exited", {"Restarting": True}), ("container_unhealthy", {"Health": {"Status": "unhealthy"}})]
        for reason, state in variants:
            with self.subTest(reason=reason, state=state), tempfile.TemporaryDirectory() as temporary:
                runner, inventory, source = self.ready_fixture(temporary)
                actions.collabora_container(inventory)["State"].update(state)
                with patch.object(Path, "read_bytes") as process, patch.object(actions.time, "sleep") as pause, \
                        self.assertRaises(actions.core.ProbeFailure):
                    runner.wait_ready(source.parent, inventory)
                self.assertEqual(runner.failure["reason"], reason)
                process.assert_not_called()
                pause.assert_not_called()

    def test_ready_wait_never_follows_replaced_container_or_restarted_process(self):
        for field, value, reason in (("Id", "d" * 64, "container_replaced"), ("Pid", 12346, "container_restarted"),
                                     ("RestartCount", 1, "container_restarted")):
            with self.subTest(field=field), tempfile.TemporaryDirectory() as temporary:
                runner, inventory, source = self.ready_fixture(temporary)
                changed = json.loads(json.dumps(actions.collabora_container(inventory)))
                (changed["State"] if field == "Pid" else changed)[field] = value
                runner.inventory.return_value = {(runner.prefix + "collabora", "anas_collabora"): changed}
                with patch.object(Path, "read_bytes", return_value=b"/usr/local/bin/anas-collabora-start\0--container-start\0"), \
                        patch.object(actions.time, "sleep") as pause, self.assertRaises(actions.core.ProbeFailure):
                    runner.wait_ready(source.parent, inventory)
                self.assertEqual(runner.failure["reason"], reason)
                self.assertEqual(pause.call_count, 1)
                runner.discovery_ready.assert_not_called()

    def test_ready_wait_rejects_other_entrypoints_without_recording_arguments(self):
        for command in (b"/bin/sh\0synthetic-password\0", b"/usr/local/bin/anas-collabora-start\0unknown\0"):
            with self.subTest(command=command), tempfile.TemporaryDirectory() as temporary:
                runner, inventory, source = self.ready_fixture(temporary)
                with patch.object(Path, "read_bytes", return_value=command), patch.object(actions.time, "sleep") as pause, \
                        self.assertRaises(actions.core.ProbeFailure) as raised:
                    runner.wait_ready(source.parent, inventory)
                pause.assert_not_called()
                self.assertEqual(runner.failure["reason"], "unexpected_entrypoint")
                self.assertNotIn("synthetic-password", str(raised.exception) + json.dumps(runner.failure))

    def test_ready_wait_preserves_uid_lease_bind_and_marker_rejection(self):
        for invalid in ("uid", "lease", "bind", "marker"):
            with self.subTest(invalid=invalid), tempfile.TemporaryDirectory() as temporary:
                runner, inventory, source = self.ready_fixture(temporary)
                if invalid == "lease":
                    runner.cli.return_value = b'{"directories":[]}'
                elif invalid == "bind":
                    actions.collabora_container(inventory)["Mounts"][0]["RW"] = False
                elif invalid == "marker":
                    (source / ".anas-temp-owner.yml").write_bytes(b"different-owned-marker")
                reads = self.process_reads([b"/usr/bin/coolwsd\0", b"/usr/bin/coolwsd\0"], uid="1000" if invalid == "uid" else "1001")
                with reads[0], reads[1], patch.object(actions.time, "sleep") as pause, self.assertRaises(actions.core.ProbeFailure):
                    runner.wait_ready(source.parent, inventory)
                self.assertEqual(runner.failure["reason"], "invalid_temporary_mount")
                pause.assert_not_called()
                runner.discovery_ready.assert_not_called()

    def test_ready_wait_rejects_wrong_template_version_symlink_and_cross_filesystem(self):
        for invalid in ("version", "symlink", "filesystem"):
            with self.subTest(invalid=invalid), tempfile.TemporaryDirectory() as temporary:
                runner, inventory, source = self.ready_fixture(temporary)
                if invalid == "version":
                    (source / ".templates-version").write_text("synthetic-password")
                elif invalid == "symlink":
                    (source / "office").rmdir()
                    (source / "office").symlink_to(source / "systemplate", target_is_directory=True)
                original_stat = Path.stat
                def altered_stat(path, *args, **kwargs):
                    value = original_stat(path, *args, **kwargs)
                    if invalid == "filesystem" and path == source / "office":
                        fields = list(value)
                        fields[2] += 1
                        return os.stat_result(fields)
                    return value
                reads = self.process_reads([b"/usr/bin/coolwsd\0", b"/usr/bin/coolwsd\0"])
                with reads[0], reads[1], patch.object(Path, "stat", altered_stat), \
                        patch.object(actions.time, "sleep") as pause, self.assertRaises(actions.core.ProbeFailure) as raised:
                    runner.wait_ready(source.parent, inventory)
                self.assertEqual(runner.failure["reason"], "invalid_templates")
                self.assertNotIn("synthetic-password", str(raised.exception) + json.dumps(runner.failure))
                pause.assert_not_called()
                runner.discovery_ready.assert_not_called()

    def test_ready_wait_rejects_docker_query_failures_instead_of_retrying(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner, inventory, source = self.ready_fixture(temporary)
            cause = actions.core.ProbeFailure("fixed Docker query failure")
            runner.inventory = Mock(side_effect=cause)
            with patch.object(actions.time, "sleep") as pause, self.assertRaises(actions.core.ProbeFailure) as raised:
                runner.wait_ready(source.parent)
            self.assertIs(raised.exception, cause)
            self.assertEqual(runner.inventory.call_count, 1)
            pause.assert_not_called()

    def test_ready_wait_rejects_unknown_runtime_state_without_claiming_oom(self):
        for state in ({}, {"OOMKilled": "synthetic-password", "Running": True, "Restarting": False},
                      {"OOMKilled": False, "Running": True, "Restarting": False, "Health": {"Status": "unknown"}}):
            with self.subTest(state=state), tempfile.TemporaryDirectory() as temporary:
                runner, inventory, source = self.ready_fixture(temporary)
                actions.collabora_container(inventory)["State"] = state
                with patch.object(actions.time, "sleep") as pause, self.assertRaises(actions.core.ProbeFailure) as raised:
                    runner.wait_ready(source.parent, inventory)
                self.assertEqual(runner.failure["reason"], "invalid_runtime_state")
                self.assertNotIn("synthetic-password", str(raised.exception) + json.dumps(runner.failure))
                pause.assert_not_called()

    def test_ready_wait_does_not_retry_final_mount_failure_or_late_docker_query_failure(self):
        for stage in ("mount", "query"):
            with self.subTest(stage=stage), tempfile.TemporaryDirectory() as temporary:
                runner, inventory, source = self.ready_fixture(temporary)
                cause = actions.core.ProbeFailure("fixed validation failure")
                if stage == "mount":
                    runner.mounted_path = Mock(side_effect=cause)
                else:
                    runner.inventory = Mock(side_effect=cause)
                reads = self.process_reads([b"/usr/bin/coolwsd\0", b"/usr/bin/coolwsd\0"])
                with reads[0], reads[1], patch.object(actions.time, "sleep") as pause, \
                        self.assertRaises(actions.core.ProbeFailure) as raised:
                    runner.wait_ready(source.parent, inventory)
                self.assertIs(raised.exception, cause)
                pause.assert_not_called()
                if stage == "mount":
                    self.assertEqual(runner.mounted_path.call_count, 1)
                    runner.discovery_ready.assert_not_called()
                else:
                    self.assertEqual(runner.inventory.call_count, 1)

    def test_ready_wait_rechecks_current_identity_after_discovery_before_success(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner, inventory, source = self.ready_fixture(temporary)
            changed = {**actions.collabora_container(inventory), "Id": "d" * 64}
            runner.inventory.return_value = {(runner.prefix + "collabora", "anas_collabora"): changed}
            reads = self.process_reads([b"/usr/bin/coolwsd\0", b"/usr/bin/coolwsd\0"])
            with reads[0], reads[1], patch.object(actions.time, "sleep") as pause, self.assertRaises(actions.core.ProbeFailure):
                runner.wait_ready(source.parent, inventory)
            self.assertEqual(runner.failure["reason"], "container_replaced")
            runner.discovery_ready.assert_called_once()
            pause.assert_not_called()

    def test_ready_wait_has_fixed_upper_bound_and_caps_each_query(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner, inventory, source = self.ready_fixture(temporary)
            elapsed = [0]
            budgets = []
            def inventory_with_budget(timeout):
                budgets.append(timeout())
                return inventory
            runner.inventory = Mock(side_effect=inventory_with_budget)
            def advance(duration):
                elapsed[0] += duration
            with patch.object(Path, "read_bytes", return_value=b"/usr/local/bin/anas-collabora-start\0--container-start\0"), \
                    patch.object(actions.time, "monotonic", side_effect=lambda: elapsed[0]), \
                    patch.object(actions.time, "sleep", side_effect=advance), self.assertRaises(actions.core.ProbeFailure):
                runner.wait_ready(source.parent, inventory, timeout=3)
            self.assertEqual(elapsed[0], 3)
            self.assertEqual(runner.failure["reason"], "timeout")
            self.assertEqual(budgets, [1])
            for timeout in (0, -1, 301, True):
                with self.subTest(timeout=timeout), self.assertRaises(actions.core.ProbeFailure):
                    runner.wait_ready(source.parent, inventory, timeout=timeout)

    def test_ready_wait_recalculates_budget_before_each_real_docker_subprocess(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner, inventory, source = self.ready_fixture(temporary)
            runner.inventory = actions.Actions.inventory.__get__(runner)
            runner.command = actions.Actions.command.__get__(runner)
            elapsed, budgets = [0], []
            item = {**actions.collabora_container(inventory), "Config": {"Labels": {
                "com.docker.compose.project.working_dir": str(runner.workspace / ".anas/deployments/one/modules/collabora"),
                "com.docker.compose.project": runner.prefix + "collabora", "com.docker.compose.service": "anas_collabora"}}}
            def run_query(args, **kwargs):
                budgets.append(kwargs["timeout"])
                elapsed[0] += 2 if len(budgets) == 1 else 1
                body = b"c" * 64 + b"\n" if len(budgets) == 1 else json.dumps([item]).encode()
                return subprocess.CompletedProcess(args, 0, stdout=body, stderr=b"")
            with patch.object(actions.time, "monotonic", side_effect=lambda: elapsed[0]), \
                    patch.object(actions.subprocess, "run", side_effect=run_query), \
                    self.assertRaises(actions.core.ProbeFailure):
                runner.wait_ready(source.parent, timeout=3)
            self.assertEqual(budgets, [3, 1])
            self.assertEqual(runner.failure["reason"], "timeout")

    def test_discovery_requires_real_current_host_wopi_xml_and_known_pending_states(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner = self.runner(temporary)
            environment = {"ANAS_TEST_DOMAIN": "temp-edit.test", "ANAS_TEST_ENTRY_IP": "10.253.71.2", "ANAS_TEST_ENTRY_PORT": "19071"}
            valid = b'<wopi-discovery><net-zone><app><action urlsrc="https://collabora.temp-edit.test:19071/browser/fixed/cool.html?"/></app></net-zone></wopi-discovery>'
            for status, body, expected in ((200, valid, True), (404, b"unknown", False), (502, b"unknown", False),
                                           (503, b"unknown", False), (504, b"unknown", False)):
                response = Mock(status=status)
                response.read.return_value = body
                connection = Mock()
                connection.getresponse.return_value = response
                with self.subTest(status=status), patch.dict(os.environ, environment), \
                        patch.object(actions.http.client, "HTTPSConnection", return_value=connection) as client:
                    self.assertEqual(runner.discovery_ready(2), expected)
                    self.assertEqual(client.call_args.args, ("10.253.71.2", 19071))
                    self.assertEqual(client.call_args.kwargs["timeout"], 2)
                    connection.request.assert_called_once_with("GET", "/hosting/discovery", headers={"Host": "collabora.temp-edit.test:19071"})
                    connection.close.assert_called_once()
            for status, body in ((401, b"synthetic-password"), (200, b"synthetic-password"), (200, b"<html/>"),
                                 (200, valid.replace(b"collabora.temp-edit.test", b"foreign.invalid")),
                                 (200, b"x" * (4 * 1024 * 1024 + 1))):
                response = Mock(status=status)
                response.read.return_value = body
                connection = Mock()
                connection.getresponse.return_value = response
                with self.subTest(status=status, body=body[:25]), patch.dict(os.environ, environment), \
                        patch.object(actions.http.client, "HTTPSConnection", return_value=connection), \
                        self.assertRaises(actions.core.ProbeFailure) as raised:
                    runner.discovery_ready(2)
                self.assertNotIn("synthetic-password", str(raised.exception) + json.dumps(runner.failure))
                connection.close.assert_called_once()

    def test_discovery_transport_failure_is_pending_but_does_not_record_raw_error(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner = self.runner(temporary)
            connection = Mock()
            connection.request.side_effect = OSError("synthetic-password")
            with patch.dict(os.environ, {"ANAS_TEST_DOMAIN": "temp-edit.test", "ANAS_TEST_ENTRY_IP": "10.253.71.2"}), \
                    patch.object(actions.http.client, "HTTPSConnection", return_value=connection):
                self.assertFalse(runner.discovery_ready(2))
            self.assertIsNone(runner.failure)
            connection.close.assert_called_once()

    def test_office_probe_reads_only_the_current_workspace_readiness_marker(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner = self.runner(temporary)
            runner.docker = "docker"
            for status, expected in ((0, True), (1, False)):
                runner.command = Mock(return_value=(status, b""))
                self.assertEqual(runner.office_ready(2), expected)
                runner.command.assert_called_once_with(
                    ["docker", "exec", "owned_edit_nextcloud", "test", "-f", "/run/nextcloud-office.ready"],
                    expected=None, operation="inspect_office_activation", timeout=2)
            runner.command = Mock(return_value=(125, b"synthetic-password"))
            with self.assertRaises(actions.core.ProbeFailure) as raised:
                runner.office_ready(2)
            self.assertEqual(runner.failure["reason"], "office_activation_probe_failed")
            self.assertNotIn("synthetic-password", str(raised.exception))

    def test_ready_wait_does_not_return_before_nextcloud_office_activation(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner, inventory, source = self.ready_fixture(temporary)
            runner.office_ready = Mock(side_effect=[False, True])
            reads = self.process_reads([b"/usr/bin/coolwsd\0"] * 4)
            with reads[0], reads[1], patch.object(actions.time, "sleep") as pause:
                runner.wait_ready(source.parent, inventory)
            self.assertEqual(runner.office_ready.call_count, 2)
            pause.assert_called_once()

    def test_every_lifecycle_action_waits_before_recording_fresh_runtime_success(self):
        for action in actions.ACTIONS:
            with self.subTest(action=action), tempfile.TemporaryDirectory() as temporary:
                runner = self.runner(temporary)
                runner.root_a, runner.root_b, runner.module_root = Path(temporary) / "a", Path(temporary) / "b", Path(temporary) / "src"
                before = {(runner.prefix + "collabora", "anas_collabora"): {"Id": "c" * 64}}
                after = {(runner.prefix + "collabora", "anas_collabora"): {"Id": "d" * 64}}
                runner.inventory = Mock(side_effect=[before, {}] if action == "stop-start" else [before])
                runner.mounted_path = Mock(return_value=runner.root_a / "old")
                expected = runner.root_b if action == "switch-a-to-b" else runner.root_a
                runner.wait_ready = Mock(return_value=(after, expected / "new"))
                runner.cli = Mock()
                report = runner.run(action)
                runner.wait_ready.assert_called_once_with(expected)
                self.assertEqual(report["status"], "passed")
                self.assertTrue(report["fresh_container"] and report["fresh_lease"])


if __name__ == "__main__":
    unittest.main()
