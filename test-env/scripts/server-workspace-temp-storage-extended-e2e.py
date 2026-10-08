#!/usr/bin/env python3
# TEST_CASES: TEMP-T-012, TEMP-T-017, TEMP-T-018, TEMP-T-019, TEMP-T-020, TEMP-T-024
"""Run-scoped loopback, backup and crash probes, against real ANAS and Docker.

This extends the core fixture without changing its script or bootstrapping a
daemon. It creates only paths below the exact authorized finance run root.
"""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys
import time

_spec = importlib.util.spec_from_file_location("temp_core", Path(__file__).with_name("server-workspace-temp-storage-e2e.py"))
core = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(core)
check = core.check

E2E_REQUIREMENTS = {"TEMP-R-" + str(number).zfill(3) for number in
                    (5, 6, 9, 10, 14, 15, 16, 17, 19, 23, 24, 26, 27, 29, 30, 33, 35, 36, 39, 41, 43, 44, 45)}
CASE_REQUIREMENTS = {
    "TEMP-T-012": ["TEMP-R-005", "TEMP-R-006"],
    "TEMP-T-017": ["TEMP-R-023", "TEMP-R-024", "TEMP-R-025"],
    "TEMP-T-018": ["TEMP-R-026", "TEMP-R-027"],
    "TEMP-T-019": ["TEMP-R-041"],
    "TEMP-T-020": ["TEMP-R-019", "TEMP-R-039", "TEMP-R-043"],
    "TEMP-T-024": ["TEMP-R-016", "TEMP-R-036", "TEMP-R-045"],
}


def require_namespace_identities(current, init, holder, docker, containerd):
    check(current != init and current == holder == docker == containerd,
          "loopback probes require this run's private mount namespace shared with dockerd and containerd")


def command_flag(arguments, flag):
    for index, value in enumerate(arguments):
        if value == flag and index + 1 < len(arguments):
            return arguments[index + 1]
        if value.startswith(flag + "="):
            return value[len(flag) + 1:]
    return None


def require_private_mount_namespace():
    root = Path(os.environ["ANAS_TEST_WORK_ROOT"])
    holder = os.environ.get("ANAS_TEST_MOUNT_NAMESPACE", "")
    check(re.fullmatch(r"/proc/[1-9][0-9]*/ns/mnt", holder) is not None,
          "ANAS_TEST_MOUNT_NAMESPACE must identify the run's persistent holder")
    identities = []
    for variable, executable, flag, expected in (
        ("ANAS_TEST_DOCKER_PID", "dockerd", "--data-root", root / "docker"),
        ("ANAS_TEST_CONTAINERD_PID", "containerd", "--root", root / "containerd"),
    ):
        pid = os.environ.get(variable, "")
        check(re.fullmatch(r"[1-9][0-9]*", pid) is not None, variable + " is required")
        proc = Path("/proc") / pid
        arguments = proc.joinpath("cmdline").read_bytes().decode().rstrip("\0").split("\0")
        check(arguments and Path(arguments[0]).name == executable and command_flag(arguments, flag) == str(expected),
              executable + " must use this exact run's private data root")
        identities.append(proc.joinpath("ns/mnt").stat().st_ino)
    require_namespace_identities(Path("/proc/self/ns/mnt").stat().st_ino, Path("/proc/1/ns/mnt").stat().st_ino,
                                 Path(holder).stat().st_ino, *identities)


def process_identity(pid):
    # The executable name may contain spaces or ')'; fields after the final
    # closing parenthesis start at the state field. Start time detects PID reuse.
    fields = Path("/proc", str(pid), "stat").read_text().rsplit(")", 1)[1].split()
    return {"ppid": int(fields[1]), "pgid": int(fields[2]), "session_id": int(fields[3]), "start_time": int(fields[19])}


def require_blocker_identity(record, parent_pid, current):
    pid = record.get("pid")
    check(type(pid) is int and pid > 1 and record.get("ppid") == parent_pid and record.get("pgid") == pid
          and record.get("session_id") == parent_pid == current.get("session_id")
          and record.get("inherited_pgid") in (parent_pid, pid)
          and record.get("start_time") == current.get("start_time") and current.get("ppid") == parent_pid
          and current.get("pgid") == pid, "paused Docker shim is not this CLI's owned subprocess group")
    return pid


def require_run_path(root, path):
    check(root.is_absolute() and path.is_absolute() and path != root and path.is_relative_to(root),
          "filesystem fixture must stay beneath this exact run root")
    check(root.resolve() == root and path.parent.resolve().is_relative_to(root),
          "filesystem fixture parent crosses a symlink boundary")
    check(not path.is_symlink(), "filesystem fixture is a symlink")


def require_storage_problem(status, present):
    check(isinstance(status.get("issues"), list), "storage issue contract is missing")
    found = any(issue.get("code") in ("temp_low_space", "temp_unavailable") for issue in status["issues"])
    check(found == present, "real filesystem shortage/recovery is not reflected by the storage issue contract")


def require_runtime_storage(status, state):
    check(isinstance(status.get("module_runtime"), list), "ordinary runtime status has no Module projection")
    modules = {item.get("module"): item for item in status["module_runtime"]}
    for name in ("producer", "consumer"):
        check(modules.get(name, {}).get("temp_storage", {}).get("state") == state,
              "ordinary Module runtime status omitted temporary shortage/recovery for " + name)


def require_restore_isolation(source_id, source_paths, target_status):
    check(target_status.get("workspace_id") and target_status["workspace_id"] != source_id,
          "restore inherited source workspace temporary identity")
    paths = {Path(item["path"]) for item in target_status.get("directories", []) if item.get("state") == "active"}
    check(len(paths) == 3 and paths.isdisjoint(set(source_paths)), "restore inherited source leases or did not allocate all declarations")
    check(all(path.is_dir() and not path.is_symlink() for path in paths), "restored temporary allocation is absent")
    return paths


