#!/usr/bin/env python3
# TEST_CASES: TEMP-T-012, TEMP-T-013, TEMP-T-014, TEMP-T-015, TEMP-T-016, TEMP-T-019
"""Real CLI/Docker probes. Never bootstrap a daemon or operate outside this run."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import time
import traceback

E2E_REQUIREMENTS = [f"TEMP-R-{number:03d}" for number in (5, 6, 9, 10, 14, 15, 16, 17, 19, 23, 24, 26, 27, 29, 30, 33, 35, 36, 39, 41, 43, 44, 45)]
CASE_REQUIREMENTS = {
    "TEMP-T-013": ["TEMP-R-009", "TEMP-R-010"],
    "TEMP-T-014": ["TEMP-R-014", "TEMP-R-015", "TEMP-R-033"],
    "TEMP-T-015": ["TEMP-R-016", "TEMP-R-035", "TEMP-R-044"],
    "TEMP-T-016": ["TEMP-R-017"],
    "TEMP-T-019": ["TEMP-R-041"],
}


class ProbeFailure(RuntimeError):
    def __init__(self, message, code="probe_failed"):
        super().__init__(message)
        self.code = code


REPORT_MESSAGES = {
    "probe_failed": "probe assertion failed; private diagnostics retained",
    "external_command_failed": "external command failed; private diagnostics retained",
    "external_command_timeout": "external command timed out; private diagnostics retained",
    "cleanup_failed": "run-scoped cleanup failed; recovery materials preserved",
}
CLI_ERROR_CODES = {"usage", "config_invalid", "config_requires_import", "init_failed", "resolution_failed",
                   "module_not_found", "module_invalid", "module_root_invalid", "module_root_missing",
                   "stop_failed", "start_failed", "temp_storage_failed", "temp_reconcile_failed",
                   "temp_mount_failed", "temp_release_failed", "temp_preflight_failed", "temp_transition_failed",
                   "temp_state_failed", "guarded_changes", "compose_missing", "image_rebuild_required",
                   "rollback_failed", "no_active_deployment", "temp_recovery_required"}


def public_error(error):
    code = error.get("code") if isinstance(error, dict) else getattr(error, "code", "probe_failed")
    code = code if isinstance(code, str) and code in REPORT_MESSAGES else "probe_failed"
    return {"code": code, "message": REPORT_MESSAGES[code]}


def require_scoped_path(root, path):
    check(root.is_absolute() and root == root.resolve() and path.is_absolute()
          and path == Path(os.path.normpath(path)) and path != root and path.is_relative_to(root)
          and path.parent.resolve().is_relative_to(root) and not path.is_symlink(),
          "test operation must stay in the run's real directory tree")


_extended_helpers = None


def private_mount_helpers():
    # The extended suite imports this file. Load its independent guards only
    # when the host-mount probe executes, after both module definitions exist.
    global _extended_helpers
    if _extended_helpers is None:
        spec = importlib.util.spec_from_file_location("temp_extended_guards", Path(__file__).with_name("server-workspace-temp-storage-extended-e2e.py"))
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        _extended_helpers = module
    try:
        _extended_helpers.require_private_mount_namespace()
    except Exception as error:
        raise ProbeFailure("private mount namespace verification failed") from error
    return _extended_helpers


def require_stop_failure(code, payload, marker, project):
    detail = payload.get("error", {}).get("detail", {})
    primary = detail.get("primary", {})
    command = primary.get("command", {})
    recovery = detail.get("recovery", [])
    check(code != 0 and marker.is_file() and payload.get("ok") is False,
          "one-shot real Compose down fault did not reject switch")
    check(payload.get("error", {}).get("code") == primary.get("code") == "stop_failed"
          and command.get("phase") == "down" and command.get("exit_code") == 98
          and command.get("project") == project and "synthetic-stop-failure" in primary.get("message", ""),
          "Compose down primary failure was lost or replaced by compensation output")
    restores = [outcome for outcome in recovery if outcome.get("phase") == "previous_restore"]
    check(len(restores) == 1 and restores[0].get("status") == "succeeded",
          "previous_restore result was not reported independently from the primary stop failure")


def require_host_mount_retained(status, lease, marker):
    directories = [item for item in status.get("directories", []) if item.get("path") == str(lease)]
    check(len(directories) == 1 and "host_mount_reference" in directories[0].get("blockers", [])
          and directories[0].get("reclaimable") is False and lease.is_dir()
          and (lease / ".anas-temp-owner.yml").is_file() and marker.read_text() == "preserve-host-mount",
          "GC failed to retain a registered tree with a remaining host bind mount")


def require_host_stop_refusal(code, payload, module):
    message = "release " + module + " temporary storage: temporary module " + module + " still has references: [host_mount_reference]"
    check(code != 0 and payload.get("ok") is False and payload.get("error", {}).get("code") == "stop_failed"
          and payload.get("error", {}).get("message") == message,
          "normal down did not report only the expected remaining host-mount release guard")


def require_cleanup_container(container, identity, name, module, workspace):
    labels = container.get("Config", {}).get("Labels", {}) or {}
    working = Path(labels.get("com.docker.compose.project.working_dir", ""))
    base = workspace / ".anas/deployments"
    check(container.get("Id") == identity and container.get("Name") == "/" + name
          and labels.get("com.docker.compose.project") == name and working.is_absolute()
          and working.is_relative_to(base) and len(working.relative_to(base).parts) == 3
          and working.relative_to(base).parts[1:] == ("modules", module)
          and type(container.get("State", {}).get("Running")) is bool,
          "inspection cleanup container identity or workspace ownership is unknown")


def mount_points():
    # ismount() cannot detect a same-filesystem bind mount. Use the actual Linux
    # mount table, including escaped whitespace and backslashes in mount paths.
    def unescape(value):
        return re.sub(r"\\([0-7]{3})", lambda match: chr(int(match[1], 8)), value)
    return {Path(unescape(fields[4])) for line in Path("/proc/self/mountinfo").read_text().splitlines()
            if len(fields := line.split()) >= 6}


def require_bind_mount(source, target):
    check(target in mount_points() and (source.stat().st_dev, source.stat().st_ino)
          == (target.stat().st_dev, target.stat().st_ino),
          "private bind target is not the exact mounted source directory")


def check(condition, message):
    if not condition:
        raise ProbeFailure(message)


def require_isolation(root, run_id, docker_host, docker_root):
    check(re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,63}", run_id) is not None, "invalid run-id")
    check(root.is_absolute() and root == Path(os.path.normpath(root)) and run_id == root.name and root.parent == Path("/home/whl/anas-temp-storage-e2e"),
          "work root must be an absolute run-id directory below anas-temp-storage-e2e")
    check(docker_host.startswith("unix:///") and docker_host not in
          ("unix:///run/docker.sock", "unix:///var/run/docker.sock") and
          "anas" in docker_host and any(s in docker_host for s in ("test", "e2e")),
          "isolated test Docker socket required")
    check(Path(docker_root) == root / "docker",
          "isolated Docker data-root required")


def tree_digest(root):
    h = hashlib.sha256()
    for file in sorted(root.rglob("*")):
        if file.is_file() and not file.is_symlink():
            h.update(str(file.relative_to(root)).encode())
            h.update(file.read_bytes())
    return h.hexdigest()


def require_changed(before, after, modules):
    check(set(before) == set(modules) == set(after), "container inventory is incomplete")
    check(all(before[name] != after[name] for name in modules), "full switch did not recreate every enabled Module")


def require_mounts(containers, expected_root):
    paths = []
    for name, item in containers.items():
        for mount in item.get("Mounts", []):
            if mount.get("Destination") not in ("/runtime", "/scratch"):
                continue
            source = Path(mount.get("Source", ""))
            check(source.is_absolute() and source.is_relative_to(expected_root), "temporary mount escaped its configured root")
            check(source.is_dir() and not source.is_symlink(), "temporary mount is not a real allocated directory")
            paths.append(source)
    check(len(paths) == 3 and len(set(paths)) == 3, "Module/instance/declaration mounts are not isolated")
    return paths


def require_event_order(events, prefix, modules):
    starts, stops = [], []
    for event in events:
        attrs = event.get("Actor", {}).get("Attributes", {})
        module = attrs.get("com.docker.compose.project", "").removeprefix(prefix)
        if module not in modules:
            continue
        action = event.get("Action", event.get("status", ""))
        if action == "start" and module not in starts:
            starts.append(module)
        if action == "destroy" and module not in stops:
            stops.append(module)
    check(stops == list(reversed(modules)), "stop order differs from frozen dependency reverse order")
    check(starts == list(modules), "start order differs from target dependency order")


class Suite:
    modules = ("producer", "consumer", "passive")

    def __init__(self):
        self.root = Path(os.environ["ANAS_TEST_WORK_ROOT"]).absolute()
        self.run_id = os.environ["ANAS_TEST_RUN_ID"]
        self.anas = os.environ["ANAS_TEST_ANAS_CMD"]
        self.docker = shutil.which(os.environ.get("DOCKER_CMD", "docker"))
        check(self.docker is not None and Path(self.anas).is_absolute(), "absolute ANAS binary and Docker client required")
        docker_root = self.command([self.docker, "info", "--format", "{{.DockerRootDir}}"])[1].decode().strip()
        require_isolation(self.root, self.run_id, os.environ.get("DOCKER_HOST", ""), docker_root)
        self.root.mkdir(parents=True, exist_ok=True)
        check(self.root.resolve() == self.root and not self.root.is_symlink(), "run root crosses a symlink boundary")
        self.report_dir = self.root / "reports"
        require_scoped_path(self.root, self.report_dir)
        self.report_dir.mkdir(mode=0o700, exist_ok=True)
        check(self.report_dir.stat().st_uid == os.geteuid() and self.report_dir.stat().st_mode & 0o077 == 0,
              "private report directory ownership or mode is unsafe")
        self.fixture = self.root / "fixture"
        self.workspace = self.root / "core-workspace"
        self.root_a, self.root_b = self.root / "temp-a", self.root / "temp-b"
        self.prefix = "anas_temp_" + hashlib.sha256(self.run_id.encode()).hexdigest()[:10] + "_"
        self.results = []
        self.created = False
        self.failure_detail = None
        self.host_bind_mounts = set()
        self.cleanup_state = "not_attempted"
        self.cleanup_errors = []
        self.source_digest = os.environ.get("ANAS_TEST_SOURCE_DIGEST")
        check(self.source_digest is not None and re.fullmatch(r"sha256:[a-f0-9]{64}", self.source_digest), "ANAS_TEST_SOURCE_DIGEST is required")

    def command(self, args, env=None, expected=0, timeout=600):
        try:
            result = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                    env=env, timeout=timeout)
        except subprocess.TimeoutExpired as error:
            self.retain_diagnostic("command-timeout", {"argv": list(args), "timeout": timeout,
                                    "stdout": self.diagnostic_text(error.stdout), "stderr": self.diagnostic_text(error.stderr)})
            raise ProbeFailure("external command timed out", "external_command_timeout") from error
        if result.returncode != 0:
            self.retain_diagnostic("command-failure", {"argv": list(args), "exit_code": result.returncode,
                                    "stdout": self.diagnostic_text(result.stdout), "stderr": self.diagnostic_text(result.stderr)})
        if expected is not None:
            if result.returncode != expected:
                error_code = None
                try:
                    error_code = json.loads(result.stdout).get("error", {}).get("code")
                except (ValueError, AttributeError):
                    pass
                tool = "anas" if args[0] == getattr(self, "anas", None) else "docker" if args[0] == getattr(self, "docker", None) else "external"
                operation = args[1] if len(args) > 1 and args[1] in {"init", "apply", "start", "stop", "rollback", "temp", "config", "info", "inspect", "exec", "compose", "ps"} else "other"
                self.failure_detail = {"tool": tool, "operation": operation, "exit_code": result.returncode,
                                       "error_code": error_code if isinstance(error_code, str) and error_code in CLI_ERROR_CODES else "command_failed"}
                raise ProbeFailure("external command failed", "external_command_failed")
        return result.returncode, result.stdout, result.stderr

    @staticmethod
    def diagnostic_text(value):
        return value.decode(errors="replace") if isinstance(value, bytes) else value or ""

    def private_diagnostic(self, category, data):
        # No writes before the exact run and daemon guard has succeeded.
        if not hasattr(self, "report_dir"):
            return
        folder = self.report_dir / "private-cli"
        require_scoped_path(self.root, folder)
        folder.mkdir(mode=0o700, exist_ok=True)
        check(folder.stat().st_uid == os.geteuid() and folder.stat().st_mode & 0o077 == 0,
              "private diagnostic directory ownership or mode is unsafe")
        path = folder / (category + "-" + str(time.time_ns()) + ".json")
        require_scoped_path(self.root, path)
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, "w") as stream:
            json.dump(data, stream, indent=2)
            stream.write("\n")

    def retain_diagnostic(self, category, data):
        try:
            self.private_diagnostic(category, data)
        except Exception:
            # Diagnostic retention must not suppress the original failure or
            # prevent scoped stop attempts and the public failure report.
            self.diagnostics_failed = True

    def private_exception(self, failure):
        self.retain_diagnostic("probe-exception", {"type": type(failure).__name__,
                              "traceback": "".join(traceback.format_exception(failure))})

    def cli(self, *args, expected=0, env=None):
        return self.command([self.anas, *args], expected=expected, env=env)

    def docker_json(self, *args):
        return json.loads(self.command([self.docker, *args])[1])

    def inspect(self):
        return {name: self.docker_json("inspect", self.prefix + name)[0] for name in self.modules}

    def ids(self):
        return {name: item["Id"] for name, item in self.inspect().items()}

    def active(self):
        raw = (self.workspace / ".anas/state/active.yml").read_text()
        match = re.search(r"^active_deployment:\s*([^\s]+)", raw, re.M)
        check(match is not None, "active deployment is not persisted")
        return match[1]

    def apply(self, expected=0, env=None):
        return self.cli("apply", "-w", str(self.workspace), "--root", str(self.fixture),
                        "--update-lock", "--no-snapshot", "-y", "--json", expected=expected, env=env)

    def set_root(self, root, expected=0, env=None):
        self.cli("config", "set", "global.temp_path", str(root), "--defer", "-w", str(self.workspace),
                 "--root", str(self.fixture), "--json")
        return self.apply(expected=expected, env=env)

    def rollback_history(self, deployment, previous_paths, artifact_digest, expected_root):
        # Rollback has no -y flag. Exercise the actual historical CLI entry,
        # then independently prove fresh leases and immutable history.
        self.cli("rollback", deployment, "-w", str(self.workspace), "--json")
        check(self.active() != deployment, "historical temp rollback reactivated a frozen old deployment")
        current = require_mounts(self.inspect(), expected_root)
        artifact = self.workspace / ".anas/deployments" / deployment
        check(set(current).isdisjoint(set(previous_paths)) and tree_digest(artifact) == artifact_digest,
              "historical rollback reused released leases or changed artifact")
        return current

    def status(self):
        value = json.loads(self.cli("temp", "status", "-w", str(self.workspace), "--json")[1])
        check(value.get("ok") is True, "temp status did not return a successful JSON contract")
        return value

    def record(self, case_id, evidence):
        self.results.append({"case_id": case_id, "requirements": CASE_REQUIREMENTS.get(case_id, []), "status": "passed", "evidence": evidence})

    def prepare(self):
        check(not self.workspace.exists(), "refusing to reuse an existing workspace; select a new run-id")
        uid, gid = (1001, 1001) if os.geteuid() == 0 else (os.getuid(), os.getgid())
        self.container_uid, self.container_gid = uid, gid
        self.fixture.mkdir()
        (self.fixture / "contracts").mkdir()
        image = os.environ.get("ANAS_TEST_TEMP_IMAGE", "busybox:1.37.0")
        for name in self.modules:
            folder = self.fixture / "modules" / name
            folder.mkdir(parents=True)
            dependencies = "" if name == "producer" else "dependencies:\n  requires: [" + ("producer" if name == "consumer" else "consumer") + "]\n"
            temporary = ""
            if name != "passive":
                for declaration in (["runtime", "scratch"] if name == "producer" else ["runtime"]):
                    temporary += f"  - name: {declaration}\n    service: anas_{name}\n    target: /{declaration}\n    lifecycle: container\n    uid: {uid}\n    gid: {gid}\n    mode: \"0750\"\n    min_free_bytes: 1048576\n    min_free_inodes: 10\n    required_features: [hardlink, exec]\n"
                temporary = "temporary_directories:\n" + temporary
            (folder / "module.yml").write_text(f"api_version: anas.module/v1\nkind: Module\nname: {name}\nversion: 1.0.0\nrevision: 1\nstatus: release\nabi:\n  supports: [anas.module-hook/v1]\nruntime:\n  type: compose\n  compose_file: docker-compose.yml\n{dependencies}{temporary}upgrade:\n  data_breaking: []\nconfig: {{}}\n")
            health = '\n    healthcheck:\n      test: ["CMD", "' + ("false" if name == "passive" else "true") + '"]\n      interval: 1s\n      timeout: 1s\n      retries: 1'
            (folder / "docker-compose.yml").write_text(f'services:\n  anas_{name}:\n    image: {image}\n    container_name: ${{CONTAINER_PREFIX}}${{MODULE_NAME}}\n    user: "{uid}:{gid}"\n    command: ["sh", "-c", "trap \'exit 0\' TERM; while :; do sleep 1; done"]\n    stop_grace_period: 5s{health}\n')
        config = self.root / "fixture.yml"
        config.write_text(f"global:\n  base_domain: temp.test\n  email: test@example.invalid\n  container_prefix: {self.prefix}\n  network_prefix: {self.prefix}\n  temp_path: {self.root_a}\nmodules:\n  producer: {{}}\n  consumer: {{}}\n  passive: {{}}\n")
        self.root_a.mkdir()
        self.root_b.mkdir()
        self.cli("init", str(self.workspace), "-c", str(config), "--module-root", str(self.fixture), "-y", "--json")
        self.created = True
        self.apply()
        check(all(x["State"]["Running"] for x in self.inspect().values()), "fixture containers are not all running")

    def core(self):
        self.prepare()
        mounts_a = require_mounts(self.inspect(), self.root_a)
        for name in ("producer", "consumer"):
            self.command([self.docker, "exec", self.prefix + name, "sh", "-c", "printf synthetic > /runtime/probe; test $(id -u) -ne 0"])
        for path in mounts_a:
            st = path.stat()
            check(st.st_uid == self.container_uid and st.st_gid == self.container_gid and (st.st_mode & 0o777) == 0o750,
                  "allocated ownership/mode differs from declaration")
            (path / "do-not-copy-temp-sentinel").write_text(self.run_id)
        check(all(str(path) in json.dumps(self.status()) for path in mounts_a), "status omitted an actual mounted registered path")
        self.record("TEMP-T-013", "actual isolated mounts and non-root write/owner/mode")
        old_ids, deployment_a = self.ids(), self.active()
        artifact_a = self.workspace / ".anas/deployments" / deployment_a
        digest_a = tree_digest(artifact_a)
        foreign = self.root_a / "unmanaged-sentinel"
        foreign.write_text("unmanaged")
        events_file = self.report_dir / "docker-events.jsonl"
        with events_file.open("wb") as stream:
            events = subprocess.Popen([self.docker, "events", "--format", "{{json .}}"], stdout=stream, stderr=subprocess.DEVNULL)
            try:
                time.sleep(0.3)
                self.set_root(self.root_b)
                time.sleep(0.3)
            finally:
                events.terminate()
                events.wait(timeout=10)
        require_changed(old_ids, self.ids(), self.modules)
        check(self.active() != deployment_a and tree_digest(artifact_a) == digest_a,
              "switch rewrote an immutable deployment or did not generate a new one")
        mounts_b = require_mounts(self.inspect(), self.root_b)
        check(all(not (path / "do-not-copy-temp-sentinel").exists() for path in mounts_b), "old temporary contents were copied")
        check(all(not path.exists() for path in mounts_a) and foreign.read_text() == "unmanaged", "switch cleanup removed unmanaged data or retained released trees")
        require_event_order([json.loads(line) for line in events_file.read_text().splitlines()], self.prefix, self.modules)
        self.record("TEMP-T-014", "all containers recreated in dependency order; immutable artifact and unmanaged sentinel preserved")
        # Same resolved path, and failed preflight, must leave runtime intact.
        before = self.ids()
        self.set_root(str(self.root_b) + "/./")
        check(self.ids() == before, "same normalized temp root caused a full restart")
        rejected = self.root / "not-a-directory"
        rejected.write_text("synthetic")
        code, _, _ = self.set_root(rejected, expected=None)
        check(code != 0 and self.ids() == before, "target preflight failure stopped current services")
        self.set_root(self.root_b)
        self.fail_once_switch()
        self.fail_once_stop_switch()
        time.sleep(3)
        check(self.inspect()["passive"]["State"].get("Health", {}).get("Status") == "unhealthy", "health-boundary fixture did not become unhealthy")
        self.set_root(self.root_a)
        check(all(item["State"]["Running"] for item in self.inspect().values()), "unrelated unhealthy probe blocked switch")
        self.record("TEMP-T-015", "preflight preserves IDs; one-shot up/down failures restore old mounted content; stop_failed primary and previous_restore succeeded are independent; unrelated unhealthy app does not block")
        self.set_root(self.root_b)
        current_mounts = self.rollback_history(deployment_a, mounts_a, digest_a, self.root_a)
        self.record("TEMP-T-019", "historical configuration yields fresh deployment and fresh leases")
        self.active_reference_gc(current_mounts)

    def fail_once_switch(self):
        shim_dir = self.root / "fault-bin"
        shim_dir.mkdir(exist_ok=True)
        marker = self.root / "up-failure.marker"
        shim = shim_dir / "docker"
        shim.write_text("#!/usr/bin/env python3\nimport os,subprocess,sys\na=sys.argv[1:]\nmarker=os.environ['ANAS_TEST_FAULT_MARKER']\nif 'compose' in a and 'up' in a and any(flag in a and a[a.index(flag)+1]==os.environ['ANAS_TEST_FAULT_PROJECT'] for flag in ('-p','--project-name')) and not os.path.exists(marker):\n open(marker,'x').close();sys.exit(97)\nos.execv(os.environ['ANAS_TEST_REAL_DOCKER'],[os.environ['ANAS_TEST_REAL_DOCKER'],*a])\n")
        shim.chmod(0o700)
        env = dict(os.environ, PATH=str(shim_dir) + os.pathsep + os.environ["PATH"], ANAS_TEST_REAL_DOCKER=self.docker,
                   ANAS_TEST_FAULT_MARKER=str(marker), ANAS_TEST_FAULT_PROJECT=self.prefix + "consumer")
        old = require_mounts(self.inspect(), self.root_b)
        for path in old:
            (path / "rollback-sentinel").write_text("preserve-on-failure")
        code, _, _ = self.set_root(self.root_a, expected=None, env=env)
        check(code != 0 and marker.exists(), "one-shot real Compose up fault did not reject switch")
        restored = require_mounts(self.inspect(), self.root_b)
        check(set(restored) == set(old) and all((path / "rollback-sentinel").read_text() == "preserve-on-failure" for path in old),
              "failed switch did not restore old registered mounts/content")
        self.set_root(self.root_b)

    def active_reference_gc(self, mounts):
        self.cli("temp", "gc", "--dry-run", "-w", str(self.workspace), "--json")
        code, _, _ = self.cli("temp", "gc", "--json", expected=None)
        check(code != 0, "explicit GC accepted an implicit workspace")
        self.cli("temp", "gc", "-w", str(self.workspace), "--json")
        check(all(path.exists() for path in mounts), "GC removed a running container's bind directory")
        self.command([self.docker, "stop", self.prefix + "consumer"])
        self.cli("temp", "gc", "-w", str(self.workspace), "--json")
        check(all(path.exists() for path in mounts), "GC treated a stopped but still bound container as released")
        env = dict(os.environ, DOCKER_HOST="unix://" + str(self.root / "missing-anas-test.sock"))
        self.cli("temp", "gc", "-w", str(self.workspace), "--json", expected=None, env=env)
        check(all(path.exists() for path in mounts), "unknown Docker state was treated as unreferenced")
        self.command([self.docker, "start", self.prefix + "consumer"])
        self.host_mount_reference_gc(mounts)
        self.record("TEMP-T-016", "running/stopped binds and unknown Docker endpoint retain trees; private host bind blocks GC with no containers; unmount permits explicit reclamation")

    def fail_once_stop_switch(self):
        folder = self.root / "stop-fault-bin"
        require_scoped_path(self.root, folder)
        folder.mkdir(mode=0o700)
        marker, shim = folder / "down-failure.marker", folder / "docker"
        shim.write_text("#!/usr/bin/env python3\nimport os,sys\na=sys.argv[1:]\nmarker=os.environ['ANAS_TEST_FAULT_MARKER']\nproject=next((a[a.index(f)+1] for f in ('-p','--project-name') if f in a), '')\nif 'compose' in a and 'down' in a and project==os.environ['ANAS_TEST_FAULT_PROJECT'] and not os.path.exists(marker):\n open(marker,'x').close();sys.stderr.write('synthetic-stop-failure\\n');sys.exit(98)\nos.execv(os.environ['ANAS_TEST_REAL_DOCKER'],[os.environ['ANAS_TEST_REAL_DOCKER'],*a])\n")
        shim.chmod(0o700)
        project = self.prefix + "consumer"
        env = dict(os.environ, PATH=str(folder) + os.pathsep + os.environ["PATH"], ANAS_TEST_REAL_DOCKER=self.docker,
                   ANAS_TEST_FAULT_MARKER=str(marker), ANAS_TEST_FAULT_PROJECT=project)
        old = require_mounts(self.inspect(), self.root_b)
        deployment = self.active()
        for path in old:
            (path / "stop-failure-sentinel").write_text("preserve-after-down-failure")
        code, stdout, _ = self.set_root(self.root_a, expected=None, env=env)
        require_stop_failure(code, json.loads(stdout), marker, project)
        restored = require_mounts(self.inspect(), self.root_b)
        check(self.active() == deployment and set(restored) == set(old)
              and all((path / "stop-failure-sentinel").read_text() == "preserve-after-down-failure" for path in restored)
              and all(item["State"]["Running"] for item in self.inspect().values()),
              "failed stop did not restore the old deployment's actual mounts and contents")
        self.set_root(self.root_b)

    def host_mount_reference_gc(self, mounts):
        private_mount_helpers()
        check(len(mounts) == 3, "host mount probe requires the complete registered allocation")
        lease = mounts[0]
        source, target = self.root / "core-host-bind-source", lease / "remaining-host-bind"
        require_scoped_path(self.root, source)
        require_scoped_path(self.root, target)
        check(not source.exists() and not target.exists(), "refusing to reuse a host bind fixture")
        source.mkdir(mode=0o700)
        target.mkdir(mode=0o700)
        marker = source / "host-bind-sentinel"
        marker.write_text("preserve-host-mount")
        privileged = ["sudo", "-n"] if os.geteuid() != 0 else []
        self.command([*privileged, "mount", "--bind", str(source), str(target)])
        self.host_bind_mounts.add(target)
        require_bind_mount(source, target)
        check((target / marker.name).read_text() == "preserve-host-mount", "private host bind contents are incorrect")
        code, output, _ = self.cli("stop", "-w", str(self.workspace), "--json", expected=None)
        require_host_stop_refusal(code, json.loads(output), "producer")
        check(not self.command([self.docker, "ps", "-aq"])[1].strip(), "host mount probe requires no remaining private Docker containers")
        self.cli("temp", "gc", "-w", str(self.workspace), "--json")
        require_host_mount_retained(self.status(), lease, marker)
        check((target / marker.name).read_text() == "preserve-host-mount", "GC altered mounted contents")
        private_mount_helpers()
        require_scoped_path(self.root, target)
        check(target in self.host_bind_mounts, "cannot unmount an unowned host bind")
        require_bind_mount(source, target)
        self.command([*privileged, "umount", str(target)])
        self.host_bind_mounts.remove(target)
        check(target not in mount_points(), "host bind mount remained after unmount")
        self.cli("stop", "-w", str(self.workspace), "--json")
        released = [item for item in self.status().get("directories", []) if item.get("path") == str(lease)]
        check(len(released) == 1 and released[0].get("state") == "released" and released[0].get("reclaimable") is True,
              "unmount did not permit safe lease release")
        self.cli("temp", "gc", "-w", str(self.workspace), "--json")
        check(all(not path.exists() for path in mounts) and marker.read_text() == "preserve-host-mount",
              "unmount did not permit GC or GC removed the external source sentinel")

    def cleanup(self):
        if self.created:
            self.cli("stop", "-w", str(self.workspace), "--json", expected=0)
            self.cli("temp", "gc", "-w", str(self.workspace), "--json", expected=0)
            remaining = self.command([self.docker, "ps", "-aq", "--filter", "name=" + self.prefix])[1]
            check(not remaining.strip(), "fixture containers remain after explicit cleanup")
        self.cleanup_state = "complete"

    def stop_owned_container(self, prefix, module, workspace):
        name = prefix + module
        query = [self.docker, "ps", "-aq", "--no-trunc", "--filter", "name=^/" + re.escape(name) + "$"]
        query_code, body, _ = self.command(query)
        check(query_code == 0, "inspection cleanup container inventory query failed")
        identities = body.decode().split()
        check(len(identities) <= 1 and (not identities or re.fullmatch(r"[a-f0-9]{64}", identities[0])),
              "inspection cleanup exact container name or identity is ambiguous")
        if not identities:
            return  # A successful inventory proves normal down already removed it.
        identity = identities[0]
        observed = self.docker_json("inspect", identity)
        check(isinstance(observed, list) and len(observed) == 1, "inspection cleanup Docker observation is incomplete")
        require_cleanup_container(observed[0], identity, name, module, workspace)
        if not observed[0]["State"]["Running"]:
            return
        code, _, _ = self.command([self.docker, "stop", identity], expected=None)
        # Stop by the observed ID, so a same-name replacement cannot be stopped.
        query_code, body, _ = self.command([self.docker, "ps", "-aq", "--no-trunc", "--filter", "id=" + identity])
        check(query_code == 0, "inspection cleanup post-stop inventory query failed")
        remaining = body.decode().split()
        if not remaining:
            return
        check(code == 0 and remaining == [identity], "inspection cleanup stop failed and the owned container still exists")
        observed = self.docker_json("inspect", identity)
        check(isinstance(observed, list) and len(observed) == 1, "inspection cleanup stopped-container observation is incomplete")
        require_cleanup_container(observed[0], identity, name, module, workspace)
        check(observed[0]["State"]["Running"] is False, "inspection cleanup container is still running")

    def stop_for_inspection(self):
        errors = []
        for name in reversed(self.modules):
            try:
                self.stop_owned_container(self.prefix, name, self.workspace)
            except Exception as failure:
                self.private_exception(failure)
                errors.append({"module": name, "code": "stop_failed"})
        self.cleanup_errors.extend(errors)
        self.cleanup_state = "failed" if errors else "stopped; recovery materials preserved"
        check(not errors, "run-scoped stop failed; remaining stops were attempted and materials retained")

    def write_report(self, status, error=None):
        covered = sorted({requirement for result in self.results for requirement in result["requirements"]})
        report = {"schema": "anas.workspace-temp-storage-e2e/v1", "run_id": self.run_id,
                  "status": status, "scope": "core", "source_digest": self.source_digest,
                  "binary_digest": "sha256:" + hashlib.sha256(Path(self.anas).read_bytes()).hexdigest(),
                  "results": self.results, "failure_detail": self.failure_detail,
                  "covered_requirements": covered,
                  "not_run": [requirement for requirement in E2E_REQUIREMENTS if requirement not in covered],
                  "cleanup": self.cleanup_state, "cleanup_errors": self.cleanup_errors,
                  "diagnostics": "retention_failed" if getattr(self, "diagnostics_failed", False) else "private-cli",
                  "error": public_error(error) if error is not None else None}
        path = self.report_dir / "workspace-temp-storage.json"
        path.write_text(json.dumps(report, indent=2) + "\n")
        path.chmod(0o600)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("suite", choices=["core"], nargs="?", default="core")
    parser.parse_args()
    suite = None
    error = None
    try:
        suite = Suite()
        suite.core()
    except Exception as failure:
        error = public_error(failure)
        if suite is not None:
            suite.private_exception(failure)
    finally:
        if suite is not None:
            try:
                if error is None:
                    suite.cleanup()
                elif suite.created:
                    suite.stop_for_inspection()
            except Exception as failure:
                suite.private_exception(failure)
                suite.cleanup_state = "failed"
                error = error or public_error(ProbeFailure("cleanup failed", "cleanup_failed"))
            suite.write_report("failed" if error else "passed", error)
    if error:
        print(error["code"] + ": " + error["message"], file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
