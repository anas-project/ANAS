#!/usr/bin/env python3
# TEST_CASES: TEMP-T-023
"""Counterexamples for the editing fixture, without SSH or real services."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import Mock, patch
from urllib.parse import urlsplit

_spec = importlib.util.spec_from_file_location("editing_fixture", Path(__file__).with_name("server-workspace-temp-storage-editing-e2e.py"))
editing = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(editing)


class EditingCounterexamples(unittest.TestCase):
    def start_fixture(self, directory):
        runner = object.__new__(editing.Editing)
        runner.workspace, runner.source, runner.root_a = Path(directory), Path(directory) / "src", Path(directory) / "a"
        runner.prefix, runner.domain, runner.docker, runner.failure = "owned_edit_", "temp-edit.test", "docker", None
        runner.cli, runner.hook_environment = Mock(), Mock(return_value={})
        runner.http = Mock(return_value=(200, b'{"installed":true}'))
        runner.command = Mock(return_value=b'{"enabled":{"richdocuments":"11.1.0"}}')
        runner.verify_credential, runner.status = Mock(), Mock()
        inventory = {(runner.prefix + name, "anas_" + name): {"Name": "/" + runner.prefix + name, "State": {"Running": True}}
                     for name in (*editing.MODULES, "nextcloud_cron", "nextcloud_push", "nextcloud_imaginary", "nextcloud_redis")}
        helper = Mock(failure=None)
        helper.inventory.return_value = inventory
        return runner, helper, inventory

    def test_start_waits_for_shared_real_collabora_checks_before_credential_and_status(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner, helper, inventory = self.start_fixture(temporary)
            events = []
            helper.wait_ready.side_effect = lambda *args: events.append("real_collabora_ready")
            runner.verify_credential.side_effect = lambda: events.append("credential_verified")
            runner.status.side_effect = lambda: events.append("runtime_status")
            with patch.object(editing.actions, "Actions", return_value=helper):
                runner.start()
            helper.wait_ready.assert_called_once_with(runner.root_a, inventory)
            helper.mounted_path.assert_not_called()
            self.assertEqual(events, ["real_collabora_ready", "credential_verified", "runtime_status"])

    def test_start_preserves_shared_readiness_first_cause_and_does_not_continue(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner, helper, _ = self.start_fixture(temporary)
            helper.failure = {"operation": "wait_collabora_ready", "reason": "container_oom"}
            cause = editing.actions.core.ProbeFailure("Collabora readiness failed: container_oom")
            helper.wait_ready.side_effect = cause
            with patch.object(editing.actions, "Actions", return_value=helper), self.assertRaises(editing.actions.core.ProbeFailure) as raised:
                runner.start()
            self.assertIs(raised.exception, cause)
            self.assertEqual(runner.failure, helper.failure)
            runner.verify_credential.assert_not_called()
            runner.status.assert_not_called()
            runner.command.assert_not_called()

    def test_network_facts_require_a_unique_private_namespace_address(self):
        entries = [{"ifname": "anas-e2e-peer", "addr_info": [{"family": "inet", "local": "10.253.71.2", "prefixlen": 30}]}]
        self.assertEqual(editing.network_facts(entries, "10.253.71.2"), ("anas-e2e-peer", 30))
        for address in ("127.0.0.1", "0.0.0.0", "169.254.1.1", "8.8.8.8", "224.0.0.1", "10.253.71.3"):
            with self.subTest(address=address), self.assertRaises(editing.actions.core.ProbeFailure):
                editing.network_facts(entries, address)
        with self.assertRaises(editing.actions.core.ProbeFailure):
            editing.network_facts(entries + entries, "10.253.71.2")

    def test_template_keeps_exact_eight_modules_and_disables_only_requested_options(self):
        template = Path(__file__).parent.parent.joinpath("server-workspace-temp-storage-editing.yml.in").read_text()
        values = {"ENTRY_PORT": 19071, "TURN_PORT": 13478, "DOMAIN": "temp-edit.test", "PREFIX": "owned_edit_",
                  "ENTRY_IP": "10.253.71.2", "TEMP_A": "/authorized/run/a", "DOCKER_SOCKET": "/run/anas-test.sock",
                  "NETWORK_NAMESPACE": "/run/netns/anas-test", "INTERFACE": "anas-e2e-peer", "GATEWAY": "10.253.71.1", "PREFIX_LENGTH": 30}
        rendered = editing.render_fixture(template, values)
        module_block = rendered.split("modules:\n", 1)[1].split("identity:\n", 1)[0]
        actual = [line.split(":", 1)[0].strip() for line in module_block.splitlines() if line.startswith("  ") and not line.startswith("    ")]
        self.assertEqual(actual, list(editing.MODULES))
        for flag in ("talk_enabled", "memories_enabled", "enable_test", "adminer_enabled"):
            self.assertIn(flag + ": false", rendered)
        self.assertIn("virtual_domain: true", rendered)
        self.assertNotIn("password", rendered)
        self.assertIn("email: test@temp-edit.test", rendered)
        with self.assertRaises(editing.actions.core.ProbeFailure):
            editing.render_fixture(template, {})

    def test_image_list_matches_enabled_fixed_compose_images(self):
        root = Path(__file__).parent.parent.parent
        images = set()
        for name in editing.MODULES:
            for line in (root / "modules" / name / "docker-compose.yml").read_text().splitlines():
                if line.strip().startswith("image:"):
                    image = line.strip().removeprefix("image:").strip().replace("${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}", "ghcr.io/anas-project")
                    if "anas-mirror-adminer:" not in image and "anas-mirror-nextcloud-talk:" not in image:
                        images.add(image)
        listed = [line for line in (root / "test-env/server-workspace-temp-storage-editing-images.txt").read_text().splitlines() if line and not line.startswith("#")]
        self.assertEqual(set(listed), images)
        self.assertEqual(len(listed), len(set(listed)))

    def test_download_proxy_stays_on_private_gateway_and_bypasses_local_stack_without_credentials(self):
        template = Path(__file__).parent.parent.joinpath("server-workspace-temp-storage-editing.yml.in").read_text()
        values = {key: "synthetic" for key in ("ENTRY_PORT", "TURN_PORT", "PREFIX", "ENTRY_IP", "TEMP_A", "DOCKER_SOCKET", "NETWORK_NAMESPACE", "INTERFACE", "PREFIX_LENGTH")}
        values.update(DOMAIN="temp-edit.test", GATEWAY="10.253.71.1")
        rendered = editing.render_fixture(template, values)
        environment = {line.strip().split(":", 1)[0]: line.strip().split(":", 1)[1].strip().strip('"') for line in rendered.splitlines() if line.strip().startswith(("HTTP_PROXY:", "HTTPS_PROXY:", "NO_PROXY:"))}
        self.assertEqual(environment["HTTP_PROXY"], environment["HTTPS_PROXY"])
        proxy = urlsplit(environment["HTTPS_PROXY"])
        self.assertEqual((proxy.scheme, proxy.hostname, proxy.port), ("http", values["GATEWAY"], 3128))
        self.assertIsNone(proxy.username)
        self.assertIsNone(proxy.password)
        self.assertEqual((proxy.path, proxy.query, proxy.fragment), ("", "", ""))
        self.assertEqual(set(environment["NO_PROXY"].split(",")), {"localhost", "127.0.0.1", ".temp-edit.test", "10.253.71.0/24", "172.27.71.0/24", "172.28.0.0/16"})

    def test_hook_compiler_is_offline_in_private_tools_container(self):
        runner = object.__new__(editing.Editing)
        runner.docker, runner.prefix, runner.source = "docker", "owned_", Path("/authorized/run/src")
        paths = (Path("/authorized/run/go"), Path("/authorized/run/build"), Path("/authorized/run/modules"), "off", [])
        command = runner.compiler_command("nextcloud", Path("/authorized/run/output"), paths)
        self.assertEqual(command[:2], ["docker", "run"])
        self.assertEqual(command[command.index("--network") + 1], "none")
        self.assertIn("type=bind,source=/authorized/run/go,target=/opt/go,readonly", command)
        self.assertIn("type=bind,source=/authorized/run/src,target=/source,readonly", command)
        self.assertIn("GOPROXY=off", command)
        self.assertIn("GOTOOLCHAIN=local", command)
        self.assertIn("CGO_ENABLED=0", command)
        self.assertEqual(command[command.index("--cap-drop") + 1], "ALL")
        self.assertEqual(command[command.index("--cap-add") + 1], "DAC_OVERRIDE")
        self.assertNotIn("--privileged", command)
        self.assertEqual(command[-7:], [editing.PLAYWRIGHT_IMAGE, "/opt/go/bin/go", "build", "-trimpath", "-o", "/output/anas-hook", "./modules/nextcloud/hook"])
        with self.assertRaises(editing.actions.core.ProbeFailure):
            runner.compiler_command("unrelated", Path("/authorized/run/output"), paths)

    def test_browser_can_read_private_source_without_broad_capabilities_or_host_handles(self):
        runner = object.__new__(editing.Editing)
        runner.docker, runner.prefix, runner.source = "docker", "owned_", Path("/authorized/run/src")
        runner.dependencies_dir, runner.browser_dir = Path("/authorized/run/js"), Path("/authorized/run/browser")
        command = runner.browser_command(Path("/authorized/run/collabora-actions.sock"), {"ANAS_TEST_USERNAME": "synthetic-user", "ANAS_TEST_PASSWORD": "synthetic-password"})
        self.assertEqual(command[command.index("--cap-drop") + 1], "ALL")
        self.assertEqual(command[command.index("--cap-add") + 1], "DAC_OVERRIDE")
        self.assertIn("type=bind,source=/authorized/run/src,target=/authorized/run/src,readonly", command)
        self.assertIn("type=bind,source=/authorized/run/js,target=/node_modules,readonly", command)
        self.assertNotIn("synthetic-password", command)
        self.assertNotIn("--privileged", command)
        self.assertNotIn("--pid", command)
        self.assertFalse(any("docker.sock" in str(argument) for argument in command))

    def test_browser_resource_limits_keep_source_readonly_and_no_daemon_access(self):
        runner = object.__new__(editing.Editing)
        runner.docker, runner.prefix, runner.source = "docker", "owned_", Path("/authorized/run/src")
        runner.dependencies_dir, runner.browser_dir = Path("/authorized/run/js"), Path("/authorized/run/browser")
        action_socket = Path("/authorized/run/collabora-actions.sock")
        command = runner.browser_command(action_socket, {"ANAS_TEST_PASSWORD": "synthetic-password"})
        for option, expected in (("--memory", "1024m"), ("--shm-size", "256m"), ("--pids-limit", "256"),
                                 ("--tmpfs", "/tmp:mode=1777,size=512m")):
            self.assertEqual(command.count(option), 1)
            self.assertEqual(command[command.index(option) + 1], expected)
        mounts = [command[index + 1] for index, item in enumerate(command) if item == "--mount"]
        self.assertEqual(set(mounts), {
            "type=bind,source=/authorized/run/src,target=/authorized/run/src,readonly",
            "type=bind,source=/authorized/run/js,target=/node_modules,readonly",
            "type=bind,source=/authorized/run/collabora-actions.sock,target=/authorized/run/collabora-actions.sock,readonly",
            "type=bind,source=/authorized/run/browser,target=/authorized/run/browser",
        })
        self.assertIn("--read-only", command)
        self.assertEqual(command[command.index("--security-opt") + 1], "no-new-privileges")
        self.assertFalse(set(command) & {"--privileged", "--pid", "--device", "--volume", "--mounts"})
        self.assertFalse(any("docker.sock" in str(item) or "containerd.sock" in str(item) for item in command))
        self.assertNotIn("synthetic-password", command)

    def test_cleanup_materials_require_current_run_safe_codes_and_restrictive_mode(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "cleanup.json"
            report = {"schema": "anas.workspace-temp-collabora-cleanup/v1", "case_id": "TEMP-T-021", "run_id": "owned-run",
                      "status": "failed", "document": "unconfirmed", "webdav_context": "closed", "first_failure_preserved": True,
                      "failures": ["temporary_document_delete_failed"]}
            path.write_text(json.dumps(report))
            path.chmod(0o600)
            self.assertEqual(editing.cleanup_materials(path, "owned-run")["status"], "failed")
            with self.assertRaises(editing.actions.core.ProbeFailure):
                editing.cleanup_materials(path, "other-run")
            path.write_text(json.dumps({**report, "failures": ["synthetic-password"]}))
            with self.assertRaises(editing.actions.core.ProbeFailure):
                editing.cleanup_materials(path, "owned-run")
            path.write_text(json.dumps(report))
            path.chmod(0o644)
            with self.assertRaises(editing.actions.core.ProbeFailure):
                editing.cleanup_materials(path, "owned-run")

    def test_prebuilt_hooks_reject_missing_wrong_binary_and_changed_input(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            runner = object.__new__(editing.Editing)
            runner.root, runner.source, runner.reports = root, root / "src", root / "reports"
            runner.run_id, runner.source_digest = "local-only", "sha256:source"
            runner.reports.mkdir()
            with self.assertRaises(editing.actions.core.ProbeFailure):
                runner.hook_environment()
            for name in editing.MODULES:
                directory = runner.source / "modules" / name / "hook"
                directory.mkdir(parents=True)
                (directory / "main.go").write_text("package main\n")
                (directory.parent / "module.yml").write_text("name: " + name + "\n")
                binary = directory / "bin/linux-amd64/anas-hook"
                binary.parent.mkdir(parents=True)
                binary.write_bytes(b"\x7fELF\x02\x01" + b"\0" * 12 + b"\x3e\x00" + b"\0" * 80)
                binary.chmod(0o755)
            for name in ("go.mod", "go.sum"):
                (runner.source / name).write_text("synthetic public input\n")
            binaries = {name: editing.compiled_hook(runner.source / "modules" / name / "hook/bin/linux-amd64/anas-hook") for name in editing.MODULES}
            manifest = {"schema": "anas.workspace-temp-storage-prebuilt-hooks/v1", "run_id": runner.run_id, "source_digest": runner.source_digest,
                        "tool_image": editing.PLAYWRIGHT_IMAGE, "inputs": editing.hook_inputs(runner.source), "binaries": binaries}
            (runner.reports / "collabora-prebuilt-hooks.json").write_text(json.dumps(manifest))
            with patch.dict(os.environ, {"GOROOT": "/host/go", "GOCACHE": "/host/build", "GOMODCACHE": "/host/modules"}):
                environment = runner.hook_environment()
                self.assertNotIn("GOROOT", environment)
                self.assertEqual(set(runner.hook_binaries), set(editing.MODULES))
            changed_binary = runner.source / "modules/lego/hook/bin/linux-amd64/anas-hook"
            original = changed_binary.read_bytes()
            changed_binary.write_bytes(original + b"changed executable")
            with self.assertRaises(editing.actions.core.ProbeFailure):
                runner.hook_environment()
            changed_binary.write_bytes(original)
            (runner.source / "go.mod").write_text("changed input\n")
            with self.assertRaises(editing.actions.core.ProbeFailure):
                runner.hook_environment()
            malformed = root / "malformed"
            malformed.write_bytes(b"host or wrong architecture executable")
            malformed.chmod(0o755)
            with self.assertRaises(editing.actions.core.ProbeFailure):
                editing.compiled_hook(malformed)

    def test_browser_report_requires_real_test_and_no_credential(self):
        report = {"schema": "anas.iam-logout-e2e/v1", "status": "passed",
                  "results": [{"title": editing.TEST_TITLE, "status": "passed", "errors": []}]}
        editing.validate_browser_report(report, "synthetic-user", "synthetic-password")
        for changed in ({**report, "results": []}, {**report, "status": "failed"},
                        {**report, "results": [{"title": "probe", "status": "passed", "errors": []}]},
                        {**report, "credential": "synthetic-password"}):
            with self.subTest(report=changed), self.assertRaises(editing.actions.core.ProbeFailure):
                editing.validate_browser_report(changed, "synthetic-user", "synthetic-password")

    def test_action_reports_require_all_runtime_evidence_and_current_digests(self):
        source, binary = "sha256:source", "sha256:binary"
        reports = [{"schema": "anas.workspace-temp-collabora-action/v1", "case_id": "TEMP-T-021", "run_id": "owned-run",
                    "action": action, "status": "passed", "source_digest": source, "binary_digest": binary,
                    "namespace_marker_verified": True, "fresh_container": True, "fresh_lease": True,
                    "all_containers_rebuilt": action == "switch-a-to-b"} for action in editing.actions.ACTIONS]
        editing.validate_action_reports(reports, source, binary, "owned-run")
        for changed in (reports[:2], [{**reports[0], "fresh_lease": False}, *reports[1:]],
                        [*reports[:2], {**reports[2], "all_containers_rebuilt": False}],
                        [{**reports[0], "run_id": "other-run"}, *reports[1:]]):
            with self.subTest(reports=changed), self.assertRaises(editing.actions.core.ProbeFailure):
                editing.validate_action_reports(changed, source, binary, "owned-run")

    def test_managed_credential_does_not_accept_external_login_url(self):
        url = "https://nc.temp-edit.test:19071/login?direct=1"
        document = {"ok": True, "account": {"username": "synthetic-user", "password": "synthetic-password", "url": url}}
        self.assertEqual(editing.credential_account(document, url), ("synthetic-user", "synthetic-password"))
        with self.assertRaises(editing.actions.core.ProbeFailure):
            editing.credential_account(document, "https://elsewhere.invalid/login")

    def test_cli_failure_output_cannot_enter_exception_or_report(self):
        runner = object.__new__(editing.Editing)
        runner.failure = None
        failure = subprocess.CompletedProcess(["anas"], 5, stdout=b"synthetic-password", stderr=b"synthetic-password")
        with patch.object(editing.subprocess, "run", return_value=failure), self.assertRaises(editing.actions.core.ProbeFailure) as raised:
            runner.command(["anas", "apply"], "apply_editing_workspace")
        self.assertNotIn("synthetic-password", str(raised.exception))
        self.assertNotIn("synthetic-password", json.dumps(runner.failure))

    def test_cli_failure_retains_safe_primary_and_independent_recovery(self):
        runner = object.__new__(editing.Editing)
        runner.anas, runner.prefix, runner.failure = Path("/authorized/run/anas"), "owned_edit_", None
        secret = "synthetic-password"
        document = {"api_version": "anas.dev/cli/v1", "ok": False, "error": {"code": "stop_failed", "message": secret,
                    "detail": {"primary": {"code": "stop_failed", "message": secret,
                               "command": {"phase": "down", "exit_code": 98, "project": runner.prefix + "collabora", "summary": secret, "argv": [secret]}},
                               "recovery": [{"phase": "previous_restore", "status": "failed", "message": secret}]}}}
        failure = subprocess.CompletedProcess([str(runner.anas)], 1, stdout=json.dumps(document).encode(), stderr=secret.encode())
        with patch.object(editing.subprocess, "run", return_value=failure), self.assertRaises(editing.actions.core.ProbeFailure) as raised:
            runner.cli("apply_editing_workspace", "apply", "--password", secret, input=secret.encode())
        self.assertEqual(runner.failure, {"operation": "apply_editing_workspace", "exit_code": 1, "error": {"code": "stop_failed", "detail": {
                         "primary": {"code": "stop_failed", "command": {"phase": "down", "exit_code": 98, "module": "collabora"}},
                         "recovery": [{"phase": "previous_restore", "status": "failed"}]}}})
        self.assertNotIn(secret, str(raised.exception) + json.dumps(runner.failure))
        self.assertNotIn(runner.prefix, json.dumps(runner.failure))

    def test_cli_failure_rejects_foreign_projects_unknown_fields_and_malformed_json(self):
        prefix = "owned_edit_"
        document = {"api_version": "anas.dev/cli/v1", "ok": False, "error": {"code": "start_failed", "detail": {
                    "primary": {"code": "synthetic-password", "command": {"phase": "synthetic-password", "exit_code": True, "project": "foreign_collabora"}},
                    "recovery": [{"phase": "previous_restore", "status": "synthetic-password"}, {"phase": ["previous_restore"], "status": "failed"}]}}}
        self.assertEqual(editing.safe_cli_error(json.dumps(document), prefix), {"code": "start_failed"})
        for project in ("foreign_collabora", prefix + "collabora-extra", prefix + "unrelated", "synthetic-password"):
            document["error"]["detail"]["primary"] = {"command": {"phase": "up", "exit_code": 99, "project": project}}
            result = editing.safe_cli_error(json.dumps(document), prefix)
            self.assertEqual(result["detail"]["primary"]["command"], {"phase": "up", "exit_code": 99})
        for raw in (b"not JSON", b"\xff", b"[]", b"null", b"[" * 2000 + b"]" * 2000, json.dumps({**document, "ok": True}),
                    json.dumps({**document, "api_version": "foreign/v1"}), json.dumps({**document, "error": {"code": "unknown"}}),
                    json.dumps({**document, "error": {"code": ["start_failed"]}})):
            with self.subTest(raw=raw):
                self.assertIsNone(editing.safe_cli_error(raw, prefix))

    def test_non_cli_failure_stays_plain_and_successful_credential_output_stays_in_memory(self):
        runner = object.__new__(editing.Editing)
        runner.anas, runner.prefix, runner.failure = Path("/authorized/run/anas"), "owned_edit_", None
        raw = json.dumps({"api_version": "anas.dev/cli/v1", "ok": False, "error": {"code": "start_failed"}}).encode()
        with patch.object(editing.subprocess, "run", return_value=subprocess.CompletedProcess(["docker"], 1, stdout=raw, stderr=b"")), self.assertRaises(editing.actions.core.ProbeFailure):
            runner.command(["docker", "info", "--json"], "inspect_private_daemon")
        self.assertEqual(runner.failure, {"operation": "inspect_private_daemon", "exit_code": 1})
        runner.failure = None
        credential = b'{"ok":true,"account":{"username":"synthetic-user","password":"synthetic-password"}}'
        with patch.object(editing.subprocess, "run", return_value=subprocess.CompletedProcess([str(runner.anas)], 0, stdout=credential, stderr=b"")):
            self.assertEqual(runner.cli("read_managed_break_glass", "admin", "local", "credential"), credential)
        self.assertIsNone(runner.failure)

    def test_credential_verification_uses_stdin_and_suppresses_command_output(self):
        runner = object.__new__(editing.Editing)
        runner.workspace = Path("/authorized/workspace")
        runner.login = "https://nc.temp-edit.test:19071/login?direct=1"
        runner.docker, runner.prefix = "docker", "owned_"
        runner.cli = Mock(return_value=json.dumps({"ok": True, "account": {"username": "synthetic-user", "password": "synthetic-password", "url": runner.login}}))
        runner.command = Mock()
        self.assertEqual(runner.verify_credential(), ("synthetic-user", "synthetic-password"))
        args, kwargs = runner.command.call_args
        self.assertNotIn("synthetic-password", str(args))
        self.assertEqual(kwargs["input"], b"synthetic-password\n")
        self.assertIn("\\OC::$server", args[0][-2])

    def test_prepare_and_failed_browser_reports_do_not_claim_requirement_coverage(self):
        with tempfile.TemporaryDirectory() as temporary:
            runner = object.__new__(editing.Editing)
            runner.reports = Path(temporary)
            runner.run_id, runner.source_digest, runner.binary_digest = "local-only", "sha256:source", "sha256:binary"
            runner.failure, runner.runtime, runner.server_shutdown = None, {}, "not_started"
            runner.hook_binaries = {}
            runner.browser_cleanup = None
            for phase, failed in (("prepare", False), ("start", False), ("browser", True)):
                runner.report(phase, failed)
                report = json.loads((runner.reports / ("collabora-stack-" + phase + ".json")).read_text())
                self.assertEqual(report["requirements"], [])
                self.assertEqual(report["not_run"], ["TEMP-R-029", "TEMP-R-030"])
                self.assertNotEqual(report["status"], "passed")


if __name__ == "__main__":
    unittest.main()