def require_initialized_restore_target(payload, target):
    check(payload.get("ok") is True and payload.get("workspace") == str(target)
          and payload.get("config_source") == "", "restore target was not initialized by the real empty-workspace CLI")
    state = target / ".anas"
    check(target.is_dir() and not target.is_symlink() and target.stat().st_mode & 0o777 == 0o700
          and state.is_dir() and not state.is_symlink(), "restore initialization did not produce a private workspace")
    check(not (state / "temp").exists() and not (state / "temp").is_symlink()
          and not (state / "state/active.yml").exists() and not (state / "state/active.yml").is_symlink(),
          "restore initialization unexpectedly acquired temporary leases or an active deployment")


def require_backup_restore_result(payload, target, backup_id):
    check(payload.get("ok") is True and payload.get("workspace") == str(target)
          and payload.get("backup_id") == backup_id and payload.get("mode") in ("snapshot", "send", "send-file", "copy"),
          "backup restore did not return the requested target and backup")
    verify = payload.get("verify", {})
    check(isinstance(verify, dict) and verify.get("ok") is True and type(verify.get("checked")) is int
          and verify["checked"] > 0 and verify.get("problems") == [], "restored workspace failed structural verification")


def require_restored_root_refusal(code, payload, before_ids, after_ids):
    check(code != 0 and payload.get("ok") is False and payload.get("error", {}).get("code") == "temp_preflight_failed"
          and before_ids == after_ids, "restore external-root probe did not reject storage preflight or controlled source containers")


def require_snapshot_metadata(payload, label):
    snapshot = payload.get("snapshot", {})
    check(payload.get("ok") is True and payload.get("problems") == [] and isinstance(snapshot, dict),
          "workspace snapshot creation did not return healthy metadata")
    identity = snapshot.get("id")
    check(isinstance(identity, str) and identity and not identity.startswith(".") and "/" not in identity and "\\" not in identity
          and snapshot.get("label") == label and snapshot.get("complete") is True
          and isinstance(snapshot.get("deployment_id"), str) and snapshot["deployment_id"],
          "workspace snapshot id, label, or completion does not match the actual CLI contract")
    return snapshot


def require_history_missing_refusal(code, payload, before_ids, after_ids, previous, active, status, root, paths):
    check(code != 0 and payload.get("ok") is False and payload.get("error", {}).get("code") == "temp_preflight_failed",
          "missing historical filesystem was accepted or failed only because of invalid rollback arguments")
    check(before_ids == after_ids and active == previous and status.get("applied_root") == str(root),
          "historical filesystem preflight changed the current runtime")
    check(all(path.is_dir() and (path / "history-current-sentinel").read_text() == "preserve-current-runtime" for path in paths),
          "historical filesystem refusal removed current temporary contents")


def require_backup_source_preserved(before_ids, previous, source_id, source_paths, root, active, status, containers):
    check({name: item["Id"] for name, item in containers.items()} == before_ids and active == previous
          and status.get("workspace_id") == source_id and status.get("applied_root") == str(root),
          "backup pause/resume replaced source containers, deployment, identity, or temporary root")
    current = core.require_mounts(containers, root)
    registered = {Path(item["path"]) for item in status.get("directories", []) if item.get("state") == "active"}
    check(set(current) == set(source_paths) == registered and all(item["State"]["Running"] for item in containers.values())
          and all((path / "excluded-temp-sentinel").read_text() == "must-not-be-backed-up" for path in source_paths),
          "backup did not resume the original registered temporary leases and source contents")


def require_snapshot_restored_runtime(before_ids, previous, source_id, source_paths, root, active, status, containers):
    current_ids = {name: item["Id"] for name, item in containers.items()}
    check(current_ids.keys() == before_ids.keys() and set(current_ids.values()).isdisjoint(before_ids.values())
          and active == previous and status.get("workspace_id") == source_id and status.get("applied_root") == str(root),
          "snapshot restore changed workspace authorization or omitted container recreation")
    current = core.require_mounts(containers, root)
    registered = {Path(item["path"]) for item in status.get("directories", []) if item.get("state") == "active"}
    check(set(current) == registered and set(current).isdisjoint(source_paths)
          and all(item["State"]["Running"] for item in containers.values())
          and all(not path.exists() for path in source_paths)
          and all(not (path / "excluded-temp-sentinel").exists() for path in current),
          "snapshot restore reused historical contents or omitted fresh registered mounts")


def transition_phase(registry):
    match = re.search(r"^\s+phase:\s*(\S+)\s*$", registry, re.M)
    check(match is not None, "interrupted switch has no persisted transition phase")
    return match[1]


def require_interrupted_gc(code, payload, old_paths, expected_preserved):
    if expected_preserved:
        check(code == 4 and payload.get("ok") is False and payload.get("error", {}).get("code") == "temp_recovery_required",
              "GC did not reject the actual uncommitted transition with its recovery precondition")
        check(all(path.is_dir() and (path / "crash-sentinel").read_text() == "preserve-before-commit" for path in old_paths),
              "GC deleted old content before transition reconciliation")
    else:
        check(code == 0 and payload.get("ok") is True and all(not path.exists() for path in old_paths),
              "GC did not finish reclaiming the committed interrupted cleanup")


def require_cleanup_pending(active, previous, status, old_paths, new_paths, immutable, new_root):
    check(active != previous and status.get("applied_root") == str(new_root)
          and status.get("transition", {}).get("phase") == "cleanup_deferred",
          "cleanup failure reverted or failed to persist the committed new runtime")
    directories = {Path(item["path"]): item for item in status.get("directories", [])}
    check(immutable.parent in old_paths and directories.get(immutable.parent, {}).get("state") == "released"
          and immutable.read_text() == "preserve-on-cleanup-failure"
          and (immutable.parent / ".anas-temp-owner.yml").is_file(),
          "failed old-tree cleanup lost its content, ownership marker, or retry registration")
    active_paths = {path for path, item in directories.items() if item.get("state") == "active"}
    check(active_paths == set(new_paths), "cleanup failure did not retain the new active leases")


