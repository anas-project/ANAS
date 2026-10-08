#!/usr/bin/env python3
"""Real ANAS/AD/Authentik/Immich workspace round trip on a guarded Linux host.

No SSH targets, host provisioning, hand-written Compose, fake IAM, or SQL-created
Immich accounts. The shell entry point supplies the existing daemon/netns guards.
"""
import argparse
import base64
import hashlib
import ipaddress
import json
import math
import os
import pathlib
import platform
import re
import secrets
import shutil
import signal
import socket
import struct
import subprocess
import sys
import time
import urllib.parse
import uuid
import zlib

FIXED_MODULES = {"postgres": ("18.4.0", 4), "immich": ("3.2.4", 1), "authentik": ("2026.5.6", 15)}
EXTENSIONS = {"cube": "1.5", "earthdistance": "1.2", "vector": "0.8.2"}
OLD_VECTOR_SOURCE = "a9094dfb85ccdde3cbb295f1086d4c71a20db1d26bf1d6c39f07a7d164033eb4"


def native_policy_denied_page(url, body, authorize, state):
    """Recognize the fixed native HTTP-200 AccessDeniedResponse, not any HTML."""
    target, expected = urllib.parse.urlsplit(url), urllib.parse.urlsplit(authorize)
    query = urllib.parse.parse_qs(target.query, keep_blank_values=True)
    original = urllib.parse.parse_qs(expected.query, keep_blank_values=True)
    return (isinstance(body, str) and (target.scheme, target.netloc, target.path)
            == (expected.scheme, expected.netloc, expected.path)
            and target.path == "/application/o/authorize/" and not target.fragment
            and query.get("state") == [state] and "code" not in query
            and original.get("client_id") and query.get("client_id") == original["client_id"]
            and re.search(r"<title>\s*Permission denied\s*-", body) is not None
            and "Request has been denied." in body and 'id="ak-back-home"' in body)


def directory_denied_callback(callback, app, state):
    """Only a matching native access_denied callback proves policy refusal."""
    target, expected = urllib.parse.urlsplit(callback), urllib.parse.urlsplit(app)
    query = urllib.parse.parse_qs(target.query, keep_blank_values=True)
    return (target.scheme == expected.scheme and target.netloc == expected.netloc
            and target.path == "/auth/login" and not target.fragment
            and query.get("state") == [state] and query.get("error") == ["access_denied"]
            and "code" not in query)


def failure_origin(error):
    """Finite code locations only: no source lines, locals, URLs or credentials."""
    frames, tb = [], error.__traceback__
    while tb is not None and len(frames) < 16:
        frames.append({"file": pathlib.Path(tb.tb_frame.f_code.co_filename).name,
                       "function": tb.tb_frame.f_code.co_name, "line": tb.tb_lineno})
        tb = tb.tb_next
    return frames


def replace_once(source, needle, replacement, name):
    if source.count(needle) != 1:
        raise Blocked("fixed workspace fixture anchor changed or repeated: " + name)
    return source.replace(needle, replacement)


def postgres_fixture_sources(modules, *, fail_after_maintenance=False, pause_after_maintenance=None):
    """Exact test-only copies; this is not a published or historical release."""
    modules = pathlib.Path(modules)
    if fail_after_maintenance and pause_after_maintenance is not None:
        raise Blocked("failure and crash-pause fixtures must be independent")
    if fail_after_maintenance or pause_after_maintenance is not None:
        path = "postgres/hook/main.go"
        source = (modules / path).read_text()
        needle = '\t\t\tif err := maintainExtensions(env); err != nil {\n\t\t\t\treturn hookResponse{}, err\n\t\t\t}\n'
        if fail_after_maintenance:
            replacement = needle + '\t\t\tif env["ANAS_POSTGRES_EXTENSION_MAINTENANCE"] == "true" {\n\t\t\t\treturn hookResponse{}, fmt.Errorf("ANAS workspace test-only failure after completed PostgreSQL maintenance")\n\t\t\t}\n'
        else:
            directory = pathlib.Path(pause_after_maintenance)
            if not directory.is_absolute() or directory.resolve() != directory or not directory.is_dir():
                raise Blocked("crash markers require the existing private absolute reports directory")
            ready = json.dumps(str(directory / "extension-crash-hook-ready.json"))
            release = json.dumps(str(directory / "extension-crash-hook-release"))
            replacement = needle + f'''\t\t\tif env["ANAS_POSTGRES_EXTENSION_MAINTENANCE"] == "true" {{
                // Private test-only fault point, after real native maintenance.
                // The same sealed candidate continues only after its release.
                release := {release}
                if _, err := os.Stat(release); os.IsNotExist(err) {{
                    ready := {ready}
                    if _, err := os.Stat(ready); !os.IsNotExist(err) {{
                        return hookResponse{{}}, fmt.Errorf("ANAS crash ready marker is not fresh")
                    }}
                    marker, err := json.Marshal(map[string]any{{"pid": os.Getpid(), "parent_pid": os.Getppid(), "workdir": req.Workdir, "phase": "after_start", "maintenance_complete": true}})
                    if err != nil {{ return hookResponse{{}}, err }}
                    if err := os.WriteFile(ready + ".tmp", marker, 0600); err != nil {{ return hookResponse{{}}, err }}
                    if err := os.Rename(ready + ".tmp", ready); err != nil {{ return hookResponse{{}}, err }}
                    deadline := time.Now().Add(15 * time.Minute)
                    for {{
                        if _, err := os.Stat(release); err == nil {{ break }} else if !os.IsNotExist(err) {{ return hookResponse{{}}, err }}
                        if time.Now().After(deadline) {{ return hookResponse{{}}, fmt.Errorf("ANAS crash fixture release timeout") }}
                        time.Sleep(100 * time.Millisecond)
                    }}
                }} else if err != nil {{ return hookResponse{{}}, err }}
            }}
'''
        return {path: replace_once(source, needle, replacement, path)}
    # Reuse the audited upstream source checksum, not the old fixture's trust
    # entrypoint. Both server/provision use the current managed SCRAM image.
    old_fixture = (modules / "postgres/tests/pgvector-0.8.1.fixture").read_text()
    old_add = f"ADD --checksum=sha256:{OLD_VECTOR_SOURCE} https://github.com/pgvector/pgvector/archive/refs/tags/v0.8.1.tar.gz /tmp/vector.tar.gz"
    if old_fixture.count(old_add) != 1:
        raise Blocked("audited real pgvector 0.8.1 fixture source checksum changed")
    replacements = {
        "postgres/postgres/Dockerfile": [("ADD --checksum=sha256:69f4019389af05dc1c9548deb8628e62878e6e207c03907f2b8af2016472cdaa https://github.com/pgvector/pgvector/archive/refs/tags/v0.8.2.tar.gz /tmp/vector.tar.gz", old_add)],
        "postgres/module.yml": [("\nrevision: 4\n", "\nrevision: 3\n")],
        "postgres/localization.yml": [("\nmodule_revision: 4\n", "\nmodule_revision: 3\n")],
        "postgres/postgres/extensions.sh": [
            ("        vector) echo 0.8.2 ;;", "        vector) echo 0.8.1 ;;"),
            ("                vector:0.8.2|cube:1.5|earthdistance:1.2) ;;", "                vector:0.8.1|cube:1.5|earthdistance:1.2) ;;"),
            ("                vector:0.8.1) [ \"$mode\" = upgrade ] || { echo 'anas: vector 0.8.1 requires controlled provider maintenance' >&2; return 1; } ;;\n", ""),
            ("SELECT 'ALTER EXTENSION vector UPDATE TO ''0.8.2'''", "SELECT 'ALTER EXTENSION vector UPDATE TO ''0.8.1'''"),
        ],
    }
    prepared = {}
    for path, changes in replacements.items():
        source = (modules / path).read_text()
        for needle, replacement in changes:
            source = replace_once(source, needle, replacement, path)
        prepared[path] = source
    path = "postgres/docker-compose.yml"
    source = (modules / path).read_text()
    if source.count("/anas-postgres:18.4.0-r4") != 2:
        raise Blocked("fixed server/provision image anchors changed")
    prepared[path] = source.replace("/anas-postgres:18.4.0-r4", "/anas-postgres:18.4.0-r3")
    return prepared


class Blocked(RuntimeError):
    pass


def command(argv, *, data=None, timeout=1800, cwd=None):
    try:
        result = subprocess.run([str(x) for x in argv], input=data, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, timeout=timeout, cwd=cwd)
    except subprocess.TimeoutExpired:
        # TimeoutExpired includes the argument vector, which may hold secrets.
        raise RuntimeError(f"{pathlib.Path(argv[0]).name} timed out") from None
    if result.returncode:
        # The argument vector and upstream response can contain credentials.
        raise RuntimeError(f"{pathlib.Path(argv[0]).name} exited {result.returncode}")
    return result.stdout


def read_yaml(path):
    import yaml  # Test-host dependency, checked before any workspace mutation.
    return yaml.safe_load(pathlib.Path(path).read_text())


def write_json(path, value):
    with pathlib.Path(path).open("w") as stream:
        os.chmod(path, 0o600)
        json.dump(value, stream, indent=2, sort_keys=True)


def write_private_bytes(path, value):
    # CLI envelopes and diagnostics may contain secrets. They are evidence for
    # this private report directory, never terminal output.
    with pathlib.Path(path).open("wb") as stream:
        os.chmod(path, 0o600)
        stream.write(value)


def digest(path):
    checksum = hashlib.sha256()
    with pathlib.Path(path).open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            checksum.update(block)
    return checksum.hexdigest()


def linux_process(pid, *, proc=pathlib.Path("/proc")):
    """Read identity including start ticks; a PID alone never authorizes a kill."""
    if type(pid) is not int or pid <= 1:
        raise Blocked("invalid test process identity")
    directory = pathlib.Path(proc) / str(pid)
    try:
        stat = (directory / "stat").read_text()
    except FileNotFoundError:
        return None
    fields = stat[stat.rindex(")") + 2:].split()
    return {"pid": pid, "state": fields[0], "parent_pid": int(fields[1]), "group": int(fields[2]),
            "session": int(fields[3]), "start_ticks": int(fields[19])}


def process_identity(record):
    return tuple(record[key] for key in ("pid", "group", "session", "start_ticks"))


def capture_cli_process_groups(launch, *, proc=pathlib.Path("/proc")):
    """Capture only descendants in the fresh Popen session, before its kill."""
    current = linux_process(launch["pid"], proc=proc)
    if current is None or current["state"] == "Z":
        return {}
    if process_identity(current) != process_identity(launch) or launch["group"] != launch["pid"] or launch["session"] != launch["pid"]:
        raise Blocked("CLI process/session identity changed; refusing cleanup")
    records = {}
    for path in pathlib.Path(proc).iterdir():
        if path.name.isdecimal() and int(path.name) > 1:
            row = linux_process(int(path.name), proc=proc)
            if row and row["session"] == launch["session"] and row["state"] != "Z":
                records[row["pid"]] = row
    groups = {}
    for row in records.values():
        parent, seen = row["pid"], set()
        while parent != launch["pid"]:
            if parent in seen or parent not in records:
                raise Blocked("unproven process in test CLI session; refusing cleanup")
            seen.add(parent)
            parent = records[parent]["parent_pid"]
        if row["group"] <= 1 or row["group"] == os.getpgrp():
            raise Blocked("test process group escaped its new session")
        groups.setdefault(row["group"], {})[row["pid"]] = row
    return groups


def validate_crash_hook(launch, ready, frozen, hook_binary, anas, *, proc=pathlib.Path("/proc")):
    """Bind the ready marker to this CLI and the exact frozen PG Hook."""
    if ready.get("maintenance_complete") is not True or ready.get("phase") != "after_start":
        raise Blocked("ready marker does not prove completed maintenance")
    hook = linux_process(ready.get("pid"), proc=proc)
    cli = linux_process(launch["pid"], proc=proc)
    if not hook or not cli or process_identity(cli) != process_identity(launch):
        raise Blocked("CLI/Hook process identity is no longer live")
    if ready.get("parent_pid") != launch["pid"] or hook["parent_pid"] != launch["pid"] or hook["session"] != launch["session"] or hook["group"] != hook["pid"]:
        raise Blocked("ready Hook does not belong to this CLI's isolated process group")
    if cli["state"] == "Z" or hook["state"] == "Z" or pathlib.Path(ready.get("workdir", "")) != frozen:
        raise Blocked("ready Hook is not executing the frozen PostgreSQL workdir")
    for row, executable, cwd in ((cli, pathlib.Path(anas).resolve(), None), (hook, hook_binary, frozen)):
        directory = pathlib.Path(proc) / str(row["pid"])
        if pathlib.Path(os.readlink(directory / "exe")) != executable or (cwd is not None and pathlib.Path(os.readlink(directory / "cwd")) != cwd):
            raise Blocked("test process executable/workdir identity changed")
        if digest(directory / "exe") != digest(executable):
            raise Blocked("test process is not the expected compiled binary")
    return hook


def kill_captured_process_groups(groups, cli_pid, *, proc=pathlib.Path("/proc")):
    """SIGKILL the CLI first, then its separately grouped, captured children."""
    for group in [cli_pid, *sorted(set(groups) - {cli_pid})]:
        expected = groups.get(group, {})
        if not expected:
            continue
        live = {}
        for path in pathlib.Path(proc).iterdir():
            if path.name.isdecimal() and int(path.name) > 1:
                row = linux_process(int(path.name), proc=proc)
                if row and row["group"] == group and row["state"] != "Z":
                    live[row["pid"]] = row
        for pid, row in live.items():
            if pid not in expected or process_identity(row) != process_identity(expected[pid]):
                raise Blocked("process group changed since ownership proof; refusing SIGKILL")
        if live:
            try:
                os.killpg(group, signal.SIGKILL)
            except ProcessLookupError:
                pass  # The captured group exited between the read and signal.


def fresh_workspace(path):
    path = pathlib.Path(path)
    if not path.is_absolute() or path.parent not in map(pathlib.Path, ("/tmp", "/srv", "/data")):
        raise Blocked("workspace must be a direct child of /tmp, /srv, or /data")
    if not re.fullmatch(r"anas-immich-workspace-[a-f0-9]{8,32}", path.name):
        raise Blocked("workspace must carry a fresh anas-immich-workspace-<hex> identity")
    if path.resolve() != path or not path.parent.is_dir():
        raise Blocked("workspace ancestors must exist and contain no symlinks")
    for candidate in (path, pathlib.Path(str(path) + ".reports"), pathlib.Path(str(path) + ".backups")):
        if candidate.exists() or candidate.is_symlink():
            raise Blocked("workspace, reports and backups must all be absent")
    return path


