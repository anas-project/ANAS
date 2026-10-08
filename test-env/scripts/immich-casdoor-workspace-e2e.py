#!/usr/bin/env python3
"""Fixed Casdoor/Immich acceptance, using the existing isolated workspace harness."""
import argparse
import base64
import concurrent.futures
import hashlib
import importlib.util
import json
import os
import pathlib
import re
import secrets
import struct
import sys
import time
import threading
import urllib.parse
import uuid
import zlib

spec = importlib.util.spec_from_file_location("immich_workspace", pathlib.Path(__file__).with_name("immich-workspace-e2e.py"))
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)
base.FIXED_MODULES = {"postgres": ("18.4.0", 4), "immich": ("3.2.4", 1), "casdoor": ("3.143.0", 11)}


class CasdoorSuite(base.Suite):
    def __init__(self, *args):
        super().__init__(*args)
        self.iam = "casdoor"
        self.report["environment"] = "real ANAS workspace, Samba AD and fixed Casdoor/Immich on isolated Linux/Btrfs"

    def directory_sync(self):
        # Use the actual watcher; no manual API synchronization hides delivery failures.
        return None

    def directory_ready(self, name, anchor, groups):
        row = self.sql("casdoor", 'SELECT external_id||\':\'||is_forbidden::int||\':\'||is_deleted::int FROM "user" WHERE owner=\'anas\' AND name=\'' + name + "';")
        if row != anchor + ":0:0":
            return False
        actual = json.loads(self.sql("casdoor", 'SELECT groups FROM "user" WHERE owner=\'anas\' AND name=\'' + name + "';"))
        return isinstance(actual, list) and all("anas/" + group in actual for group in groups)

    def login(self, username, password, *, cookie=None, allow_denied=False):
        verifier, state = secrets.token_urlsafe(32), secrets.token_urlsafe(24)
        challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).decode().rstrip("=")
        status, body, _ = self.http(self.app + "/api/oauth/authorize", {"redirectUri": self.app + "/auth/login", "state": state, "codeChallenge": challenge})
        assert status in (200, 201), "Immich native authorize failed"
        query = urllib.parse.parse_qs(urllib.parse.urlsplit(body["url"]).query)
        parameters = {key: values[0] for key, values in query.items()}
        for old, new in (("client_id", "clientId"), ("response_type", "responseType"), ("redirect_uri", "redirectUri")):
            if old in parameters:
                parameters[new] = parameters.pop(old)
        own_cookie = cookie is None
        cookie = cookie or self.reports / ("cookie-" + uuid.uuid4().hex)
        try:
            status, reply, _ = self.http(self.idp + "/api/login?" + urllib.parse.urlencode(parameters),
                                        {"application": "app-anas-immich", "organization": "anas", "username": username,
                                         "password": password, "autoSignin": False, "type": "code", "signinMethod": "Password"}, cookie=cookie)
            if allow_denied and (status != 200 or reply.get("status") != "ok" or not reply.get("data")):
                return 403, {"directory_outcome": "native-casdoor-denied"}
            valid = status == 200 and reply.get("status") == "ok" and isinstance(reply.get("data"), str) and bool(reply["data"])
            if not valid:
                message = str(reply.get("msg", "")).replace(username, "[fixture-user]").replace(password, "[redacted]")
                if re.search("client_secret|authorization|bearer|password=|token=", message, re.I):
                    message = "filtered authentication error"
                self.report["native_login_failure"] = {"http_status": status, "native_status": reply.get("status"), "data_type": type(reply.get("data")).__name__, "consent_required": isinstance(reply.get("data"), dict) and reply["data"].get("required") is True, "message": message[:400]}
            assert valid, "native Casdoor authorization failed"
            callback = self.app + "/auth/login?" + urllib.parse.urlencode({"code": reply["data"], "state": state})
            return self.http(self.app + "/api/oauth/callback", {"url": callback, "state": state, "codeVerifier": verifier}, cookie=cookie)[:2]
        finally:
            if own_cookie:
                cookie.unlink(missing_ok=True)

    def create_user(self, name, password, *, admin=False, email=None):
        self.samba("user", "add", name, password, "--userou=OU=People", "--mail-address=" + (email or name + "@" + self.domain))
        self.samba("group", "addmembers", "APP_immich", name)
        if admin:
            self.samba("group", "addmembers", "Admins", name)
        anchors = []
        def ready():
            found = re.search(r"^anasIdentityAnchor: ([a-f0-9-]+)$", self.samba("user", "show", name, "--attributes=anasIdentityAnchor"), re.M)
            if not found:
                return False
            anchor = str(uuid.UUID(found[1]))
            if not self.directory_ready(name, anchor, ["APP_immich", *( ["Admins"] if admin else [])]):
                return False
            anchors[:] = [anchor]
            return True
        self.wait(ready)
        return anchors[0]

    def verify_directory_role_write_guard(self, state):
        cookie = self.reports / ("cookie-role-guard-" + uuid.uuid4().hex)
        name = state["user"]["username"]
        try:
            status, _ = self.login(name, state["password"], cookie=cookie)
            assert status in (200, 201)
            query = urllib.parse.urlencode({"id": "anas/" + name})
            status, profile, _ = self.http(self.idp + "/api/get-user?" + query, cookie=cookie)
            assert status == 200 and profile.get("status") == "ok" and profile["data"]["name"] == name, "native ordinary profile session is not authenticated"
            endpoint = self.idp + "/api/update-user?" + query
            status, reply, _ = self.http(endpoint + "&columns=displayName", {"displayName": profile["data"]["displayName"]}, cookie=cookie)
            assert status == 200 and reply.get("status") == "ok", "native ordinary profile update baseline failed"
            before = self.sql("casdoor", "SELECT groups||':'||external_id||':'||is_admin::int FROM \"user\" WHERE owner='anas' AND name='" + name + "';")
            for field, value in (("groups", ["anas/APP_immich", "anas/Admins"]), ("isAdmin", True), ("externalId", str(uuid.uuid4()))):
                status, reply, _ = self.http(endpoint + "&columns=" + field, {field: value}, cookie=cookie)
                assert status == 200 and reply.get("status") == "error" and "managed directory identity and privileges" in reply.get("msg", ""), "ordinary profile did not enforce the server guard"
                after = self.sql("casdoor", "SELECT groups||':'||external_id||':'||is_admin::int FROM \"user\" WHERE owner='anas' AND name='" + name + "';")
                assert after == before
            self.mark("native Casdoor ordinary profile rejects directory group, admin and anchor changes")
        finally:
            cookie.unlink(missing_ok=True)

    def identity_cases(self, state):
        self.phase = "identity-cases"
        names = {case: "ic" + case[:2] + secrets.token_hex(4) for case in ("email", "conflict", "parallel", "deleted")}
        anchors = {case: self.create_user(name, state["password"]) for case, name in names.items()}
        status, owner = self.login(names["email"], state["password"])
        assert status in (200, 201)
        self.samba("user", "rename", names["email"], "--mail-address=changed-" + names["email"] + "@" + self.domain)
        self.wait(lambda: self.sql("casdoor", 'SELECT email FROM "user" WHERE owner=\'anas\' AND name=\'' + names["email"] + "';") == "changed-" + names["email"] + "@" + self.domain)
        status, updated = self.login(names["email"], state["password"])
        assert status in (200, 201) and updated["userId"] == owner["userId"]
        self.samba("user", "rename", names["conflict"], "--mail-address=" + names["email"] + "@" + self.domain)
        self.wait(lambda: self.sql("casdoor", 'SELECT email FROM "user" WHERE owner=\'anas\' AND name=\'' + names["conflict"] + "';") == names["email"] + "@" + self.domain)
        assert self.login(names["conflict"], state["password"])[0] == 400
        barrier = threading.Barrier(4, timeout=180)
        native_http = self.http
        def simultaneous_callback(url, *args, **kwargs):
            if url == self.app + "/api/oauth/callback":
                barrier.wait()
            return native_http(url, *args, **kwargs)
        self.http = simultaneous_callback
        try:
            with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
                replies = list(pool.map(lambda _: self.login(names["parallel"], state["password"]), range(4)))
        finally:
            self.http = native_http
        successful = [account for status, account in replies if status in (200, 201)]
        rows = self.sql("immich", 'SELECT id FROM "user" WHERE "oauthId"=\'' + anchors["parallel"] + '\' AND "deletedAt" IS NULL;').splitlines()
        evidence = {"callback_statuses": [status for status, _ in replies], "account_rows": len(rows), "independent_grants_waited_before_callback": True}
        base.write_json(self.reports / "concurrent-callbacks.json", evidence)
        assert successful and len(rows) == 1
        assert all(account["userId"] == rows[0] and not account["isAdmin"] for account in successful)
        assert all(status in (200, 201, 400, 500) for status, _ in replies)
        assert all(status in (200, 201) or "accessToken" not in account for status, account in replies)
        self.report["concurrent_callbacks"] = evidence
        status, deleted = self.login(names["deleted"], state["password"])
        assert status in (200, 201)
        status, admin = self.login(state["admin"]["username"], state["password"])
        assert status in (200, 201)
        assert self.http(self.app + "/api/admin/users/" + deleted["userId"], {"force": False}, token=admin["accessToken"], method="DELETE")[0] in (200, 204)
        self.verify_deleted_binding(names["deleted"], state["password"], anchors["deleted"], deleted)
        self.mark("real Casdoor email continuity/conflict, concurrent JIT and soft-deleted binding guard")

    def verify_deleted_binding(self, name, password, anchor, account):
        statement = 'SELECT id||\':\'||"deletedAt"::text FROM "user" WHERE "oauthId"=\'' + anchor + "';"
        before = self.sql("immich", statement)
        assert before.startswith(account["userId"] + ":") and len(before.splitlines()) == 1
        email = "after-delete-" + name + "@" + self.domain
        self.samba("user", "rename", name, "--mail-address=" + email)
        self.wait(lambda: self.sql("casdoor", 'SELECT email FROM "user" WHERE owner=\'anas\' AND name=\'' + name + "';") == email)
        status, denied = self.login(name, password)
        evidence = {"callback_status": status, "retained_deleted_binding": self.sql("immich", statement) == before}
        base.write_json(self.reports / "soft-delete-callback.json", evidence)
        assert 400 <= status < 600 and "accessToken" not in denied and evidence["retained_deleted_binding"]
        if "accessToken" in account:
            assert self.http(self.app + "/api/users/me", token=account["accessToken"])[0] == 401
        self.report["soft_delete"] = evidence
        self.mark("native soft-deleted anchor rejects fresh-email registration and retains its tombstone")

    def directory_cases(self, state):
        evidence = {}
        for case in ("disable", "remove-app-group", "delete", "logout-first", "admin-role-loss"):
            self.phase = "casdoor-directory-" + case
            name = "icrev" + secrets.token_hex(4)
            anchor = self.create_user(name, state["password"], admin=case == "admin-role-loss")
            cookie = self.reports / ("cookie-" + uuid.uuid4().hex)
            status, account = self.login(name, state["password"], cookie=cookie)
            assert status in (200, 201)
            token = account["accessToken"]
            status, key, _ = self.http(self.app + "/api/api-keys", {"name": "casdoor-revoke", "permissions": ["all"]}, token=token)
            assert status in (200, 201)
            # Create real, owner-specific media before its real share.
            photo = self.reports.joinpath("seed.png").read_bytes()
            asset = self.upload(token, name + ".png", photo, "image/png")
            status, share, _ = self.http(self.app + "/api/shared-links", {"type": "INDIVIDUAL", "assetIds": [asset]}, token=token)
            assert status in (200, 201)
            def credentials():
                observed = self.credential_status({"token":token,"api_key":key["secret"],"share_path":"/api/shared-links/me?key="+urllib.parse.quote(share["key"])})
                return [observed[name] for name in ("bearer","cookie","api_key","share")]
            assert credentials() == [200] * 4
            if case == "logout-first":
                assert self.http(self.idp + "/api/logout?client_id=immich", cookie=cookie)[0] == 200
                self.wait(lambda: credentials()[:2] == [401, 401], timeout=90)
                assert credentials()[2:] == [200, 200]
            started = time.monotonic()
            if case == "disable":
                self.samba("user", "disable", name)
            elif case == "delete":
                self.samba("user", "delete", name)
            elif case == "admin-role-loss":
                self.samba("group", "removemembers", "Admins", name)
            else:
                self.samba("group", "removemembers", "APP_immich", name)
            self.wait(lambda: credentials() == [401] * 4, timeout=300)
            assert self.sql("immich", 'SELECT "oauthId" FROM "user" WHERE id=\'' + account["userId"] + "';") == anchor
            assert self.sql("immich", 'SELECT count(*) FROM asset WHERE id=\'' + asset + "';") == "1"
            if case == "admin-role-loss":
                self.wait_later_oidc_issuance(self.receiver_signed_cutoff(anchor)["epoch"])
                status, fresh = self.login(name, state["password"])
                assert status in (200, 201) and fresh["userId"] == account["userId"] and not fresh["isAdmin"]
                assert self.http(self.app + "/api/users/me", token=fresh["accessToken"])[0] == 200
            else:
                assert self.login(name, state["password"], allow_denied=True)[0] == 403
            status, unaffected = self.login(state["user"]["username"], state["password"])
            assert status in (200, 201) and self.http(self.app + "/api/users/me", token=unaffected["accessToken"])[0] == 200
            evidence[case] = {"status": "passed", "seconds": round(time.monotonic() - started, 3), "old_credentials": [401] * 4, "binding_and_media_preserved": True}
            base.write_json(self.reports / "directory-cases.json", evidence)
        self.report["casdoor_directory"] = evidence
        self.mark("automatic Samba/Casdoor policy delivery revokes old sessions/API keys/shares in five cases")

    def verify_post_restore_lifecycle(self, state, old_point, old_images, failed_modules, backup_id, expected_images, photo):
        """Reuse recovery checks, then exercise the actual Casdoor watcher."""
        self.extension_failure_restore(state, old_point, old_images, failed_modules, backup_id, expected_images)
        self.extension_crash_retry(state, old_point, old_images, backup_id, expected_images)
        # The base suite's directory probes execute Authentik's native shell.
        # Casdoor instead repeats its five automatic directory delivery cases
        # against the restored workspace; no fabricated policy token or sync.
        self.directory_cases(state)
        self.report["status"] = "passed"

    def run(self):
        self.reports.mkdir(mode=0o700)
        self.backups.mkdir(mode=0o700)
        old_modules = self.module_fixture("pgvector-0.8.1-test-only")
        failed_modules = self.module_fixture("pgvector-0.8.2-failure-test-only", fail_after_maintenance=True)
        self.report["binary_sha256"] = {"anas": base.digest(self.args.anas), "anas-helper": base.digest(pathlib.Path(self.args.anas).parent / "anas-helper")}
        config = {"modules": {"samba_dc": {"config": {"domain": "ad." + self.domain, "application_dns_mode": "separate_zone", "anchor_scan_interval": "30"}},
                              "postgres": {}, "casdoor": {}, "immich": {"config": {"machine_learning": False}}, "traefik": {"config": {"base_port": self.args.port}}},
                  "identity": {"iam": {"provider": "casdoor"}}, "rollback": {"snapshot": {"backend": "btrfs"}},
                  "global": {"base_domain": self.domain, "email": "admin@" + self.domain, "virtual_domain": True, "timezone": "Etc/UTC", "container_prefix": self.prefix, "network_prefix": self.prefix,
                             "host_ip": self.args.host_ip, "host_lan_ip": self.args.host_lan_ip, "host_lan_bridge_ip": self.args.host_lan_bridge_ip,
                             "chinese_speedup": False, "chinese_build_speedup": False}, "env": {"ANAS_IMAGE_REGISTRY": self.args.image_registry}}
        path = self.reports / "input-config.json"
        base.write_json(path, config)
        self.phase = "new-install"
        self.anas("new-init", "init", self.workspace, "-c", path, "--module-root", old_modules, "-y")
        self.initialized = True
        self.anas("new-apply", "apply", "-w", self.workspace, "--module-root", old_modules, "--update-lock", "-y")
        self.run_after_install(old_modules, failed_modules)

    def run_after_install(self, old_modules, failed_modules):
        self.load_envs()
        self.wait(lambda: self.http(self.app + "/api/server/ping")[0] == 200)
        self.phase = "native-first-users"
        password = "Anas!" + secrets.token_hex(16)
        names = {role: "ic" + role[0] + secrets.token_hex(4) for role in ("admin", "user")}
        anchors = {role: self.create_user(name, password, admin=role == "admin") for role, name in names.items()}
        assert self.sql("immich", 'SELECT count(*) FROM "user";') == "0"
        assert self.login(names["user"], password)[0] == 400 and self.sql("immich", 'SELECT count(*) FROM "user";') == "0"
        state = {"password": password}
        for role in ("admin", "user"):
            status, account = self.login(names[role], password)
            assert status in (200, 201) and account["isAdmin"] == (role == "admin")
            state[role] = {"username": names[role], "anchor": anchors[role], "user_id": account["userId"]}
            if role == "user":
                token = account["accessToken"]
        self.mark("native Casdoor OIDC first admin/ordinary JIT with anchor=sub=oauthId")
        base.write_json(self.reports / "private-login-state.json", state)
        self.verify_directory_role_write_guard(state)
        self.verify_entry_restrictions(state)
        self.identity_cases(state)
        self.verify_application_lifecycle(state, old_modules, failed_modules)

    def verify_application_lifecycle(self, state, old_modules, failed_modules):
        self.phase = "media-upload"
        status, account = self.login(state["user"]["username"], state["password"])
        assert status in (200, 201)
        token = account["accessToken"]
        chunk = lambda kind, body: struct.pack("!I", len(body)) + kind + body + struct.pack("!I", zlib.crc32(kind + body) & 0xffffffff)
        photo = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack("!2I5B", 16, 16, 8, 2, 0, 0, 0)) + chunk(b"IDAT", zlib.compress((b"\0" + bytes([32, 96, 160]) * 16) * 16)) + chunk(b"IEND", b"")
        (self.reports / "seed.png").write_bytes(photo)
        photo_id = self.upload(token, "seed.png", photo, "image/png")
        app_image = self.runtime()[("immich", "anas_immich")]["image"]
        self.docker("run", "--rm", "--network", "none", "--label", "anas.test.immich-workspace=" + self.prefix, "--entrypoint", "ffmpeg", "--volume", str(self.reports) + ":/fixture", app_image,
                    "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=blue:s=64x64:r=10", "-t", "1", "-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", "/fixture/seed.mp4")
        video = (self.reports / "seed.mp4").read_bytes()
        video_id = self.upload(token, "video.mp4", video, "video/mp4")
        state["assets"] = {photo_id: hashlib.sha256(photo).hexdigest(), video_id: hashlib.sha256(video).hexdigest()}
        state["vector_media_hash"] = hashlib.sha256(photo).hexdigest()
        self.sql("immich", "CREATE TABLE anas_workspace_vectors(id integer PRIMARY KEY,embedding vector(3),media_hash text); INSERT INTO anas_workspace_vectors VALUES(1,'[1,2,3]', '" + state["vector_media_hash"] + "'); CREATE INDEX anas_workspace_vectors_hnsw ON anas_workspace_vectors USING hnsw(embedding vector_l2_ops);")
        self.wait(lambda: all(self.http(self.app + "/api/assets/" + asset + "/thumbnail?size=thumbnail", token=token, raw=True)[0] == 200 for asset in state["assets"]))
        status, album, _ = self.http(self.app + "/api/albums", {"albumName": "ANAS recovery album", "assetIds": list(state["assets"])}, token=token)
        assert status in (200, 201)
        state["album_id"] = album["id"]
        self.sql("casdoor", "CREATE TABLE anas_workspace_recovery(id integer PRIMARY KEY,value text NOT NULL); INSERT INTO anas_workspace_recovery VALUES(1,'before-backup');")
        assert self.docker("exec", self.prefix + "immich_valkey", "valkey-cli", "SET", "anas:workspace:e2e", "before-backup").decode().strip() == "OK"
        base.write_json(self.reports / "private-state.json", state)
        self.verify_state(state)
        self.verify_seed_lifecycle(state, old_modules)
        self.directory_cases(state)
        self.cache_declared_images()
        self.verify_recovery_lifecycle(state, old_modules, failed_modules, photo)
        self.report["status"] = "passed"


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("anas", "modules", "workspace", "host-ip", "host-lan-ip", "host-lan-bridge-ip"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--port", required=True, type=int)
    parser.add_argument("--image-registry", default="ghcr.io/anas-project")
    parser.add_argument("--keep-workspace", action="store_true")
    parser.add_argument("--preflight-only", action="store_true")
    args = parser.parse_args()
    workspace, modules = base.preflight(args)
    if args.preflight_only:
        print(json.dumps({"status": "preflight_passed", "workspace_mutated": False}))
        return 0
    suite = CasdoorSuite(args, workspace, modules)
    result = 0
    try:
        suite.run()
    except BaseException as error:
        result = 1
        suite.report.update(status="failed", failed_phase=suite.phase, error_type=type(error).__name__, failure_origin=base.failure_origin(error))
    finally:
        try:
            suite.cleanup()
        except BaseException:
            result = 1
            suite.report["cleanup"] = {"error": "scoped cleanup failed; workspace retained"}
        if result:
            suite.report["status"] = "failed"
        if suite.reports.exists():
            base.write_json(suite.reports / "report.json", suite.report)
    print(json.dumps({"status": suite.report["status"], "report": str(suite.reports / "report.json")}))
    return result


if __name__ == "__main__":
    sys.exit(main())
