#!/usr/bin/env python3
# TEST_CASES: TEMP-T-021
"""Three fixed lifecycle actions for the isolated Collabora browser fixture."""
import argparse
import hashlib
import http.client
import importlib.util
import io
import ipaddress
import json
import os
from pathlib import Path
import re
import shutil
import signal
import socket
import ssl
import stat
import struct
import subprocess
import tarfile
import time
import xml.etree.ElementTree as ET

_spec = importlib.util.spec_from_file_location("temp_core", Path(__file__).with_name("server-workspace-temp-storage-e2e.py"))
core = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(core)
_namespace_spec = importlib.util.spec_from_file_location("temp_extended", Path(__file__).with_name("server-workspace-temp-storage-extended-e2e.py"))
namespace = importlib.util.module_from_spec(_namespace_spec)
_namespace_spec.loader.exec_module(namespace)
ACTIONS = ("stop-start", "rebuild", "switch-a-to-b")
TARGET = "/var/lib/anas-collabora"
MODULES = ("lego", "traefik", "samba_dc", "postgres", "llng", "eturnal", "nextcloud", "collabora")
COLLABORA_READY_TIMEOUT = 300


def safe_cli_error(raw, prefix):
    # Retain enumerated causes only. Messages, command tails and full project
    # names may contain credentials or host paths and never enter the report.
    codes = {"internal", "usage", "confirmation_required", "compose_missing", "workspace_missing", "workspace_exists",
             "module_root_missing", "module_root_invalid", "module_bootstrap_failed", "config_import_failed", "config_invalid",
             "mkdir_failed", "layout_failed", "fstype_unknown", "write_failed", "state_unreadable", "deployment_unreadable",
             "resource_invalid", "resource_revoke_failed", "resource_state_failed", "stop_failed", "start_failed", "quiesce_failed",
             "temp_recovery_failed", "temp_reconcile_failed", "temp_preflight_failed", "temp_path_invalid", "temp_plan_failed",
             "temp_transition_failed", "temp_snapshot_failed", "temp_release_failed", "temp_state_failed", "temp_mount_failed"}
    try:
        document = json.loads(raw)
    except (ValueError, UnicodeError, TypeError, RecursionError):
        return None
    if not isinstance(document, dict) or document.get("api_version") != "anas.dev/cli/v1" or document.get("ok") is not False:
        return None
    error = document.get("error")
    if not isinstance(error, dict) or not isinstance(error.get("code"), str) or error["code"] not in codes:
        return None
    result = {"code": error["code"]}
    detail = error.get("detail")
    if not isinstance(detail, dict):
        return result
    safe = {}
    primary = detail.get("primary")
    if isinstance(primary, dict):
        cause = {}
        if isinstance(primary.get("code"), str) and primary["code"] in codes:
            cause["code"] = primary["code"]
        command = primary.get("command")
        if isinstance(command, dict):
            fields = {}
            if isinstance(command.get("phase"), str) and command["phase"] in {"up", "down", "start", "stop", "restart", "rm", "run", "build", "config", "pull", "ps", "exec"}:
                fields["phase"] = command["phase"]
            if type(command.get("exit_code")) is int and -1 <= command["exit_code"] <= 255:
                fields["exit_code"] = command["exit_code"]
            if isinstance(prefix, str) and prefix:
                module = next((name for name in MODULES if command.get("project") == prefix + name), None)
                if module:
                    fields["module"] = module
            if fields:
                cause["command"] = fields
        if cause:
            safe["primary"] = cause
    recovery = detail.get("recovery")
    if isinstance(recovery, list):
        outcomes = [{"phase": item["phase"], "status": item["status"]} for item in recovery
                    if isinstance(item, dict) and isinstance(item.get("phase"), str)
                    and item["phase"] in {"candidate_stop", "previous_restore", "primary_persist", "recovery_persist"}
                    and isinstance(item.get("status"), str) and item["status"] in {"succeeded", "failed"}]
        if outcomes:
            safe["recovery"] = outcomes
    if safe:
        result["detail"] = safe
    return result




