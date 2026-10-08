#!/usr/bin/env python3
# TEST_CASES: TEMP-T-021
"""Prepare/run the fixed eight-Module editing fixture; never manage the daemon.

CLI, Compose and browser output remain in memory and are suppressed. The only
credentials used are Nextcloud's existing managed break_glass account, passed
through stdin or a disposable browser process environment.
"""
import argparse
import hashlib
import http.client
import importlib.util
import ipaddress
import json
import os
from pathlib import Path
import re
import shutil
import socket
import ssl
import stat
import subprocess
import sys
import time

_spec = importlib.util.spec_from_file_location("collabora_actions", Path(__file__).with_name("server-workspace-temp-collabora-action.py"))
actions = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(actions)
check = actions.core.check
MODULES = actions.MODULES
PLAYWRIGHT_IMAGE = "mcr.microsoft.com/playwright@sha256:dcc5531e97840b9b5e794f2814476b21571c5124a3fca2267d73041f56e7580e"
TEST_TITLE = "Collabora saves one document across normal stop/start, rebuild, and temporary root switch"


safe_cli_error = actions.safe_cli_error


def network_facts(entries, address):
    parsed = ipaddress.ip_address(address)
    check(parsed.version == 4 and parsed.is_private and not parsed.is_loopback and not parsed.is_link_local
          and not parsed.is_unspecified and not parsed.is_multicast, "editing entry IP must be a private IPv4 address")
    matches = [(entry["ifname"], info["prefixlen"]) for entry in entries for info in entry.get("addr_info", [])
               if info.get("family") == "inet" and info.get("local") == address]
    check(len(matches) == 1 and re.fullmatch(r"[a-zA-Z0-9_.-]{1,15}", matches[0][0]),
          "editing entry IP must belong to exactly one interface in this private namespace")
    return matches[0]


def render_fixture(template, values):
    rendered = template
    for key, value in values.items():
        # All values are validated fixed paths or private namespace facts.
        rendered = rendered.replace("@" + key + "@", str(value))
    check(not re.search(r"@[A-Z_]+@", rendered), "editing fixture has an unresolved placeholder")
    return rendered


def validate_browser_report(report, username, password):
    raw = json.dumps(report)
    check(all(secret and secret not in raw for secret in (username, password)), "browser report contains a credential")
    results = report.get("results")
    check(report.get("schema") == "anas.iam-logout-e2e/v1" and report.get("status") == "passed"
          and isinstance(results, list) and len(results) == 1 and results[0].get("title") == TEST_TITLE
          and results[0].get("status") == "passed" and not results[0].get("errors"),
          "browser report does not prove the actual fixed Collabora document test passed")


def validate_action_reports(reports, source_digest, binary_digest, run_id):
    check(len(reports) == len(actions.ACTIONS), "all three real lifecycle action reports are required")
    for action, report in zip(actions.ACTIONS, reports):
        check(report.get("schema") == "anas.workspace-temp-collabora-action/v1" and report.get("case_id") == "TEMP-T-021"
              and report.get("run_id") == run_id and report.get("action") == action and report.get("status") == "passed"
              and report.get("source_digest") == source_digest and report.get("binary_digest") == binary_digest
              and report.get("namespace_marker_verified") is True and report.get("fresh_container") is True
              and report.get("fresh_lease") is True and (action != "switch-a-to-b" or report.get("all_containers_rebuilt") is True),
              "lifecycle report belongs to another run or lacks an actual runtime assertion")


def cleanup_materials(path, run_id):
    check(path.is_file() and stat.S_IMODE(path.stat().st_mode) == 0o600, "browser cleanup report is absent or has wrong permissions")
    report = json.loads(path.read_text())
    check(report.get("schema") == "anas.workspace-temp-collabora-cleanup/v1" and report.get("case_id") == "TEMP-T-021"
          and report.get("run_id") == run_id and report.get("status") in ("complete", "failed")
          and report.get("document") in ("removed", "not_created", "unconfirmed")
          and report.get("webdav_context") in ("closed", "failed")
          and type(report.get("first_failure_preserved")) is bool
          and isinstance(report.get("failures"), list)
          and set(report["failures"]).issubset({"temporary_document_delete_failed", "webdav_context_dispose_failed"}),
          "browser cleanup report has foreign or unsafe fields")
    return {key: report[key] for key in ("status", "document", "webdav_context", "first_failure_preserved", "failures")}


