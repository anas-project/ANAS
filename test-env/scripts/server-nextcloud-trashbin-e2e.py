#!/usr/bin/env python3
# TEST_CASES: NCT-T-002
"""Exercise official Nextcloud trash settings on an already-applied test fixture."""
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import tempfile
from urllib.parse import quote, unquote, urlsplit
import xml.etree.ElementTree as ET


class ProbeFailure(RuntimeError):
    pass


def check(condition, message):
    if not condition:
        raise ProbeFailure(message)


def require_status(status, expected, operation):
    check(status in expected, f"{operation}: HTTP {status}, expected {expected}")


def trash_entries(body, user):
    root = ET.fromstring(body)
    entries = {}
    for response in root.findall("{DAV:}response"):
        href = response.findtext("{DAV:}href", "")
        for propstat in response.findall("{DAV:}propstat"):
            if " 200 " not in propstat.findtext("{DAV:}status", ""):
                continue
            filename = propstat.findtext("{DAV:}prop/{http://nextcloud.org/ns}trashbin-filename")
            if filename:
                path = urlsplit(href).path
                check(unquote(path).startswith(f"/remote.php/dav/trashbin/{user}/trash/"),
                      "trash listing returned a path outside the temporary user")
                entries[filename] = path
    return entries


class Probe:
    def __init__(self):
        self.docker = os.environ.get("DOCKER_CMD", "docker")
        self.container = os.environ["ANAS_TEST_CONTAINER_PREFIX"] + "nextcloud"
        self.workspace = Path(os.environ["ANAS_TEST_WORKSPACE"]).resolve()
        self.user = "anas-trash-e2e-" + secrets.token_hex(8)
        self.password = secrets.token_urlsafe(32)
        self.original = None
        self.user_created = False
        self.events = []

    def command(self, args, data=None):
        result = subprocess.run(args, input=data, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, timeout=180)
        # Do not report command arguments, stderr, config lists or credentials.
        check(result.returncode == 0, "external command failed (details suppressed)")
        return result.stdout

    def occ(self, *args):
        return self.command([self.docker, "exec", "--user", "www-data", self.container,
                             "php", "/var/www/html/occ", *args])

    def prepare(self):
        socket = os.environ["ANAS_TEST_DOCKER_SOCKET"]
        host = os.environ.get("DOCKER_HOST", "")
        check(host == "unix://" + socket and socket.startswith("/") and
              "anas" in socket and any(word in socket for word in ("test", "e2e", "anchor")),
              "an explicit isolated ANAS test Docker socket is required")
        data_root = self.command([self.docker, "info", "--format", "{{.DockerRootDir}}"]).decode().strip()
        check("anas" in data_root and any(word in data_root for word in ("test", "e2e", "anchor")),
              "Docker data root is not test-scoped")
        metadata = json.loads(self.command([self.docker, "inspect", self.container]))[0]
        label = metadata["Config"].get("Labels", {}).get("com.docker.compose.project.working_dir", "")
        check(bool(label) and Path(label).resolve().is_relative_to(self.workspace / ".anas" / "deployments"),
              "Nextcloud container does not belong to the selected workspace deployment")
        check(metadata["State"]["Running"], "Nextcloud is not running")
        env = dict(item.split("=", 1) for item in metadata["Config"]["Env"] if "=" in item)
        check(env.get("NEXTCLOUD_FILES_TRASH_DELETE") == "false", "fixture must use the default deletion setting")
        check(env.get("NEXTCLOUD_TRASHBIN_RETENTION_OBLIGATION") == "60,365", "fixture must use default retention")
        self.url = env["NEXTCLOUD_DOMAIN_FULL"].rstrip("/")
        parts = urlsplit(self.url)
        check(parts.scheme == "https" and parts.hostname and parts.path == "", "unexpected Nextcloud test URL")
        self.resolve = f"{parts.hostname}:{parts.port or 443}:{os.environ['ANAS_TEST_ENTRY_IP']}"
        status = json.loads(self.occ("status", "--output=json"))
        check(status.get("installed") is True and status.get("versionstring") == "34.0.2" and
              status.get("maintenance") is False, "fixture requires installed Nextcloud 34.0.2 outside maintenance")
        config = json.loads(self.occ("config:list", "system"))["system"]
        self.original = {key: config[key] for key in ("files.trash.delete", "trashbin_retention_obligation")}
        check(self.original["files.trash.delete"] is False, "live files.trash.delete must be boolean false")
        check(self.original["trashbin_retention_obligation"] == "60,365", "live retention differs from startup configuration")
        self.events.append("default_effective_config_and_types")

    def create_user(self):
        # Password travels via stdin, never Docker argv or test reports.
        # Also attempt cleanup if user:add created the account before failing.
        self.user_created = True
        self.command([self.docker, "exec", "-i", "--user", "www-data", self.container,
                      "sh", "-c", 'IFS= read -r OC_PASS; export OC_PASS; exec php /var/www/html/occ user:add --password-from-env "$1"',
                      "trashbin-e2e", self.user], (self.password + "\n").encode())
        self.files = f"/remote.php/dav/files/{self.user}/"
        self.trash = f"/remote.php/dav/trashbin/{self.user}/trash"

    def http(self, method, path, body=None, headers=()):
        # curl --config stdin keeps Basic authentication out of process arguments.
        config = f'user = "{self.user}:{self.password}"\n'
        args = ["curl", "--config", "-", "--silent", "--show-error", "--insecure",
                "--connect-timeout", "10", "--max-time", "120", "--resolve", self.resolve,
                "--request", method, "--header", "X-Requested-With: XMLHttpRequest",
                "--write-out", "\n%{http_code}", self.url + path]
        for header in headers:
            args.extend(["--header", header])
        # Only synthetic content is placed in argv; the secret stays on stdin.
        if body is not None:
            args.extend(["--data-binary", body])
        output = self.command(args, config.encode())
        payload, status = output.rsplit(b"\n", 1)
        return int(status), payload

    def list_trash(self):
        body = '<d:propfind xmlns:d="DAV:" xmlns:nc="http://nextcloud.org/ns"><d:prop><nc:trashbin-filename/></d:prop></d:propfind>'
        status, payload = self.http("PROPFIND", self.trash, body, ["Depth: 1", "Content-Type: application/xml"])
        require_status(status, (207,), "list trash")
        return trash_entries(payload, self.user)

    def seed_trash(self, filename):
        path = self.files + quote(filename)
        content = "synthetic trash-bin fixture " + filename
        status, _ = self.http("PUT", path, content)
        require_status(status, (201, 204), "upload fixture")
        status, _ = self.http("GET", path)
        require_status(status, (200,), "authenticate fixture user")
        status, _ = self.http("DELETE", path)
        require_status(status, (204,), "move active file to trash")
        status, _ = self.http("GET", path)
        require_status(status, (404,), "active file must be absent")
        entries = self.list_trash()
        check(filename in entries, "deleted fixture did not enter the trash bin")
        return entries[filename], content

    def set_config(self, key, value):
        kind = "boolean" if isinstance(value, bool) else "string"
        text = str(value).lower() if isinstance(value, bool) else value
        self.occ("config:system:set", key, "--type=" + kind, "--value=" + text)
        config = json.loads(self.occ("config:list", "system"))["system"]
        check(type(config[key]) is type(value) and config[key] == value, f"occ did not apply {key} with the required type")

    def expiration(self):
        # Evaluate synthetic ages with the real container service and live config;
        # never rewrite trash DB timestamps or advance the host clock.
        code = r'''require_once '/var/www/html/lib/base.php';
$e = \OC::$server->get(\OCA\Files_Trashbin\Expiration::class);
$now = time();
echo json_encode(['enabled'=>$e->isEnabled(), 'day59'=>$e->isExpired($now-59*86400,true),
 'day61'=>$e->isExpired($now-61*86400,true), 'day366'=>$e->isExpired($now-366*86400)]);'''
        return json.loads(self.command([self.docker, "exec", "--user", "www-data", self.container, "php", "-r", code]))

    def exercise(self):
        entry, content = self.seed_trash("restore.txt")
        status, _ = self.http("DELETE", entry)
        require_status(status, (403,), "permanent deletion disabled")
        check(self.list_trash().get("restore.txt") == entry, "rejected deletion removed the fixture")
        status, _ = self.http("DELETE", self.trash)
        require_status(status, (403,), "empty trash disabled")
        check(self.list_trash().get("restore.txt") == entry, "rejected empty operation removed the fixture")
        destination = self.url + entry.replace("/trash/", "/restore/", 1)
        status, _ = self.http("MOVE", entry, headers=["Destination: " + destination, "Overwrite: F"])
        require_status(status, (201, 204), "restore fixture")
        status, restored = self.http("GET", self.files + "restore.txt")
        require_status(status, (200,), "read restored file")
        check(restored == content.encode(), "restored content changed")
        check("restore.txt" not in self.list_trash(), "restore left the original trash item behind")
        self.events.append("delete_and_empty_rejected_restore_content_matches")
        check(self.expiration() == {"enabled": True, "day59": False, "day61": True, "day366": True},
              "live 60,365 expiration boundaries differ")
        self.set_config("trashbin_retention_obligation", "disabled")
        check(self.expiration() == {"enabled": False, "day59": False, "day61": False, "day366": False},
              "disabled retention still expires synthetic old entries")
        # Changing retention must not re-enable manual deletion.
        entry, _ = self.seed_trash("disabled-retention.txt")
        status, _ = self.http("DELETE", entry)
        require_status(status, (403,), "disabled retention preserves manual-deletion protection")
        check("disabled-retention.txt" in self.list_trash(), "disabled-retention fixture disappeared")
        self.events.append("live_expiration_boundaries_and_disabled_retention")
        self.set_config("files.trash.delete", True)
        status, _ = self.http("DELETE", entry)
        require_status(status, (204,), "permanent deletion enabled")
        check("disabled-retention.txt" not in self.list_trash(), "enabled deletion did not remove the fixture")
        self.seed_trash("empty-a.txt")
        self.seed_trash("empty-b.txt")
        status, _ = self.http("DELETE", self.trash)
        require_status(status, (204,), "empty trash enabled")
        check(not self.list_trash(), "enabled empty operation left trash items")
        self.events.append("enabled_manual_delete_and_empty_succeed")

    def cleanup(self):
        errors = []
        if self.original is not None:
            for key, value in self.original.items():
                try:
                    self.set_config(key, value)
                except Exception:
                    errors.append("restore " + key)
        if self.user_created:
            try:
                self.occ("user:delete", self.user)
                code = r'''require_once '/var/www/html/lib/base.php';
echo json_encode(\OC::$server->get(\OCP\IUserManager::class)->userExists($argv[1]));'''
                remaining = json.loads(self.command([self.docker, "exec", "--user", "www-data", self.container,
                                                       "php", "-r", code, self.user]))
                check(remaining is False, "temporary user still exists after cleanup")
            except Exception:
                errors.append("delete temporary user")
        check(not errors, "cleanup failed: " + ", ".join(errors))
        self.events.append("original_configuration_restored_temporary_user_deleted")

    def run(self):
        self.prepare()
        try:
            self.create_user()
            self.exercise()
        finally:
            self.cleanup()


def write_report(report_dir, result):
    report_dir.mkdir(parents=True, exist_ok=True)
    descriptor, report = tempfile.mkstemp(prefix="nextcloud-trashbin-", suffix=".json", dir=report_dir)
    with os.fdopen(descriptor, "w") as stream:
        json.dump(result, stream, ensure_ascii=False, indent=2)
        stream.write("\n")
    return report


def main():
    probe = Probe()
    result = {"test_case": "NCT-T-002", "passed": False, "checks": probe.events}
    try:
        probe.run()
        result["passed"] = True
    except Exception as error:
        # Generic errors deliberately avoid raw curl/Docker diagnostics.
        result["failure"] = str(error) if isinstance(error, ProbeFailure) else type(error).__name__
    report_dir = Path(__file__).resolve().parents[1] / "reports"
    report = write_report(report_dir, result)
    print(("PASS" if result["passed"] else "FAIL") + ": Nextcloud trash-bin E2E; report=" + report)
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