def scoped_path(root, path):
    core.check(path.is_absolute() and path == Path(os.path.normpath(path)) and path.is_relative_to(root) and path != root,
               "Collabora fixture path escaped the explicit run scope")
    current = path
    while current != root.parent:
        core.check(not current.is_symlink(), "Collabora fixture path contains a symlink")
        current = current.parent
    core.check(path.resolve().is_relative_to(root), "Collabora fixture path resolves outside the run scope")
    return path


def owned_containers(items, workspace):
    result = {}
    artifacts = workspace / ".anas" / "deployments"
    for item in items:
        labels = item.get("Config", {}).get("Labels") or {}
        directory = Path(labels.get("com.docker.compose.project.working_dir", "/"))
        if not directory.is_absolute() or not directory.is_relative_to(artifacts):
            continue
        core.check(not directory.is_symlink() and directory.resolve().is_relative_to(artifacts),
                   "fixture Compose working directory resolves outside its workspace")
        if labels.get("com.docker.compose.oneoff", "false").lower() == "true":
            continue
        key = (labels.get("com.docker.compose.project"), labels.get("com.docker.compose.service"))
        core.check(all(key) and key not in result, "fixture container ownership is ambiguous")
        result[key] = item
    return result


def collabora_container(items):
    matches = [item for (_, service), item in items.items() if service == "anas_collabora"]
    core.check(len(matches) == 1, "exactly one owned Collabora container is required")
    return matches[0]