def checked_media_path(workspace, container_path):
    path = pathlib.PurePosixPath(container_path)
    if not path.is_absolute() or ".." in path.parts or path.parts[:2] != ("/", "data"):
        raise RuntimeError("Immich media path is outside its managed /data tree")
    root = pathlib.Path(workspace) / "data" / "immich" / "media"
    target = root.joinpath(*path.parts[2:])
    if target.resolve() == root.resolve() or not target.resolve().is_relative_to(root.resolve()):
        raise RuntimeError("Immich original escapes the managed media tree")
    return target


def env_file(path):
    # Runner-generated .env values are read as data, never sourced or evaluated.
    import shlex
    out = {}
    for line in pathlib.Path(path).read_text().splitlines():
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        if value.startswith(("'", '"')):
            value = "".join(shlex.split(value))
        out[key] = value
    return out


def preflight(args):
    if not __debug__:
        raise Blocked("Python assertions must be enabled for acceptance checks")
    if platform.system() != "Linux":
        raise Blocked("Linux with real Btrfs is required; Docker Desktop is not workspace acceptance")
    workspace = fresh_workspace(args.workspace)
    if os.geteuid() != 0:
        raise Blocked("run in the explicitly selected test netns as root for Btrfs coverage and cleanup")
    namespace = os.environ.get("ANAS_UPGRADE_NETNS_PATH", "")
    if not re.fullmatch(r"/(?:var/)?run/netns/anas-[a-zA-Z0-9_-]+", namespace):
        raise Blocked("an explicit ANAS test netns is required")
    selected_ns, current_ns = os.stat(namespace), os.stat("/proc/self/ns/net")
    if (selected_ns.st_dev, selected_ns.st_ino) != (current_ns.st_dev, current_ns.st_ino):
        raise Blocked("runner is outside the explicitly selected network namespace")
    host = os.environ.get("DOCKER_HOST", "")
    if not host.startswith("unix:///") or host in ("unix:///run/docker.sock", "unix:///var/run/docker.sock"):
        raise Blocked("an explicitly selected isolated ANAS Docker socket is required")
    if not re.search(r"anas.*(?:test|e2e|anchor)", host):
        raise Blocked("Docker socket is not test scoped")
    root = command(["docker", "info", "--format", "{{.DockerRootDir}}"], timeout=30).decode().strip()
    if not re.search(r"anas.*(?:test|e2e|anchor)", root):
        raise Blocked("Docker data root is not test scoped")
    if command(["docker", "ps", "--all", "--quiet"], timeout=30).strip():
        raise Blocked("this source-building suite requires an empty isolated daemon; existing deployments are never removed")
    for tool in ("docker", "btrfs", "findmnt", "rsync", "curl", "go"):
        if not shutil.which(tool):
            raise Blocked(f"required test-host tool is absent: {tool}")
    try:
        import yaml
    except ImportError:
        raise Blocked("python3-pyyaml is required to inspect private backup/manifest metadata") from None
    if command(["findmnt", "-n", "-o", "FSTYPE", "--target", workspace.parent]).decode().strip() != "btrfs":
        raise Blocked("workspace parent is not real Btrfs; no fallback filesystem is accepted")
    for executable in (args.anas, str(pathlib.Path(args.anas).parent / "anas-helper")):
        p = pathlib.Path(executable)
        if not p.is_file() or p.is_symlink() or not os.access(p, os.X_OK):
            raise Blocked("anas and its same-source anas-helper must be executable regular files")
    modules = pathlib.Path(args.modules).resolve()
    if not all((modules.parent / file).is_file() for file in ("go.mod", "go.sum")):
        raise Blocked("same-source repository Go module metadata is required for private Module fixture builds")
    for name, version in FIXED_MODULES.items():
        meta = read_yaml(modules / name / "module.yml")
        if (str(meta["version"]), meta["revision"]) != version:
            raise Blocked(f"{name} release changed; review the fixed combination before running this suite")
    addresses = [ipaddress.ip_address(x) for x in (args.host_ip, args.host_lan_ip, args.host_lan_bridge_ip)]
    if len(set(addresses)) != 3 or any(x.version != 4 or not x.is_private for x in addresses):
        raise Blocked("three distinct private IPv4 addresses must be allocated to this test namespace")
    if not 1 <= args.port <= 65535:
        raise Blocked("port must be in the TCP port range")
    with socket.socket() as sock:
        sock.bind((args.host_ip, args.port))
    # Resolve official build inputs before creating a workspace. Registry HTTP
    # responses need not be 200 (anonymous /v2 commonly returns 401).
    for host in ("ghcr.io", "github.com"):
        socket.getaddrinfo(host, 443)
        command(["curl", "--head", "--silent", "--show-error", "--max-time", "20", "--output", "/dev/null", "https://" + host])
    return workspace, modules


