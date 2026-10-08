#!/usr/bin/env python3
"""Crash-driver qualification only; never a real ANAS/Btrfs host acceptance."""
import argparse
import importlib.util
import json
import os
import pathlib
import platform
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

SOURCE = pathlib.Path(__file__).with_name("immich-workspace-e2e.py")
spec = importlib.util.spec_from_file_location("immich_workspace_crash", SOURCE)
harness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(harness)
MODULES = SOURCE.parents[2] / "modules"


def proc_entry(root, pid, *, parent, group, session, start=123, executable=None, cwd=None):
    directory = pathlib.Path(root) / str(pid)
    directory.mkdir(exist_ok=True)
    fields = ["S", str(parent), str(group), str(session)] + ["0"] * 15 + [str(start)]
    (directory / "stat").write_text(str(pid) + " (test process) " + " ".join(fields))
    if executable:
        (directory / "exe").symlink_to(executable)
    if cwd:
        (directory / "cwd").symlink_to(cwd)
    return harness.linux_process(pid, proc=root)


class CrashTests(unittest.TestCase):
    def test_pause_requires_independent_private_absolute_markers_and_fixed_success_anchor(self):
        with tempfile.TemporaryDirectory() as directory:
            pause = harness.postgres_fixture_sources(MODULES, pause_after_maintenance=pathlib.Path(directory).resolve())
            self.assertEqual(set(pause), {"postgres/hook/main.go"})
            source = pause["postgres/hook/main.go"]
            completed = source.index("if err := maintainExtensions(env);")
            ready = source.index("extension-crash-hook-ready.json")
            self.assertLess(completed, ready)
            self.assertIn('env["ANAS_POSTGRES_EXTENSION_MAINTENANCE"] == "true"', source[completed:ready])
            self.assertIn(json.dumps(str(pathlib.Path(directory).resolve() / "extension-crash-hook-release")), source)
            self.assertIn('"pid": os.Getpid(), "parent_pid": os.Getppid(), "workdir": req.Workdir', source)
            self.assertIn("os.Rename(ready + \".tmp\", ready)", source)
            self.assertIn("marker, 0600", source)
            self.assertNotIn("test-only failure after completed", source)
            self.assertIn('validate_crash_hook(launch, marker, frozen, frozen / ".hook.bin", self.args.anas)', SOURCE.read_text())
            self.assertNotIn('digest(frozen / "hook/main.go")', SOURCE.read_text())
            self.assertIn('digest(frozen / ".hook.bin") == digest(self.workspace / ".anas/hook-bin/postgres")', SOURCE.read_text())
            with self.assertRaisesRegex(harness.Blocked, "independent"):
                harness.postgres_fixture_sources(MODULES, fail_after_maintenance=True, pause_after_maintenance=pathlib.Path(directory).resolve())
            with self.assertRaisesRegex(harness.Blocked, "absolute reports"):
                harness.postgres_fixture_sources(MODULES, pause_after_maintenance="relative")

    def test_test_only_pg_prebuilt_binary_is_removed_without_changing_other_modules_or_source(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory).resolve()
            modules = root / "source/modules"
            for name in ("postgres", "other"):
                binary = modules / name / "hook/bin/linux-amd64/anas-hook"
                binary.parent.mkdir(parents=True)
                binary.write_bytes(name.encode())
            (modules / "postgres/hook/main.go").write_text("original")
            for name in ("go.mod", "go.sum"):
                (modules.parent / name).write_text("private-fixture-metadata")
            (modules.parent / "contracts").mkdir()
            reports = root / "reports"
            reports.mkdir()
            with mock.patch.dict(os.environ, {"DOCKER_HOST": "unix:///run/anas-crash-test.sock"}):
                suite = harness.Suite(argparse.Namespace(), root / "workspace", modules)
            suite.reports = reports
            with mock.patch.object(harness, "postgres_fixture_sources", return_value={"postgres/hook/main.go": "paused"}) as prepare:
                copy = suite.module_fixture("pause-copy", pause_after_maintenance=reports)
            prepare.assert_called_once_with(modules, fail_after_maintenance=False, pause_after_maintenance=reports)
            self.assertFalse((copy / "postgres/hook/bin").exists())
            self.assertEqual((copy / "other/hook/bin/linux-amd64/anas-hook").read_bytes(), b"other")
            self.assertEqual((modules / "postgres/hook/bin/linux-amd64/anas-hook").read_bytes(), b"postgres")
            self.assertEqual((modules / "postgres/hook/main.go").read_text(), "original")

    def test_fixture_copies_current_contract_catalog_and_rejects_drift(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory).resolve()
            modules = root / "source/modules"
            (modules / "postgres/hook").mkdir(parents=True)
            (modules / "postgres/hook/main.go").write_text("original")
            for name in ("go.mod", "go.sum"):
                (modules.parent / name).write_text("same-source metadata")
            resource = modules.parent / "contracts/relational_database/schemas/resource.yml"
            resource.parent.mkdir(parents=True)
            resource.write_text("postgres.extensions: current names-only contract\n")
            reports = root / "reports"
            reports.mkdir()
            with mock.patch.dict(os.environ, {"DOCKER_HOST": "unix:///run/anas-crash-test.sock"}):
                suite = harness.Suite(argparse.Namespace(), root / "workspace", modules)
            suite.reports = reports
            with mock.patch.object(harness, "postgres_fixture_sources", return_value={}):
                old = suite.module_fixture("old")
                copied = old.parent / "contracts/relational_database/schemas/resource.yml"
                self.assertEqual(copied.read_bytes(), resource.read_bytes())
                second = suite.module_fixture("failure")
                self.assertEqual(second.parent / "contracts", old.parent / "contracts")
                copied.write_text("unqualified catalog")
                with self.assertRaisesRegex(RuntimeError, "Contract catalog changed"):
                    suite.module_fixture("crash")
            self.assertEqual(resource.read_text(), "postgres.extensions: current names-only contract\n")

    def test_hook_requires_exact_live_cli_parent_session_workdir_and_binary(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory).resolve()
            proc = root / "proc"
            proc.mkdir()
            cli, hook = root / "anas", root / "hook"
            cli.write_bytes(b"CLI"); hook.write_bytes(b"Hook")
            frozen = root / "frozen-postgres"
            frozen.mkdir()
            launch = proc_entry(proc, 200, parent=100, group=200, session=200, executable=cli)
            proc_entry(proc, 210, parent=200, group=210, session=200, executable=hook, cwd=frozen)
            marker = {"pid": 210, "parent_pid": 200, "workdir": str(frozen), "phase": "after_start", "maintenance_complete": True}
            self.assertEqual(harness.validate_crash_hook(launch, marker, frozen, hook, cli, proc=proc)["pid"], 210)
            for change in ({"parent_pid": 999}, {"workdir": "/outside"}, {"maintenance_complete": False}, {"pid": True}):
                with self.subTest(change=change), self.assertRaises(harness.Blocked):
                    harness.validate_crash_hook(launch, {**marker, **change}, frozen, hook, cli, proc=proc)
            (proc / "210/stat").write_text((proc / "210/stat").read_text().replace("S 200 210 200", "S 200 210 999"))
            with self.assertRaisesRegex(harness.Blocked, "does not belong"):
                harness.validate_crash_hook(launch, marker, frozen, hook, cli, proc=proc)

    def test_pid_reuse_or_unproven_descendant_never_authorizes_a_signal(self):
        with tempfile.TemporaryDirectory() as directory:
            proc = pathlib.Path(directory)
            launch = proc_entry(proc, 200, parent=100, group=200, session=200)
            proc_entry(proc, 210, parent=200, group=210, session=200)
            proc_entry(proc, 999, parent=1, group=999, session=999)
            groups = harness.capture_cli_process_groups(launch, proc=proc)
            self.assertEqual(set(groups), {200, 210})
            with mock.patch.object(harness.os, "killpg") as kill:
                harness.kill_captured_process_groups(groups, 200, proc=proc)
                self.assertEqual(kill.call_args_list, [mock.call(200, signal.SIGKILL), mock.call(210, signal.SIGKILL)])
            stat = proc / "200/stat"
            stat.write_text(stat.read_text().rsplit(" ", 1)[0] + " 999")
            with self.assertRaisesRegex(harness.Blocked, "identity changed"):
                harness.capture_cli_process_groups(launch, proc=proc)
            with mock.patch.object(harness.os, "killpg") as kill, self.assertRaisesRegex(harness.Blocked, "changed since ownership"):
                harness.kill_captured_process_groups(groups, 200, proc=proc)
            kill.assert_not_called()
            stat.write_text(stat.read_text().rsplit(" ", 1)[0] + " 123")
            proc_entry(proc, 230, parent=999, group=230, session=200)
            with self.assertRaisesRegex(harness.Blocked, "unproven process"):
                harness.capture_cli_process_groups(launch, proc=proc)

    @unittest.skipUnless(shutil.which("go"), "Go is needed to compile exact test-only Hook sources")
    def test_all_hook_fixtures_compile_and_pause_release_follows_completed_maintenance(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory).resolve()
            reports = root / "reports"
            reports.mkdir()
            variants = {
                "old": harness.postgres_fixture_sources(MODULES).get("postgres/hook/main.go", (MODULES / "postgres/hook/main.go").read_text()),
                "failure": harness.postgres_fixture_sources(MODULES, fail_after_maintenance=True)["postgres/hook/main.go"],
                "pause": harness.postgres_fixture_sources(MODULES, pause_after_maintenance=reports)["postgres/hook/main.go"],
            }
            binaries = {}
            for name, source in variants.items():
                file = root / (name + ".go")
                file.write_text(source)
                binary = root / (name + "-hook")
                built = subprocess.run([shutil.which("go"), "build", "-o", str(binary), str(file)], cwd=root, capture_output=True, timeout=120)
                self.assertEqual(built.returncode, 0, built.stderr.decode())
                binaries[name] = binary
            # This double qualifies the Go fault-point driver only; actual
            # PostgreSQL/ANAS/SIGKILL acceptance belongs to the guarded suite.
            tools = root / "tools"
            tools.mkdir()
            docker = tools / "docker"
            docker.write_text("#!/bin/sh\nexit ${ANAS_CRASH_DRIVER_DOCKER_STATUS:-0}\n")
            docker.chmod(0o700)
            environment = {**os.environ, "PATH": str(tools) + os.pathsep + os.environ["PATH"]}
            request = {"abi": "anas.module-hook/v1", "phase": "after_start", "module": "postgres", "workdir": str(root), "env": {"POSTGRES_HOST": "private-driver-postgres", "ANAS_POSTGRES_EXTENSION_MAINTENANCE": "true"}}
            payload = json.dumps(request).encode()
            failed = subprocess.run([binaries["pause"]], input=payload, capture_output=True, env={**environment, "ANAS_CRASH_DRIVER_DOCKER_STATUS": "1"}, timeout=10)
            self.assertNotEqual(failed.returncode, 0)
            self.assertFalse((reports / "extension-crash-hook-ready.json").exists())
            regular = subprocess.run([binaries["pause"]], input=json.dumps({**request, "env": {"POSTGRES_HOST": "private-driver-postgres"}}).encode(), capture_output=True, env=environment, timeout=10)
            self.assertEqual(regular.returncode, 0)
            self.assertFalse((reports / "extension-crash-hook-ready.json").exists())
            process = subprocess.Popen([binaries["pause"]], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=environment)
            try:
                process.stdin.write(payload); process.stdin.close(); process.stdin = None
                deadline = time.monotonic() + 10
                ready = reports / "extension-crash-hook-ready.json"
                while not ready.exists() and time.monotonic() < deadline:
                    time.sleep(0.02)
                self.assertTrue(ready.exists(), "compiled post-maintenance Hook did not pause")
                marker = json.loads(ready.read_text())
                self.assertEqual(marker["pid"], process.pid)
                self.assertEqual(marker["parent_pid"], os.getpid())
                self.assertEqual(marker["workdir"], str(root))
                self.assertIsNone(process.poll())
                (reports / "extension-crash-hook-release").write_text("released")
                output, error = process.communicate(timeout=10)
                self.assertEqual(process.returncode, 0, error.decode())
                self.assertEqual(json.loads(output), {})
                retry = subprocess.run([binaries["pause"]], input=payload, capture_output=True, env=environment, timeout=10)
                self.assertEqual(retry.returncode, 0)
                self.assertEqual(json.loads(ready.read_text())["pid"], marker["pid"], "same candidate retry replaced its original process evidence")
            finally:
                if process.poll() is None:
                    process.kill(); process.communicate(timeout=10)

    @unittest.skipUnless(platform.system() == "Linux", "actual /proc and separate Hook groups require Linux")
    def test_actual_linux_cli_and_separately_grouped_child_receive_sigkill_without_touching_unrelated_process(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory).resolve()
            marker = root / "ready.json"
            code = '''import json,os,pathlib,time
child=os.fork()
if child==0:
    os.setpgid(0,0)
    pathlib.Path(%r).write_text(json.dumps({'pid':os.getpid(),'parent_pid':os.getppid(),'workdir':os.getcwd(),'phase':'after_start','maintenance_complete':True}))
    time.sleep(60)
else:
    time.sleep(60)
''' % str(marker)
            unrelated = subprocess.Popen([sys.executable, "-c", "import time;time.sleep(60)"], start_new_session=True)
            cli = subprocess.Popen([sys.executable, "-c", code], start_new_session=True, cwd=root)
            launch = harness.linux_process(cli.pid)
            groups = {}
            try:
                deadline = time.monotonic() + 10
                while not marker.exists() and time.monotonic() < deadline:
                    time.sleep(0.02)
                self.assertTrue(marker.exists())
                ready = json.loads(marker.read_text())
                child = ready["pid"]
                executable = pathlib.Path(sys.executable).resolve()
                self.assertEqual(harness.validate_crash_hook(launch, ready, root, executable, executable)["pid"], child)
                with self.assertRaisesRegex(harness.Blocked, "does not belong"):
                    harness.validate_crash_hook(launch, {**ready, "pid": unrelated.pid}, root, executable, executable)
                groups = harness.capture_cli_process_groups(launch)
                self.assertEqual(set(groups), {cli.pid, child})
                harness.kill_captured_process_groups(groups, cli.pid)
                self.assertEqual(cli.wait(timeout=10), -signal.SIGKILL)
                deadline = time.monotonic() + 5
                while (harness.linux_process(child) or {"state": "Z"})["state"] != "Z" and time.monotonic() < deadline:
                    time.sleep(0.02)
                self.assertEqual((harness.linux_process(child) or {"state": "Z"})["state"], "Z")
                self.assertIsNone(unrelated.poll())
            finally:
                if not groups:
                    groups = harness.capture_cli_process_groups(launch)
                harness.kill_captured_process_groups(groups, cli.pid)
                if cli.poll() is None:
                    cli.wait(timeout=10)
                unrelated.kill(); unrelated.wait(timeout=10)


if __name__ == "__main__":
    unittest.main()