class Actions:
    def __init__(self):
        self.root = Path(os.environ["ANAS_TEST_WORK_ROOT"])
        self.run_id = os.environ["ANAS_TEST_RUN_ID"]
        self.prefix = "anas_temp_" + hashlib.sha256(self.run_id.encode()).hexdigest()[:10] + "_edit_"
        self.failure = None
        self.anas = Path(os.environ["ANAS_TEST_ANAS_CMD"])
        self.docker = shutil.which(os.environ.get("DOCKER_CMD", "docker"))
        core.check(self.docker is not None and self.anas.is_absolute() and self.anas.is_file(), "absolute ANAS binary and Docker client required")
        core.check(re.fullmatch(r"sha256:[a-f0-9]{64}", os.environ.get("ANAS_TEST_SOURCE_DIGEST", "")) is not None,
                   "the frozen source digest is required")
        self.source_digest = os.environ["ANAS_TEST_SOURCE_DIGEST"]
        self.binary_digest = "sha256:" + hashlib.sha256(self.anas.read_bytes()).hexdigest()
        core.check(not os.environ.get("DOCKER_CONTEXT"), "Docker context override is forbidden for the browser fixture")
        namespace.require_private_mount_namespace()
        # The mount holder can keep the host network. Actions must instead
        # share the private network of both verified run-scoped daemons.
        net_identities = [Path("/proc/self/ns/net").stat().st_ino,
                          *[Path("/proc", os.environ[key], "ns/net").stat().st_ino
                            for key in ("ANAS_TEST_DOCKER_PID", "ANAS_TEST_CONTAINERD_PID")]]
        core.check(len(set(net_identities)) == 1 and net_identities[0] != Path("/proc/1/ns/net").stat().st_ino,
                   "browser lifecycle helper must share this run's private network namespace")
        docker_root = self.command([self.docker, "info", "--format", "{{.DockerRootDir}}"], operation="inspect_private_daemon")[1].decode().strip()
        core.require_isolation(self.root, self.run_id, os.environ.get("DOCKER_HOST", ""), docker_root)
        self.workspace = scoped_path(self.root, self.root / "collabora-workspace")
        self.module_root = scoped_path(self.root, Path(os.environ["ANAS_TEST_MODULE_ROOT"]))
        core.check(self.module_root == self.root / "src" and (self.module_root / "modules/collabora/module.yml").is_file(),
                   "browser actions require this run's frozen source root")
        self.root_a = scoped_path(self.root, self.root / "collabora-temp-a")
        self.root_b = scoped_path(self.root, self.root / "collabora-temp-b")
        self.report = scoped_path(self.root, self.root / "reports")
        core.check(self.report.is_dir() and self.workspace.is_dir(), "prepared fixture and report directories are required")

    def command(self, args, expected=0, operation="inspect_runtime", timeout=900):
        timeout = timeout() if callable(timeout) else timeout
        try:
            result = subprocess.run([str(arg) for arg in args], stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
        except subprocess.TimeoutExpired:
            self.failure = {"operation": operation, "reason": "timeout"}
            raise core.ProbeFailure("fixed lifecycle operation timed out; output suppressed")
        if expected is not None:
            if result.returncode != expected:
                self.failure = {"operation": operation, "exit_code": result.returncode}
                if args and str(args[0]) == str(self.anas) and "--json" in args:
                    error = safe_cli_error(result.stdout, self.prefix)
                    if error is not None:
                        self.failure["error"] = error
            core.check(result.returncode == expected, f"fixed lifecycle operation failed (exit {result.returncode}; output suppressed)")
        return result.returncode, result.stdout

    def cli(self, *args, timeout=900):
        operation = {"temp": "inspect_temporary_storage", "stop": "normal_stop_collabora", "start": "start_collabora",
                     "restart": "rebuild_collabora", "config": "configure_temporary_root", "apply": "switch_temporary_root"}[args[0]]
        return self.command([self.anas, *args, "-w", self.workspace, "--json"], operation=operation, timeout=timeout)[1]

    def inventory(self, timeout=900):
        ids = self.command([self.docker, "ps", "--all", "--quiet", "--no-trunc"], timeout=timeout)[1].decode().split()
        items = json.loads(self.command([self.docker, "inspect", *ids], timeout=timeout)[1]) if ids else []
        return owned_containers(items, self.workspace)

    def mounted_path(self, items, expected_root, timeout=900):
        container = collabora_container(items)
        core.check(container.get("State", {}).get("Running") is True, "Collabora did not remain running")
        pid = container.get("State", {}).get("Pid")
        core.check(type(pid) is int and pid > 1, "Collabora must have a live host process")
        process = Path("/proc", str(pid))
        arguments = process.joinpath("cmdline").read_bytes().split(b"\0")
        core.check(arguments and arguments[0] == b"/usr/bin/coolwsd", "container PID 1 must be coolwsd after wrapper exec")
        identity = process.joinpath("status").read_text()
        for kind in ("Uid", "Gid"):
            value = re.search(r"^" + kind + r":\s+([0-9]+)\s+([0-9]+)\s+([0-9]+)\s+([0-9]+)$", identity, re.M)
            core.check(value is not None and set(value.groups()) == {"1001"}, "coolwsd must run as UID/GID 1001")
        mounts = [item for item in container.get("Mounts", []) if item.get("Destination") == TARGET]
        core.check(len(mounts) == 1 and mounts[0].get("Type") == "bind" and mounts[0].get("RW") is True,
                   "Collabora runtime must have one writable bind mount")
        source = scoped_path(self.root, Path(mounts[0].get("Source", "")))
        core.check(source.is_relative_to(expected_root) and source.is_dir(), "Collabora runtime mount uses the wrong temporary root")
        status = json.loads(self.cli("temp", "status", timeout=timeout))
        registered = [item for item in status.get("directories", []) if item.get("module") == "collabora" and
                      item.get("name") == "runtime" and item.get("state") == "active" and item.get("path") == str(source)]
        core.check(len(registered) == 1, "actual Collabora mount is not an active registered lease")
        marker = source / ".anas-temp-owner.yml"
        raw = self.command([self.docker, "cp", container["Id"] + ":" + TARGET + "/.anas-temp-owner.yml", "-"], timeout=timeout)[1]
        with tarfile.open(fileobj=io.BytesIO(raw), mode="r:*") as archive:
            members = archive.getmembers()
            core.check(len(members) == 1 and members[0].isfile() and members[0].size <= 1048576,
                       "container ownership marker must be one regular file")
            mounted_marker = archive.extractfile(members[0]).read()
        core.check(marker.is_file() and not marker.is_symlink() and marker.read_bytes() == mounted_marker,
                   "container namespace marker differs from the registered host directory")
        return source

    def readiness_check(self, condition, reason):
        if not condition:
            self.failure = {"operation": "wait_collabora_ready", "reason": reason}
        core.check(condition, "Collabora readiness failed: " + reason)

    def readiness_state(self, container):
        state = container.get("State")
        self.readiness_check(isinstance(state, dict) and all(type(state.get(name)) is bool
                             for name in ("OOMKilled", "Running", "Restarting")), "invalid_runtime_state")
        self.readiness_check(state["OOMKilled"] is False, "container_oom")
        self.readiness_check(state["Running"] is True and state["Restarting"] is False, "container_exited")
        health = state.get("Health")
        self.readiness_check(health is None or isinstance(health, dict), "invalid_runtime_state")
        status = (health or {}).get("Status")
        self.readiness_check(status in (None, "starting", "healthy", "unhealthy"), "invalid_runtime_state")
        self.readiness_check(status != "unhealthy", "container_unhealthy")
        return state

    def discovery_ready(self, timeout):
        domain = os.environ.get("ANAS_TEST_DOMAIN", "temp-edit.test")
        self.readiness_check(re.fullmatch(r"[a-z0-9][a-z0-9.-]*\.(?:test|invalid)", domain) is not None and ".." not in domain,
                             "invalid_discovery_domain")
        address = ipaddress.ip_address(os.environ["ANAS_TEST_ENTRY_IP"])
        self.readiness_check(address.version == 4 and address.is_private and not address.is_loopback
                             and not address.is_link_local and not address.is_unspecified and not address.is_multicast,
                             "invalid_discovery_address")
        port = int(os.environ.get("ANAS_TEST_ENTRY_PORT", "19071"))
        self.readiness_check(1024 <= port <= 65535, "invalid_discovery_port")
        host = "collabora." + domain + ":" + str(port)
        connection = http.client.HTTPSConnection(str(address), port, context=ssl._create_unverified_context(), timeout=timeout)
        try:
            connection.request("GET", "/hosting/discovery", headers={"Host": host})
            response = connection.getresponse()
            self.readiness_check(response.status in (200, 404, 502, 503, 504), "unexpected_discovery_status")
            if response.status != 200:
                return False
            body = response.read(4 * 1024 * 1024 + 1)
            self.readiness_check(len(body) <= 4 * 1024 * 1024, "invalid_discovery_body")
            try:
                document = ET.fromstring(body)
            except ET.ParseError:
                self.readiness_check(False, "invalid_discovery_body")
            self.readiness_check(document.tag == "wopi-discovery" and any(
                action.get("urlsrc", "").startswith("https://" + host + "/browser/")
                for action in document.findall("net-zone/app/action")), "invalid_discovery_body")
            return True
        except (OSError, http.client.HTTPException):
            # Only transport startup failures and the explicit proxy startup
            # statuses above are pending. Validation failures are never retried.
            return False
        finally:
            connection.close()

    def office_ready(self, timeout):
        # A live CODE endpoint alone does not prove Nextcloud has activated its
        # discovery cache after a full rebuild. This is a read-only probe of the
        # real Module startup worker; the test never activates Office itself.
        status, _ = self.command([self.docker, "exec", self.prefix + "nextcloud", "test", "-f",
                                  "/run/nextcloud-office.ready"], expected=None,
                                 operation="inspect_office_activation", timeout=timeout)
        self.readiness_check(status in (0, 1), "office_activation_probe_failed")
        return status == 0

    def wait_ready(self, expected_root, items=None, timeout=COLLABORA_READY_TIMEOUT):
        self.readiness_check(type(timeout) in (int, float) and 0 < timeout <= COLLABORA_READY_TIMEOUT,
                             "invalid_wait_timeout")
        deadline = time.monotonic() + timeout
        def remaining():
            value = deadline - time.monotonic()
            self.readiness_check(value > 0, "timeout")
            return min(30, value)
        current = self.inventory(timeout=remaining) if items is None else items
        first = collabora_container(current)
        state = self.readiness_state(first)
        identifier, pid, restarts = first.get("Id"), state.get("Pid"), first.get("RestartCount")
        self.readiness_check(isinstance(identifier, str) and re.fullmatch(r"[a-f0-9]{64}", identifier) is not None,
                             "invalid_container_identity")
        self.readiness_check(type(pid) is int and pid > 1 and type(restarts) is int and restarts >= 0,
                             "invalid_process_identity")
        self.readiness_check(restarts == 0, "container_restarted")
        def observe(items):
            container = collabora_container(items)
            self.readiness_check(container.get("Id") == identifier, "container_replaced")
            state = self.readiness_state(container)
            self.readiness_check(state.get("Pid") == pid and container.get("RestartCount") == restarts, "container_restarted")
            return container
        while True:
            remaining()
            observe(current)
            arguments = Path("/proc", str(pid), "cmdline").read_bytes().split(b"\0")
            if arguments[0] == b"/usr/bin/coolwsd":
                try:
                    source = self.mounted_path(current, expected_root, timeout=remaining)
                except core.ProbeFailure:
                    if self.failure is None:
                        self.failure = {"operation": "wait_collabora_ready", "reason": "invalid_temporary_mount"}
                    raise
                marker = source / ".templates-version"
                self.readiness_check(marker.is_file() and not marker.is_symlink()
                                     and marker.read_text().strip() == "26.04.2.4.1", "invalid_templates")
                self.readiness_check(all((source / name).is_dir() and not (source / name).is_symlink()
                                         and (source / name).stat().st_dev == source.stat().st_dev
                                         for name in ("systemplate", "office", "child-roots", "cache")), "invalid_templates")
                if self.discovery_ready(min(5, remaining())) and self.office_ready(remaining):
                    current = self.inventory(timeout=remaining)
                    observe(current)
                    remaining()
                    return current, source
            else:
                self.readiness_check(arguments[:2] == [b"/usr/local/bin/anas-collabora-start", b"--container-start"],
                                     "unexpected_entrypoint")
            time.sleep(min(2, remaining()))
            current = self.inventory(timeout=remaining)

    def run(self, action):
        core.check(action in ACTIONS, "only fixed lifecycle actions are allowed")
        path = self.report / ("collabora-lifecycle-" + action + ".json")
        core.check(not path.exists() and not path.is_symlink(), "refusing to repeat a recorded lifecycle action in the same run")
        self.failure = None
        try:
            return self.run_action(action, path)
        except Exception as error:
            if self.failure is None:
                reason = next((name for kind, name in ((core.ProbeFailure, "ProbeFailure"), (subprocess.TimeoutExpired, "TimeoutExpired"),
                              (OSError, "OSError"), (ValueError, "ValueError"), (KeyError, "KeyError"), (TypeError, "TypeError"))
                               if isinstance(error, kind)), "unexpected_exception")
                self.failure = {"operation": "validate_lifecycle_action", "reason": reason}
            value = {"schema": "anas.workspace-temp-collabora-action/v1", "case_id": "TEMP-T-021", "run_id": self.run_id,
                     "action": action, "status": "failed", "source_digest": self.source_digest,
                     "binary_digest": self.binary_digest, "failure": self.failure}
            try:
                self.write_report(path, value)
            except Exception:
                # Report persistence must not replace the actual first cause.
                self.failure["receipt_write"] = "failed"
            raise

    def write_report(self, path, value):
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, "w") as output:
            json.dump(value, output, indent=2)
            output.write("\n")

    def run_action(self, action, path):
        before = self.inventory()
        old_path = self.mounted_path(before, self.root_a)
        if action == "stop-start":
            self.cli("stop", "collabora")
            core.check(not any(service == "anas_collabora" for _, service in self.inventory()),
                       "normal stop left the Collabora container referenced")
            self.cli("start", "collabora")
        elif action == "rebuild":
            self.cli("restart", "collabora")
        else:
            self.cli("config", "set", "global.temp_path", self.root_b, "--defer", "--root", self.module_root)
            self.cli("apply", "--root", self.module_root, "--update-lock", "--no-snapshot", "-y")
        expected = self.root_b if action == "switch-a-to-b" else self.root_a
        after, new_path = self.wait_ready(expected)
        core.check(collabora_container(before)["Id"] != collabora_container(after)["Id"] and old_path != new_path,
                   "lifecycle action did not rebuild Collabora with a fresh lease")
        if action == "switch-a-to-b":
            core.check(set(before) == set(after) and all(before[key]["Id"] != after[key]["Id"] for key in before),
                       "temporary root switch did not rebuild every enabled container")
            core.check(not old_path.exists(), "successful switch retained the released old temporary tree")
        value = {"schema": "anas.workspace-temp-collabora-action/v1", "case_id": "TEMP-T-021",
                 "run_id": self.run_id, "action": action, "status": "passed", "namespace_marker_verified": True,
                 "fresh_container": True, "fresh_lease": True, "all_containers_rebuilt": action == "switch-a-to-b",
                 "source_digest": self.source_digest, "binary_digest": self.binary_digest}
        self.write_report(path, value)
        return value