def credential_account(document, login_url):
    account = document.get("account", {})
    check(document.get("ok") is True and account.get("username") and account.get("password")
          and account.get("url") == login_url, "managed Nextcloud break_glass credential is unavailable or uses the wrong fixture URL")
    return account["username"], account["password"]


def hook_inputs(source):
    paths = {source / "go.mod", source / "go.sum"}
    for name in MODULES:
        paths.add(source / "modules" / name / "module.yml")
        paths.update((source / "modules" / name / "hook").rglob("*.go"))
    return {str(path.relative_to(source)): "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()
            for path in sorted(paths)}


def compiled_hook(path):
    check(path.is_file() and not path.is_symlink() and stat.S_IMODE(path.stat().st_mode) == 0o755,
          "prebuilt Hook must be a real executable with mode 0755")
    raw = path.read_bytes()
    check(len(raw) > 64 and raw[:6] == b"\x7fELF\x02\x01" and raw[18:20] == b"\x3e\x00",
          "prebuilt Hook must be a Linux amd64 executable")
    return "sha256:" + hashlib.sha256(raw).hexdigest()


class Editing:
    def __init__(self):
        os.environ.pop("DOCKER_CONTEXT", None)
        actions.namespace.require_private_mount_namespace()
        self.root = Path(os.environ["ANAS_TEST_WORK_ROOT"])
        self.run_id = os.environ["ANAS_TEST_RUN_ID"]
        self.docker = shutil.which(os.environ.get("DOCKER_CMD", "docker"))
        check(self.docker is not None, "Docker client is required")
        self.failure = None
        docker_root = self.command([self.docker, "info", "--format", "{{.DockerRootDir}}"], "inspect_private_daemon").decode().strip()
        actions.core.require_isolation(self.root, self.run_id, os.environ.get("DOCKER_HOST", ""), docker_root)
        self.netns = Path(os.environ["ANAS_TEST_NETWORK_NAMESPACE"])
        check(self.netns.is_absolute() and re.fullmatch(r"/run/netns/anas-[a-zA-Z0-9_-]+", str(self.netns)), "named private ANAS network namespace is required")
        net = Path("/proc/self/ns/net").stat().st_ino
        check(net != Path("/proc/1/ns/net").stat().st_ino and self.netns.stat().st_ino == net
              and all(Path("/proc", os.environ[key], "ns/net").stat().st_ino == net
                      for key in ("ANAS_TEST_DOCKER_PID", "ANAS_TEST_CONTAINERD_PID")),
              "editing runner must share both private daemon namespaces")
        self.source = actions.scoped_path(self.root, Path(os.environ["ANAS_TEST_MODULE_ROOT"]))
        check(self.source == self.root / "src" and all((self.source / "modules" / name / "module.yml").is_file() for name in MODULES),
              "editing stack requires this run's frozen source root and eight requested Modules")
        self.anas = actions.scoped_path(self.root, Path(os.environ["ANAS_TEST_ANAS_CMD"]))
        check(self.anas.is_file(), "run-scoped ANAS binary is required")
        self.source_digest = os.environ["ANAS_TEST_SOURCE_DIGEST"]
        check(re.fullmatch(r"sha256:[a-f0-9]{64}", self.source_digest), "source digest is required")
        self.binary_digest = "sha256:" + hashlib.sha256(self.anas.read_bytes()).hexdigest()
        self.workspace = actions.scoped_path(self.root, self.root / "collabora-workspace")
        self.root_a = actions.scoped_path(self.root, self.root / "collabora-temp-a")
        self.root_b = actions.scoped_path(self.root, self.root / "collabora-temp-b")
        self.reports = actions.scoped_path(self.root, self.root / "reports")
        self.reports.mkdir(mode=0o700, exist_ok=True)
        self.browser_dir = actions.scoped_path(self.root, self.root / "collabora-browser-artifacts")
        self.dependencies_dir = actions.scoped_path(self.root, self.root / "collabora-browser-node-modules")
        self.prefix = "anas_temp_" + hashlib.sha256(self.run_id.encode()).hexdigest()[:10] + "_edit_"
        self.domain = os.environ.get("ANAS_TEST_DOMAIN", "temp-edit.test")
        check(re.fullmatch(r"[a-z0-9][a-z0-9.-]*\.(?:test|invalid)", self.domain) and ".." not in self.domain, "reserved virtual editing domain required")
        self.entry = os.environ["ANAS_TEST_ENTRY_IP"]
        self.interface, self.prefix_length = network_facts(json.loads(self.command(["ip", "-j", "address", "show"], "inspect_private_interface")), self.entry)
        self.port = int(os.environ.get("ANAS_TEST_ENTRY_PORT", "19071"))
        self.turn_port = int(os.environ.get("ANAS_TEST_TURN_PORT", "13478"))
        check(1024 <= self.port <= 65535 and 1024 <= self.turn_port <= 65535 and self.port != self.turn_port, "distinct unprivileged fixture ports are required")
        self.nc = f"https://nc.{self.domain}:{self.port}"
        self.collabora = f"https://collabora.{self.domain}:{self.port}"
        self.login = self.nc + "/login?direct=1"
        self.server_shutdown = "not_started"
        self.runtime = {}
        self.hook_binaries = {}
        self.browser_cleanup = None

    def command(self, arguments, operation, env=None, input=None, timeout=3600):
        try:
            result = subprocess.run([str(arg) for arg in arguments], stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                    env=env, input=input, timeout=timeout)
        except subprocess.TimeoutExpired:
            self.failure = {"operation": operation, "reason": "timeout"}
            raise actions.core.ProbeFailure("editing operation timed out; output suppressed")
        if result.returncode != 0:
            self.failure = {"operation": operation, "exit_code": result.returncode}
            if arguments and str(arguments[0]) == str(getattr(self, "anas", "")) and "--json" in arguments:
                error = safe_cli_error(result.stdout, getattr(self, "prefix", None))
                if error is not None:
                    self.failure["error"] = error
            raise actions.core.ProbeFailure("editing operation failed; output suppressed")
        return result.stdout

    def cli(self, operation, *arguments, **options):
        return self.command([self.anas, *arguments, "--json"], operation, **options)

    def hook_environment(self):
        # Runner prefers this published prebuilt layout. Refuse a missing or
        # stale derivative instead of letting it fall back to host Go.
        path = self.reports / "collabora-prebuilt-hooks.json"
        check(path.is_file(), "build all eight Hooks in the tools container before preparing the fixture")
        manifest = json.loads(path.read_text())
        check(manifest.get("schema") == "anas.workspace-temp-storage-prebuilt-hooks/v1"
              and manifest.get("run_id") == self.run_id and manifest.get("source_digest") == self.source_digest
              and manifest.get("tool_image") == PLAYWRIGHT_IMAGE and manifest.get("inputs") == hook_inputs(self.source)
              and set(manifest.get("binaries", {})) == set(MODULES), "prebuilt Hook evidence does not match the frozen input source")
        self.hook_binaries = {}
        for name in MODULES:
            binary = actions.scoped_path(self.root, self.source / "modules" / name / "hook/bin/linux-amd64/anas-hook")
            digest = compiled_hook(binary)
            check(digest == manifest["binaries"][name], "prebuilt Hook changed after the tools-container build")
            self.hook_binaries[name] = digest
        env = dict(os.environ)
        env.pop("GOROOT", None)
        env.pop("GOCACHE", None)
        env.pop("GOMODCACHE", None)
        return env

    def compiler_paths(self):
        sdk = self.root / "tools/go1.26.6/go"
        actions.scoped_path(self.root, sdk)
        check((sdk / "bin/go").is_file(), "run-scoped Go SDK is required as a read-only tools-container mount")
        caches = self.root / "collabora-hook-cache"
        actions.scoped_path(self.root, caches)
        caches.mkdir(mode=0o700, exist_ok=True)
        build_cache = actions.scoped_path(self.root, Path(os.environ.get("ANAS_TEST_GOCACHE", str(caches / "build"))))
        module_cache = actions.scoped_path(self.root, Path(os.environ.get("ANAS_TEST_GOMODCACHE", str(caches / "modules"))))
        for cache in (build_cache, module_cache):
            cache.mkdir(mode=0o700, parents=True, exist_ok=True)
        proxy = os.environ.get("ANAS_TEST_GOPROXY", "off")
        mounts = []
        if proxy != "off":
            check(proxy.startswith("file:///"), "Hook compiler accepts only an offline run-scoped file proxy")
            proxy_path = actions.scoped_path(self.root, Path(proxy[len("file://"):]))
            check(proxy_path.is_dir() and proxy == "file://" + str(proxy_path), "Go file proxy must be an existing exact run-scoped path")
            mounts = ["--mount", f"type=bind,source={proxy_path},target={proxy_path},readonly"]
        return sdk, build_cache, module_cache, proxy, mounts

    def compiler_command(self, name, output, paths):
        check(name in MODULES, "Hook compiler accepts only the fixed editing Module list")
        sdk, build_cache, module_cache, proxy, mounts = paths
        return [self.docker, "run", "--pull=never", "--rm", "--name", self.prefix + "hook_" + name,
                "--network", "none", "--memory", "1024m", "--pids-limit", "256", "--read-only",
                "--tmpfs", "/tmp:mode=1777,size=256m", "--cap-drop", "ALL", "--cap-add", "DAC_OVERRIDE", "--security-opt", "no-new-privileges",
                "--workdir", "/source", "--mount", f"type=bind,source={self.source},target=/source,readonly",
                "--mount", f"type=bind,source={sdk},target=/opt/go,readonly",
                "--mount", f"type=bind,source={build_cache},target=/go-build",
                "--mount", f"type=bind,source={module_cache},target=/go-mod",
                "--mount", f"type=bind,source={output},target=/output", *mounts,
                "--env", "GOROOT=/opt/go", "--env", "GOENV=off", "--env", "GOTOOLCHAIN=local",
                "--env", "GOCACHE=/go-build", "--env", "GOMODCACHE=/go-mod", "--env", "GOPROXY=" + proxy,
                "--env", "GOSUMDB=off", "--env", "GOFLAGS=-p=1", "--env", "GOMAXPROCS=2",
                "--env", "CGO_ENABLED=0", "--env", "GOOS=linux", "--env", "GOARCH=amd64", "--env", "HOME=/tmp",
                PLAYWRIGHT_IMAGE, "/opt/go/bin/go", "build", "-trimpath", "-o", "/output/anas-hook", "./modules/" + name + "/hook"]

    def hooks(self):
        check(not self.workspace.exists(), "prebuild Hooks before preparing or starting the editing stack")
        paths = self.compiler_paths()
        build_root = actions.scoped_path(self.root, self.root / "collabora-prebuilt-hooks")
        check(not build_root.exists() and not (self.reports / "collabora-prebuilt-hooks.json").exists(), "refusing to replace an existing Hook build")
        destinations = {name: actions.scoped_path(self.root, self.source / "modules" / name / "hook/bin/linux-amd64/anas-hook") for name in MODULES}
        check(all(not binary.exists() for binary in destinations.values()), "source already contains a prebuilt editing Hook")
        inputs = hook_inputs(self.source)
        build_root.mkdir(mode=0o700)
        binaries = {}
        for name in MODULES:
            output = build_root / name
            output.mkdir(mode=0o700)
            self.command(self.compiler_command(name, output, paths), "compile_hook_" + name)
            built = output / "anas-hook"
            built.chmod(0o755)
            binaries[name] = compiled_hook(built)
        check(hook_inputs(self.source) == inputs, "Hook input source changed during the isolated build")
        for name, destination in destinations.items():
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(build_root / name / "anas-hook", destination)
            destination.chmod(0o755)
        manifest = {"schema": "anas.workspace-temp-storage-prebuilt-hooks/v1", "run_id": self.run_id,
                    "source_digest": self.source_digest, "tool_image": PLAYWRIGHT_IMAGE,
                    "inputs": inputs, "binaries": binaries}
        path = self.reports / "collabora-prebuilt-hooks.json"
        path.write_text(json.dumps(manifest, indent=2) + "\n")
        path.chmod(0o600)
        self.hook_environment()

    def prepare(self):
        check(not self.workspace.exists(), "editing workspace already exists; use start/status instead of prepare")
        existing = self.command([self.docker, "ps", "-aq", "--filter", "name=" + self.prefix], "check_fixture_inventory")
        check(not existing.strip(), "editing container prefix is already occupied")
        for path in (self.root_a, self.root_b):
            check(not path.exists(), "editing temporary root already exists")
            path.mkdir(mode=0o750)
        routes = json.loads(self.command(["ip", "-j", "route", "show", "default"], "inspect_private_gateway"))
        gateways = [route.get("gateway") for route in routes if route.get("dev") == self.interface and route.get("gateway")]
        check(len(gateways) == 1, "private interface requires one explicit default gateway")
        for port, kind in ((53, socket.SOCK_STREAM), (53, socket.SOCK_DGRAM), (88, socket.SOCK_STREAM),
                           (389, socket.SOCK_STREAM), (636, socket.SOCK_STREAM), (self.port, socket.SOCK_STREAM),
                           (self.turn_port, socket.SOCK_STREAM), (self.turn_port, socket.SOCK_DGRAM)):
            with socket.socket(socket.AF_INET, kind) as probe:
                probe.bind(("0.0.0.0", port))
        values = {"ENTRY_PORT": self.port, "TURN_PORT": self.turn_port, "DOMAIN": self.domain, "PREFIX": self.prefix,
                  "ENTRY_IP": self.entry, "TEMP_A": self.root_a, "DOCKER_SOCKET": os.environ["ANAS_TEST_DOCKER_SOCKET"],
                  "NETWORK_NAMESPACE": self.netns, "INTERFACE": self.interface, "GATEWAY": gateways[0], "PREFIX_LENGTH": self.prefix_length}
        config = actions.scoped_path(self.root, self.root / "collabora-fixture.yml")
        check(not config.exists(), "refusing to replace an existing editing configuration")
        template = (self.source / "test-env/server-workspace-temp-storage-editing.yml.in").read_text()
        config.write_text(render_fixture(template, values))
        config.chmod(0o600)
        self.cli("init_editing_workspace", "init", self.workspace, "-c", config, "--module-root", self.source, "-y", env=self.hook_environment())

    def http(self, domain, path):
        connection = http.client.HTTPSConnection(self.entry, self.port, context=ssl._create_unverified_context(), timeout=15)
        try:
            connection.request("GET", path, headers={"Host": domain + ":" + str(self.port)})
            response = connection.getresponse()
            return response.status, response.read(4 * 1024 * 1024)
        finally:
            connection.close()

    def start(self):
        check(self.workspace.is_dir(), "prepare must initialize the editing workspace first")
        self.cli("apply_editing_workspace", "apply", "-w", self.workspace, "--root", self.source,
                 "--update-lock", "--no-snapshot", "-y", env=self.hook_environment())
        deadline = time.monotonic() + 1200
        while True:
            try:
                status, body = self.http("nc." + self.domain, "/status.php")
                if status == 200 and json.loads(body).get("installed") is True:
                    break
            except (OSError, ValueError, http.client.HTTPException):
                pass
            check(time.monotonic() < deadline, "Nextcloud did not report real installed readiness")
            time.sleep(5)
        helper = actions.Actions()
        inventory = helper.inventory()
        by_name = {item.get("Name", "").lstrip("/"): item for item in inventory.values()}
        names = set(by_name)
        check(all(self.prefix + name in names for name in MODULES), "requested Module runtime inventory is incomplete")
        check(all(by_name[self.prefix + name].get("State", {}).get("Running") is True for name in MODULES),
              "a requested Module main service is not running")
        check(not any(self.prefix + name in names for name in ("nextcloud_talk", "postgres_adminer")), "disabled optional service was started")
        check(all(self.prefix + name in names for name in ("nextcloud_cron", "nextcloud_push", "nextcloud_imaginary", "nextcloud_redis")),
              "minimal editing stack omitted a required existing Nextcloud service")
        try:
            helper.wait_ready(self.root_a, inventory)
        except Exception:
            if helper.failure is not None:
                self.failure = helper.failure
            raise
        installed = json.loads(self.command([self.docker, "exec", "--user", "www-data", self.prefix + "nextcloud", "php", "occ", "app:list", "--output=json"], "inspect_nextcloud_apps"))
        check(not {"spreed", "memories"}.intersection(installed.get("enabled", {})), "disabled Nextcloud app was enabled")
        self.verify_credential()
        self.status()

    def verify_credential(self):
        credential = json.loads(self.cli("read_managed_break_glass", "admin", "local", "credential", "nextcloud", "break_glass", "-w", self.workspace))
        username, password = credential_account(credential, self.login)
        program = 'require_once "/var/www/html/lib/base.php"; $p=rtrim(stream_get_contents(STDIN), "\\r\\n"); $u=\\OC::$server->get(\\OCP\\IUserManager::class); exit($u->checkPassword($argv[1], $p) ? 0 : 1);'
        self.command([self.docker, "exec", "-i", "--user", "www-data", self.prefix + "nextcloud", "php", "-r", program, username],
                     "verify_managed_break_glass", input=(password + "\n").encode())
        return username, password

    def dependencies(self):
        for path in (self.dependencies_dir, self.root / "collabora-npm-cache"):
            actions.scoped_path(self.root, path)
            path.mkdir(mode=0o700, exist_ok=True)
        check(not (self.dependencies_dir / "@playwright/test/package.json").exists(), "browser dependencies already exist")
        args = [self.docker, "run", "--pull=never", "--rm", "--name", self.prefix + "dependencies", "--network", "host", "--memory", "768m",
                "--pids-limit", "256", "--cap-drop", "ALL", "--cap-add", "DAC_OVERRIDE", "--security-opt", "no-new-privileges",
                "--workdir", "/install", "--mount", f"type=bind,source={self.source}/package.json,target=/install/package.json,readonly",
                "--mount", f"type=bind,source={self.source}/package-lock.json,target=/install/package-lock.json,readonly",
                "--mount", f"type=bind,source={self.dependencies_dir},target=/install/node_modules",
                "--mount", f"type=bind,source={self.root}/collabora-npm-cache,target=/root/.npm",
                "--env", "HTTPS_PROXY", "--env", "HTTP_PROXY", "--env", "NO_PROXY", PLAYWRIGHT_IMAGE,
                "npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund"]
        self.command(args, "install_locked_browser_dependencies")

    def browser_command(self, action_socket, browser_values):
        args = [self.docker, "run", "--pull=never", "--rm", "--name", self.prefix + "browser", "--network", "host", "--memory", "1024m",
                "--pids-limit", "256", "--shm-size", "256m", "--read-only", "--tmpfs", "/tmp:mode=1777,size=512m", "--cap-drop", "ALL",
                "--cap-add", "DAC_OVERRIDE", "--security-opt", "no-new-privileges", "--workdir", self.source,
                "--mount", f"type=bind,source={self.source},target={self.source},readonly",
                # Keep JS modules outside the read-only frozen source mount.
                "--mount", f"type=bind,source={self.dependencies_dir},target=/node_modules,readonly",
                "--mount", f"type=bind,source={action_socket},target={action_socket},readonly",
                "--mount", f"type=bind,source={self.browser_dir},target={self.browser_dir}"]
        for key in browser_values:
            args.extend(["--env", key])
        return [*args, PLAYWRIGHT_IMAGE, "node", "/node_modules/@playwright/test/cli.js", "test", "--config", "test-env/playwright/workspace-temp-storage-collabora.config.mjs"]

    def browser(self):
        check((self.dependencies_dir / "@playwright/test/package.json").is_file(), "prepare locked browser dependencies first")
        check(json.loads((self.dependencies_dir / "@playwright/test/package.json").read_text()).get("version") == "1.62.1", "browser dependency version differs from the fixed image")
        self.browser_dir.mkdir(mode=0o700, exist_ok=True)
        report_file = self.browser_dir / "playwright.json"
        cleanup_file = Path(str(report_file) + ".cleanup.json")
        check(not report_file.exists() and not cleanup_file.exists(), "browser action reports are single-run evidence; use a fresh editing fixture")
        username, password = self.verify_credential()
        env = self.hook_environment()
        server = subprocess.Popen([sys.executable, str(self.source / "test-env/scripts/server-workspace-temp-collabora-action.py"), "--serve"],
                                  env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            action_socket = self.root / "collabora-actions.sock"
            deadline = time.monotonic() + 30
            while not action_socket.exists() and server.poll() is None and time.monotonic() < deadline:
                time.sleep(0.1)
            check(server.poll() is None and action_socket.exists(), "fixed lifecycle socket server did not start")
            browser_values = {"ANAS_TEST_WORK_ROOT": str(self.root), "ANAS_TEST_RUN_ID": self.run_id, "ANAS_TEST_DOMAIN": self.domain,
                              "ANAS_TEST_ENTRY_IP": self.entry, "ANAS_TEST_NEXTCLOUD_URL": self.nc, "ANAS_TEST_COLLABORA_URL": self.collabora,
                              "ANAS_TEST_NEXTCLOUD_LOGIN_URL": self.login, "ANAS_TEST_USERNAME": username, "ANAS_TEST_PASSWORD": password,
                              "ANAS_TEST_REPORT_FILE": str(report_file), "ANAS_TEST_PLAYWRIGHT_OUTPUT": str(self.browser_dir / "output")}
            self.command(self.browser_command(action_socket, browser_values),
                         "execute_real_collabora_document_test", env=dict(os.environ, **browser_values), timeout=4000)
            check(report_file.is_file() and (report_file.stat().st_mode & 0o777) == 0o600, "sanitized browser report is absent or has wrong permissions")
            validate_browser_report(json.loads(report_file.read_text()), username, password)
            self.browser_cleanup = cleanup_materials(cleanup_file, self.run_id)
            check(self.browser_cleanup["status"] == "complete" and self.browser_cleanup["document"] == "removed"
                  and self.browser_cleanup["webdav_context"] == "closed" and not self.browser_cleanup["failures"],
                  "real Collabora test cleanup is incomplete")
            evidence = [json.loads((self.reports / ("collabora-lifecycle-" + action + ".json")).read_text()) for action in actions.ACTIONS]
            validate_action_reports(evidence, self.source_digest, self.binary_digest, self.run_id)
        finally:
            if cleanup_file.is_file() and self.browser_cleanup is None:
                try:
                    self.browser_cleanup = cleanup_materials(cleanup_file, self.run_id)
                except (OSError, ValueError, actions.core.ProbeFailure):
                    self.browser_cleanup = {"status": "unconfirmed"}
            if server.poll() is None:
                server.terminate()
            try:
                server.wait(timeout=30)
                self.server_shutdown = "complete"
            except subprocess.TimeoutExpired:
                self.server_shutdown = "failed"
                raise actions.core.ProbeFailure("lifecycle server stop failed; fixture retained for inspection")

    def status(self):
        check(self.workspace.is_dir(), "editing workspace is not prepared")
        document = json.loads(self.cli("inspect_editing_runtime", "status", "-w", self.workspace))
        self.runtime = {item["module"]: {"runtime": item.get("runtime"), "health": item.get("health"),
                         "containers": item.get("containers"), "temp_storage": (item.get("temp_storage") or {}).get("state")}
                        for item in document.get("module_runtime", []) if item.get("module") in MODULES}

    def stop(self):
        self.cli("normal_stop_editing_workspace", "stop", "-w", self.workspace, env=self.hook_environment())
        check(not actions.Actions().inventory(), "normal stop left owned editing containers")

    def report(self, phase, failed):
        passed = phase == "browser" and not failed
        value = {"schema": "anas.workspace-temp-storage-editing/v1", "case_id": "TEMP-T-021", "run_id": self.run_id,
                 "source_digest": self.source_digest, "binary_digest": self.binary_digest, "phase": phase,
                 "status": "failed" if failed else "passed" if passed else "ready" if phase == "start" else "complete",
                 "modules": list(MODULES), "playwright_image": PLAYWRIGHT_IMAGE, "failure": self.failure,
                 "derived_hook_binaries": self.hook_binaries,
                 "browser_cleanup": self.browser_cleanup,
                 "runtime": self.runtime,
                 "requirements": ["TEMP-R-029", "TEMP-R-030"] if passed else [],
                 "not_run": [] if passed else ["TEMP-R-029", "TEMP-R-030"], "action_server_shutdown": self.server_shutdown,
                 "materials": "workspace, reports and recovery content retained"}
        path = self.reports / ("collabora-stack-" + phase + ".json")
        path.write_text(json.dumps(value, indent=2) + "\n")
        path.chmod(0o600)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("phase", choices=("hooks", "prepare", "dependencies", "start", "browser", "status", "stop"), nargs="?", default="prepare")
    phase = parser.parse_args().phase
    runner, failed = None, False
    try:
        runner = Editing()
        getattr(runner, phase)()
    except Exception as error:
        failed = True
        if runner is not None and runner.failure is None:
            runner.failure = {"reason": type(error).__name__}
        print("Editing fixture operation failed; credentials and raw output suppressed", file=sys.stderr)
    finally:
        if runner is not None:
            runner.report(phase, failed)
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