def require_wrong_mount_refusal(code, payload, marker, wrong, target_root, active, previous, status, old_paths):
    check(code == 1 and payload.get("ok") is False and payload.get("error", {}).get("code") == "start_failed"
          and marker.is_file(), "wrong-mount injection did not fail in the real activation chain")
    evidence = json.loads(marker.read_text())
    check(evidence.get("container_id") and evidence.get("actual_source") == str(wrong)
          and evidence.get("destination") == "/runtime"
          and Path(evidence.get("registered_source", "")).is_relative_to(target_root),
          "fault evidence does not prove an actual mismatched candidate bind")
    check(active == previous and status.get("applied_root") != str(target_root),
          "wrong temporary mount was committed as the new active runtime")
    check(all(path.is_dir() and (path / "wrong-mount-old-sentinel").read_text() == "must-preserve-before-commit" for path in old_paths),
          "wrong mount refusal lost old pre-commit content")


class ExtendedSuite(core.Suite):
    def __init__(self):
        os.environ.pop("DOCKER_CONTEXT", None)
        require_private_mount_namespace()
        super().__init__()
        self.fixture = self.root / "extended-fixture"
        self.workspace = self.root / "extended-workspace"
        self.root_a, self.root_b = self.root / "extended-temp-a", self.root / "extended-temp-b"
        self.prefix += "ext_"
        self.mounts = {}
        self.images = set()
        self.clones = []
        self.interrupted_processes = []
        self.blocked_shims = {}
        self.immutable_files = set()
        self.section = None
        self.cleanup_state = "not_attempted"
        self.cleanup_errors = []

    def record(self, case_id, evidence, requirements=None):
        self.results.append({"case_id": case_id, "status": "passed", "evidence": evidence,
                             "requirements": requirements if requirements is not None else CASE_REQUIREMENTS[case_id]})

    def require_storage_state(self, present):
        require_storage_problem(self.status(), present)
        runtime = json.loads(self.cli("status", "-w", str(self.workspace), "--json")[1])
        require_runtime_storage(runtime, "low_space" if present else "ok")

    def prepare(self):
        existing = self.command([self.docker, "ps", "-aq", "--filter", "name=" + self.prefix])[1]
        check(not existing.strip(), "refusing to reuse existing test containers")
        super().prepare()

    def privileged(self, *args):
        return self.command((["sudo", "-n"] if os.geteuid() != 0 else []) + list(args))

    def mount_existing(self, image, target):
        require_run_path(self.root, image)
        require_run_path(self.root, target)
        check(image in self.images and image.is_file() and target not in self.mounts,
              "mount can use only this suite's owned loopback image")
        self.privileged("mount", "-o", "loop,nosuid,nodev", str(image), str(target))
        self.mounts[target] = image
        self.privileged("chown", f"{os.getuid()}:{os.getgid()}", str(target))
        check(target.stat().st_dev != self.root.stat().st_dev, "loop fixture did not mount a separate filesystem")

    def unmount(self, target):
        require_run_path(self.root, target)
        check(target in self.mounts, "cannot unmount an unowned filesystem")
        self.privileged("umount", str(target))
        del self.mounts[target]

    def mount_image(self, name, fs_type, inodes=None):
        check(re.fullmatch(r"[a-z][a-z0-9-]{0,40}", name) and fs_type in ("ext4", "btrfs"), "invalid loop fixture")
        image, target = self.root / (name + ".img"), self.root / name
        require_run_path(self.root, image)
        require_run_path(self.root, target)
        check(not image.exists() and not target.exists(), "refusing to reuse an existing loop fixture")
        self.command(["truncate", "-s", "192M" if fs_type == "ext4" else "512M", str(image)])
        mkfs = ["mkfs." + fs_type, "-F" if fs_type == "ext4" else "-f"]
        if inodes is not None:
            check(128 <= inodes <= 4096, "bounded inode fixture required")
            mkfs.extend(["-N", str(inodes), "-m", "0"])
        self.command([*mkfs, str(image)])
        self.images.add(image)
        target.mkdir()
        self.mount_existing(image, target)
        return image, target

    def path_attack(self):
        before = self.ids()
        link = self.root / "extended-root-link"
        link.symlink_to(self.root_a, target_is_directory=True)
        code, _, _ = self.set_root(link, expected=None)
        check(code != 0 and self.ids() == before, "symlink root accepted or preflight stopped services")
        self.set_root(self.root_a)
        paths = core.require_mounts(self.inspect(), self.root_a)
        self.cli("stop", "-w", str(self.workspace), "--json")
        outside = self.root / "extended-outside-managed-tree"
        outside.mkdir()
        sentinel = outside / "sentinel"
        sentinel.write_text("must-survive")
        path, held = paths[0], paths[0].with_name(paths[0].name + ".held")
        path.rename(held)
        path.symlink_to(outside, target_is_directory=True)
        try:
            self.cli("temp", "gc", "-w", str(self.workspace), "--json", expected=None)
            check(path.is_symlink() and sentinel.read_text() == "must-survive", "GC followed a substituted lease path")
        finally:
            path.unlink()
            held.rename(path)
        self.cli("start", "-w", str(self.workspace), "--json")

    def filesystem(self):
        self.path_attack()
        image, target = self.mount_image("extended-capacity-fs", "ext4", inodes=256)
        self.set_root(target)
        mounts = core.require_mounts(self.inspect(), target)
        sentinel = mounts[0] / "active-sentinel"
        sentinel.write_text("do-not-delete")
        before = self.ids()
        filler = target / "byte-filler"
        with filler.open("wb") as stream:
            for _ in range(4096):
                fs = os.statvfs(target)
                if fs.f_bavail * fs.f_frsize < 524288:
                    break
                try:
                    stream.write(b"x" * 65536)
                    stream.flush()
                except OSError as error:
                    if error.errno != 28:
                        raise
                    break
        fs = os.statvfs(target)
        check(fs.f_bavail * fs.f_frsize < 1048576, "byte fixture did not reach declared lower bound")
        self.require_storage_state(True)
        code, _, _ = self.set_root(target / "new-root", expected=None)
        check(code != 0 and self.ids() == before, "low-space allocation did not reject before stopping services")
        filler.unlink()
        self.set_root(target)
        self.require_storage_state(False)
        inode_dir = target / "inode-filler"
        inode_dir.mkdir()
        for count in range(4096):
            if os.statvfs(target).f_favail < 10:
                break
            try:
                (inode_dir / str(count)).touch()
            except OSError as error:
                if error.errno != 28:
                    raise
                break
        check(os.statvfs(target).f_favail < 10, "inode fixture did not reach declared lower bound")
        self.require_storage_state(True)
        check(self.ids() == before and sentinel.read_text() == "do-not-delete", "shortage restarted containers or removed active files")
        shutil.rmtree(inode_dir)
        self.require_storage_state(False)
        self.record("TEMP-T-017", "bounded ext4 loopback reaches real byte/inode limits; issues appear and clear without restart/deletion")
        self.cli("stop", "-w", str(self.workspace), "--json")
        self.unmount(target)
        code, _, _ = self.cli("start", "-w", str(self.workspace), "--json", expected=None)
        check(code != 0 and not self.command([self.docker, "ps", "-aq", "--filter", "name=" + self.prefix])[1].strip(),
              "missing registered filesystem fell back to its parent filesystem")
        replacement, replacement_target = self.mount_image("extended-replacement-fs", "ext4")
        self.unmount(replacement_target)
        self.mount_existing(replacement, target)
        code, _, _ = self.cli("start", "-w", str(self.workspace), "--json", expected=None)
        check(code != 0, "changed registered filesystem identity was accepted")
        self.unmount(target)
        self.mount_existing(image, target)
        self.cli("start", "-w", str(self.workspace), "--json")
        self.history_filesystem(image, target)
        self.record("TEMP-T-012", "symlink/substituted lease preserves outside sentinel; absent/changed filesystem rejects; original remount retains stable identity")

    def history_filesystem(self, image, target):
        historical = self.active()
        historical_paths = core.require_mounts(self.inspect(), target)
        artifact = self.workspace / ".anas/deployments" / historical
        artifact_digest = core.tree_digest(artifact)
        for path in historical_paths:
            (path / "released-history-sentinel").write_text("never-restore-temporary-content")
        self.set_root(self.root_a)
        current = core.require_mounts(self.inspect(), self.root_a)
        for path in current:
            (path / "history-current-sentinel").write_text("preserve-current-runtime")
        before_ids, previous = self.ids(), self.active()
        check(all(not path.exists() for path in historical_paths), "historical leases were not released before disk removal")
        self.unmount(target)
        code, output, _ = self.cli("rollback", historical, "-w", str(self.workspace), "--json", expected=None)
        require_history_missing_refusal(code, json.loads(output), before_ids, self.ids(), previous, self.active(),
                                        self.status(), self.root_a, current)
        check(core.tree_digest(artifact) == artifact_digest, "failed historical rollback rewrote its frozen artifact")
        self.mount_existing(image, target)
        fresh = self.rollback_history(historical, historical_paths, artifact_digest, target)
        check(all(not (path / "released-history-sentinel").exists() for path in fresh), "historical rollback restored temporary content")
        self.record("TEMP-T-019", "missing historical loop filesystem rejects real rollback before stopping current IDs/content; original disk remount rolls back via fresh deployment/leases and immutable history")
        self.set_root(self.root_a)

    def create_backup(self, mode, destination):
        args = ["backup", "create", "--mode", mode, "--to", str(destination)]
        if mode != "snapshot":
            args.append("--no-stop")
        return json.loads(self.cli(*args, "-y", "-w", str(self.workspace), "--json")[1])

    def restore_backup(self, destination, backup_id, target):
        require_run_path(self.root, target)
        check(not target.exists(), "refusing to reuse a backup restore target")
        # restore requires an initialized workspace. Empty init creates its
        # skeleton without importing source configuration or starting Modules.
        target.mkdir(mode=0o700)
        initialized = json.loads(self.cli("init", str(target), "-y", "--json")[1])
        require_initialized_restore_target(initialized, target)
        result = self.cli("backup", "restore", "--from", str(destination), "--backup-id", backup_id,
                          "-w", str(target), "-y", "--json")
        require_backup_restore_result(json.loads(result[1]), target, backup_id)
        return result

    def set_clone_prefix(self, clone, suffix):
        prefix = self.prefix + suffix + "_"
        for key in ("container_prefix", "network_prefix"):
            self.cli("config", "set", "global." + key, prefix, "--defer", "-w", str(clone), "--root", str(self.fixture), "--json")
        return prefix

    def verify_clone(self, clone, suffix, source_id, source_paths, source_ids, source_deployment, source_root, external_root=None):
        require_backup_source_preserved(source_ids, source_deployment, source_id, source_paths, source_root,
                                        self.active(), self.status(), self.inspect())
        prefix = self.set_clone_prefix(clone, suffix)
        self.clones.append((clone, prefix))
        if external_root is not None:
            # A restored desired path must undergo target-side preflight. A
            # source lease or a valid source host is never that authorization.
            invalid = self.root / "extended-restored-external-link"
            invalid.symlink_to(source_paths[0], target_is_directory=True)
            before = self.ids()
            self.cli("config", "set", "global.temp_path", str(invalid), "--defer", "-w", str(clone), "--root", str(self.fixture), "--json")
            code, output, _ = self.cli("apply", "-w", str(clone), "--root", str(self.fixture), "--update-lock", "--no-snapshot", "-y", "--json", expected=None)
            require_restored_root_refusal(code, json.loads(output), before, self.ids())
            invalid.unlink()
            self.cli("config", "set", "global.temp_path", str(external_root), "--defer", "-w", str(clone), "--root", str(self.fixture), "--json")
        self.cli("apply", "-w", str(clone), "--root", str(self.fixture), "--update-lock", "--no-snapshot", "-y", "--json")
        status = json.loads(self.cli("temp", "status", "-w", str(clone), "--json")[1])
        paths = require_restore_isolation(source_id, source_paths, status)
        if external_root is not None:
            check(all(path.is_relative_to(external_root) for path in paths) and external_root.stat().st_dev != self.workspace.stat().st_dev,
                  "restore did not allocate on the target's independently validated external filesystem")
        for name in self.modules:
            item = self.docker_json("inspect", prefix + name)[0]
            check(item["State"]["Running"], "restored Module did not start")
            for mount in item["Mounts"]:
                if mount["Destination"] in ("/runtime", "/scratch"):
                    check(Path(mount["Source"]) in paths, "restored actual bind bypassed its newly allocated lease")
        self.cli("stop", "-w", str(clone), "--json")
        self.cli("temp", "gc", "-w", str(clone), "--json")
        require_backup_source_preserved(source_ids, source_deployment, source_id, source_paths, source_root,
                                        self.active(), self.status(), self.inspect())

    def backup(self):
        # Use the default resolved location explicitly: config set requires an
        # absolute path. Real workspace-local content must be in backup scope;
        # an external root would be excluded even without the tmp policy.
        source_root = self.workspace / "tmp"
        self.set_root(source_root)
        business = self.workspace / "data/business-sentinel"
        business.write_text(self.run_id)
        source_id = self.status()["workspace_id"]
        source_ids, source_deployment = self.ids(), self.active()
        source_paths = core.require_mounts(self.inspect(), source_root)
        for path in source_paths:
            (path / "excluded-temp-sentinel").write_text("must-not-be-backed-up")
        _, other_fs = self.mount_image("extended-backup-btrfs", "btrfs")
        destinations = {"snapshot": self.root / "extended-backup-snapshot", "send": other_fs / "backups",
                        "send-file": self.root / "extended-backup-stream", "copy": self.root / "extended-backup-copy"}
        for mode, destination in destinations.items():
            destination.mkdir()
            outcome = self.create_backup(mode, destination)
            check(outcome.get("ok") is True and outcome.get("mode") == mode and outcome.get("backup_id"), "backup did not execute requested transfer mode")
            require_backup_source_preserved(source_ids, source_deployment, source_id, source_paths, source_root,
                                            self.active(), self.status(), self.inspect())
            self.cli("backup", "verify", "--to", str(destination), "--backup-id", outcome["backup_id"], "--json")
            restored = self.root / ("extended-restore-" + mode)
            self.restore_backup(destination, outcome["backup_id"], restored)
            require_backup_source_preserved(source_ids, source_deployment, source_id, source_paths, source_root,
                                            self.active(), self.status(), self.inspect())
            check((restored / "data/business-sentinel").read_text() == self.run_id, "backup restore lost business sentinel")
            check(not (restored / ".anas/temp/registry.yml").exists() and not (restored / "tmp").exists(), "backup included source temporary content or leases")
            check(not list(restored.rglob("excluded-temp-sentinel")), "temporary content entered restored backup coverage")
            self.verify_clone(restored, mode.replace("-", ""), source_id, source_paths, source_ids, source_deployment, source_root,
                              other_fs / "restored-external-temp" if mode == "copy" else None)
        clone = self.root / "extended-copied-workspace"
        shutil.copytree(self.workspace, clone, symlinks=True, ignore=shutil.ignore_patterns("snapshots", "go-build-cache", "build-cache"))
        self.verify_clone(clone, "clone", source_id, source_paths, source_ids, source_deployment, source_root)
        require_backup_source_preserved(source_ids, source_deployment, source_id, source_paths, source_root,
                                        self.active(), self.status(), self.inspect())
        snapshot = require_snapshot_metadata(json.loads(self.cli("snapshot", "create", "--label", "temp-exclusion", "-w", str(self.workspace), "--json")[1]), "temp-exclusion")
        check(snapshot["deployment_id"] == source_deployment, "workspace snapshot captured a different active deployment")
        require_backup_source_preserved(source_ids, source_deployment, source_id, source_paths, source_root,
                                        self.active(), self.status(), self.inspect())
        snapshot_tree = self.workspace / "snapshots" / snapshot["id"]
        check(not list(snapshot_tree.rglob("excluded-temp-sentinel")) and not (snapshot_tree / "meta/temp").exists(), "workspace snapshot captured temporary contents or registry")
        self.cli("snapshot", "restore", snapshot["id"], "-w", str(self.workspace), "-y", "--json")
        self.cli("start", "-w", str(self.workspace), "--json")
        status, containers = self.gc_snapshot_released(source_root, source_paths)
        require_snapshot_restored_runtime(source_ids, snapshot["deployment_id"], source_id, source_paths, source_root,
                                          self.active(), status, containers)
        check((self.workspace / "data/business-sentinel").read_text() == self.run_id, "snapshot restore lost business sentinel")
        self.record("TEMP-T-018", "snapshot backup pauses/resumes original container IDs/active leases/content; new private targets use actual empty init then verified restore without controlling the source; snapshot/send/send-file/copy exclude temp; restored/copy identities and actual binds are independent; target external symlink rejected and separate loopback filesystem revalidated; healthy labeled workspace snapshot restores fresh leases; explicit GC removes released old trees while preserving all new container IDs, binds and ownership markers")
        self.set_root(self.root_a)

    def gc_snapshot_released(self, root, old_paths):
        # start allocates fresh leases but only registers the old leases as
        # released. Reclaim them explicitly, without replacing the new runtime.
        before = self.inspect()
        before_ids = {name: item["Id"] for name, item in before.items()}
        paths = set(core.require_mounts(before, root))
        def bindings(containers):
            return {name: sorted((mount.get("Destination"), mount.get("Source")) for mount in item.get("Mounts", [])
                                 if mount.get("Destination") in ("/runtime", "/scratch")) for name, item in containers.items()}
        def markers():
            values = {}
            for path in paths:
                marker = path / ".anas-temp-owner.yml"
                check(marker.is_file() and not marker.is_symlink(), "snapshot runtime has no regular ownership marker")
                values[path] = core.hashlib.sha256(marker.read_bytes()).hexdigest()
            return values
        before_bindings, before_markers = bindings(before), markers()
        code, output, _ = self.cli("temp", "gc", "-w", str(self.workspace), "--json")
        check(code == 0 and json.loads(output).get("ok") is True, "snapshot released-tree GC did not succeed")
        after = self.inspect()
        check({name: item["Id"] for name, item in after.items()} == before_ids
              and all(item["State"]["Running"] for item in after.values())
              and set(core.require_mounts(after, root)) == paths and bindings(after) == before_bindings
              and markers() == before_markers, "snapshot GC replaced or changed the new running containers, binds, or ownership markers")
        status = self.status()
        registered = {Path(item["path"]) for item in status.get("directories", []) if item.get("state") == "active"}
        check(registered == paths and all(not path.exists() and not path.is_symlink() for path in old_paths),
              "snapshot GC did not preserve new active leases or reclaim released old trees")
        return status, after

    def blocking_docker(self, phase, old_paths):
        folder = self.root / ("extended-block-" + phase)
        folder.mkdir()
        marker, release, last_up = folder / "blocked", folder / "release", folder / "last-up"
        shim = folder / "docker"
        source = r'''#!/usr/bin/env python3
import json,os,subprocess,sys,time
from pathlib import Path
def isolate_blocked_shim():
 pid,parent=os.getpid(),os.getppid()
 session,group=os.getsid(0),os.getpgrp()
 if parent<=1 or os.getsid(parent)!=parent or session!=parent or group not in (parent,pid):
  raise SystemExit('paused Docker shim does not inherit its CLI parent/session')
 # The ordinary CLI Compose branch inherits its parent's group, while
 # context-owned Docker queries already have a separate group. Isolate only
 # this selected blocker, before recording it or starting any Docker child.
 os.setpgid(0,0)
 if os.getppid()!=parent or os.getsid(0)!=session or os.getpgrp()!=pid:
  raise SystemExit('paused Docker shim process identity changed during isolation')
 return {'pid':pid,'ppid':parent,'pgid':pid,'session_id':session,'inherited_pgid':group}
a=sys.argv[1:]
phase=os.environ['ANAS_TEST_BLOCK_PHASE']
project=next((a[a.index(f)+1] for f in ('-p','--project-name') if f in a), '')
marker=Path(os.environ['ANAS_TEST_BLOCK_MARKER'])
release=Path(os.environ['ANAS_TEST_BLOCK_RELEASE'])
last=Path(os.environ['ANAS_TEST_BLOCK_LAST_UP'])
prefix=os.environ['ANAS_TEST_BLOCK_PREFIX']
block=phase=='stop' and 'compose' in a and 'down' in a and project==prefix+'passive'
block=block or (phase=='start' and 'compose' in a and 'up' in a and project==prefix+'consumer')
block=block or (phase=='pre-commit' and 'inspect' in a and last.exists())
if phase=='partial-cleanup' and 'info' in a:
 old=[Path(p) for p in json.loads(os.environ['ANAS_TEST_BLOCK_OLD_PATHS'])]
 registry=Path(os.environ['ANAS_TEST_BLOCK_REGISTRY']).read_text()
 target=os.environ['ANAS_TEST_BLOCK_TARGET_ROOT']
 block=('applied_root: '+target in registry and any(not p.exists() for p in old) and any(p.exists() for p in old))
if block and not marker.exists():
 identity=isolate_blocked_shim()
 fields=Path('/proc',str(os.getpid()),'stat').read_text().rsplit(')',1)[1].split()
 identity['start_time']=int(fields[19])
 marker.write_text(json.dumps(identity))
 while not release.exists(): time.sleep(0.05)
real=os.environ['ANAS_TEST_REAL_DOCKER']
if phase=='pre-commit' and 'compose' in a and 'up' in a and project==prefix+'passive':
 result=subprocess.run([real,*a])
 if result.returncode==0: last.write_text('started')
 sys.exit(result.returncode)
os.execv(real,[real,*a])
'''
        shim.write_text(source)
        shim.chmod(0o700)
        env = dict(os.environ, PATH=str(folder) + os.pathsep + os.environ["PATH"], ANAS_TEST_REAL_DOCKER=self.docker,
                   ANAS_TEST_BLOCK_PHASE=phase, ANAS_TEST_BLOCK_MARKER=str(marker), ANAS_TEST_BLOCK_RELEASE=str(release),
                   ANAS_TEST_BLOCK_LAST_UP=str(last_up), ANAS_TEST_BLOCK_PREFIX=self.prefix,
                   ANAS_TEST_BLOCK_OLD_PATHS=json.dumps([str(path) for path in old_paths]),
                   ANAS_TEST_BLOCK_REGISTRY=str(self.workspace / ".anas/temp/registry.yml"),
                   ANAS_TEST_BLOCK_TARGET_ROOT=str(self.root_b))
        return env, marker, release

    def spawn_cli(self, args, env=None, capture_output=False):
        process = subprocess.Popen([self.anas, *args], stdout=subprocess.PIPE if capture_output else subprocess.DEVNULL,
                                   stderr=subprocess.DEVNULL,
                                   env=env, start_new_session=True)
        self.interrupted_processes.append(process)
        return process

    def own_blocked_shim(self, marker, parent):
        require_run_path(self.root, marker)
        record = json.loads(marker.read_text())
        pid = record.get("pid")
        check(type(pid) is int and pid > 1, "paused shim has no valid process identity")
        require_blocker_identity(record, parent.pid, process_identity(pid))
        self.blocked_shims[pid] = record["start_time"]
        return pid

    def kill_blocked_shim(self, pid):
        try:
            current = process_identity(pid)
        except FileNotFoundError:
            self.blocked_shims.pop(pid, None)
            return
        check(current["start_time"] == self.blocked_shims[pid] and current["pgid"] == pid,
              "refusing to signal a paused shim after its process identity changed")
        os.killpg(pid, signal.SIGKILL)
        self.blocked_shims.pop(pid, None)

    def concurrency(self):
        for phase in ("stop", "start", "pre-commit", "partial-cleanup"):
            self.set_root(self.root_a)
            old_paths = core.require_mounts(self.inspect(), self.root_a)
            for path in old_paths:
                (path / "crash-sentinel").write_text("preserve-before-commit")
            old_deployment = self.active()
            self.cli("config", "set", "global.temp_path", str(self.root_b), "--defer", "-w", str(self.workspace), "--root", str(self.fixture), "--json")
            env, marker, release = self.blocking_docker(phase, old_paths)
            apply = self.spawn_cli(["apply", "-w", str(self.workspace), "--root", str(self.fixture), "--update-lock", "--no-snapshot", "-y", "--json"], env)
            deadline = time.monotonic() + 120
            while not marker.exists() and apply.poll() is None and time.monotonic() < deadline:
                time.sleep(0.05)
            check(marker.exists() and apply.poll() is None, "real transition did not reach the selected crash boundary")
            blocked_pid = self.own_blocked_shim(marker, apply)
            persisted = transition_phase((self.workspace / ".anas/temp/registry.yml").read_text())
            check(persisted in ({"stopping"} if phase == "stop" else {"starting"} if phase in ("start", "pre-commit") else {"committed", "cleaning"}),
                  "crash boundary does not match persisted phase")
            gc = self.spawn_cli(["temp", "gc", "-w", str(self.workspace), "--json"], capture_output=True)
            lifecycle = self.spawn_cli(["start", "-w", str(self.workspace), "--json"])
            time.sleep(0.5)
            check(gc.poll() is None and lifecycle.poll() is None, "GC/lifecycle did not share the switch workspace execution lock")
            # Do not assume flock queues are FIFO. Cancel the still-blocked
            # lifecycle contender, let GC observe the interrupted phase, then
            # explicitly run lifecycle reconciliation after that observation.
            os.killpg(lifecycle.pid, signal.SIGKILL)
            lifecycle.wait(timeout=10)
            os.killpg(apply.pid, signal.SIGKILL)
            check(apply.wait(timeout=10) < 0, "test did not actually interrupt the CLI process group")
            # The selected blocker establishes its own process group before
            # the marker. Killing the CLI does not cancel this owned shim.
            self.kill_blocked_shim(blocked_pid)
            gc_output, _ = gc.communicate(timeout=120)
            require_interrupted_gc(gc.returncode, json.loads(gc_output), old_paths, phase != "partial-cleanup")
            self.cli("start", "-w", str(self.workspace), "--json")
            expected_root = self.root_b if phase == "partial-cleanup" else self.root_a
            core.require_mounts(self.inspect(), expected_root)
            check(all(item["State"]["Running"] for item in self.inspect().values()), "reconciled transition did not restore complete runtime")
            if phase != "partial-cleanup":
                check(self.active() == old_deployment and all((path / "crash-sentinel").read_text() == "preserve-before-commit" for path in old_paths),
                      "uncommitted interruption lost old applied deployment/content")
            else:
                check(self.active() != old_deployment, "committed interruption incorrectly rolled back new runtime")
            release.touch()
        self.record("TEMP-T-020", "SIGKILL at stop/start/pre-commit/partial-cleanup; waiting GC and lifecycle share lock; actual runtime/state reconciled before cleanup")

    def cleanup_failure(self):
        _, target = self.mount_image("extended-cleanup-fs", "ext4")
        self.set_root(target)
        old_paths = core.require_mounts(self.inspect(), target)
        immutable = old_paths[0] / "cleanup-must-retry"
        immutable.write_text("preserve-on-cleanup-failure")
        self.privileged("chattr", "+i", str(immutable))
        self.immutable_files.add(immutable)
        old_deployment = self.active()
        code, stdout, stderr = self.set_root(self.root_b)
        check(code == 0 and b"temp_cleanup_pending" in stdout + stderr,
              "successful new runtime did not report real old-tree deletion failure")
        new_ids = self.ids()
        new_paths = core.require_mounts(self.inspect(), self.root_b)
        require_cleanup_pending(self.active(), old_deployment, self.status(), old_paths, new_paths, immutable, self.root_b)
        check(all(item["State"]["Running"] for item in self.inspect().values()), "cleanup failure stopped the committed new runtime")
        # Keep the deletion fault active while exercising normal operations.
        # Removing it first would miss the lifecycle barrier regression.
        self.cli("stop", "-w", str(self.workspace), "--json")
        self.cli("start", "-w", str(self.workspace), "--json")
        self.set_root(self.root_b)
        new_ids = self.ids()
        new_paths = core.require_mounts(self.inspect(), self.root_b)
        require_cleanup_pending(self.active(), old_deployment, self.status(), old_paths, new_paths, immutable, self.root_b)
        check(all(item["State"]["Running"] for item in self.inspect().values()),
              "deferred old cleanup blocked stop/start/apply of the new runtime")
        self.privileged("chattr", "-i", str(immutable))
        self.immutable_files.remove(immutable)
        self.cli("temp", "gc", "-w", str(self.workspace), "--json")
        check(all(not path.exists() for path in old_paths) and self.ids() == new_ids,
              "explicit retry did not delete old registrations or restarted the new runtime")
        core.require_mounts(self.inspect(), self.root_b)
        self.record("TEMP-T-024", "immutable file causes real post-commit cleanup failure; stop/start/apply succeed while fault persists; new mounts remain running; explicit GC retry succeeds",
                    ["TEMP-R-036"])
        self.set_root(self.root_a)

    def wrong_mount(self):
        self.set_root(self.root_a)
        old_paths = core.require_mounts(self.inspect(), self.root_a)
        for path in old_paths:
            (path / "wrong-mount-old-sentinel").write_text("must-preserve-before-commit")
        old_deployment = self.active()
        folder = self.root / "extended-wrong-mount-bin"
        folder.mkdir()
        wrong = self.root / "extended-unregistered-mount"
        wrong.mkdir(mode=0o750)
        self.privileged("chown", f"{self.container_uid}:{self.container_gid}", str(wrong))
        marker = folder / "actual-mount.json"
        shim = folder / "docker"
        shim.write_text(r'''#!/usr/bin/env python3
import json,os,subprocess,sys
from pathlib import Path
a=sys.argv[1:]
real=os.environ['ANAS_TEST_REAL_DOCKER']
project=next((a[a.index(f)+1] for f in ('-p','--project-name') if f in a), '')
marker=Path(os.environ['ANAS_TEST_WRONG_MOUNT_MARKER'])
source=os.environ.get('ANAS_TEMP_RUNTIME','')
target=Path(os.environ['ANAS_TEST_WRONG_MOUNT_ROOT'])
if 'compose' in a and 'up' in a and project==os.environ['ANAS_TEST_WRONG_MOUNT_PROJECT'] and source and Path(source).is_relative_to(target) and not marker.exists():
 wrong=os.environ['ANAS_TEST_WRONG_MOUNT_PATH']
 os.environ['ANAS_TEMP_RUNTIME']=wrong
 result=subprocess.run([real,*a])
 if result.returncode!=0: sys.exit(result.returncode)
 inventory=subprocess.run([real,'inspect',os.environ['ANAS_TEST_WRONG_MOUNT_CONTAINER']],stdout=subprocess.PIPE,check=True)
 item=json.loads(inventory.stdout)[0]
 mounts=[m for m in item['Mounts'] if m['Destination']=='/runtime']
 if len(mounts)!=1 or mounts[0]['Source']!=wrong or not item['State']['Running']: sys.exit(96)
 marker.write_text(json.dumps({'container_id':item['Id'],'registered_source':source,'actual_source':mounts[0]['Source'],'destination':'/runtime'}))
 sys.exit(0)
os.execv(real,[real,*a])
''')
        shim.chmod(0o700)
        env = dict(os.environ, PATH=str(folder) + os.pathsep + os.environ["PATH"], ANAS_TEST_REAL_DOCKER=self.docker,
                   ANAS_TEST_WRONG_MOUNT_MARKER=str(marker), ANAS_TEST_WRONG_MOUNT_ROOT=str(self.root_b),
                   ANAS_TEST_WRONG_MOUNT_PROJECT=self.prefix + "producer", ANAS_TEST_WRONG_MOUNT_PATH=str(wrong),
                   ANAS_TEST_WRONG_MOUNT_CONTAINER=self.prefix + "producer")
        code, stdout, _ = self.set_root(self.root_b, expected=None, env=env)
        require_wrong_mount_refusal(code, json.loads(stdout), marker, wrong, self.root_b, self.active(), old_deployment,
                                   self.status(), old_paths)
        failure = json.loads(stdout)
        check(failure.get("ok") is False and failure.get("error", {}).get("code"),
              "wrong mount failed without reporting the activation error")
        core.require_mounts(self.inspect(), self.root_a)
        check(all(item["State"]["Running"] for item in self.inspect().values()), "wrong mount recovery did not restart the old runtime")
        self.record("TEMP-T-024", "real Compose bind redirected to unregistered source; Docker inspect proves fault; activation rejects before commit and restores old registered binds/content",
                    ["TEMP-R-016", "TEMP-R-045"])
        self.set_root(self.root_a)

    def faults(self):
        self.cleanup_failure()
        self.wrong_mount()

    def cleanup(self):
        for process in self.interrupted_processes:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait(timeout=10)
        for pid in list(self.blocked_shims):
            self.kill_blocked_shim(pid)
        for path in list(self.immutable_files):
            require_run_path(self.root, path)
            self.privileged("chattr", "-i", str(path))
            self.immutable_files.remove(path)
        for clone, prefix in self.clones:
            self.cli("stop", "-w", str(clone), "--json")
            self.cli("temp", "gc", "-w", str(clone), "--json")
            check(not self.command([self.docker, "ps", "-aq", "--filter", "name=" + prefix])[1].strip(), "restored test containers remain")
        super().cleanup()
        for target in list(reversed(self.mounts)):
            self.unmount(target)
        self.cleanup_state = "complete"

    def stop_for_inspection(self):
        errors = []
        for process in self.interrupted_processes:
            if process.poll() is None:
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait(timeout=10)
                except Exception as error:
                    errors.append("stop interrupted CLI: " + type(error).__name__)
        for pid in list(self.blocked_shims):
            try:
                self.kill_blocked_shim(pid)
            except Exception as error:
                errors.append("stop paused Docker shim: " + type(error).__name__)
        for workspace, prefix in [(self.workspace, self.prefix), *self.clones]:
            for name in self.modules:
                try:
                    self.stop_owned_container(prefix, name, workspace)
                except Exception as error:
                    self.private_exception(error)
                    errors.append("Docker inspection/stop " + prefix + name + ": " + type(error).__name__)
        self.cleanup_errors.extend(errors)
        self.cleanup_state = "failed" if errors else "stopped; owned loopbacks and recovery materials preserved"
        check(not errors, "run-scoped stop failed; remaining stops were attempted and materials retained")

    def write_report(self, status, error=None):
        covered = {requirement for result in self.results if result["status"] == "passed" for requirement in result["requirements"]}
        report = {"schema": "anas.workspace-temp-storage-extended-e2e/v1", "run_id": self.run_id,
                  "status": status, "scope": self.section, "source_digest": self.source_digest,
                  "binary_digest": "sha256:" + core.hashlib.sha256(Path(self.anas).read_bytes()).hexdigest(),
                  "results": self.results, "failure_detail": self.failure_detail,
                  "covered": sorted(covered), "not_run": sorted(E2E_REQUIREMENTS - covered),
                  "cleanup": self.cleanup_state, "cleanup_errors": self.cleanup_errors,
                  "retained_mounts": [str(path) for path in self.mounts],
                  "retained_immutable_files": [str(path) for path in self.immutable_files], "error": error}
        path = self.report_dir / ("workspace-temp-storage-extended-" + self.section + ".json")
        path.write_text(json.dumps(report, indent=2) + "\n")
        path.chmod(0o600)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--verify-isolation", action="store_true", help="check private mount namespace before any Docker query")
    parser.add_argument("section", choices=("filesystem", "backup", "concurrency", "faults", "all"), nargs="?", default="all")
    arguments = parser.parse_args()
    if arguments.verify_isolation:
        require_private_mount_namespace()
        return 0
    section = arguments.section
    suite, error = None, None
    try:
        suite = ExtendedSuite()
        suite.section = section
        suite.prepare()
        if section in ("filesystem", "all"):
            suite.filesystem()
        if section in ("backup", "all"):
            suite.backup()
        if section in ("concurrency", "all"):
            suite.concurrency()
        if section in ("faults", "all"):
            suite.faults()
        suite.cleanup()
    except Exception as failure:
        error = core.public_error(failure)
        if suite is not None:
            suite.private_exception(failure)
            try:
                suite.stop_for_inspection()
            except Exception as cleanup_failure:
                suite.private_exception(cleanup_failure)
                suite.cleanup_state = "failed"
    finally:
        if suite is not None:
            suite.write_report("failed" if error else "passed", error)
    if error:
        print(error["code"] + ": " + error["message"], file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