def action_socket_path():
    root = Path(os.environ["ANAS_TEST_WORK_ROOT"])
    run_id = os.environ["ANAS_TEST_RUN_ID"]
    core.check(re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,63}", run_id) is not None and
               root == Path("/home/whl/anas-temp-storage-e2e") / run_id,
               "action socket requires this exact isolated run directory")
    expected = root / "collabora-actions.sock"
    configured = Path(os.environ.get("ANAS_TEST_COLLABORA_ACTION_SOCKET", str(expected)))
    core.check(configured == expected, "action socket must use the fixed run-scoped path")
    core.check(len(os.fsencode(configured)) < 108, "run-id makes the UNIX socket path too long")
    return scoped_path(root, configured)


def receive(connection):
    body = bytearray()
    while b"\n" not in body:
        block = connection.recv(1025 - len(body))
        core.check(bool(block), "action socket closed before its structured response")
        body.extend(block)
        core.check(len(body) <= 1024, "action socket message exceeded its fixed size limit")
    core.check(body.endswith(b"\n") and body.count(b"\n") == 1, "action socket accepts one JSON message")
    return json.loads(body)


def fixed_action(message):
    core.check(type(message) is dict and set(message) == {"action"} and message["action"] in ACTIONS,
               "action socket only accepts a fixed lifecycle action")
    return message["action"]