class Suite:
    iam = "authentik"

    def __init__(self, args, workspace, modules):
        self.args, self.workspace, self.modules = args, workspace, modules
        self.reports = pathlib.Path(str(workspace) + ".reports")
        self.backups = pathlib.Path(str(workspace) + ".backups")
        self.prefix = "anas_iw_" + workspace.name.rsplit("-", 1)[-1][:12] + "_"
        self.domain = "iw" + secrets.token_hex(4) + ".immich.test"
        self.report = {"environment": "real ANAS workspace, Samba AD and Authentik on caller-selected isolated Linux/Btrfs host",
                       "status": "running", "checks": {}, "fixed_modules": FIXED_MODULES,
                       "workspace": str(workspace), "docker_host": os.environ["DOCKER_HOST"]}
        self.phase, self.initialized = "preflight", False
        self.envs = {}
        self.iam = "authentik"
        self.vector_version = "0.8.1"

    def module_fixture(self, name, *, fail_after_maintenance=False, pause_after_maintenance=None):
        prepared = postgres_fixture_sources(self.modules, fail_after_maintenance=fail_after_maintenance, pause_after_maintenance=pause_after_maintenance)
        destination = self.reports / "module-fixtures" / name
        if destination.exists():
            raise RuntimeError("test-only module fixture must be fresh")
        destination.parent.mkdir(mode=0o700, exist_ok=True)
        # These source-build fixtures sit outside the checkout. Preserve the
        # same existing Go module/dependencies for go build ./hook (including
        # Traefik's existing x/crypto); do not rely on GOPATH or add a module ABI.
        for file in ("go.mod", "go.sum"):
            target = destination.parent / file
            original = self.modules.parent / file
            if target.exists() and target.read_bytes() != original.read_bytes():
                raise RuntimeError("private fixture Go module metadata changed")
            if not target.exists():
                shutil.copyfile(original, target)
        # Core resolves the existing Contract catalog beside the Module root.
        # A private root must carry that same catalog, not just Go metadata.
        contracts = self.modules.parent / "contracts"
        copied_contracts = destination.parent / "contracts"
        if not copied_contracts.exists():
            shutil.copytree(contracts, copied_contracts)
        else:
            original_files = {path.relative_to(contracts): path.read_bytes() for path in contracts.rglob("*") if path.is_file()}
            copied_files = {path.relative_to(copied_contracts): path.read_bytes() for path in copied_contracts.rglob("*") if path.is_file()}
            if original_files != copied_files:
                raise RuntimeError("private fixture Contract catalog changed")
        shutil.copytree(self.modules, destination, ignore=shutil.ignore_patterns(".git", "node_modules", "__pycache__"))
        # ensureHookBinary prefers prebuilt binaries over source. Remove only
        # the private PG copy so no cached release binary hides this fault point.
        prebuilt = destination / "postgres/hook/bin"
        if prebuilt.is_symlink():
            prebuilt.unlink()
        elif prebuilt.exists():
            shutil.rmtree(prebuilt)
        for path, source in prepared.items():
            (destination / path).write_text(source)
        description = "PG18.4/SCRAM/current Provider with real audited pgvector0.8.1 binary/control/SQL.\n"
        if fail_after_maintenance:
            description = "PG Hook fails only after successful native controlled maintenance.\n"
        elif pause_after_maintenance is not None:
            description = "PG Hook pauses only after successful native controlled maintenance for real SIGKILL/exact frozen candidate retry.\n"
        (destination / "WORKSPACE-TEST-ONLY.txt").write_text("Private acceptance fixture, never a published/historical ANAS release.\n" + description)
        return destination

    def docker(self, *args, data=None):
        argv = list(args)
        shell = next((i for i in range(len(argv) - 1) if argv[i:i + 2] == ["ak", "shell"]), None)
        if shell is not None:
            argv[shell + 2:shell + 2] = ["--verbosity", "0"]
        result = command(["docker", *argv], data=data)
        if shell is not None:
            # The pinned Authentik command prints two unconditional banner
            # lines even at verbosity 0. Preserve every other stdout byte.
            lines = result.splitlines(keepends=True)
            if len(lines) < 2 or not lines[0].startswith(b"### authentik shell (2026.5.6)") or not lines[1].startswith(b"### Node "):
                raise RuntimeError("fixed Authentik shell banner changed")
            return b"".join(lines[2:])
        return result

    def anas_result(self, name, *args):
        self.phase = name
        try:
            result = subprocess.run([self.args.anas, *map(str, args), "--json"], stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=1800)
        except subprocess.TimeoutExpired as error:
            write_private_bytes(self.reports / (name + ".json"), error.stdout or error.output or b"")
            write_private_bytes(self.reports / (name + ".stderr"), error.stderr or b"")
            raise RuntimeError(f"{pathlib.Path(self.args.anas).name} timed out") from None
        # Preserve both streams before evaluating status or parsing the reply,
        # so an unexpected failure remains diagnosable after scoped cleanup.
        write_private_bytes(self.reports / (name + ".json"), result.stdout)
        write_private_bytes(self.reports / (name + ".stderr"), result.stderr)
        return result

    def anas(self, name, *args):
        result = self.anas_result(name, *args)
        if result.returncode:
            raise RuntimeError(f"{pathlib.Path(self.args.anas).name} exited {result.returncode}")
        return json.loads(result.stdout)

    def anas_rejected(self, name, *args, codes):
        result = self.anas_result(name, *args)
        assert result.returncode != 0, "a guarded ANAS operation unexpectedly succeeded"
        reply = json.loads(result.stdout)
        assert reply["error"]["code"] in codes, "ANAS rejection was unrelated to the expected maintenance guard"
        return reply

    def transition_with_stop_evidence(self, name, callback, before, *, expect_pg_start=True):
        path = self.reports / (name + "-docker-events.jsonl")
        with path.open("wb") as output:
            watcher = subprocess.Popen(["docker", "events", "--filter", "type=container", "--format", "{{json .}}"], stdout=output, stderr=subprocess.PIPE)
            try:
                time.sleep(0.5)
                assert watcher.poll() is None, "Docker event observer failed before ANAS transition"
                response = callback()
            finally:
                watcher.terminate()
                watcher.communicate(timeout=15)
        stopped = set()
        old_ids = {row["id"] for row in before.values()}
        pg_started = False
        evidence = []
        for line in path.read_text().splitlines():
            event = json.loads(line)
            actor = event.get("Actor", {})
            attributes = actor.get("Attributes", {})
            if not attributes.get("com.docker.compose.project", "").startswith(self.prefix):
                continue
            identifier, action = actor["ID"], event.get("Action", event.get("status"))
            if not expect_pg_start:
                assert action != "start", "restore unexpectedly resumed a workspace container before explicit verified start"
            if identifier in old_ids and action in ("die", "stop", "destroy"):
                stopped.add(identifier)
            if action == "start" and attributes.get("name") == self.prefix + "postgres" and identifier not in old_ids:
                assert expect_pg_start, "restore unexpectedly started PostgreSQL before explicit verified start"
                assert old_ids <= stopped, "target PostgreSQL started before the full previous workspace stopped writing"
                pg_started = True
            evidence.append({"id": identifier, "action": action, "time_nano": event.get("timeNano"), "name": attributes.get("name")})
        if expect_pg_start:
            assert pg_started, "no real target PostgreSQL start was observed"
        else:
            assert old_ids <= stopped, "restore did not stop every previously running workspace container"
            assert not self.docker("ps", "--quiet", "--filter", "name=^/" + self.prefix).strip(), "restore left workspace writers running"
        self.report[name + "_container_events"] = evidence
        return response

    def recovery_point(self, before_id, target_id, expected_images):
        candidates = []
        for path in (self.workspace / "snapshots").glob("*/snapshot.yml"):
            meta = read_yaml(path)
            if meta.get("from_deployment") == before_id and meta.get("to_deployment") == target_id and meta.get("reason") == "module_upgrade_breaking":
                candidates.append((path.parent, meta))
        assert len(candidates) == 1, "controlled PostgreSQL maintenance has no unique ANAS recovery point"
        root, meta = candidates[0]
        assert meta["complete"] and any(x["tree"] == "data" and x["captured"] for x in meta["coverage"])
        manifest = read_yaml(root / "deployment/deployment.yml")
        assert manifest["modules"]["postgres"]["revision"] == 3, "recovery point did not capture the test-only 0.8.1 Provider"
        images = read_yaml(root / "meta/compose-images.yml")
        assert "sha256:" + digest(root / "meta/compose-images.tar") == images["archive_sha256"]
        inventory = {(x["module"], x["service"]): x["id"] for x in images["services"]}
        assert all(inventory[key] == value["image"] for key, value in expected_images.items())
        self.anas("pin-" + meta["id"], "snapshot", "pin", meta["id"], "-w", self.workspace)
        self.anas("verify-" + meta["id"], "snapshot", "verify", meta["id"], "-w", self.workspace)
        return meta["id"], inventory

    def extension_failure_restore(self, state, old_point, old_images, failed_modules, new_backup, new_images):
        before = self.runtime()
        self.transition_with_stop_evidence("restore-upgrade-old-point", lambda: self.anas("restore-upgrade-old-point", "snapshot", "restore", old_point, "-w", self.workspace, "-y"), before, expect_pg_start=False)
        self.vector_version = "0.8.1"
        self.anas("restored-old-point-start", "start", "-w", self.workspace)
        self.load_envs()
        self.verify_state(state)
        assert all(old_images[key] == value["image"] for key, value in self.runtime().items())
        before_id, before = self.active(), self.runtime()
        rejected = self.transition_with_stop_evidence("extension-maintenance-failure", lambda: self.anas_rejected("extension-maintenance-failure-apply", "apply", "-w", self.workspace, "--module-root", failed_modules, "--update-lock", "-y", codes={"start_failed"}), before)
        failure_text = json.dumps(rejected) + (self.reports / "extension-maintenance-failure-apply.stderr").read_text()
        assert "ANAS workspace test-only failure after completed PostgreSQL maintenance" in failure_text, "failure did not come from the post-maintenance test Hook"
        assert self.active() == before_id
        active = read_yaml(self.workspace / ".anas/state/active.yml")
        source_state = read_yaml(self.workspace / ".anas/state/deployments" / (before_id + ".yml"))
        pending = source_state["failure_detail"]["postgres_maintenance_target"]
        assert pending and pending != before_id and active["runtime_status"] == "stopped"
        assert not self.docker("ps", "--quiet", "--filter", "name=^/" + self.prefix).strip(), "failed maintenance resumed a workspace consumer"
        failed_point, failed_images = self.recovery_point(before_id, pending, before)
        self.verify_failed_maintenance_recovery(state, before_id, pending, failed_point, failed_images, rejected, new_backup, new_images)

    def older_postgres_artifact(self, active_id, pending_id):
        """Use a real non-active old fixture, not rollback's already-active no-op."""
        for path in sorted((self.workspace / ".anas/deployments").glob("*/deployment.yml"), reverse=True):
            candidate = path.parent.name
            if candidate in (active_id, pending_id):
                continue
            module = read_yaml(path).get("modules", {}).get("postgres", {})
            state = self.workspace / ".anas/state/deployments" / (candidate + ".yml")
            if module.get("version") == "18.4.0" and module.get("revision") == 3 and state.is_file() and read_yaml(state).get("status") == "previous":
                return candidate
        raise RuntimeError("no qualified non-active PostgreSQL 0.8.1 fixture for rollback barrier")

    def verify_failed_maintenance_recovery(self, state, before_id, pending, failed_point, failed_images, rejected, new_backup, new_images):
        """Check the durable barrier and restore matching old/new recovery sets."""
        assert self.active() == before_id
        assert not self.docker("ps", "--quiet", "--filter", "name=^/" + self.prefix).strip(), "failed maintenance resumed a workspace consumer"
        rollback_target = self.older_postgres_artifact(before_id, pending)
        self.report["maintenance_blocked_rollback_target"] = rollback_target
        blocked = {}
        for name, args in {
            "start": ("start", "-w", self.workspace),
            "restart": ("restart", "-w", self.workspace),
            "different-apply": ("apply", "-w", self.workspace, "--module-root", self.modules, "--update-lock", "-y"),
            "old-rollback": ("rollback", rollback_target, "-w", self.workspace),
        }.items():
            reply = self.anas_rejected("maintenance-blocks-" + name, *args, codes={"postgres_recovery_required"})
            blocked[name] = reply["error"]["code"]
        self.report["extension_maintenance_failure"] = {"code": rejected["error"]["code"], "pending_candidate": pending,
                                                          "recovery_point": failed_point, "blocked_operations": blocked,
                                                          "failed_hook_after_native_maintenance": True}
        # No old artifact ever opens the uncertain upgraded live data. Restore
        # old coupled data/images first, then invoke the ordinary start barrier.
        self.anas("restore-failed-maintenance-point", "snapshot", "restore", failed_point, "-w", self.workspace, "-y")
        assert not self.docker("ps", "--quiet", "--filter", "name=^/" + self.prefix).strip()
        self.anas("recovered-maintenance-old-start", "start", "-w", self.workspace)
        self.load_envs()
        self.verify_state(state)
        assert all(failed_images[key] == value["image"] for key, value in self.runtime().items())
        self.mark("real ANAS post-maintenance failure blocks ordinary start/apply/rollback and matching old snapshot restores 18.4/0.8.1 with media/HNSW/shared consumer")
        self.transition_with_stop_evidence("restore-upgraded-new-backup", lambda: self.anas("restore-upgraded-new-backup", "backup", "restore", "-w", self.workspace, "--from", self.backups, "--backup-id", new_backup, "-y"), self.runtime(), expect_pg_start=False)
        self.vector_version = "0.8.2"
        self.anas("recovered-upgraded-new-start", "start", "-w", self.workspace)
        self.load_envs()
        self.verify_state(state)
        assert all(new_images[key] == value["image"] for key, value in self.runtime().items())
        self.report["extension_maintenance_failure"]["old_restored_vector"] = "0.8.1"
        self.report["extension_maintenance_failure"]["new_restored_vector"] = "0.8.2"
        self.mark("ANAS restores the matching upgraded full workspace backup to 18.4/0.8.2 and its exact images after old-point recovery")

    def extension_crash_retry(self, state, old_point, old_images, new_backup, new_images):
        self.transition_with_stop_evidence("crash-restore-old-point", lambda: self.anas("crash-restore-old-point", "snapshot", "restore", old_point, "-w", self.workspace, "-y"), self.runtime(allow_empty=True), expect_pg_start=False)
        self.verify_extension_crash_retry(state, old_images, new_backup, new_images)

    def verify_extension_crash_retry(self, state, old_images, new_backup, new_images):
        """Qualify crash/retry only after a separately verified matching old restore."""
        self.vector_version = "0.8.1"
        self.anas("crash-old-point-start", "start", "-w", self.workspace)
        self.load_envs()
        self.verify_state(state)
        assert all(old_images[key] == value["image"] for key, value in self.runtime().items())
        ready = self.reports / "extension-crash-hook-ready.json"
        release = self.reports / "extension-crash-hook-release"
        assert not any(path.exists() or path.is_symlink() for path in (ready, release, pathlib.Path(str(ready) + ".tmp"))), "crash process markers must be fresh"
        crash_modules = self.module_fixture("pgvector-0.8.2-crash-test-only", pause_after_maintenance=self.reports)
        before_id, before = self.active(), self.runtime()
        process, launch, groups = None, None, {}
        observed = {}
        stdout = self.reports / "extension-crash-apply.stdout"
        stderr = self.reports / "extension-crash-apply.stderr"

        def pause_and_kill():
            nonlocal process, launch, groups
            self.phase = "extension-crash-apply"
            argv = [self.args.anas, "apply", "-w", str(self.workspace), "--module-root", str(crash_modules), "--update-lock", "-y", "--json"]
            with stdout.open("xb") as output, stderr.open("xb") as errors:
                os.chmod(stdout, 0o600)
                os.chmod(stderr, 0o600)
                process = subprocess.Popen(argv, stdout=output, stderr=errors, start_new_session=True)
                launch = linux_process(process.pid)
                assert launch and launch["group"] == process.pid and launch["session"] == process.pid, "CLI did not establish its fresh session"
                deadline = time.monotonic() + 1800
                while not ready.exists():
                    assert process.poll() is None, "real apply exited before its post-maintenance Hook pause"
                    if time.monotonic() > deadline:
                        raise RuntimeError("post-maintenance crash ready timeout")
                    time.sleep(0.1)
                assert not ready.is_symlink() and ready.stat().st_mode & 0o777 == 0o600
                marker = json.loads(ready.read_text())
                active = read_yaml(self.workspace / ".anas/state/active.yml")
                source = read_yaml(self.workspace / ".anas/state/deployments" / (before_id + ".yml"))
                pending = source["failure_detail"]["postgres_maintenance_target"]
                assert re.fullmatch(r"[A-Za-z0-9._-]+", pending) and pending != before_id
                assert active["active_deployment"] == before_id and active["runtime_status"] == "stopped"
                frozen = self.workspace / ".anas/deployments" / pending / "modules/postgres"
                assert digest(frozen / ".hook.bin") == digest(self.workspace / ".anas/hook-bin/postgres"), "ready Hook differs from the binary compiled and sealed for this candidate"
                # Activation prefers the binary sealed in this exact artifact,
                # not the render-time .anas/hook-bin cache.
                hook = validate_crash_hook(launch, marker, frozen, frozen / ".hook.bin", self.args.anas)
                groups = capture_cli_process_groups(launch)
                assert hook["pid"] in groups.get(hook["group"], {}), "frozen Hook group escaped its CLI session"
                running = self.runtime()
                assert any(module == "postgres" for module, _ in running)
                assert not any(module in ("immich", self.iam) for module, _ in running), "a shared consumer started while the Provider barrier was paused"
                assert self.sql("immich", "SELECT extversion FROM pg_extension WHERE extname='vector';") == "0.8.2", "pause happened before actual extension maintenance completed"
                assert self.sql(self.iam, "SELECT value FROM anas_workspace_recovery WHERE id=1;") == "before-backup"
                observed.update({"pending_candidate": pending, "cli": launch, "hook": hook, "ready": marker,
                                 "running_modules_at_pause": sorted(set(module for module, _ in running)),
                                 "actual_vector_at_pause": "0.8.2", "frozen_manifest_sha256": digest(frozen.parents[1] / "deployment.yml"),
                                 "frozen_hook_sha256": digest(frozen / ".hook.bin"), "fixture_hook_source_sha256": digest(crash_modules / "postgres/hook/main.go")})
                write_json(self.reports / "extension-crash-processes.json", observed)
                # Kill the CLI before its independent Hook group. Otherwise
                # the live CLI could observe Hook failure and compensate,
                # turning this into an ordinary returned-error test.
                kill_captured_process_groups(groups, process.pid)
                process.wait(timeout=15)
                assert process.returncode == -signal.SIGKILL, "CLI did not actually die from SIGKILL"
                self.wait(lambda: (linux_process(hook["pid"]) or {"state": "Z"})["state"] == "Z", timeout=15)
                observed["cli_exit_status"] = process.returncode
                write_json(self.reports / "extension-crash-processes.json", observed)
                return observed

        try:
            self.transition_with_stop_evidence("extension-crash", pause_and_kill, before)
        finally:
            if process is not None:
                # Ownership is rechecked at every signal. A malformed ready
                # marker never authorizes killing an unrelated PID/group.
                if not groups and launch is not None:
                    groups = capture_cli_process_groups(launch)
                if groups:
                    kill_captured_process_groups(groups, process.pid)
                if process.poll() is None:
                    process.wait(timeout=15)
        pending = observed["pending_candidate"]
        source = read_yaml(self.workspace / ".anas/state/deployments" / (before_id + ".yml"))
        assert source["failure_detail"]["postgres_maintenance_target"] == pending and self.active() == before_id
        active = read_yaml(self.workspace / ".anas/state/active.yml")
        assert active["runtime_status"] == "stopped"
        assert not any(module in ("immich", self.iam) for module, _ in self.runtime()), "crash unexpectedly resumed consumers"
        original_point, original_images = self.recovery_point(before_id, pending, before)
        point_root = self.workspace / "snapshots" / original_point
        point_hashes = {name: digest(point_root / name) for name in ("snapshot.yml", "meta/compose-images.yml", "meta/compose-images.tar")}
        rollback_target = self.older_postgres_artifact(before_id, pending)
        self.report["crash_blocked_rollback_target"] = rollback_target
        blocked = {}
        for name, args in {
            "start": ("start", "-w", self.workspace),
            "restart": ("restart", "-w", self.workspace),
            "different-apply": ("apply", "-w", self.workspace, "--module-root", self.modules, "--update-lock", "-y"),
            "old-rollback": ("rollback", rollback_target, "-w", self.workspace),
        }.items():
            rejected = self.anas_rejected("crash-blocks-" + name, *args, codes={"postgres_recovery_required"})
            blocked[name] = rejected["error"]["code"]
        write_json(release, {"pending_candidate": pending, "verified_original_recovery_point": original_point})
        self.transition_with_stop_evidence("extension-crash-retry", lambda: self.anas("extension-crash-exact-retry", "apply", "--deployment", pending, "-w", self.workspace, "-y"), self.runtime())
        assert self.active() == pending
        frozen = self.workspace / ".anas/deployments" / pending / "modules/postgres"
        assert digest(frozen.parents[1] / "deployment.yml") == observed["frozen_manifest_sha256"] and digest(frozen / ".hook.bin") == observed["frozen_hook_sha256"], "retry changed its frozen candidate"
        same_point, same_images = self.recovery_point(before_id, pending, before)
        assert same_point == original_point and same_images == original_images
        assert all(digest(point_root / name) == expected for name, expected in point_hashes.items()), "retry replaced or changed the original ANAS recovery point"
        self.vector_version = "0.8.2"
        self.load_envs()
        self.verify_state(state)
        target = read_yaml(self.workspace / ".anas/state/deployments" / (pending + ".yml"))
        assert target["status"] == "active" and not target.get("failure_detail"), "successful frozen retry did not clear its active maintenance guard"
        self.anas("crash-retry-normal-start", "start", "-w", self.workspace)
        self.verify_state(state)
        self.report["extension_maintenance_crash_retry"] = {"pending_candidate": pending, "original_recovery_point": original_point,
                                                           "actual_vector_at_pause": observed["actual_vector_at_pause"], "cli_exit_status": observed["cli_exit_status"],
                                                           "blocked_operations": blocked, "exact_frozen_retry": True, "original_recovery_point_preserved": True}
        self.mark("real ANAS SIGKILL after native maintenance preserves durable guard and retries only the exact frozen candidate with its original data/image recovery point")
        self.transition_with_stop_evidence("crash-restore-new-backup", lambda: self.anas("crash-restore-new-backup", "backup", "restore", "-w", self.workspace, "--from", self.backups, "--backup-id", new_backup, "-y"), self.runtime(), expect_pg_start=False)
        self.vector_version = "0.8.2"
        self.anas("crash-restored-new-start", "start", "-w", self.workspace)
        self.load_envs()
        self.verify_state(state)
        assert all(new_images[key] == value["image"] for key, value in self.runtime().items())
        self.mark("matching new full workspace backup restores media/HNSW/shared database and exact images after crash retry")

    def mark(self, name):
        self.report["checks"][name] = True
        print("PASS: " + name, flush=True)

    def active(self):
        return read_yaml(self.workspace / ".anas/state/active.yml")["active_deployment"]

    def load_envs(self):
        artifact = self.workspace / ".anas/deployments" / self.active()
        manifest = read_yaml(artifact / "deployment.yml")
        for name in ("immich", self.iam, "postgres"):
            source = manifest["modules"][name].get("artifact_deployment") or manifest["id"]
            directory = self.workspace / ".anas/deployments" / source / "modules" / name
            self.envs[name] = env_file(directory / ".env")
            if name == "immich":
                self.issuer = json.loads((directory / "config.json").read_text())["oauth"]["issuerUrl"]
        self.app = self.envs["immich"]["IMMICH_DOMAIN_FULL"]
        self.idp = self.envs[self.iam][self.iam.upper() + "_DOMAIN_FULL"]
        assert self.issuer == self.idp or self.issuer.startswith(self.idp + "/"), "frozen Immich issuer does not belong to this workspace IAM"
        self.ca = pathlib.Path(self.envs["immich"]["ANAS_TLS_CERTS_DIR"]) / self.envs["immich"]["ANAS_TLS_INTERNAL_CA_NAME"]

    def cache_declared_images(self):
        """Backup includes inactive services too; cache their fixed binaries."""
        artifact = self.workspace / ".anas/deployments" / self.active()
        manifest = read_yaml(artifact / "deployment.yml")
        for name, module in manifest["modules"].items():
            if module["runtime"] != "compose":
                continue
            source = module.get("artifact_deployment") or manifest["id"]
            directory = self.workspace / ".anas/deployments" / source / "modules" / name
            images = command(["docker", "compose", "--project-name", self.prefix + name,
                              "--env-file", ".env", "--file", module.get("compose_file") or "docker-compose.yml",
                              "config", "--images"], cwd=directory).decode().splitlines()
            for image in set(images):
                try:
                    self.docker("image", "inspect", image)
                except RuntimeError:
                    self.docker("pull", image)

    def prepare_immich_mirrors(self):
        # New Module mirrors need not be published yet. Use the existing
        # release catalog's exact upstream digest in this isolated daemon.
        catalog = json.loads((self.modules.parent / ".github/mirrors.json").read_text())
        for entry in catalog:
            if "immich" not in entry["modules"]:
                continue
            source = entry["source"] + "@" + entry["digest"]
            target = self.args.image_registry + "/" + entry["image"] + ":" + entry["tag"]
            self.docker("pull", source)
            self.docker("tag", source, target)
            image_id = self.docker("image", "inspect", "--format", "{{.Id}}", source).decode().strip()
            assert image_id == self.docker("image", "inspect", "--format", "{{.Id}}", target).decode().strip()
            self.report.setdefault("mirror_sources", {})[entry["image"]] = {"source": source, "target": target, "image_id": image_id}

    def http(self, url, body=None, token=None, *, raw=False, method=None, cookie=None, follow=False, headers=None):
        name = "http-" + uuid.uuid4().hex
        output = self.reports / name
        argv = ["curl", "--silent", "--show-error", "--noproxy", "*", "--connect-timeout", "10", "--max-time", "90",
                "--cacert", self.ca, "--output", output, "--write-out", "%{http_code}\n%{url_effective}"]
        for base in (self.app, self.idp):
            parts = urllib.parse.urlsplit(base)
            argv += ["--resolve", f"{parts.hostname}:{parts.port or 443}:{self.args.host_ip}"]
        if follow:
            argv += ["--location"]
        if cookie:
            argv += ["--cookie", cookie, "--cookie-jar", cookie]
        if token:
            argv += ["--header", "Authorization: Bearer " + token]
        for key, value in (headers or {}).items():
            argv += ["--header", key + ": " + value]
        if method:
            argv += ["--request", method]
        data = None
        if body is not None:
            if isinstance(body, (dict, list)):
                body = json.dumps(body).encode()
                argv += ["--header", "Content-Type: application/json"]
            data = body
            argv += ["--data-binary", "@-"]
        result = command([*argv, url], data=data, timeout=100).decode().splitlines()
        payload = output.read_bytes()
        output.unlink()
        if not raw:
            try:
                payload = json.loads(payload) if payload else None
            except json.JSONDecodeError:
                payload = payload.decode(errors="replace")
        return int(result[0]), payload, result[1]

    def wait(self, callback, timeout=900):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            try:
                if callback():
                    return
            except (RuntimeError, OSError, ValueError):
                pass
            time.sleep(3)
        raise RuntimeError("readiness timeout in " + self.phase)

    def sql(self, consumer, statement, password=None):
        e, prefix = self.envs[consumer], consumer.upper()
        value = password if password is not None else e[prefix + "_DB_PASSWORD"]
        return self.docker("exec", "-i", "-e", "PGPASSWORD=" + value, self.prefix + "postgres", "psql", "-X", "-h", "127.0.0.1",
                           "-U", e[prefix + "_DB_USERNAME"], "-d", e[prefix + "_DB_NAME"], "-At", "-v", "ON_ERROR_STOP=1",
                           data=statement.encode()).decode().strip()

    def provider_sql(self, statement):
        e = self.envs["postgres"]
        # Restricted settings are checked as the Provider administrator over
        # private TCP. Application roles receive no monitoring/settings grant.
        return self.docker("exec", "-i", "-e", "PGPASSWORD=" + e["POSTGRES_PASSWORD"], self.prefix + "postgres", "psql", "-X", "-h", "127.0.0.1",
                           "-U", e["POSTGRES_USER"], "-d", "postgres", "-At", "-v", "ON_ERROR_STOP=1",
                           data=statement.encode()).decode().strip()

    def samba(self, *args):
        # Match the existing IAM-matrix LDAP/audit helper; no DSDB bypass.
        script = '''auth_file=$(mktemp); trap 'rm -f "$auth_file"' EXIT
chmod 0600 "$auth_file"
printf "username = %s\\npassword = %s\\n" "$SAMBA_DC_ADMIN_NAME" "$SAMBA_DC_ADMIN_PASSWORD" >"$auth_file"
samba-tool "$@" -H ldap://127.0.0.1 -A "$auth_file"'''
        return self.docker("exec", self.prefix + "samba_dc", "bash", "-lc", script, "immich-workspace-e2e", *args).decode()

    def directory_sync(self):
        self.docker("exec", self.prefix + "authentik", "ak", "shell", "-c",
                    "from authentik.tasks.schedules.models import Schedule; [s.send() for s in Schedule.objects.filter(actor_name='authentik.sources.ldap.tasks.ldap_sync') if getattr(s.rel_obj,'slug',None)=='samba-ad']")

    def login(self, username, password, *, cookie=None, allow_denied=False):
        verifier, state = secrets.token_urlsafe(32), secrets.token_urlsafe(24)
        challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).decode().rstrip("=")
        code, body, _ = self.http(self.app + "/api/oauth/authorize", {"redirectUri": self.app + "/auth/login", "state": state, "codeChallenge": challenge})
        self.report["native_authorize_probe"] = {"http_status": code, "body_kind": type(body).__name__}
        assert code in (200, 201), "Immich did not begin native OIDC authorization"
        own_cookie = cookie is None
        cookie = cookie or self.reports / ("cookie-" + uuid.uuid4().hex)
        authorize_url = body["url"]
        code, _, flow = self.http(authorize_url, cookie=cookie, follow=True)
        parts = urllib.parse.urlsplit(flow)
        assert "/if/flow/" in parts.path, "OIDC did not reach real Authentik"
        api = urllib.parse.urlunsplit((parts.scheme, parts.netloc, parts.path.replace("/if/flow/", "/api/v3/flows/executor/", 1), urllib.parse.urlencode({"query": parts.query}), ""))
        code, body, _ = self.http(api, cookie=cookie)
        assert body["component"] == "ak-stage-identification"
        code, body, _ = self.http(api, {"uid_field": username}, cookie=cookie, follow=True)
        assert body["component"] == "ak-stage-password"
        code, body, _ = self.http(api, {"password": password}, cookie=cookie, follow=True)
        if allow_denied and code in (200, 400) and body.get("component") == "ak-stage-password":
            if own_cookie:
                cookie.unlink(missing_ok=True)
            return 401, {"directory_outcome": "credential-denied"}
        assert body["component"] == "xak-flow-redirect", "AD credential login failed"
        code, authorization_body, callback = self.http(urllib.parse.urljoin(api, body["to"]), cookie=cookie, follow=True, headers={"Accept-Language": "en"})
        if allow_denied and code == 200 and native_policy_denied_page(callback, authorization_body, authorize_url, state):
            if own_cookie:
                cookie.unlink(missing_ok=True)
            return 403, {"directory_outcome": "native-policy-denied-page"}
        if allow_denied and code in (403, 404):
            if own_cookie:
                cookie.unlink(missing_ok=True)
            return code, {"directory_outcome": "application-policy-denied"}
        # Auto-authorize is normally configured. Explicitly handle an upstream
        # consent stage as the existing Authentik authorization-code E2E does.
        if "/if/flow/" in urllib.parse.urlsplit(callback).path:
            p = urllib.parse.urlsplit(callback)
            consent_api = urllib.parse.urlunsplit((p.scheme, p.netloc, p.path.replace("/if/flow/", "/api/v3/flows/executor/", 1), urllib.parse.urlencode({"query": p.query}), ""))
            _, body, _ = self.http(consent_api, cookie=cookie)
            if allow_denied and body.get("component") == "ak-stage-access-denied":
                if own_cookie:
                    cookie.unlink(missing_ok=True)
                return 403, {"directory_outcome": "application-policy-denied"}
            if body["component"] == "ak-stage-consent":
                _, body, _ = self.http(consent_api, {"component": "ak-stage-consent", "token": body["token"]}, cookie=cookie)
            assert body["component"] == "xak-flow-redirect", "application authorization failed"
            _, _, callback = self.http(urllib.parse.urljoin(consent_api, body["to"]), cookie=cookie, follow=True)
        if allow_denied and directory_denied_callback(callback, self.app, state):
            if own_cookie:
                cookie.unlink(missing_ok=True)
            return 403, {"directory_outcome": "native-oidc-access-denied"}
        query = urllib.parse.parse_qs(urllib.parse.urlsplit(callback).query)
        if not (query.get("code") and query.get("state") == [state]):
            parts = urllib.parse.urlsplit(callback)
            expected = urllib.parse.urlsplit(self.app)
            known_errors = {"access_denied", "server_error", "invalid_request", "login_required", "interaction_required"}
            error_kind = query.get("error", ["absent"])[0]
            diagnosis = {"matching_app_origin": (parts.scheme, parts.netloc) == (expected.scheme, expected.netloc),
                         "callback_path": parts.path if parts.path in ("/auth/login", "/photos", "/") else "other",
                         "has_code": bool(query.get("code")), "state_matches": query.get("state") == [state],
                         "error_kind": error_kind if error_kind in known_errors else "absent-or-other"}
            raise AssertionError("real IdP did not return an OIDC code/state: " + json.dumps(diagnosis, sort_keys=True))
        response = self.http(self.app + "/api/oauth/callback", {"url": callback, "state": state, "codeVerifier": verifier}, cookie=cookie)[:2]
        if own_cookie:
            cookie.unlink(missing_ok=True)
        return response

    def verify_entry_restrictions(self, state):
        """Exercise native controllers with real OIDC roles, retaining bindings."""
        self.phase = "entry-restrictions"
        accounts = {}
        for role in ("admin", "user"):
            code, account = self.login(state[role]["username"], state["password"])
            assert code in (200, 201) and account["isAdmin"] == (role == "admin")
            assert str(uuid.UUID(account["userId"])) == state[role]["user_id"]
            accounts[role] = account
        ids = ",".join("'" + str(uuid.UUID(state[role]["user_id"])) + "'" for role in ("admin", "user"))
        binding_sql = f"""SELECT json_agg(json_build_object('id',id,'oauthId',"oauthId",'isAdmin',"isAdmin") ORDER BY id)::text FROM "user" WHERE id IN ({ids});"""
        before = self.sql("immich", binding_sql)
        sentinel = "local-denied-" + uuid.uuid4().hex + "@" + self.domain
        password = state["password"] + "New1!"
        local = {"email": sentinel, "password": password, "name": "Denied local fixture"}
        admin, user = accounts["admin"]["accessToken"], accounts["user"]["accessToken"]
        cases = [("/api/auth/admin-sign-up", local, None, "POST"),
                 ("/api/admin/users", local, admin, "POST"),
                 ("/api/oauth/unlink", b"", user, "POST"),
                 ("/api/admin/auth/unlink-all", b"", admin, "POST"),
                 ("/api/auth/change-password", {"password": "x", "newPassword": password}, user, "POST"),
                 ("/api/oauth/link", {"url": self.app + "/auth/login?code=forbidden", "state": "forbidden", "codeVerifier": "forbidden"}, user, "POST"),
                 ("/api/users/me", {"password": password}, user, "PUT"),
                 ("/api/admin/users/" + accounts["user"]["userId"], {"password": password}, admin, "PUT")]
        statuses = []
        for route, body, token, method in cases:
            status = self.http(self.app + route, body, token=token, method=method)[0]
            assert status in (400, 403), "native account entry not explicitly refused: " + method + " " + route
            statuses.append({"method": method, "route": route, "status": status})
        assert self.sql("immich", binding_sql) == before, "entry rejection changed identity/role"
        assert self.sql("immich", f"SELECT count(*) FROM \"user\" WHERE email='{sentinel}';") == "0"
        assert self.sql("immich", 'SELECT count(*) FROM "user" WHERE "oauthId" IS NULL OR btrim("oauthId")=\'\';') == "0"
        assert self.sql("immich", f"SELECT count(*) FROM \"user\" WHERE id IN ({ids}) AND password IS NOT NULL AND password<>'';") == "0"
        self.report["entry_restriction_http_statuses"] = statuses
        self.mark("native host local/setup/password/link/unlink/global-unlink controllers refuse mutation without unbound accounts or changed bindings")

    def create_credentials(self, account, asset_id, name):
        code, key, _ = self.http(self.app + "/api/api-keys", {"name": name, "permissions": ["all"]}, token=account["accessToken"])
        assert code in (200, 201), "native API key creation failed"
        code, share, _ = self.http(self.app + "/api/shared-links", {"type": "INDIVIDUAL", "assetIds": [asset_id]}, token=account["accessToken"])
        assert code in (200, 201), "native public share creation failed"
        return {"token": account["accessToken"], "api_key": key["secret"],
                "share_path": "/api/shared-links/me?key=" + urllib.parse.quote(share["key"])}

    def credential_status(self, credentials):
        path = self.app + "/api/users/me"
        return {
            "bearer": self.http(path, token=credentials["token"], raw=True)[0],
            "cookie": self.http(path, headers={"Cookie": "immich_access_token=" + credentials["token"] + "; immich_auth_type=oauth"}, raw=True)[0],
            "api_key": self.http(path, headers={"x-api-key": credentials["api_key"]}, raw=True)[0],
            "share": self.http(self.app + credentials["share_path"], raw=True)[0],
        }

    def receiver_signed_cutoff(self, anchor):
        # This application-owned ledger is written by the signed native event
        # receiver. It identifies the target after native DONE tasks have
        # cleared their binary messages; never infer a target from a DONE row.
        anchor = str(uuid.UUID(anchor))
        value = float(self.sql("immich", f'''SELECT EXTRACT(EPOCH FROM "revokedBefore")::double precision FROM public.anas_immich_directory_revocation WHERE "oauthId"='{anchor}';'''))
        if not math.isfinite(value) or value <= 0:
            raise ValueError("signed directory cutoff must be a positive finite epoch")
        return {"oauthId": anchor, "epoch": value}

    def wait_later_oidc_issuance(self, cutoff):
        # Native ID-token iat is an integer PG second. An event can arrive
        # within that second; do not turn its deliberately closed boundary
        # into a spurious fresh-login acceptance failure.
        self.wait(lambda: int(self.sql("immich", "SELECT floor(EXTRACT(EPOCH FROM clock_timestamp()))::bigint;")) > cutoff)

    def identity_lifecycle(self, state):
        """Use private AD identities; keep the core restore users and media intact."""
        import concurrent.futures
        import threading

        self.phase = "identity-lifecycle"
        users = {case: "iwid" + case[0] + secrets.token_hex(5) for case in ("email", "conflict", "parallel", "deleted")}
        emails = {name: name + "@" + self.domain for name in users.values()}
        anchors = {}
        evidence = {"retention": "private directory users and application identity rows remain until scoped workspace cleanup; native soft deletion is not forced erasure"}
        self.report["identity_lifecycle"] = evidence
        for name in users.values():
            self.samba("user", "add", name, state["password"], "--userou=OU=People", "--mail-address=" + emails[name])
            self.samba("group", "addmembers", "APP_immich", name)
            def anchor_ready():
                found = re.search(r"^anasIdentityAnchor: ([a-f0-9-]+)$", self.samba("user", "show", name, "--attributes=anasIdentityAnchor"), re.M)
                if found:
                    anchors[name] = str(uuid.UUID(found.group(1)))
                return name in anchors
            self.wait(anchor_ready)

        def synced():
            # The fixed source's default-mail mapping writes User.email. Read
            # actual source connections, rather than treating enqueue as sync.
            self.directory_sync()
            observed = {}
            probe = f'''import json
from authentik.core.models import UserSourceConnection
from authentik.sources.ldap.models import LDAPSource
s=LDAPSource.objects.get(slug='samba-ad')
expected={emails!r}
anchors={anchors!r}
connections={{n:UserSourceConnection.objects.get(source=s,user__username=n) for n in expected}}
assert len({{c.user_id for c in connections.values()}})==len(expected)
for n,c in connections.items():
    assert c.identifier==anchors[n] and c.user.email==expected[n] and c.user.is_active
    assert c.user.all_groups().filter(name='APP_immich').exists()
print(json.dumps({{n:c.user_id for n,c in connections.items()}}))'''
            def ready():
                observed.update(json.loads(self.docker("exec", self.prefix + "authentik", "ak", "shell", "-c", probe).decode()))
                return True
            self.wait(ready)
            return observed

        directory_ids = synced()

        def rows(name):
            # SELECT only: every identity mutation goes through LDAP or the
            # native HTTP controller, including the soft-delete tombstone.
            return json.loads(self.sql("immich", f'''SELECT COALESCE(json_agg(json_build_object('id',id,'oauthId',"oauthId",'email',email,'deletedAt',"deletedAt") ORDER BY id),'[]'::json)::text FROM "user" WHERE "oauthId"='{anchors[name]}';'''))

        def change_email(name, email):
            self.samba("user", "rename", name, "--mail-address=" + email)
            row = self.samba("user", "show", name, "--attributes=anasIdentityAnchor,mail")
            assert re.search(r"^mail: " + re.escape(email) + r"$", row, re.M), "native AD mail change was not persisted"
            assert re.search(r"^anasIdentityAnchor: " + re.escape(anchors[name]) + r"$", row, re.M), "mail edit changed the directory anchor"
            emails[name] = email
            assert synced() == directory_ids, "mail edit merged or replaced a real directory identity"

        name = users["email"]
        code, owner = self.login(name, state["password"])
        assert code in (200, 201) and not owner["isAdmin"]
        baseline = rows(name)
        assert len(baseline) == 1 and baseline[0]["id"] == owner["userId"] and baseline[0]["deletedAt"] is None
        original_email = emails[name]
        change_email(name, "changed-" + name + "@" + self.domain)
        code, renamed = self.login(name, state["password"])
        assert code in (200, 201) and renamed["userId"] == owner["userId"] and not renamed["isAdmin"]
        assert rows(name) == baseline, "same-sub mail change rebound the account"
        # v3.2.4 keeps an existing account's application email; this checks
        # identity continuity, without claiming automatic email replication.
        assert self.http(self.app + "/api/users/me", token=owner["accessToken"])[0] == 200
        evidence["email_change"] = {"anchor": anchors[name], "user_id": owner["userId"], "directory_email": emails[name], "application_email": baseline[0]["email"], "callback_status": code}
        self.mark("real AD mail change and LDAP synchronization preserve OIDC anchor and internal user.id")

        conflict = users["conflict"]
        change_email(conflict, original_email)
        code, denied = self.login(conflict, state["password"])
        assert code == 400 and "accessToken" not in denied, "different directory sub acquired an already-bound email"
        assert not rows(conflict) and rows(name) == baseline, "email conflict created an account or changed the owner binding"
        evidence["email_conflict"] = {"owner_anchor": anchors[name], "conflicting_anchor": anchors[conflict], "distinct_directory_user_ids": [directory_ids[name], directory_ids[conflict]], "callback_status": code}
        self.mark("real distinct AD identities cannot claim the same bound Immich email")

        parallel = users["parallel"]
        assert not rows(parallel), "parallel identity was already registered"
        callback_barrier = threading.Barrier(2, timeout=180)
        native_http = self.http
        def simultaneous_callback(url, *args, **kwargs):
            if url == self.app + "/api/oauth/callback":
                callback_barrier.wait()
            return native_http(url, *args, **kwargs)
        self.http = simultaneous_callback
        try:
            with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
                results = list(pool.map(lambda _: self.login(parallel, state["password"]), range(2)))
        finally:
            self.http = native_http
        successful = [account for code, account in results if code in (200, 201)]
        assert successful and all(code in (200, 201, 400, 500) for code, _ in results), "concurrent native callback did not yield a usable account"
        account_rows = rows(parallel)
        assert len(account_rows) == 1 and account_rows[0]["deletedAt"] is None
        assert all(account["userId"] == account_rows[0]["id"] and not account["isAdmin"] for account in successful)
        assert all(code in (200, 201) or "accessToken" not in account for code, account in results)
        evidence["concurrent_callbacks"] = {"anchor": anchors[parallel], "user_id": account_rows[0]["id"], "callback_statuses": [code for code, _ in results], "account_rows": len(account_rows), "independent_grants_waited_before_callback": True}
        self.mark("real independent OIDC grants enter concurrent callbacks and retain one same-sub account")

        deleted = users["deleted"]
        code, account = self.login(deleted, state["password"])
        assert code in (200, 201) and not account["isAdmin"]
        deleted_id = account["userId"]
        code, admin = self.login(state["admin"]["username"], state["password"])
        assert code in (200, 201) and admin["isAdmin"] and admin["userId"] == state["admin"]["user_id"]
        code, tombstone, _ = self.http(self.app + "/api/admin/users/" + deleted_id, {"force": False}, token=admin["accessToken"], method="DELETE")
        assert code == 200 and tombstone["id"] == deleted_id and tombstone["status"] == "deleted"
        before_retry = rows(deleted)
        assert len(before_retry) == 1 and before_retry[0]["id"] == deleted_id and before_retry[0]["deletedAt"] is not None
        # A new email prevents an email uniqueness failure from masking an
        # absent oauthId constraint on a retained native soft-delete row.
        change_email(deleted, "after-delete-" + deleted + "@" + self.domain)
        retry_code, denied = self.login(deleted, state["password"])
        assert 400 <= retry_code < 600 and "accessToken" not in denied, "native soft-deleted anchor was registered again"
        assert rows(deleted) == before_retry, "soft-delete retry created or rebound an identity"
        assert self.http(self.app + "/api/users/me", token=account["accessToken"])[0] == 401
        evidence["native_soft_delete"] = {"anchor": anchors[deleted], "user_id": deleted_id, "delete_status": code, "callback_status": retry_code, "account_rows": 1, "retained_deletedAt": before_retry[0]["deletedAt"], "directory_identity_still_active": True}
        self.mark("native administrator soft deletion retains the anchor and refuses fresh-email re-registration")
        for role in ("admin", "user"):
            code, protected = self.login(state[role]["username"], state["password"])
            assert code in (200, 201) and protected["userId"] == state[role]["user_id"] and protected["isAdmin"] == (role == "admin")
            assert self.sql("immich", f'''SELECT "oauthId" FROM "user" WHERE id='{state[role]["user_id"]}';''') == state[role]["anchor"]
        self.mark("private identity lifecycle leaves primary administrator and restore-user bindings intact")

    def directory_revocation(self, state, photo):
        """Real LDAP sync and native task delivery, never a fabricated JWT."""
        self.phase = "directory-revocation"
        code, protected = self.login(state["user"]["username"], state["password"])
        assert code in (200, 201)
        protected_credentials = self.create_credentials(protected, next(iter(state["assets"])), "protected-workspace-user")
        assert all(status == 200 for status in self.credential_status(protected_credentials).values())
        observations = {}
        self.report["directory_revocation_http_statuses"] = observations
        provider_pk = int(self.docker("exec", self.prefix + "authentik", "ak", "shell", "-c",
                                      "from authentik.providers.oauth2.models import OAuth2Provider; print(OAuth2Provider.objects.get(name='immich').pk)").decode().strip())
        issuer = self.issuer
        for mode in ("disable", "remove-app-group", "delete", "logout-first", "admin-role-loss"):
            username = "iwrev" + secrets.token_hex(5)
            self.samba("user", "add", username, state["password"], "--userou=OU=People", "--mail-address=" + username + "@" + self.domain)
            self.samba("group", "addmembers", "APP_immich", username)
            if mode == "admin-role-loss":
                self.samba("group", "addmembers", "Admins", username)
            anchors = {}
            def anchor_ready():
                found = re.search(r"^anasIdentityAnchor: ([a-f0-9-]+)$", self.samba("user", "show", username, "--attributes=anasIdentityAnchor"), re.M)
                if found:
                    anchors[username] = str(uuid.UUID(found.group(1)))
                return username in anchors
            self.wait(anchor_ready)
            self.directory_sync()
            probe = "from authentik.core.models import UserSourceConnection; from authentik.sources.ldap.models import LDAPSource; s=LDAPSource.objects.get(slug='samba-ad'); c=UserSourceConnection.objects.get(source=s,user__username=" + repr(username) + "); assert c.user.all_groups().filter(name='APP_immich').exists()"
            if mode == "admin-role-loss":
                probe += "; assert c.user.all_groups().filter(name='Admins').exists()"
            self.wait(lambda: bool(self.docker("exec", self.prefix + "authentik", "ak", "shell", "-c", probe) or True))
            cookie = self.reports / ("revocation-cookie-" + uuid.uuid4().hex)
            code, account = self.login(username, state["password"], cookie=cookie)
            assert code in (200, 201) and account["isAdmin"] == (mode == "admin-role-loss")
            user_id = str(uuid.UUID(account["userId"]))
            asset_id = self.upload(account["accessToken"], "revocation-" + mode + ".png", photo, "image/png")
            self.wait(lambda: self.http(self.app + "/api/assets/" + asset_id + "/thumbnail?size=thumbnail", token=account["accessToken"], raw=True)[0] == 200)
            credentials = self.create_credentials(account, asset_id, "revocation-" + mode)
            assert all(status == 200 for status in self.credential_status(credentials).values())
            if mode == "logout-first":
                token_probe = "from authentik.providers.oauth2.models import AccessToken; from django.utils import timezone; assert AccessToken.objects.filter(user__username=" + repr(username) + ",provider__name='immich',expires__gt=timezone.now()).exists()"
                self.docker("exec", self.prefix + "authentik", "ak", "shell", "-c", token_probe)
                assert self.http(self.idp + "/api/v3/core/users/me/", cookie=cookie)[0] == 200, "ordinary IAM session was not authenticated before logout"
                code, out, _ = self.http(self.app + "/api/auth/logout", {}, cookie=cookie)
                self.report["ordinary_rp_logout_probe"] = {"http_status": code, "body_kind": type(out).__name__}
                assert code == 200 and out["successful"] is True
                assert out["redirectUri"].startswith(self.idp + "/"), "ordinary logout did not return the native IAM end-session URL"
                _, _, redirect = self.http(out["redirectUri"], cookie=cookie, follow=True)
                parts = urllib.parse.urlsplit(redirect)
                if "/if/flow/" in parts.path:
                    api = urllib.parse.urlunsplit((parts.scheme, parts.netloc, parts.path.replace("/if/flow/", "/api/v3/flows/executor/", 1), urllib.parse.urlencode({"query": parts.query}), ""))
                    flow_status, flow, flow_url = self.http(api, cookie=cookie)
                    self.report["ordinary_iam_flow_probe"] = {"http_status": flow_status, "body_kind": type(flow).__name__,
                                                              "same_iam_origin": urllib.parse.urlsplit(flow_url).netloc == urllib.parse.urlsplit(self.idp).netloc}
                    if flow_status == 200:
                        assert isinstance(flow, dict) and flow.get("component") == "xak-flow-redirect", "IAM end-session flow did not complete"
                        self.http(urllib.parse.urljoin(api, flow["to"]), cookie=cookie, follow=True)
                    else:
                        # Native SessionDelete can finish with an empty HTTP
                        # redirect instead of the frontend's JSON challenge.
                        assert flow_status == 302 and flow is None and flow_url == api, "unexpected native IAM end-session response"
                self.wait(lambda: self.http(self.idp + "/api/v3/core/users/me/", cookie=cookie)[0] in (401, 403))
                self.wait(lambda: bool(self.docker("exec", self.prefix + "authentik", "ak", "shell", "-c", token_probe.replace("assert AccessToken", "assert not AccessToken")) or True))
                after_logout = self.credential_status(credentials)
                assert after_logout == {"bearer": 401, "cookie": 401, "api_key": 200, "share": 200}, "ordinary logout changed independent API key/share behavior"
                self.mark("ordinary real RP/IAM logout removes active OAuth grant but retains independent API key/share")
            cookie.unlink(missing_ok=True)
            sync_started = time.time()
            if mode == "disable":
                self.samba("user", "disable", username)
            elif mode == "delete":
                self.samba("user", "delete", username)
            elif mode == "admin-role-loss":
                self.samba("group", "removemembers", "Admins", username)
            else:
                self.samba("group", "removemembers", "APP_immich", username)
            self.directory_sync()
            status_capture = {}
            def revoked():
                assert all(status == 200 for status in self.credential_status(protected_credentials).values()), "directory notification revoked an unrelated user credential"
                statuses = self.credential_status(credentials)
                status_capture["last"] = statuses
                return statuses["bearer"] == statuses["cookie"] == statuses["api_key"] == 401 and statuses["share"] in (400, 401, 403, 404)
            self.wait(revoked)
            # DONE native tasks clear message. These IDs/times are window
            # evidence only; the signed receiver ledger identifies this sub,
            # and the real HTTP checks establish credential invalidation.
            delivery_probe = f'''import datetime,json
from authentik.tasks.models import Task
from django_dramatiq_postgres.models import TaskState
from authentik.core.models import User,UserSourceConnection
from authentik.sources.ldap.models import LDAPSource
source=LDAPSource.objects.get(slug='samba-ad')
started=datetime.datetime.fromtimestamp({sync_started!r},datetime.timezone.utc)
assert Task.objects.filter(actor_name='authentik.sources.ldap.tasks.ldap_sync',state=TaskState.DONE,mtime__gte=started).exists()
rows=[{{'message_id':str(row.message_id),'retries':row.retries,'mtime':row.mtime.isoformat()}} for row in Task.objects.filter(actor_name='authentik.providers.oauth2.tasks.send_backchannel_logout_request',state=TaskState.DONE,mtime__gte=started)]
assert rows
user=User.objects.filter(username={username!r}).first()
if {mode!r}=='delete':
    assert not UserSourceConnection.objects.filter(source=source,user__username={username!r}).exists()
elif {mode!r}=='disable':
    assert user is not None and not user.is_active
elif {mode!r}=='admin-role-loss':
    assert user is not None and user.is_active and user.all_groups().filter(name='APP_immich').exists() and not user.all_groups().filter(name='Admins').exists()
else:
    assert user is not None and not user.all_groups().filter(name='APP_immich').exists()
print(json.dumps(rows))'''
            delivery = {}
            def delivered():
                delivery[mode] = json.loads(self.docker("exec", self.prefix + "authentik", "ak", "shell", "-c", delivery_probe))
                return True
            self.wait(delivered)
            signed_cutoff = self.receiver_signed_cutoff(anchors[username])
            observations[mode] = {"http_statuses": status_capture["last"], "sender_tasks_done_window": delivery[mode],
                                  "receiver_signed_cutoff": signed_cutoff}
            if mode == "admin-role-loss":
                self.wait_later_oidc_issuance(signed_cutoff["epoch"])
                code, demoted = self.login(username, state["password"])
                assert code in (200, 201) and demoted["userId"] == user_id and demoted["isAdmin"] is False, "admitted role loss did not retain identity and demote fresh native login"
                demoted_credentials = self.create_credentials(demoted, asset_id, "demoted-user")
                assert all(status == 200 for status in self.credential_status(demoted_credentials).values())
                assert revoked(), "fresh ordinary login resurrected old administrator credentials"
                observations[mode]["fresh_native_login"] = {"user_id": user_id, "is_admin": False}
                self.mark("real Admins loss retains APP_immich admission and demotes the same identity on fresh native OIDC login")
            else:
                code, _ = self.login(username, state["password"], allow_denied=True)
                assert code in (400, 401, 403, 404), "directory-revoked user obtained a new application session"
            assert self.sql("immich", f'''SELECT "oauthId" FROM "user" WHERE id='{user_id}';''') == anchors[username], "revocation removed or rebound the internal account"
            assert self.sql("immich", f"SELECT count(*) FROM asset WHERE id='{asset_id}' AND \"ownerId\"='{user_id}';") == "1", "revocation removed managed media"
            media_path = self.sql("immich", f'''SELECT "originalPath" FROM asset WHERE id='{asset_id}';''')
            assert digest(checked_media_path(self.workspace, media_path)) == hashlib.sha256(photo).hexdigest(), "revocation changed the managed original"
            self.mark("real LDAP " + mode + " invalidates old Bearer/Cookie/API key/share and protects unrelated user")
            if mode == "remove-app-group":
                # Retry the same native delivery after access is re-admitted.
                # The provider creates a fresh signed iat/jti, retaining the
                # original event_timestamp. New credentials must survive it.
                self.samba("group", "addmembers", "APP_immich", username)
                self.directory_sync()
                admitted_probe = "from authentik.core.models import User; assert User.objects.get(username=" + repr(username) + ").all_groups().filter(name='APP_immich').exists()"
                self.wait(lambda: bool(self.docker("exec", self.prefix + "authentik", "ak", "shell", "-c", admitted_probe) or True))
                self.wait_later_oidc_issuance(signed_cutoff["epoch"])
                code, renewed = self.login(username, state["password"])
                assert code in (200, 201) and renewed["userId"] == user_id
                fresh_credentials = self.create_credentials(renewed, asset_id, "readmitted-user")
                assert all(status == 200 for status in self.credential_status(fresh_credentials).values())
                revoked_at = signed_cutoff["epoch"]
                retry_probe = f'''from authentik.tasks.models import Task
from authentik.providers.oauth2.models import OAuth2Provider
from authentik.providers.oauth2.tasks import send_backchannel_logout_request
p=OAuth2Provider.objects.get(pk={provider_pk!r},name='immich')
args=(p.pk,{issuer!r},{anchors[username]!r},None,{revoked_at!r})
print(send_backchannel_logout_request.send_with_options(args=args,rel_obj=p).message_id)'''
                retry_id = self.docker("exec", self.prefix + "authentik", "ak", "shell", "-c", retry_probe).decode().strip()
                retry_done = "from authentik.tasks.models import Task; from django_dramatiq_postgres.models import TaskState; assert Task.objects.filter(message_id=" + repr(retry_id) + ",state=TaskState.DONE).exists()"
                self.wait(lambda: bool(self.docker("exec", self.prefix + "authentik", "ak", "shell", "-c", retry_done) or True))
                assert all(status == 200 for status in self.credential_status(fresh_credentials).values()), "old directory-event retry revoked post-readmission credentials"
                assert all(status == 200 for status in self.credential_status(protected_credentials).values()), "old directory-event retry revoked unrelated credentials"
                assert revoked(), "readmission resurrected an old credential"
                observations[mode]["retry_task_done"] = retry_id
                observations[mode]["after_retry_http_statuses"] = status_capture["last"]
                self.mark("native directory retry preserves post-readmission session/API key/share and never resurrects old credentials")
        self.report["directory_revocation_http_statuses"] = observations
        self.verify_state(state)

    def verify_directory_event_delivery(self, state, photo):
        """Prove the journal path without manually triggering the tested sync."""
        self.phase = "directory-event-delivery"
        username = "iwevent" + secrets.token_hex(5)
        self.samba("user", "add", username, state["password"], "--userou=OU=People", "--mail-address=" + username + "@" + self.domain)
        self.samba("group", "addmembers", "APP_immich", username)
        anchor = {}
        def anchored():
            found = re.search(r"^anasIdentityAnchor: ([a-f0-9-]+)$", self.samba("user", "show", username, "--attributes=anasIdentityAnchor"), re.M)
            if found:
                anchor["value"] = str(uuid.UUID(found.group(1)))
            return bool(anchor)
        self.wait(anchored)
        # Only fixture preparation uses an explicit sync. After the mutation
        # below, the existing subscriber/scheduler is the sole trigger path.
        self.directory_sync()
        admitted = "from authentik.core.models import User; assert User.objects.get(username=" + repr(username) + ").all_groups().filter(name='APP_immich').exists()"
        self.wait(lambda: bool(self.docker("exec", self.prefix + "authentik", "ak", "shell", "-c", admitted) or True))
        code, account = self.login(username, state["password"])
        assert code in (200, 201) and account["isAdmin"] is False
        asset = self.upload(account["accessToken"], "directory-event.png", photo, "image/png")
        self.wait(lambda: self.http(self.app + "/api/assets/" + asset + "/thumbnail?size=thumbnail", token=account["accessToken"], raw=True)[0] == 200)
        credentials = self.create_credentials(account, asset, "automatic-directory-event")
        code, protected = self.login(state["user"]["username"], state["password"])
        assert code in (200, 201)
        protected_credentials = self.create_credentials(protected, next(iter(state["assets"])), "automatic-event-unrelated-user")
        assert all(x == 200 for x in self.credential_status(credentials).values())
        assert all(x == 200 for x in self.credential_status(protected_credentials).values())
        journal = pathlib.Path(self.envs["authentik"]["AUTHENTIK_DIRWATCH_EVENTS_DIR"]) / "events.jsonl"
        health_path = self.workspace / "data/authentik/data/anas-dirwatch/health.json"
        for path in (journal, health_path):
            assert path.is_file() and path.resolve().is_relative_to((self.workspace / "data").resolve()), "event evidence escaped this workspace"
        def events():
            return [json.loads(line) for line in journal.read_text().splitlines() if line.strip()]
        before_seq = max((x["seq"] for x in events()), default=0)
        group = re.search(r"^dn:\s*(.+)$", self.samba("group", "show", "APP_immich"), re.M)
        assert group, "native group DN missing"
        started_wall, started = time.time(), time.monotonic()
        self.samba("group", "removemembers", "APP_immich", username)
        observed = {}
        def delivered():
            assert all(x == 200 for x in self.credential_status(protected_credentials).values()), "automatic event revoked unrelated credentials"
            statuses = self.credential_status(credentials)
            matching = [x for x in events() if x["seq"] > before_seq and x.get("op") == "Modify" and x.get("dn", "").casefold() == group.group(1).casefold() and "member" in [v.casefold() for v in x.get("attributes", [])]]
            health = json.loads(health_path.read_text())
            observed.update(http_statuses=statuses, matching_event_sequences=[x["seq"] for x in matching], watcher_cursor=health["cursor"], watcher_trigger_at=health["last_trigger_at"])
            self.report["automatic_directory_event_probe"] = {**observed, "elapsed_seconds": round(time.monotonic() - started, 3),
                                                             "acceptance_bound_seconds": 300, "manual_sync_after_mutation": False}
            return (bool(matching) and health["ready"] is True and not health["last_error"]
                    and health["cursor"] >= max(x["seq"] for x in matching)
                    and health["last_trigger_at"] >= int(started_wall)
                    and statuses["bearer"] == statuses["cookie"] == statuses["api_key"] == 401
                    and statuses["share"] in (400, 401, 403, 404))
        self.wait(delivered, timeout=300)
        elapsed = time.monotonic() - started
        assert elapsed <= 300, "automatic directory delivery exceeded this host acceptance bound"
        observed.update(elapsed_seconds=round(elapsed, 3), acceptance_bound_seconds=300, manual_sync_after_mutation=False,
                        receiver_signed_cutoff=self.receiver_signed_cutoff(anchor["value"]))
        native_probe = f'''from authentik.core.models import User
from authentik.tasks.models import Task
from django_dramatiq_postgres.models import TaskState
import datetime,json
started=datetime.datetime.fromtimestamp({started_wall!r},datetime.timezone.utc)
assert not User.objects.get(username={username!r}).all_groups().filter(name='APP_immich').exists()
counts={{name:Task.objects.filter(actor_name=actor,state=TaskState.DONE,mtime__gte=started).count() for name,actor in [('ldap_sync_done','authentik.sources.ldap.tasks.ldap_sync'),('signed_delivery_done','authentik.providers.oauth2.tasks.send_backchannel_logout_request')]}}
assert all(counts.values())
print(json.dumps(counts))'''
        native = {}
        def completed():
            native.update(json.loads(self.docker("exec", self.prefix + "authentik", "ak", "shell", "-c", native_probe)))
            return True
        self.wait(completed, timeout=300)
        observed["native_tasks_done"] = native
        assert self.sql("immich", f'''SELECT "oauthId" FROM "user" WHERE id='{account["userId"]}';''') == anchor["value"]
        assert self.sql("immich", f"SELECT count(*) FROM asset WHERE id='{asset}' AND \"ownerId\"='{account['userId']}';") == "1"
        original = self.sql("immich", f'''SELECT "originalPath" FROM asset WHERE id='{asset}';''')
        assert digest(checked_media_path(self.workspace, original)) == hashlib.sha256(photo).hexdigest()
        self.report["automatic_directory_event"] = observed
        self.mark("automatic Samba journal/subscriber/native sync/signed notification revokes old credentials within the 300-second host acceptance bound")
        self.verify_state(state)

    def upload(self, token, name, content, mime):
        path = self.reports / name
        path.write_bytes(content)
        # curl -F supplies the real multipart upload shape used by the client.
        argv = ["curl", "--silent", "--show-error", "--noproxy", "*", "--cacert", self.ca, "--fail-with-body",
                "--header", "Authorization: Bearer " + token]
        p = urllib.parse.urlsplit(self.app)
        argv += ["--resolve", f"{p.hostname}:{p.port}:{self.args.host_ip}"]
        for key, value in {"deviceAssetId": name, "deviceId": self.prefix, "fileCreatedAt": "2026-10-03T00:00:00Z", "fileModifiedAt": "2026-10-03T00:00:00Z"}.items():
            argv += ["--form-string", key + "=" + value]
        result = json.loads(command([*argv, "--form", f"assetData=@{path};type={mime}", self.app + "/api/assets"]))
        return result["id"]

    def runtime(self, *, allow_empty=False):
        ids = self.docker("ps", "--filter", "name=^/" + self.prefix, "--format", "{{.ID}}").decode().split()
        if not ids and allow_empty:
            return {}
        assert ids, "ANAS created no running containers"
        result = {}
        for item in json.loads(self.docker("inspect", *ids)):
            labels = item["Config"]["Labels"] or {}
            project = labels.get("com.docker.compose.project", "")
            source = pathlib.Path(labels.get("com.docker.compose.project.working_dir", "/"))
            if not project.startswith(self.prefix) or not source.is_relative_to(self.workspace):
                raise RuntimeError("container ownership escaped the freshly allocated workspace")
            result[(project[len(self.prefix):], labels["com.docker.compose.service"])] = {"id": item["Id"], "image": item["Image"]}
        return result

    def verify_database_access(self):
        for consumer in ("immich", self.iam):
            flags = self.sql(consumer, "SELECT current_user||':'||rolsuper::int||':'||rolcreatedb::int||':'||rolcreaterole::int FROM pg_roles WHERE rolname=current_user;")
            assert flags == consumer + ":0:0:0", "application role has privilege or does not match its actual TCP connection"
            try:
                self.sql(consumer, "SELECT 1;", password="deliberately-wrong-e2e-password")
            except RuntimeError:
                pass
            else:
                raise AssertionError("application TCP authentication accepted a wrong password")
        versions = dict(x.split("=", 1) for x in self.sql("immich", "SELECT extname||'='||extversion FROM pg_extension WHERE extname IN ('vector','cube','earthdistance') ORDER BY extname;").splitlines())
        assert versions == {**EXTENSIONS, "vector": self.vector_version}
        assert self.sql("immich", "SELECT current_setting('server_version_num');") == "180004"
        assert self.provider_sql("SELECT current_setting('shared_preload_libraries');") == ""

    def verify_maintenance_plan(self, name, modules):
        """Record the actual CLI consumer/stop scope without changing runtime."""
        before_runtime = self.runtime()
        files = (self.workspace / "config.yml", self.workspace / ".anas/secrets.yml",
                 self.workspace / ".anas/state/active.yml")
        before_hashes = {str(path): digest(path) for path in files}
        reply = self.anas(name, "plan", "-w", self.workspace, "--module-root", modules)
        provider = reply["module_plans"]["postgres"]
        consumers = provider["database_consumers"].split(",")
        assert set(consumers) == {self.iam, "immich"} and len(consumers) == 2, "ANAS plan omitted or duplicated a shared PostgreSQL consumer"
        rule = provider["extension_upgrade"]
        assert "full workspace stop, ANAS recovery point, and provider qualification before consumers start" in rule, "ANAS plan did not expose the maintenance barrier"
        assert rule.count("stop/restart scope=") == 1, "ANAS plan has no unique stop/restart scope"
        scope = rule.split("stop/restart scope=", 1)[1].split(",")
        assert scope == reply["modules"] and {"postgres", *consumers} <= set(scope), "ANAS plan does not cover the complete resolved workspace"
        assert self.runtime() == before_runtime and all(digest(path) == before_hashes[str(path)] for path in files), "read-only ANAS plan changed runtime or managed state"
        self.report.setdefault("postgres_maintenance_plans", {})[name] = {"database_consumers": consumers, "stop_restart_scope": scope, "managed_state_unchanged": True}
        self.mark("actual ANAS plan exposes both shared PostgreSQL consumers and full workspace stop/restart scope: " + name)

    def verify_state(self, state):
        self.phase = "verify-state"
        self.wait(lambda: self.http(self.app + "/api/server/ping")[0] == 200)
        self.verify_database_access()
        assert self.sql(self.iam, "SELECT value FROM anas_workspace_recovery WHERE id=1;") == "before-backup"
        assert self.docker("exec", self.prefix + "immich_valkey", "valkey-cli", "--raw", "GET", "anas:workspace:e2e").decode().strip() == "before-backup"
        assert self.sql("immich", "SELECT media_hash FROM anas_workspace_vectors WHERE id=1;") == state["vector_media_hash"]
        index_plan = self.sql("immich", "SET enable_seqscan=off; EXPLAIN SELECT embedding <-> '[1,2,4]'::vector FROM anas_workspace_vectors ORDER BY embedding <-> '[1,2,4]'::vector LIMIT 1;")
        assert "Index Scan using anas_workspace_vectors_hnsw" in index_plan, "the genuine pre-upgrade HNSW index is not queryable"
        assert self.sql("immich", "SET enable_seqscan=off; SELECT embedding <-> '[1,2,4]'::vector FROM anas_workspace_vectors ORDER BY embedding <-> '[1,2,4]'::vector LIMIT 1;").splitlines()[-1] == "1"
        code, version, _ = self.http(self.app + "/api/server/version")
        assert code == 200 and (version["major"], version["minor"], version["patch"]) == (3, 2, 4)
        for role, expected_admin in (("admin", True), ("user", False)):
            code, account = self.login(state[role]["username"], state["password"])
            assert code in (200, 201) and account["isAdmin"] == expected_admin
            assert account["userId"] == state[role]["user_id"]
            anchor = state[role]["anchor"]
            assert self.sql("immich", f'''SELECT "oauthId" FROM "user" WHERE id='{account["userId"]}';''') == anchor
            assert account["userId"] != anchor
            if role == "user":
                token = account["accessToken"]
        for asset, expected in state["assets"].items():
            code, body, _ = self.http(self.app + "/api/assets/" + asset + "/original", token=token, raw=True)
            assert code == 200 and hashlib.sha256(body).hexdigest() == expected
        code, album, _ = self.http(self.app + "/api/albums/" + state["album_id"], token=token)
        assert code == 200 and album["albumName"] == "ANAS recovery album" and album["assetCount"] == len(state["assets"])
        # Fixed v3.2.4 AlbumResponseDto carries metadata/counts, not assets.
        # Query the native metadata API to verify exact album membership.
        code, result, _ = self.http(self.app + "/api/search/metadata",
                                    {"filter": {"albumIds": {"any": [state["album_id"]]}}}, token=token)
        assert code in (200, 201)
        assert result["assets"]["nextCursor"] is None, "seed album unexpectedly spans multiple pages"
        assert set(x["id"] for x in result["assets"]["items"]) == set(state["assets"])


    def run(self):
        self.reports.mkdir(mode=0o700)
        self.backups.mkdir(mode=0o700)
        old_modules = self.module_fixture("pgvector-0.8.1-test-only")
        failed_modules = self.module_fixture("pgvector-0.8.2-failure-test-only", fail_after_maintenance=True)
        self.report["test_only_postgres_baseline"] = {"module_version": "18.4.0", "revision": 3, "vector": "0.8.1", "source_sha256": OLD_VECTOR_SOURCE, "published_release": False}
        self.report["binary_sha256"] = {"anas": digest(self.args.anas), "anas-helper": digest(pathlib.Path(self.args.anas).parent / "anas-helper")}
        config = {"modules": {"samba_dc": {"config": {"domain": "ad." + self.domain, "application_dns_mode": "separate_zone", "anchor_scan_interval": "30"}},
                              "postgres": {}, "authentik": {}, "immich": {"config": {"machine_learning": False}}, "traefik": {"config": {"base_port": self.args.port}}},
                  "identity": {"iam": {"provider": "authentik"}}, "rollback": {"snapshot": {"backend": "btrfs"}},
                  "global": {"base_domain": self.domain, "email": "admin@" + self.domain, "virtual_domain": True, "timezone": "Etc/UTC",
                             "container_prefix": self.prefix, "network_prefix": self.prefix, "host_ip": self.args.host_ip,
                             "host_lan_ip": self.args.host_lan_ip, "host_lan_bridge_ip": self.args.host_lan_bridge_ip,
                             "chinese_speedup": False, "chinese_build_speedup": False},
                  "env": {"ANAS_IMAGE_REGISTRY": self.args.image_registry}}
        config_path = self.reports / "input-config.json"
        write_json(config_path, config)
        # PostgreSQL's published Module image is built by the repository image
        # workflow rather than a Compose build service. Build that exact source
        # image explicitly; all deployment/application operations still use ANAS.
        self.phase = "prepare-fixed-immich-mirrors"
        self.prepare_immich_mirrors()
        self.phase = "build-postgres-module-image"
        self.docker("build", "--build-arg", "ANAS_IMAGE_REGISTRY=" + self.args.image_registry,
                    "--tag", self.args.image_registry + "/anas-postgres:18.4.0-r4", self.modules / "postgres/postgres")
        self.docker("build", "--build-arg", "ANAS_IMAGE_REGISTRY=" + self.args.image_registry,
                    "--tag", self.args.image_registry + "/anas-postgres:18.4.0-r3", old_modules / "postgres/postgres")
        self.anas("new-init", "init", self.workspace, "-c", config_path, "--module-root", old_modules, "-y")
        self.initialized = True
        self.anas("new-apply", "apply", "-w", self.workspace, "--module-root", old_modules, "--update-lock", "--build", "-y")
        self.load_envs()
        self.wait(lambda: self.http(self.app + "/api/server/ping")[0] == 200)
        self.phase = "cache-fixed-declared-images"
        self.cache_declared_images()
        app_image = self.runtime()[("immich", "anas_immich")]["image"]
        self.docker("run", "--rm", "--network", "none", "--label", "anas.test.immich-workspace=" + self.prefix,
                    "--entrypoint", "node", app_image, "-e",
                    "const c=require('/usr/src/app/server/dist/constants.js'),s=require('/usr/src/app/server/node_modules/semver'); if(!s.satisfies('0.8.1',c.VECTOR_VERSION_RANGE))throw new Error('fixed Immich no longer accepts genuine pgvector0.8.1');")
        self.mark("real ANAS new apply starts fixed Immich/PostgreSQL and shared Authentik")
        baseline = self.runtime()
        before_secret = digest(self.workspace / ".anas/secrets.yml")
        self.anas("repeat-apply", "apply", "-w", self.workspace, "--module-root", old_modules, "--update-lock", "-y")
        self.load_envs()
        assert self.runtime() == baseline, "repeat ANAS apply recreated an unchanged runtime container"
        assert digest(self.workspace / ".anas/secrets.yml") == before_secret, "repeat apply changed generated secrets"
        self.mark("real ANAS repeat apply preserves runtime containers and secrets")
        self.phase = "directory-users"
        password = "Anas-" + secrets.token_hex(12) + "-E2e9!"
        users = {role: "iwe" + role[0] + secrets.token_hex(4) for role in ("admin", "user")}
        for role, user in users.items():
            self.samba("user", "add", user, password, "--userou=OU=People", "--mail-address=" + user + "@" + self.domain)
            self.samba("group", "addmembers", "Admins" if role == "admin" else "APP_immich", user)
        anchors = {}
        for role, user in users.items():
            def anchor_ready():
                row = self.samba("user", "show", user, "--attributes=anasIdentityAnchor")
                found = re.search(r"^anasIdentityAnchor: ([a-f0-9-]+)$", row, re.M)
                if found:
                    anchors[role] = str(uuid.UUID(found.group(1)))
                return role in anchors
            self.wait(anchor_ready)
        self.docker("exec", self.prefix + "authentik", "ak", "shell", "-c",
                    "from authentik.tasks.schedules.models import Schedule; [s.send() for s in Schedule.objects.filter(actor_name='authentik.sources.ldap.tasks.ldap_sync') if getattr(s.rel_obj,'slug',None)=='samba-ad']")
        names = list(users.values())
        sync_probe = "from authentik.core.models import UserSourceConnection; from authentik.sources.ldap.models import LDAPSource; s=LDAPSource.objects.get(slug='samba-ad'); assert all(UserSourceConnection.objects.filter(source=s,user__username=n).exists() for n in " + repr(names) + ")"
        self.wait(lambda: bool(self.docker("exec", self.prefix + "authentik", "ak", "shell", "-c", sync_probe) or True))
        assert self.sql("immich", 'SELECT count(*) FROM "user";') == "0"
        code, _ = self.login(users["user"], password)
        assert code == 400 and self.sql("immich", 'SELECT count(*) FROM "user";') == "0", "first ordinary login created an account"
        state = {"password": password}
        for role in ("admin", "user"):
            code, account = self.login(users[role], password)
            assert code in (200, 201) and account["isAdmin"] == (role == "admin")
            state[role] = {"username": users[role], "anchor": anchors[role], "user_id": account["userId"]}
            if role == "user":
                token = account["accessToken"]
        self.mark("native real AD/Authentik OIDC creates roleClaim administrator and ordinary anchored account")
        self.identity_lifecycle(state)
        self.verify_entry_restrictions(state)
        for path, body in (("/api/auth/admin-sign-up", {"email": "local@" + self.domain, "password": "Forbidden1!", "name": "local"}), ("/api/oauth/unlink", {})):
            code, _, _ = self.http(self.app + path, body, token=token)
            assert code in (400, 403), "a local or unbind account entry point was accepted"
        chunk = lambda kind, body: struct.pack("!I", len(body)) + kind + body + struct.pack("!I", zlib.crc32(kind + body) & 0xffffffff)
        photo = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack("!2I5B", 16, 16, 8, 2, 0, 0, 0)) + chunk(b"IDAT", zlib.compress((b"\0" + bytes([32, 96, 160]) * 16) * 16)) + chunk(b"IEND", b"")
        photo_id = self.upload(token, "seed.png", photo, "image/png")
        app_image = self.runtime()[("immich", "anas_immich")]["image"]
        self.docker("run", "--rm", "--network", "none", "--label", "anas.test.immich-workspace=" + self.prefix,
                    "--entrypoint", "ffmpeg", "--volume", str(self.reports) + ":/fixture", app_image,
                    "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=blue:s=64x64:r=10", "-t", "1", "-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", "/fixture/seed.mp4")
        video = (self.reports / "seed.mp4").read_bytes()
        video_id = self.upload(token, "video.mp4", video, "video/mp4")
        state["assets"] = {photo_id: hashlib.sha256(photo).hexdigest(), video_id: hashlib.sha256(video).hexdigest()}
        state["vector_media_hash"] = hashlib.sha256(photo).hexdigest()
        self.sql("immich", "CREATE TABLE anas_workspace_vectors(id integer PRIMARY KEY,embedding vector(3),media_hash text); INSERT INTO anas_workspace_vectors VALUES(1,'[1,2,3]', '" + hashlib.sha256(photo).hexdigest() + "'); CREATE INDEX anas_workspace_vectors_hnsw ON anas_workspace_vectors USING hnsw(embedding vector_l2_ops);")
        self.wait(lambda: all(self.http(self.app + "/api/assets/" + asset + "/thumbnail?size=thumbnail", token=token, raw=True)[0] == 200 for asset in state["assets"]))
        code, album, _ = self.http(self.app + "/api/albums", {"albumName": "ANAS recovery album", "assetIds": list(state["assets"])}, token=token)
        assert code in (200, 201)
        state["album_id"] = album["id"]
        self.sql(self.iam, "CREATE TABLE anas_workspace_recovery(id integer PRIMARY KEY,value text NOT NULL); INSERT INTO anas_workspace_recovery VALUES(1,'before-backup');")
        assert self.docker("exec", self.prefix + "immich_valkey", "valkey-cli", "SET", "anas:workspace:e2e", "before-backup").decode().strip() == "OK"
        write_json(self.reports / "private-state.json", state)
        self.verify_state(state)
        self.mark("actual ordinary-role TCP connections, extension/preload, photo/video hashes and album verified")
        self.verify_seed_lifecycle(state, old_modules)
        self.verify_recovery_lifecycle(state, old_modules, failed_modules, photo)

    def verify_seed_lifecycle(self, state, old_modules):
        """Check unchanged apply and restart before a version transition."""
        data_runtime = self.runtime()
        data_secrets = digest(self.workspace / ".anas/secrets.yml")
        self.anas("data-repeat-apply", "apply", "-w", self.workspace, "--module-root", old_modules, "--update-lock", "-y")
        self.load_envs()
        assert self.runtime() == data_runtime and digest(self.workspace / ".anas/secrets.yml") == data_secrets
        self.verify_state(state)
        self.mark("real ANAS repeat apply preserves uploaded media, album, anchors and Valkey state")
        self.anas("restart", "restart", "-w", self.workspace)
        self.load_envs()
        self.verify_state(state)
        self.mark("real ANAS restart preserves OIDC identity, media, albums and other shared consumer")

    def verify_recovery_lifecycle(self, state, plan_modules, failed_modules, photo):
        """Continue server lifecycle checks with an already verified seed."""
        password = state["password"]
        users = {role: state[role]["username"] for role in ("admin", "user")}
        photo_hash = hashlib.sha256(photo).hexdigest()
        assert photo_hash == state["vector_media_hash"]
        photo_ids = [asset for asset, digest in state["assets"].items() if digest == photo_hash]
        assert len(photo_ids) == 1, "seed photo must identify one managed original"
        photo_id = photo_ids[0]
        old_deployment, old_runtime = self.active(), self.runtime()
        self.verify_maintenance_plan("extension-upgrade-before-plan", plan_modules)
        self.transition_with_stop_evidence("extension-upgrade", lambda: self.anas("extension-upgrade-apply", "apply", "-w", self.workspace, "--module-root", self.modules, "--update-lock", "-y"), old_runtime)
        upgraded_deployment = self.active()
        assert upgraded_deployment != old_deployment
        old_point, old_images = self.recovery_point(old_deployment, upgraded_deployment, old_runtime)
        self.vector_version = "0.8.2"
        self.load_envs()
        self.verify_state(state)
        self.verify_maintenance_plan("extension-upgrade-after-plan", self.modules)
        self.report["extension_upgrade"] = {"from_vector": "0.8.1", "to_vector": "0.8.2", "old_recovery_point": old_point, "old_image_ids": sorted(set(old_images.values())), "upgraded_deployment": upgraded_deployment}
        self.anas_rejected("upgrade-direct-rollback-rejected", "rollback", old_deployment, "-w", self.workspace, codes={"postgres_restore_required"})
        self.mark("real ANAS controlled extension upgrade stops all writers, captures old actual images, upgrades before consumers and preserves ordinary-role HNSW/media/shared databases")
        backup_id, expected_images = self.verify_whole_workspace_restore(state)
        self.verify_post_restore_lifecycle(state, old_point, old_images, failed_modules, backup_id, expected_images, photo)

    def verify_whole_workspace_restore(self, state):
        """Check the current fixed images, data, configuration and Secret together."""
        password = state["password"]
        users = {role: state[role]["username"] for role in ("admin", "user")}
        photo_ids = [asset for asset, checksum in state["assets"].items() if checksum == state["vector_media_hash"]]
        assert len(photo_ids) == 1, "restore requires exactly one managed seed photo"
        photo_id = photo_ids[0]
        saved_config, saved_secret = digest(self.workspace / "config.yml"), digest(self.workspace / ".anas/secrets.yml")
        created = self.anas("backup-create", "backup", "create", "-w", self.workspace, "--to", self.backups, "--mode", "copy", "-y")
        backup_id = created["backup_id"]
        backup_root = self.backups / backup_id
        self.anas("backup-verify", "backup", "verify", "--to", self.backups, "--backup-id", backup_id)
        images = read_yaml(backup_root / "meta/compose-images.yml")
        assert "sha256:" + digest(backup_root / "meta/compose-images.tar") == images["archive_sha256"]
        expected_images = {(x["module"], x["service"]): x["id"] for x in images["services"]}
        assert all(expected_images[key] == value["image"] for key, value in self.runtime().items())
        self.mark("ANAS consistent workspace backup carries database/media/config/Secret and actual service image archive")
        # Backup restarts consumers; their process may precede HTTP readiness.
        # Confirm the original business state before introducing test damage.
        self.verify_state(state)
        # Damage only this newly created test workspace, then recover through
        # ANAS. No alternate dump/restore or application backup scheduler.
        self.phase = "post-backup-mutation"
        original_path = self.sql("immich", f'''SELECT "originalPath" FROM asset WHERE id='{photo_id}';''')
        checked_media_path(self.workspace, original_path).write_bytes(b"post-backup-media-damage")
        self.sql(self.iam, "UPDATE anas_workspace_recovery SET value='after-backup' WHERE id=1;")
        self.docker("exec", self.prefix + "immich_valkey", "valkey-cli", "SET", "anas:workspace:e2e", "after-backup")
        code, account = self.login(users["user"], password)
        assert code in (200, 201)
        code, _, _ = self.http(self.app + "/api/albums/" + state["album_id"], {"albumName": "after backup"}, token=account["accessToken"], method="PATCH")
        assert code in (200, 201), "post-backup album mutation failed"
        assert self.sql("immich", f'''SELECT "albumName" FROM album WHERE id='{state["album_id"]}';''') == "after backup"
        self.anas("config-mutation", "config", "set", "-w", self.workspace, "--root", self.modules, "--defer", "--update-lock", "global.timezone", "Asia/Shanghai")
        store = read_yaml(self.workspace / ".anas/secrets.yml")
        store["secrets"]["IMMICH_WORKSPACE_E2E_AFTER_BACKUP"] = {"value": secrets.token_hex(32), "owner": "runner", "kind": "generated", "provenance": "isolated-e2e"}
        write_json(self.workspace / ".anas/secrets.yml", store)
        assert digest(self.workspace / "config.yml") != saved_config and digest(self.workspace / ".anas/secrets.yml") != saved_secret
        self.anas("backup-restore", "backup", "restore", "-w", self.workspace, "--from", self.backups, "--backup-id", backup_id, "-y")
        assert digest(self.workspace / "config.yml") == saved_config and digest(self.workspace / ".anas/secrets.yml") == saved_secret
        self.anas("restored-start", "start", "-w", self.workspace)
        self.load_envs()
        self.verify_state(state)
        assert all(expected_images[key] == value["image"] for key, value in self.runtime().items())
        self.mark("real ANAS full workspace restore recovers both consumers, identities, original media, album, configuration, secrets and matching images")
        self.report["restored_image_ids"] = sorted(set(expected_images.values()))
        return backup_id, expected_images

    def verify_post_restore_lifecycle(self, state, old_point, old_images, failed_modules, backup_id, expected_images, photo):
        """Verify failure recovery, interrupted maintenance and native revocation."""
        self.extension_failure_restore(state, old_point, old_images, failed_modules, backup_id, expected_images)
        self.extension_crash_retry(state, old_point, old_images, backup_id, expected_images)
        self.directory_revocation(state, photo)
        self.verify_directory_event_delivery(state, photo)
        self.report["status"] = "passed"

    def preserve_failure_evidence(self):
        # Only lifecycle state from this freshly allocated workspace is kept.
        # Do not copy configuration, Secret files, envs, media, or artifacts.
        # A finite 32 deployment files / 64KiB each also handles corrupt state.
        if not self.reports.is_dir() or self.reports.is_symlink():
            self.report["failure_evidence"] = {"status": "unavailable"}
            return
        evidence = {"failed_phase": self.report.get("failed_phase", self.phase), "files": []}

        def safe_path(target):
            if self.workspace.is_symlink():
                return False
            relative = target.relative_to(self.workspace)
            current = self.workspace
            for component in relative.parts:
                current = current / component
                if current.is_symlink():
                    return False
            return target.resolve().is_relative_to(self.workspace.resolve())

        def capture(target):
            item = {"path": str(target.relative_to(self.workspace))}
            try:
                if not safe_path(target):
                    item["status"] = "unsafe_path_skipped"
                elif not target.exists():
                    item["status"] = "absent"
                elif not target.is_file():
                    item["status"] = "non_file_skipped"
                else:
                    with target.open("rb") as stream:
                        content = stream.read(65537)
                    if len(content) > 65536:
                        item["status"] = "oversized_skipped"
                    else:
                        item.update(status="captured", content=content.decode("utf-8", errors="replace"))
            except (OSError, ValueError):
                item["status"] = "unreadable"
            evidence["files"].append(item)

        capture(self.workspace / ".anas/state/active.yml")
        states = self.workspace / ".anas/state/deployments"
        try:
            if safe_path(states) and states.is_dir():
                candidates = sorted(states.glob("*.yml"))
                evidence["omitted_deployment_files"] = max(0, len(candidates) - 32)
                for target in candidates[:32]:
                    capture(target)
        except (OSError, ValueError):
            evidence["deployment_listing"] = "unreadable"
        try:
            write_json(self.reports / "failure-workspace-state.json", evidence)
            self.report["failure_evidence"] = {"file": "failure-workspace-state.json", "files": len(evidence["files"])}
        except OSError:
            self.report["failure_evidence"] = {"status": "unavailable"}

    def cleanup(self):
        if self.report["status"] == "failed":
            # Stop and snapshot deletion can rewrite/remove the guard evidence.
            # Capture it before the first cleanup command, even with --keep.
            self.preserve_failure_evidence()
        self.report["cleanup"] = {"containers_stopped": False, "workspace_removed": False}
        if self.initialized:
            try:
                self.anas("cleanup-stop", "stop", "-w", self.workspace)
                if (self.docker("ps", "--all", "--quiet", "--filter", "name=^/" + self.prefix).strip()
                        or self.docker("ps", "--all", "--quiet", "--filter", "label=anas.test.immich-workspace=" + self.prefix).strip()):
                    self.report["cleanup"]["error"] = "workspace containers remain after ANAS stop; workspace retained"
                    return
                self.report["cleanup"]["containers_stopped"] = True
            except BaseException:
                self.report["cleanup"]["error"] = "ANAS stop failed; workspace retained for inspection"
                return
        if self.args.keep_workspace:
            self.report["cleanup"]["retained_by_request"] = True
            return
        if self.workspace.exists():
            # Scope was checked while absent. Remove subvolumes explicitly;
            # never a global prune, broad wildcard, or another deployment.
            for item in (self.workspace / "snapshots").glob("*"):
                self.anas("cleanup-snapshot-" + item.name, "snapshot", "delete", item.name, "-w", self.workspace, "--force", "-y")
            for tree in ("data", "userdata"):
                target = self.workspace / tree
                if target.exists():
                    if target.is_symlink():
                        raise RuntimeError("managed cleanup tree unexpectedly became a symlink")
                    # Btrfs subvolume roots have inode 256. An optional userdata
                    # directory need not be a subvolume; rmtree below handles it.
                    if target.stat().st_ino == 256:
                        command(["btrfs", "subvolume", "delete", target])
            shutil.rmtree(self.workspace)
        if self.backups.exists():
            shutil.rmtree(self.backups)
        self.report["cleanup"]["workspace_removed"] = not self.workspace.exists()


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--anas", required=True, help="same-source built anas, with anas-helper beside it")
    parser.add_argument("--modules", required=True)
    parser.add_argument("--workspace", required=True)
    parser.add_argument("--host-ip", required=True)
    parser.add_argument("--host-lan-ip", required=True)
    parser.add_argument("--host-lan-bridge-ip", required=True)
    parser.add_argument("--port", required=True, type=int)
    parser.add_argument("--image-registry", default="ghcr.io/anas-project")
    parser.add_argument("--keep-workspace", action="store_true")
    parser.add_argument("--preflight-only", action="store_true")
    args = parser.parse_args()
    try:
        workspace, modules = preflight(args)
    except (Blocked, OSError, RuntimeError, ValueError) as error:
        print(json.dumps({"status": "blocked", "phase": "preflight", "reason": str(error), "workspace_mutated": False}), file=sys.stderr)
        return 2
    if args.preflight_only:
        print(json.dumps({"status": "preflight_passed", "workspace_mutated": False}))
        return 0
    suite = Suite(args, workspace, modules)
    exit_code = 0
    try:
        suite.run()
    except BaseException as error:
        exit_code = 1
        suite.report.update(status="failed", failed_phase=suite.phase, reason=str(error), error_type=type(error).__name__, failure_origin=failure_origin(error))
    finally:
        try:
            suite.cleanup()
        except BaseException:
            suite.report["cleanup"] = {"error": "scoped cleanup failed; workspace retained"}
            exit_code = 1
        if suite.report.get("cleanup", {}).get("error"):
            suite.report["status"] = "failed"
            exit_code = 1
        if suite.reports.exists():
            write_json(suite.reports / "report.json", suite.report)
    print(json.dumps({"status": suite.report["status"], "report": str(suite.reports / "report.json")}))
    return exit_code


if __name__ == "__main__":
    sys.exit(main())
