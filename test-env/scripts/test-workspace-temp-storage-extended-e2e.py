#!/usr/bin/env python3
# TEST_CASES: TEMP-T-023
"""Counterexamples for extended probes; no real daemon, SSH, or mounts."""
import importlib.util
import ast
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import Mock, patch

_spec = importlib.util.spec_from_file_location("extended_suite", Path(__file__).with_name("server-workspace-temp-storage-extended-e2e.py"))
suite = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(suite)


class ExtendedProbeCounterexamples(unittest.TestCase):
    def test_mount_namespace_must_be_private_and_shared_by_both_daemons(self):
        suite.require_namespace_identities(7, 1, 7, 7, 7)
        for identities in ((1, 1, 1, 1, 1), (7, 1, 8, 7, 7), (7, 1, 7, 8, 7), (7, 1, 7, 7, 8)):
            with self.subTest(identities=identities), self.assertRaises(suite.core.ProbeFailure):
                suite.require_namespace_identities(*identities)
        self.assertEqual(suite.command_flag(["dockerd", "--data-root", "/run-owned/docker"], "--data-root"), "/run-owned/docker")
        self.assertEqual(suite.command_flag(["dockerd", "--data-root=/run-owned/docker"], "--data-root"), "/run-owned/docker")
        self.assertIsNone(suite.command_flag(["dockerd", "--data-root"], "--data-root"))

    def test_paused_docker_subprocess_requires_cli_parent_and_unique_group(self):
        record = {"pid": 42, "ppid": 24, "pgid": 42, "session_id": 24, "inherited_pgid": 24, "start_time": 123}
        current = {"ppid": 24, "pgid": 42, "session_id": 24, "start_time": 123}
        self.assertEqual(suite.require_blocker_identity(record, 24, current), 42)
        for changed_record, changed_current in (
            ({**record, "pid": 1}, current), ({**record, "ppid": 1}, current),
            ({**record, "pgid": 24}, current), (record, {**current, "start_time": 124}),
            (record, {**current, "ppid": 1}), (record, {**current, "pgid": 24}),
            ({**record, "session_id": 1}, current), (record, {**current, "session_id": 1}),
            ({**record, "inherited_pgid": 99}, current),
        ):
            with self.subTest(record=changed_record, current=changed_current), self.assertRaises(suite.core.ProbeFailure):
                suite.require_blocker_identity(changed_record, 24, changed_current)

    def test_generated_blocker_isolates_its_real_cli_child_group_and_rejects_foreign_session(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            probe = object.__new__(suite.ExtendedSuite)
            probe.root, probe.workspace, probe.root_b = root, root / "workspace", root / "target"
            probe.docker, probe.prefix = sys.executable, "fixture_"
            with patch.dict(suite.os.environ, {"PATH": os.environ["PATH"]}):
                _, marker, _ = probe.blocking_docker("stop", [])
            tree = ast.parse(marker.with_name("docker").read_text())
            boundary = next(node for node in tree.body if isinstance(node, ast.If) and isinstance(node.test, ast.BoolOp)
                            and isinstance(node.test.op, ast.And) and isinstance(node.test.values[0], ast.Name)
                            and node.test.values[0].id == "block")
            first = boundary.body[0]
            self.assertIsInstance(first, ast.Assign)
            self.assertEqual(ast.unparse(first.value), "isolate_blocked_shim()")
            self.assertEqual(sum(isinstance(node, ast.Call) and isinstance(node.func, ast.Name)
                                 and node.func.id == "isolate_blocked_shim" for node in ast.walk(tree)), 1)
            # Run the exact generated syscall helper in real Python processes;
            # no Go, Docker or synthetic /proc start-time data is involved.
            helper = next(node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == "isolate_blocked_shim")
            child = "import json,os\n" + ast.unparse(helper) + "\nprint(json.dumps(isolate_blocked_shim()),flush=True)\n"
            driver = r'''
import json,os,subprocess,sys
mode,child=sys.argv[1:]
parent=os.getpid()
sibling=subprocess.Popen([sys.executable,'-c','import sys; sys.stdin.read()'],stdin=subprocess.PIPE)
try:
 before={'pid':parent,'pgid':os.getpgrp(),'session_id':os.getsid(0),'sibling_pgid':os.getpgid(sibling.pid)}
 options={'process_group':0} if mode=='own-group' else {'start_new_session':True} if mode=='foreign-session' else {}
 result=subprocess.run([sys.executable,'-c',child],capture_output=True,text=True,timeout=5,**options)
 after={'pgid':os.getpgrp(),'session_id':os.getsid(0),'sibling_pgid':os.getpgid(sibling.pid),'sibling_running':sibling.poll() is None}
 print(json.dumps({'before':before,'after':after,'exit':result.returncode,'record':json.loads(result.stdout) if result.returncode==0 else None}),flush=True)
finally:
 sibling.stdin.close()
 sibling.wait(timeout=5)
'''
            for mode in ("inherited-group", "own-group", "foreign-session"):
                with self.subTest(mode=mode):
                    result = subprocess.run([sys.executable, "-c", driver, mode, child], start_new_session=True,
                                            capture_output=True, text=True, timeout=15)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    evidence = json.loads(result.stdout)
                    before, after = evidence["before"], evidence["after"]
                    self.assertEqual(before["pid"], before["pgid"])
                    self.assertEqual(before["pid"], before["session_id"])
                    self.assertEqual(after, {"pgid": before["pgid"], "session_id": before["session_id"],
                                             "sibling_pgid": before["sibling_pgid"], "sibling_running": True})
                    if mode == "foreign-session":
                        self.assertNotEqual(evidence["exit"], 0)
                        self.assertIsNone(evidence["record"])
                    else:
                        self.assertEqual(evidence["exit"], 0)
                        record = evidence["record"]
                        self.assertEqual(record["ppid"], before["pid"])
                        self.assertEqual(record["session_id"], before["pid"])
                        self.assertEqual(record["pgid"], record["pid"])
                        self.assertEqual(record["inherited_pgid"], before["pid"] if mode == "inherited-group" else record["pid"])

    def test_pid_reuse_prevents_signalling_a_different_process_group(self):
        probe = object.__new__(suite.ExtendedSuite)
        probe.blocked_shims = {42: 123}
        with patch.object(suite, "process_identity", return_value={"ppid": 1, "pgid": 42, "start_time": 124}), patch.object(suite.os, "killpg") as kill:
            with self.assertRaises(suite.core.ProbeFailure):
                probe.kill_blocked_shim(42)
            kill.assert_not_called()
        with patch.object(suite, "process_identity", return_value={"ppid": 1, "pgid": 42, "start_time": 123}), patch.object(suite.os, "killpg") as kill:
            probe.kill_blocked_shim(42)
            kill.assert_called_once_with(42, suite.signal.SIGKILL)
        self.assertEqual(probe.blocked_shims, {})

    def test_mount_scope_rejects_escape_and_symlink(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            suite.require_run_path(root, root / "fixture.img")
            for path in (root, root.parent / "escape.img", Path("relative.img")):
                with self.subTest(path=path), self.assertRaises(suite.core.ProbeFailure):
                    suite.require_run_path(root, path)
            link = root / "link"
            link.symlink_to(root.parent, target_is_directory=True)
            with self.assertRaises(suite.core.ProbeFailure):
                suite.require_run_path(root, link / "escape.img")
            with self.assertRaises(suite.core.ProbeFailure):
                suite.require_run_path(root, link)

    def test_unowned_mount_is_rejected_before_privileged_command(self):
        with tempfile.TemporaryDirectory() as temporary:
            probe = object.__new__(suite.ExtendedSuite)
            probe.root = Path(temporary).resolve()
            probe.mounts = {}
            probe.images = set()
            probe.privileged = Mock()
            target = probe.root / "unowned"
            target.mkdir()
            image = probe.root / "unowned.img"
            image.touch()
            with self.assertRaises(suite.core.ProbeFailure):
                probe.mount_existing(image, target)
            with self.assertRaises(suite.core.ProbeFailure):
                probe.unmount(target)
            probe.privileged.assert_not_called()

    def test_low_space_and_recovery_require_real_issue_transition(self):
        low = {"issues": [{"code": "temp_low_space"}]}
        clear = {"issues": []}
        suite.require_storage_problem(low, True)
        suite.require_storage_problem(clear, False)
        for value, expected in ((clear, True), (low, False), ({}, True), ({"issues": "not-an-array"}, False)):
            with self.subTest(value=value), self.assertRaises(suite.core.ProbeFailure):
                suite.require_storage_problem(value, expected)

    def test_ordinary_runtime_projection_is_required_even_when_temp_status_is_correct(self):
        value = {"module_runtime": [{"module": name, "temp_storage": {"state": "low_space"}} for name in ("producer", "consumer")]}
        suite.require_storage_problem({"issues": [{"code": "temp_low_space"}]}, True)
        suite.require_runtime_storage(value, "low_space")
        with self.assertRaises(suite.core.ProbeFailure):
            suite.require_runtime_storage(value, "ok")
        value["module_runtime"][0]["temp_storage"] = {"state": "ok"}
        with self.assertRaises(suite.core.ProbeFailure):
            suite.require_runtime_storage(value, "low_space")
        for status in ({}, {"module_runtime": []}, {"module_runtime": "not-an-array"}):
            with self.subTest(status=status), self.assertRaises(suite.core.ProbeFailure):
                suite.require_runtime_storage(status, "low_space")

    def test_restore_cannot_reuse_identity_or_source_paths(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            source = root / "source"
            source.mkdir()
            paths = [root / str(index) for index in range(3)]
            for path in paths:
                path.mkdir()
            status = {"workspace_id": "new-id", "directories": [{"path": str(path), "state": "active"} for path in paths]}
            self.assertEqual(suite.require_restore_isolation("source-id", [source], status), set(paths))
            with self.assertRaises(suite.core.ProbeFailure):
                suite.require_restore_isolation("new-id", [source], status)
            status["directories"][0]["path"] = str(source)
            with self.assertRaises(suite.core.ProbeFailure):
                suite.require_restore_isolation("source-id", [source], status)
            status["directories"] = []
            with self.assertRaises(suite.core.ProbeFailure):
                suite.require_restore_isolation("source-id", [source], status)

    def test_backup_requires_real_workspace_tmp_mounts_and_an_explicit_absolute_root(self):
        class ReachedLoopback(Exception):
            pass
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            workspace = root / "workspace"
            (workspace / "data").mkdir(parents=True)
            for actual_root, accepted in ((workspace / "tmp", True), (root / "external-temp", False)):
                with self.subTest(actual_root=actual_root):
                    paths = [actual_root / str(index) for index in range(3)]
                    for path in paths:
                        path.mkdir(parents=True)
                    probe = object.__new__(suite.ExtendedSuite)
                    probe.root, probe.workspace, probe.run_id = root, workspace, "synthetic-only"
                    probe.set_root = Mock()
                    probe.status = Mock(return_value={"workspace_id": "source-id"})
                    probe.ids = Mock(return_value={"producer": "before", "consumer": "before", "passive": "before"})
                    probe.active = Mock(return_value="source-deployment")
                    probe.inspect = Mock(return_value={"producer": {"Mounts": [
                        {"Destination": "/runtime", "Source": str(paths[0])}, {"Destination": "/scratch", "Source": str(paths[1])}]},
                        "consumer": {"Mounts": [{"Destination": "/runtime", "Source": str(paths[2])}]}})
                    probe.mount_image = Mock(side_effect=ReachedLoopback())
                    with self.assertRaises(ReachedLoopback if accepted else suite.core.ProbeFailure):
                        probe.backup()
                    probe.set_root.assert_called_once_with(workspace / "tmp")
                    self.assertTrue(probe.set_root.call_args.args[0].is_absolute())
                    if accepted:
                        self.assertEqual((workspace / "data/business-sentinel").read_text(), "synthetic-only")
                        self.assertTrue(all((path / "excluded-temp-sentinel").read_text() == "must-not-be-backed-up" for path in paths))
                        probe.mount_image.assert_called_once_with("extended-backup-btrfs", "btrfs")
                    else:
                        probe.mount_image.assert_not_called()
                        self.assertTrue(all(not (path / "excluded-temp-sentinel").exists() for path in paths))

    def test_missing_history_refusal_rejects_usage_and_changes_to_current_runtime(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            path = root / "current"
            path.mkdir()
            sentinel = path / "history-current-sentinel"
            sentinel.write_text("preserve-current-runtime")
            payload = {"ok": False, "error": {"code": "temp_preflight_failed"}}
            ids = {"producer": "current-id"}
            status = {"applied_root": str(root)}
            suite.require_history_missing_refusal(1, payload, ids, ids, "current", "current", status, root, [path])
            for code, response, after_ids, active, state in (
                (0, payload, ids, "current", status),
                (1, {"ok": False, "error": {"code": "usage"}}, ids, "current", status),
                (1, payload, {"producer": "new-id"}, "current", status),
                (1, payload, ids, "new-active", status),
                (1, payload, ids, "current", {"applied_root": str(root / "missing")}),
            ):
                with self.subTest(code=code, response=response, active=active), self.assertRaises(suite.core.ProbeFailure):
                    suite.require_history_missing_refusal(code, response, ids, after_ids, "current", active, state, root, [path])
            sentinel.write_text("changed")
            with self.assertRaises(suite.core.ProbeFailure):
                suite.require_history_missing_refusal(1, payload, ids, ids, "current", "current", status, root, [path])

    def test_historical_disk_probe_runs_refusal_then_original_remount_and_fresh_rollback(self):
        with tempfile.TemporaryDirectory() as temporary:
            probe = object.__new__(suite.ExtendedSuite)
            probe.root = Path(temporary).resolve()
            probe.workspace, probe.root_a = probe.root / "workspace", probe.root / "root-a"
            image, target = probe.root / "original.img", probe.root / "external"
            artifact = probe.workspace / ".anas/deployments/history"
            artifact.mkdir(parents=True)
            (artifact / "frozen").write_text("immutable")
            historical_paths = [target / ("old-" + str(index)) for index in range(3)]
            for path in historical_paths:
                path.mkdir(parents=True)
            state = {"active": "history", "paths": historical_paths, "mounted": True}
            stages = []
            def containers():
                return {str(index): {"Id": state["active"] + str(index), "Mounts": [{"Destination": "/runtime", "Source": str(path)}]} for index, path in enumerate(state["paths"])}
            def set_root(root):
                self.assertEqual(root, probe.root_a)
                stages.append("switch-current")
                for path in historical_paths:
                    if path.exists():
                        suite.shutil.rmtree(path)
                paths = [root / ("current-" + str(index)) for index in range(3)]
                for path in paths:
                    path.mkdir(parents=True, exist_ok=True)
                state.update(active="current", paths=paths)
            def unmount(root):
                self.assertEqual(root, target)
                stages.append("unmount")
                state["mounted"] = False
            def mount(original, root):
                self.assertEqual((original, root), (image, target))
                stages.append("remount")
                state["mounted"] = True
            def cli(*args, **kwargs):
                self.assertEqual(args, ("rollback", "history", "-w", str(probe.workspace), "--json"))
                if kwargs.get("expected") is None and "expected" in kwargs:
                    self.assertFalse(state["mounted"])
                    stages.append("rollback-refused")
                    return 1, json.dumps({"ok": False, "error": {"code": "temp_preflight_failed"}}), b""
                self.assertTrue(state["mounted"])
                stages.append("rollback-success")
                paths = [target / ("fresh-" + str(index)) for index in range(3)]
                for path in paths:
                    path.mkdir(parents=True)
                state.update(active="fresh-deployment", paths=paths)
                return 0, b"{}", b""
            probe.active = Mock(side_effect=lambda: state["active"])
            probe.inspect = Mock(side_effect=containers)
            probe.ids = Mock(side_effect=lambda: {name: item["Id"] for name, item in containers().items()})
            probe.status = Mock(return_value={"applied_root": str(probe.root_a)})
            probe.set_root, probe.unmount, probe.mount_existing = Mock(side_effect=set_root), Mock(side_effect=unmount), Mock(side_effect=mount)
            probe.cli, probe.record = Mock(side_effect=cli), Mock()
            probe.history_filesystem(image, target)
            self.assertEqual(stages, ["switch-current", "unmount", "rollback-refused", "remount", "rollback-success", "switch-current"])
            self.assertEqual(probe.cli.call_count, 2)
            self.assertEqual(probe.record.call_args.args[0], "TEMP-T-019")

    def test_snapshot_backup_uses_pause_and_other_transfers_keep_no_stop(self):
        probe = object.__new__(suite.ExtendedSuite)
        probe.workspace = Path("/synthetic/workspace")
        for mode in ("snapshot", "send", "send-file", "copy"):
            with self.subTest(mode=mode):
                probe.cli = Mock(return_value=(0, '{"ok":true}', b""))
                self.assertEqual(probe.create_backup(mode, Path("/synthetic/destination")), {"ok": True})
                command = probe.cli.call_args.args
                self.assertEqual(command[:6], ("backup", "create", "--mode", mode, "--to", "/synthetic/destination"))
                self.assertEqual("--no-stop" in command, mode != "snapshot")
                self.assertIn("-y", command)

    def test_backup_restore_uses_real_empty_init_then_restore_without_source_lifecycle(self):
        with tempfile.TemporaryDirectory() as temporary:
            probe = object.__new__(suite.ExtendedSuite)
            probe.root = Path(temporary).resolve()
            target, destination = probe.root / "restored", probe.root / "backup"
            source = probe.root / "source"
            source.mkdir()
            sentinel = source / "source-lease-sentinel"
            sentinel.write_text("preserve-source")
            calls = []
            def cli(*args):
                self.assertTrue(target.is_dir())
                self.assertEqual(target.stat().st_mode & 0o777, 0o700)
                calls.append(args[0])
                if args[0] == "init":
                    self.assertEqual(list(target.iterdir()), [])
                    self.assertEqual(args, ("init", str(target), "-y", "--json"))
                    # This mock simulates the real init side effect. The E2E
                    # harness never constructs workspace metadata itself.
                    (target / ".anas").mkdir()
                    return 0, json.dumps({"ok": True, "workspace": str(target), "config_source": ""}), b""
                self.assertTrue((target / ".anas").is_dir())
                self.assertEqual(calls, ["init", "backup"])
                self.assertEqual(args, ("backup", "restore", "--from", str(destination), "--backup-id", "frozen-backup", "-w", str(target), "-y", "--json"))
                return 0, json.dumps({"ok": True, "workspace": str(target), "backup_id": "frozen-backup", "mode": "copy",
                                      "verify": {"ok": True, "checked": 7, "problems": []}}), b""
            probe.cli = Mock(side_effect=cli)
            probe.command = Mock()
            probe.restore_backup(destination, "frozen-backup", target)
            self.assertEqual(probe.cli.call_count, 2)
            self.assertEqual(calls, ["init", "backup"])
            probe.command.assert_not_called()
            self.assertEqual(sentinel.read_text(), "preserve-source")

    def test_failed_or_unqualified_init_cannot_reach_restore(self):
        with tempfile.TemporaryDirectory() as temporary:
            probe = object.__new__(suite.ExtendedSuite)
            probe.root = Path(temporary).resolve()
            for index, outcome in enumerate(("failure", "no-state", "leases", "active", "imported", "wrong-workspace", "public-mode", "state-link")):
                target = probe.root / ("restore-" + str(index))
                def init(*args):
                    self.assertEqual(args, ("init", str(target), "-y", "--json"))
                    if outcome == "failure":
                        raise suite.core.ProbeFailure("init failed")
                    state = target / ".anas"
                    if outcome == "state-link":
                        state.symlink_to(probe.root, target_is_directory=True)
                    elif outcome != "no-state":
                        state.mkdir()
                    if outcome == "leases":
                        (state / "temp").mkdir()
                        (state / "temp/registry.yml").write_text("source lease")
                    elif outcome == "active":
                        (state / "state").mkdir()
                        (state / "state/active.yml").write_text("source deployment")
                    elif outcome == "public-mode":
                        target.chmod(0o755)
                    return 0, json.dumps({"ok": True, "workspace": str(target if outcome != "wrong-workspace" else probe.root),
                                          "config_source": "source.yml" if outcome == "imported" else ""}), b""
                with self.subTest(outcome=outcome):
                    probe.cli = Mock(side_effect=init)
                    with self.assertRaises(suite.core.ProbeFailure):
                        probe.restore_backup(probe.root / "backup", "backup-id", target)
                    probe.cli.assert_called_once()

    def test_restore_success_requires_target_backup_and_structural_verification(self):
        target = Path("/synthetic/restored")
        good = {"ok": True, "workspace": str(target), "backup_id": "backup-id", "mode": "copy",
                "verify": {"ok": True, "checked": 7, "problems": []}}
        suite.require_backup_restore_result(good, target, "backup-id")
        for changed in ({**good, "ok": False}, {**good, "workspace": "/synthetic/source"}, {**good, "backup_id": "different"},
                        {**good, "mode": "unknown"}, {**good, "verify": None}, {**good, "verify": {"ok": False, "checked": 7, "problems": []}},
                        {**good, "verify": {"ok": True, "checked": 0, "problems": []}}, {**good, "verify": {"ok": True, "checked": True, "problems": []}},
                        {**good, "verify": {"ok": True, "checked": 7, "problems": [{"code": "metadata_unreadable"}]}}):
            with self.subTest(payload=changed), self.assertRaises(suite.core.ProbeFailure):
                suite.require_backup_restore_result(changed, target, "backup-id")

    def test_restored_external_root_must_fail_storage_preflight_not_usage(self):
        ids = {"producer": "source-id"}
        good = {"ok": False, "error": {"code": "temp_preflight_failed"}}
        suite.require_restored_root_refusal(1, good, ids, ids)
        for code, payload, after in ((0, good, ids), (1, {"ok": False, "error": {"code": "usage"}}, ids),
                                     (1, {"ok": False, "error": {"code": "start_failed"}}, ids),
                                     (1, {**good, "ok": True}, ids), (1, good, {"producer": "replacement"})):
            with self.subTest(code=code, payload=payload), self.assertRaises(suite.core.ProbeFailure):
                suite.require_restored_root_refusal(code, payload, ids, after)

    def test_snapshot_contract_uses_nested_id_label_completion_and_health(self):
        meta = {"id": "20261002T000000Z-manual", "label": "temp-exclusion", "complete": True, "deployment_id": "source-deployment"}
        good = {"ok": True, "snapshot": meta, "problems": []}
        self.assertEqual(suite.require_snapshot_metadata(good, "temp-exclusion"), meta)
        for changed in ({"ok": True, "id": meta["id"], "label": meta["label"], "problems": []},
                        {**good, "ok": False}, {**good, "problems": [{"code": "data_missing"}]}, {**good, "snapshot": None},
                        *({**good, "snapshot": {**meta, "id": identity}} for identity in ("", ".hidden", "../outside", "/absolute", "dir\\id", 7)),
                        {**good, "snapshot": {**meta, "label": "different"}}, {**good, "snapshot": {**meta, "complete": False}},
                        {**good, "snapshot": {**meta, "deployment_id": ""}}, {**good, "snapshot": {**meta, "deployment_id": None}}):
            with self.subTest(payload=changed), self.assertRaises(suite.core.ProbeFailure):
                suite.require_snapshot_metadata(changed, "temp-exclusion")

    def test_backup_restore_refuses_existing_or_linked_or_outside_targets_before_cli(self):
        with tempfile.TemporaryDirectory() as temporary:
            probe = object.__new__(suite.ExtendedSuite)
            probe.root = Path(temporary).resolve()
            existing, link = probe.root / "existing", probe.root / "link"
            existing.mkdir()
            (existing / "sentinel").write_text("preserve-existing")
            link.symlink_to(probe.root / "absent", target_is_directory=True)
            for target in (existing, link, probe.root.parent / "outside-restore"):
                with self.subTest(target=target):
                    probe.cli = Mock()
                    with self.assertRaises(suite.core.ProbeFailure):
                        probe.restore_backup(probe.root / "backup", "backup-id", target)
                    probe.cli.assert_not_called()
            self.assertEqual((existing / "sentinel").read_text(), "preserve-existing")

    def test_backup_resume_cannot_reallocate_or_change_original_source(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            paths = [root / str(index) for index in range(3)]
            for path in paths:
                path.mkdir()
                (path / "excluded-temp-sentinel").write_text("must-not-be-backed-up")
            containers = {str(index): {"Id": "original-" + str(index), "State": {"Running": True}, "Mounts": [{"Destination": "/runtime", "Source": str(path)}]} for index, path in enumerate(paths)}
            ids = {name: item["Id"] for name, item in containers.items()}
            status = {"workspace_id": "source-id", "applied_root": str(root), "directories": [{"path": str(path), "state": "active"} for path in paths]}
            suite.require_backup_source_preserved(ids, "previous", "source-id", paths, root, "previous", status, containers)
            for field, wrong in (("Id", "replacement"), ("State", {"Running": False})):
                original = containers["0"][field]
                containers["0"][field] = wrong
                with self.assertRaises(suite.core.ProbeFailure):
                    suite.require_backup_source_preserved(ids, "previous", "source-id", paths, root, "previous", status, containers)
                containers["0"][field] = original
            for changed_status in ({**status, "workspace_id": "copied-id"}, {**status, "directories": []}, {**status, "applied_root": str(root / "other")}):
                with self.assertRaises(suite.core.ProbeFailure):
                    suite.require_backup_source_preserved(ids, "previous", "source-id", paths, root, "previous", changed_status, containers)
            (paths[0] / "excluded-temp-sentinel").write_text("removed")
            with self.assertRaises(suite.core.ProbeFailure):
                suite.require_backup_source_preserved(ids, "previous", "source-id", paths, root, "previous", status, containers)

    def test_every_restored_or_copied_clone_checks_full_source_before_and_after(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            original_paths = [root / "source-tmp" / str(index) for index in range(3)]
            target_paths = [root / "target-tmp" / str(index) for index in range(3)]
            for path in original_paths + target_paths:
                path.mkdir(parents=True)
            for path in original_paths:
                (path / "excluded-temp-sentinel").write_text("must-not-be-backed-up")
            names = ("producer", "consumer", "passive")
            original = {name: {"Id": "source-" + name, "State": {"Running": True},
                               "Mounts": [{"Destination": "/runtime", "Source": str(original_paths[index])}]}
                        for index, name in enumerate(names)}
            before_ids = {name: item["Id"] for name, item in original.items()}
            source_status = {"workspace_id": "source-identity", "applied_root": str(root / "source-tmp"),
                             "directories": [{"path": str(path), "state": "active"} for path in original_paths]}
            target_status = {"workspace_id": "target-identity", "directories": [{"path": str(path), "state": "active"} for path in target_paths]}
            for suffix in ("snapshot", "send", "sendfile", "copy", "clone"):
                for change in ("none", "before", "during-apply"):
                    with self.subTest(suffix=suffix, change=change):
                        state = json.loads(json.dumps(original))
                        if change == "before":
                            state["producer"]["Id"] = "recreated-before-clone"
                        probe = object.__new__(suite.ExtendedSuite)
                        probe.clones, probe.modules = [], names
                        probe.fixture = root / "fixture"
                        probe.set_clone_prefix = Mock(return_value="target_")
                        probe.inspect = Mock(side_effect=lambda: state)
                        probe.active, probe.status = Mock(return_value="source-deployment"), Mock(return_value=source_status)
                        probe.docker_json = Mock(side_effect=lambda *args: [{"State": {"Running": True},
                                                                             "Mounts": [{"Destination": "/runtime", "Source": str(target_paths[names.index(args[1].removeprefix("target_"))])}]}])
                        def cli(*args, **kwargs):
                            if args[0] == "apply" and change == "during-apply":
                                state["producer"]["Id"] = "recreated-by-target"
                            return 0, json.dumps(target_status if args[:2] == ("temp", "status") else {"ok": True}), b""
                        probe.cli = Mock(side_effect=cli)
                        invoke = lambda: probe.verify_clone(root / ("restored-" + suffix), suffix, "source-identity", original_paths,
                                                            before_ids, "source-deployment", root / "source-tmp")
                        if change == "none":
                            invoke()
                            self.assertEqual(probe.inspect.call_count, 2)
                            self.assertEqual([call.args[0] for call in probe.cli.call_args_list], ["apply", "temp", "stop", "temp"])
                        else:
                            with self.assertRaises(suite.core.ProbeFailure):
                                invoke()
                            self.assertTrue(all(item["State"]["Running"] for item in state.values()))
                            self.assertTrue(all((path / "excluded-temp-sentinel").read_text() == "must-not-be-backed-up" for path in original_paths))
                            if change == "before":
                                probe.set_clone_prefix.assert_not_called()
                                probe.cli.assert_not_called()

    def test_snapshot_restore_recreates_runtime_without_changing_local_authorization(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            old_paths, new_paths = [root / ("old-" + str(index)) for index in range(3)], [root / ("new-" + str(index)) for index in range(3)]
            for path in new_paths:
                path.mkdir()
            before = {str(index): "old-id-" + str(index) for index in range(3)}
            containers = {str(index): {"Id": "new-id-" + str(index), "State": {"Running": True},
                                       "Mounts": [{"Destination": "/runtime", "Source": str(path)}]} for index, path in enumerate(new_paths)}
            status = {"workspace_id": "local-identity", "applied_root": str(root),
                      "directories": [{"path": str(path), "state": "active"} for path in new_paths]}
            verify = lambda items=containers, active="snapshot-deployment", state=status: suite.require_snapshot_restored_runtime(
                before, "snapshot-deployment", "local-identity", old_paths, root, active, state, items)
            verify()
            for active, state in (("foreign", status), ("snapshot-deployment", {**status, "workspace_id": "new-identity"}),
                                  ("snapshot-deployment", {**status, "applied_root": str(root / "other")}),
                                  ("snapshot-deployment", {**status, "directories": []})):
                with self.subTest(active=active, state=state), self.assertRaises(suite.core.ProbeFailure):
                    verify(active=active, state=state)
            for field, value in (("Id", before["0"]), ("State", {"Running": False}), ("Mounts", [{"Destination": "/runtime", "Source": str(new_paths[1])}])):
                changed = json.loads(json.dumps(containers))
                changed["0"][field] = value
                with self.subTest(field=field), self.assertRaises(suite.core.ProbeFailure):
                    verify(items=changed)
            old_paths[0].mkdir()
            with self.assertRaises(suite.core.ProbeFailure):
                verify()
            old_paths[0].rmdir()
            (new_paths[0] / "excluded-temp-sentinel").write_text("historical-content")
            with self.assertRaises(suite.core.ProbeFailure):
                verify()

    def test_snapshot_explicit_gc_reclaims_old_trees_without_replacing_new_runtime(self):
        for change in ("none", "id", "bind-map", "stopped", "marker", "missing-marker", "symlink-marker",
                       "old-retained", "registry", "ok-false", "nonzero"):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary).resolve()
                old_paths = [root / ("old-" + str(index)) for index in range(3)]
                paths = [root / ("new-" + str(index)) for index in range(3)]
                for index, path in enumerate(paths):
                    path.mkdir()
                    (path / ".anas-temp-owner.yml").write_text("new-owner-" + str(index))
                for path in old_paths:
                    path.mkdir()
                state = {str(index): {"Id": "new-id-" + str(index), "State": {"Running": True},
                                     "Mounts": [{"Destination": "/runtime", "Source": str(path)}]} for index, path in enumerate(paths)}
                status = {"directories": [{"path": str(path), "state": "active"} for path in paths]}
                probe = object.__new__(suite.ExtendedSuite)
                probe.workspace = root / "workspace"
                order = []
                def inspect():
                    order.append("inspect")
                    if order == ["inspect"]:
                        self.assertTrue(all(path.is_dir() for path in old_paths))
                    return json.loads(json.dumps(state))
                def cli(*args):
                    order.append("gc")
                    self.assertEqual(args, ("temp", "gc", "-w", str(probe.workspace), "--json"))
                    if change != "old-retained":
                        for path in old_paths:
                            path.rmdir()
                    if change == "id":
                        state["0"]["Id"] = "recreated-by-gc"
                    elif change == "bind-map":
                        state["0"]["Mounts"], state["1"]["Mounts"] = state["1"]["Mounts"], state["0"]["Mounts"]
                    elif change == "stopped":
                        state["0"]["State"]["Running"] = False
                    elif change == "marker":
                        (paths[0] / ".anas-temp-owner.yml").write_text("reissued-owner")
                    elif change in ("missing-marker", "symlink-marker"):
                        (paths[0] / ".anas-temp-owner.yml").unlink()
                        if change == "symlink-marker":
                            (paths[0] / ".anas-temp-owner.yml").symlink_to(paths[1] / ".anas-temp-owner.yml")
                    elif change == "registry":
                        status["directories"] = []
                    return (1 if change == "nonzero" else 0), json.dumps({"ok": change != "ok-false"}), b""
                probe.inspect, probe.cli, probe.status = Mock(side_effect=inspect), Mock(side_effect=cli), Mock(return_value=status)
                if change == "none":
                    actual_status, actual_containers = probe.gc_snapshot_released(root, old_paths)
                    self.assertEqual(actual_status, status)
                    self.assertEqual(actual_containers, state)
                    self.assertEqual(order, ["inspect", "gc", "inspect"])
                    probe.status.assert_called_once_with()
                else:
                    with self.assertRaises(suite.core.ProbeFailure):
                        probe.gc_snapshot_released(root, old_paths)
                    self.assertEqual(order[:2], ["inspect", "gc"])

    def test_spawned_gc_retains_the_actual_recovery_document_for_its_oracle(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary)
            (path / "crash-sentinel").write_text("preserve-before-commit")
            probe = object.__new__(suite.ExtendedSuite)
            probe.anas, probe.interrupted_processes = sys.executable, []
            script = "import json,sys; print(json.dumps({'ok':False,'error':{'code':'temp_recovery_required'}})); sys.exit(4)"
            process = probe.spawn_cli(["-c", script], capture_output=True)
            output, _ = process.communicate(timeout=5)
            self.assertEqual(process.returncode, 4)
            suite.require_interrupted_gc(process.returncode, json.loads(output), [path], True)

    def test_uncommitted_gc_must_refuse_and_preserve_contents(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary)
            sentinel = path / "crash-sentinel"
            sentinel.write_text("preserve-before-commit")
            refusal = {"ok": False, "error": {"code": "temp_recovery_required"}}
            suite.require_interrupted_gc(4, refusal, [path], True)
            for code, payload in ((0, refusal), (1, refusal), (2, {"ok": False, "error": {"code": "usage"}}),
                                  (1, {"ok": False, "error": {"code": "temp_storage_failed"}}),
                                  (4, {"ok": False, "error": {"code": "compose_missing"}}), (4, {"ok": True})):
                with self.subTest(code=code, payload=payload), self.assertRaises(suite.core.ProbeFailure):
                    suite.require_interrupted_gc(code, payload, [path], True)
            sentinel.write_text("wrong")
            with self.assertRaises(suite.core.ProbeFailure):
                suite.require_interrupted_gc(4, refusal, [path], True)

    def test_committed_interrupted_gc_requires_successful_actual_deletion(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "old"
            suite.require_interrupted_gc(0, {"ok": True}, [path], False)
            for code, payload in ((2, {"ok": False, "error": {"code": "usage"}}),
                                  (1, {"ok": False, "error": {"code": "temp_storage_failed"}}),
                                  (4, {"ok": False, "error": {"code": "temp_recovery_required"}}), (0, {"ok": False})):
                with self.subTest(code=code, payload=payload), self.assertRaises(suite.core.ProbeFailure):
                    suite.require_interrupted_gc(code, payload, [path], False)
            path.mkdir()
            with self.assertRaises(suite.core.ProbeFailure):
                suite.require_interrupted_gc(0, {"ok": True}, [path], False)

    def test_transition_cannot_be_inferred_without_persisted_phase(self):
        self.assertEqual(suite.transition_phase("transition:\n    phase: starting\n"), "starting")
        with self.assertRaises(suite.core.ProbeFailure):
            suite.transition_phase("applied_root: /somewhere\n")

    def test_failed_stop_is_not_reported_as_complete_cleanup_and_other_stops_continue(self):
        probe = object.__new__(suite.ExtendedSuite)
        probe.interrupted_processes = []
        probe.blocked_shims = {}
        probe.clones = []
        probe.prefix = "owned_"
        probe.workspace = Path("/synthetic/workspace")
        probe.modules = ("producer", "consumer", "passive")
        probe.docker = "docker"
        probe.stop_owned_container = Mock(side_effect=[suite.core.ProbeFailure("synthetic stop failed"), RuntimeError("stop error"), None])
        probe.private_exception = Mock()
        probe.cleanup_state = "not_attempted"
        probe.cleanup_errors = []
        with self.assertRaises(suite.core.ProbeFailure):
            probe.stop_for_inspection()
        self.assertEqual(probe.cleanup_state, "failed")
        self.assertEqual(probe.stop_owned_container.call_count, 3)
        self.assertEqual(len(probe.cleanup_errors), 2)
        self.assertIn("ProbeFailure", probe.cleanup_errors[0])

    def test_cleanup_failure_requires_retained_retry_marker_and_new_active_leases(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            old, new = root / "old", root / "new"
            old.mkdir()
            new.mkdir()
            immutable = old / "cleanup-must-retry"
            immutable.write_text("preserve-on-cleanup-failure")
            marker = old / ".anas-temp-owner.yml"
            marker.touch()
            status = {"applied_root": str(root / "target"), "transition": {"phase": "cleanup_deferred"},
                      "directories": [{"path": str(old), "state": "released"}, {"path": str(new), "state": "active"}]}
            suite.require_cleanup_pending("new", "old", status, [old], [new], immutable, root / "target")
            with self.assertRaises(suite.core.ProbeFailure):
                suite.require_cleanup_pending("old", "old", status, [old], [new], immutable, root / "target")
            status["transition"]["phase"] = "complete"
            with self.assertRaises(suite.core.ProbeFailure):
                suite.require_cleanup_pending("new", "old", status, [old], [new], immutable, root / "target")
            status["transition"]["phase"] = "cleanup_deferred"
            marker.unlink()
            with self.assertRaises(suite.core.ProbeFailure):
                suite.require_cleanup_pending("new", "old", status, [old], [new], immutable, root / "target")

    def test_wrong_mount_refusal_requires_real_fault_and_unchanged_applied_state(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            old = root / "old"
            old.mkdir()
            (old / "wrong-mount-old-sentinel").write_text("must-preserve-before-commit")
            marker = root / "actual.json"
            wrong, target = root / "wrong", root / "target"
            marker.write_text(json.dumps({"container_id": "actual-id", "actual_source": str(wrong),
                                          "registered_source": str(target / "registered"), "destination": "/runtime"}))
            status = {"applied_root": str(root / "old-root")}
            failure = {"ok": False, "error": {"code": "start_failed"}}
            suite.require_wrong_mount_refusal(1, failure, marker, wrong, target, "old-id", "old-id", status, [old])
            for code, active, reported in ((0, "old-id", status), (1, "new-id", status), (1, "old-id", {"applied_root": str(target)})):
                with self.subTest(code=code, active=active), self.assertRaises(suite.core.ProbeFailure):
                    suite.require_wrong_mount_refusal(code, failure, marker, wrong, target, active, "old-id", reported, [old])
            for code, payload in ((2, {"ok": False, "error": {"code": "usage"}}),
                                  (1, {"ok": False, "error": {"code": "temp_storage_failed"}}),
                                  (1, {"ok": False, "error": {"code": "compose_missing"}}), (1, {"ok": True})):
                with self.subTest(code=code, payload=payload), self.assertRaises(suite.core.ProbeFailure):
                    suite.require_wrong_mount_refusal(code, payload, marker, wrong, target, "old-id", "old-id", status, [old])
            marker.unlink()
            with self.assertRaises(suite.core.ProbeFailure):
                suite.require_wrong_mount_refusal(1, failure, marker, wrong, target, "old-id", "old-id", status, [old])

    def test_generated_wrong_bind_shim_observes_the_actual_source_before_claiming_injection(self):
        class Generated(Exception):
            pass
        with tempfile.TemporaryDirectory() as temporary:
            probe = object.__new__(suite.ExtendedSuite)
            probe.root = Path(temporary).resolve()
            probe.root_a, probe.root_b = probe.root / "a", probe.root / "b"
            probe.root_a.mkdir()
            probe.root_b.mkdir()
            old_paths = [probe.root_a / str(index) for index in range(3)]
            for path in old_paths:
                path.mkdir()
            probe.prefix = "owned_"
            probe.container_uid = probe.container_gid = 1001
            probe.inspect = Mock()
            probe.active = Mock(return_value="old-id")
            probe.privileged = Mock()
            probe.docker = str(probe.root / "real-docker")
            Path(probe.docker).write_text("#!/usr/bin/env python3\nimport json,os,sys\nfrom pathlib import Path\ns=Path(os.environ['FAKE_STATE'])\nif 'compose' in sys.argv: s.write_text(os.environ['ANAS_TEMP_RUNTIME'])\nelse: print(json.dumps([{'Id':'real-observation','State':{'Running':True},'Mounts':[{'Destination':'/runtime','Source':s.read_text()}]}]))\n")
            Path(probe.docker).chmod(0o700)
            probe.set_root = Mock(side_effect=[None, Generated()])
            with patch.object(suite.core, "require_mounts", return_value=old_paths), self.assertRaises(Generated):
                probe.wrong_mount()
            environment = probe.set_root.call_args.kwargs["env"]
            shim = probe.root / "extended-wrong-mount-bin/docker"
            compile(shim.read_text(), str(shim), "exec")
            registered = probe.root_b / "registered"
            environment.update(ANAS_TEMP_RUNTIME=str(registered), FAKE_STATE=str(probe.root / "fake-state"))
            result = subprocess.run([sys.executable, str(shim), "compose", "-p", "owned_producer", "up", "-d"], env=environment, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr.decode())
            marker = probe.root / "extended-wrong-mount-bin/actual-mount.json"
            evidence = json.loads(marker.read_text())
            self.assertEqual(evidence["registered_source"], str(registered))
            self.assertEqual(evidence["actual_source"], str(probe.root / "extended-unregistered-mount"))
            # A subsequent compensation start uses real Docker unchanged;
            # a one-shot injection must not sabotage restoration too.
            environment["ANAS_TEMP_RUNTIME"] = str(probe.root_a / "0")
            second = subprocess.run([sys.executable, str(shim), "compose", "-p", "owned_producer", "up", "-d"], env=environment, capture_output=True)
            self.assertEqual(second.returncode, 0, second.stderr.decode())
            self.assertEqual((probe.root / "fake-state").read_text(), str(probe.root_a / "0"))

    def test_report_only_claims_passed_requirements_and_lists_other_e2e_requirements_as_not_run(self):
        with tempfile.TemporaryDirectory() as temporary:
            probe = object.__new__(suite.ExtendedSuite)
            probe.report_dir = Path(temporary)
            probe.anas = str(probe.report_dir / "binary")
            Path(probe.anas).write_bytes(b"test-only-binary")
            probe.section = "faults"
            probe.run_id = "local-only"
            probe.source_digest = "sha256:" + "a" * 64
            probe.results = [{"case_id": "TEMP-T-024", "status": "passed", "requirements": ["TEMP-R-036"]},
                             {"case_id": "TEMP-T-024", "status": "failed", "requirements": ["TEMP-R-045"]}]
            probe.failure_detail = None
            probe.cleanup_state = "failed"
            probe.cleanup_errors = ["synthetic cleanup failure"]
            probe.mounts = {}
            probe.immutable_files = set()
            probe.write_report("failed", "synthetic acceptance failure")
            report = json.loads((probe.report_dir / "workspace-temp-storage-extended-faults.json").read_text())
            self.assertEqual(report["status"], "failed")
            self.assertEqual(report["covered"], ["TEMP-R-036"])
            self.assertIn("TEMP-R-045", report["not_run"])
            self.assertEqual(len(report["not_run"]), 22)
            self.assertEqual(report["cleanup"], "failed")


if __name__ == "__main__":
    unittest.main()