def serve():
    # This process runs in the holder namespaces. The unprivileged browser
    # container only sees a mode-0600 socket and never receives host namespace
    # handles, a Docker socket, or permission to execute arbitrary commands.
    Actions()  # Refuse to create an endpoint until the fixture is verified.
    path = action_socket_path()
    core.check(not path.exists(), "refusing to replace an existing action socket")
    server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    server.bind(str(path))
    path.chmod(0o600)
    server.listen(1)
    owner = path.stat().st_ino
    def terminate(*_):
        raise SystemExit(0)
    signal.signal(signal.SIGTERM, terminate)
    try:
        while True:
            connection, _ = server.accept()
            with connection:
                connection.settimeout(1800)
                try:
                    _, uid, _ = struct.unpack("3i", connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
                    core.check(uid in (0, os.getuid()), "action socket peer UID is not authorized")
                    message = receive(connection)
                    value = Actions().run(fixed_action(message))
                except Exception:
                    value = {"status": "failed", "exit_code": 1, "error": "fixed_action_failed"}
                connection.sendall((json.dumps(value) + "\n").encode())
    finally:
        server.close()
        if path.exists() and path.stat().st_ino == owner:
            path.unlink()


def client(action):
    core.check(action in ACTIONS, "only fixed lifecycle actions are allowed")
    path = action_socket_path()
    info = path.stat()
    core.check(stat.S_ISSOCK(info.st_mode) and stat.S_IMODE(info.st_mode) == 0o600 and info.st_uid in (0, os.getuid()),
               "action endpoint must be its owner's private UNIX socket")
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as connection:
        connection.settimeout(1800)
        connection.connect(str(path))
        connection.sendall((json.dumps({"action": action}) + "\n").encode())
        value = receive(connection)
    core.check(type(value) is dict and value.get("status") == "passed" and value.get("action") == action,
               "fixed lifecycle action failed (server diagnostics preserved separately)")
    return value


def main():
    parser = argparse.ArgumentParser()
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--serve", action="store_true")
    mode.add_argument("--client", action="store_true")
    parser.add_argument("action", choices=ACTIONS, nargs="?")
    args = parser.parse_args()
    try:
        if args.serve:
            core.check(args.action is None, "server does not accept a startup action")
            serve()
        else:
            core.check(args.action is not None, "a fixed action is required")
            print(json.dumps(client(args.action) if args.client else Actions().run(args.action)))
    except Exception as failure:
        # CLI/hook output may contain secrets; only the controlled error text
        # and exception class are exposed to the sanitized browser reporter.
        message = str(failure) if isinstance(failure, core.ProbeFailure) else type(failure).__name__
        parser.exit(1, "Collabora lifecycle action failed: " + message + "\n")


if __name__ == "__main__":
    main()
