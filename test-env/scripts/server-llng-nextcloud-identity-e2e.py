#!/usr/bin/env python3
"""LLNG 2.23.2 anchor subjects and real Nextcloud cookies; isolated Docker only.

Requires the test host's cryptography package for RS256 verification.
The LLNG fixture must register anchorprobe and direct its logout URI to this
host's --callback-ip:18881/logout. Nextcloud must already point to this LLNG.
"""
import argparse
import base64
import html
from html.parser import HTMLParser
import http.cookiejar
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import re
import secrets
import socket
import ssl
import subprocess
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.asymmetric import padding, rsa


def run(*args, check=True, data=None):
    result = subprocess.run(args, input=data, capture_output=True, text=True)
    if check and result.returncode:
        raise RuntimeError(f"fixture command failed: {args[0]}")
    return result.stdout.strip()


class Form(HTMLParser):
    def __init__(self):
        super().__init__()
        self.action, self.values, self.active, self.found = "", [], False, False

    def handle_starttag(self, tag, attrs):
        item = dict(attrs)
        if tag == "form" and not self.found:
            self.action, self.active, self.found = item.get("action", ""), True, True
        elif tag == "input" and self.active and item.get("name") not in (None, "user", "password"):
            self.values.append((item["name"], item.get("value", "")))

    def handle_endtag(self, tag):
        if tag == "form":
            self.active = False


class Callback(Exception):
    def __init__(self, url):
        self.url = url


class StopCallback(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, url):
        if urllib.parse.urlsplit(url).hostname == "callback.nas.test":
            raise Callback(url)
        return super().redirect_request(req, fp, code, msg, headers, url)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--entry-ip", required=True)
    parser.add_argument("--callback-ip", required=True)
    parser.add_argument("--issuer", required=True)
    parser.add_argument("--llng-container")
    parser.add_argument("--case", choices=("all", "missing-anchor"), default="all")
    args = parser.parse_args()
    run("bash", str(Path(__file__).with_name("server-require-isolated-docker.sh")))
    prefix = os.environ["ANAS_TEST_CONTAINER_PREFIX"]
    dc, nc = prefix + "samba_dc", prefix + "nextcloud"
    llng = args.llng_container or prefix + "llng"
    issuer = args.issuer.rstrip("/")
    base = run("docker", "exec", nc, "printenv", "NEXTCLOUD_DOMAIN_FULL").rstrip("/")
    domains = {urllib.parse.urlsplit(url).hostname for url in (base, issuer)}
    original_resolve = socket.getaddrinfo

    def resolve(host, port, *rest, **kw):
        return original_resolve(args.entry_ip if host in domains else host, port, *rest, **kw)

    socket.getaddrinfo = resolve
    tls = ssl._create_unverified_context()  # Test CA only.

    def client():
        return urllib.request.build_opener(urllib.request.ProxyHandler({}), StopCallback(), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()), urllib.request.HTTPSHandler(context=tls))

    def request(c, url, method="GET", data=None, headers=None):
        try:
            response = c.open(urllib.request.Request(url, data=data, method=method, headers=headers or {}), timeout=90)
        except urllib.error.HTTPError as error:
            response = error
        return response.status, response.geturl(), response.read()

    def samba(*words, check=True):
        return run("docker", "exec", dc, "bash", "-c", """
set -e
file=$(mktemp)
trap 'rm -f "$file"' EXIT
chmod 0600 "$file"
printf 'username = %s\npassword = %s\n' "$SAMBA_DC_ADMIN_NAME" "$SAMBA_DC_ADMIN_PASSWORD" > "$file"
samba-tool "$@" -H ldap://127.0.0.1 -A "$file"
""", "llng-anchor-fixture", *words, check=check)

    def anchor(name):
        deadline = time.monotonic() + 120
        while time.monotonic() < deadline:
            info = samba("user", "show", name, "--attributes=anasIdentityAnchor")
            match = re.search(r"^anasIdentityAnchor: (.+)$", info, re.M)
            if match:
                return match.group(1)
            time.sleep(2)
        raise RuntimeError("Samba anchor did not converge")

    password = "Anas-LLNG-" + secrets.token_hex(12) + "!"
    username = "lli" + time.strftime("%H%M%S")
    renamed, peer, missing = "llr" + username[3:], "llp" + username[3:], "llm" + username[3:]
    fixture_uids = []

    def login(c, name, start):
        status, url, body = request(c, start)
        form = Form()
        form.feed(body.decode(errors="replace"))
        if form.found and urllib.parse.urlsplit(url).hostname == urllib.parse.urlsplit(issuer).hostname:
            form.values.extend([("user", name), ("password", password)])
            return request(c, urllib.parse.urljoin(url, form.action), "POST", urllib.parse.urlencode(form.values).encode(), {"Content-Type": "application/x-www-form-urlencoded"})
        return status, url, body

    def nc_login(name, expected, display):
        c = client()
        status, _, body = login(c, name, base + "/apps/user_oidc/login/1")
        text = body.decode(errors="replace")
        uid = re.search(r'data-user=["\']([^"\']+)', text)
        csrf = re.search(r'data-requesttoken=["\']([^"\']+)', text)
        shown = re.search(r'data-user-displayname=["\']([^"\']+)', text)
        assert status == 200 and uid and uid.group(1) == expected and csrf, "Nextcloud anchor login failed"
        assert shown and html.unescape(shown.group(1)) == display, "Nextcloud page exposed a different display name"
        assert authenticated(c) == expected, "OCS cookie UID does not match anchor"
        return c, html.unescape(csrf.group(1))

    def authenticated(c):
        status, _, body = request(c, base + "/ocs/v2.php/cloud/user?format=json", headers={"OCS-APIRequest": "true"})
        try:
            value = json.loads(body)["ocs"]["data"]
            return value.get("id") if status == 200 and isinstance(value, dict) else None
        except (ValueError, KeyError):
            return None

    discovery_status, _, discovery_body = request(client(), issuer + "/.well-known/openid-configuration")
    assert discovery_status == 200, f"LLNG discovery unavailable: HTTP {discovery_status}"
    discovery = json.loads(discovery_body)
    jwks_status, _, jwks_body = request(client(), discovery["jwks_uri"])
    assert jwks_status == 200, f"LLNG JWKS unavailable: HTTP {jwks_status}"
    jwks = json.loads(jwks_body)["keys"]

    def decode(value):
        return base64.urlsafe_b64decode(value + "=" * (-len(value) % 4))

    def verify(token, aud, expected):
        header, payload, signature = token.split(".")
        meta, claims = json.loads(decode(header)), json.loads(decode(payload))
        assert meta["alg"] == "RS256"
        key = next(k for k in jwks if k["kid"] == meta["kid"])
        public = rsa.RSAPublicNumbers(int.from_bytes(decode(key["e"]), "big"), int.from_bytes(decode(key["n"]), "big")).public_key()
        public.verify(decode(signature), (header + "." + payload).encode(), padding.PKCS1v15(), hashes.SHA256())
        assert claims["iss"] == discovery["issuer"] and claims["sub"] == expected
        assert aud in ([claims["aud"]] if isinstance(claims["aud"], str) else claims["aud"])
        assert claims["exp"] > time.time()
        return claims

    callbacks = []

    class Logout(BaseHTTPRequestHandler):
        def do_POST(self):
            body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
            callbacks.append(urllib.parse.parse_qs(body.decode())["logout_token"][0])
            self.send_response(200)
            self.end_headers()

        def log_message(self, *_):
            pass

    server = ThreadingHTTPServer((args.callback_ip, 18881), Logout)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    probe_secret = os.environ["ANAS_TEST_LLNG_PROBE_SECRET"]
    credentials = base64.b64encode(("anchorprobe:" + probe_secret).encode()).decode()

    def token(form):
        status, _, body = request(client(), discovery["token_endpoint"], "POST", urllib.parse.urlencode(form).encode(), {"Authorization": "Basic " + credentials, "Content-Type": "application/x-www-form-urlencoded"})
        assert status == 200, "LLNG token grant failed"
        return json.loads(body)

    def authorize(c, name, expected):
        state, nonce = secrets.token_hex(16), secrets.token_hex(16)
        url = discovery["authorization_endpoint"] + "?" + urllib.parse.urlencode({"client_id": "anchorprobe", "response_type": "code", "redirect_uri": "https://callback.nas.test/cb", "scope": "openid profile email", "state": state, "nonce": nonce})
        try:
            status, final_url, body = login(c, name, url)
        except Callback as callback:
            query = urllib.parse.parse_qs(urllib.parse.urlsplit(callback.url).query)
            assert query.get("state") == [state]
            if expected is None:
                assert "code" not in query and query.get("error") == ["access_denied"], "missing anchor did not return an authorization denial"
                return
            answer = token({"grant_type": "authorization_code", "code": query["code"][0], "redirect_uri": "https://callback.nas.test/cb"})
            claims = verify(answer["id_token"], "anchorprobe", expected)
            assert claims["nonce"] == nonce and claims["preferred_username"] == name
            info_status, _, info = request(c, discovery["userinfo_endpoint"], headers={"Authorization": "Bearer " + answer["access_token"]})
            assert info_status == 200 and json.loads(info)["sub"] == expected
            refreshed = token({"grant_type": "refresh_token", "refresh_token": answer["refresh_token"]})
            assert verify(refreshed["id_token"], "anchorprobe", expected)["sid"] == claims["sid"]
            return claims
        assert expected is None, "LLNG did not return an authorization code"
        assert status in (200, 403), f"missing-anchor check encountered HTTP {status} instead of a policy denial"
        assert urllib.parse.urlsplit(final_url).hostname == urllib.parse.urlsplit(issuer).hostname
        assert re.search(r"denied|forbidden|not authorized|error|alert-danger", body.decode(errors="replace"), re.I), "missing anchor did not produce a denial page"

    def mapping(uid):
        assert re.fullmatch(r"[A-Za-z0-9_-]+", uid)
        result = run("docker", "exec", prefix + "postgres", "psql", "-U", "postgres", "-d", "nextcloud", "-At", "-F", "|", "-c", f"SELECT owncloud_name,directory_uuid FROM oc_ldap_user_mapping WHERE owncloud_name='{uid}'")
        assert result == uid + "|" + uid, "LDAP persisted a different account binding"

    def create(name, surname):
        samba("user", "add", name, password, "--userou=OU=People", "--given-name=LLNG", "--surname=" + surname, "--mail-address=" + name + "@nas.test")
        samba("group", "addmembers", "APP_nextcloud", name)
        uid = anchor(name)
        fixture_uids.append(uid)
        return uid

    try:
        if args.case == "all":
            uid, peer_uid = create(username, "Acceptance"), create(peer, "Peer")
            original, csrf = nc_login(username, uid, "LLNG Acceptance")
            unaffected, _ = nc_login(peer, peer_uid, "LLNG Peer")
            mapping(uid)
            path = base + "/remote.php/dav/files/" + uid + "/llng-anchor-e2e.txt"
            content = secrets.token_hex(24).encode()
            assert request(original, path, "PUT", content, {"requesttoken": csrf})[0] in (201, 204)
            print("llng_nextcloud_login=passed uid_is_anchor=true human_display=true file=created", flush=True)
            protocol = client()
            claims = authorize(protocol, username, uid)
            print("llng_subject=passed signed_id_token=true userinfo=true refresh=true", flush=True)
            request(original, issuer + "/?logout=1")
            request(protocol, issuer + "/?logout=1")
            deadline = time.monotonic() + 90
            while time.monotonic() < deadline and authenticated(original) is not None:
                time.sleep(2)
            assert authenticated(original) is None and authenticated(unaffected) == peer_uid, "LLNG browser logout did not isolate the target user"
            assert callbacks, "LLNG did not send a back-channel logout token"
            logout = verify(callbacks[-1], "anchorprobe", uid)
            assert logout["sid"] == claims["sid"] and "http://schemas.openid.net/event/backchannel-logout" in logout["events"] and "nonce" not in logout
            print("llng_logout=passed signed_anchor_sub=true original_cookie=revoked peer=active", flush=True)
            samba("user", "rename", username, "--samaccountname=" + renamed)
            assert anchor(renamed) == uid
            after, _ = nc_login(renamed, uid, "LLNG Acceptance")
            mapping(uid)
            assert request(after, path)[2] == content, "rename lost the original file"
            authorize(client(), renamed, uid)
            print("llng_rename=passed same_uid=true same_sub=true original_file=preserved", flush=True)
            # sAMAccountName rename alone leaves the original UPN/CN reserved.
            # Release those labels before creating a different directory object.
            samba("user", "rename", renamed, "--upn=" + renamed + "@nas.test", "--force-new-cn=" + renamed)
            replacement = create(username, "Replacement")
            assert replacement != uid
            replacement_client, _ = nc_login(username, replacement, "LLNG Replacement")
            denied_status = request(replacement_client, path)[0]
            assert authenticated(replacement_client) != uid and denied_status in (401, 403, 404), f"recycled label inherited the original identity: HTTP {denied_status}"
            authorize(client(), username, replacement)
            print("llng_label_reuse=passed different_uid=true different_sub=true old_file=denied", flush=True)
        missing_uid = create(missing, "Missing")
        # Simulate a persisted SSO session missing its exported anchor. This is
        # deterministic even while Samba's reconciler fills directory anchors.
        missing_client = client()
        authorize(missing_client, missing, missing_uid)
        session_id = run("docker", "exec", prefix + "postgres", "psql", "-U", "postgres", "-d", os.environ.get("ANAS_TEST_LLNG_DB", "llng_anchor"), "-At", "-c", "SELECT id FROM sessions WHERE a_session->>'_whatToTrace'='" + missing + "'")
        assert re.fullmatch(r"[0-9a-f]{32,64}", session_id), "missing-anchor counterexample must target one SSO session"
        statement = "UPDATE sessions SET a_session=a_session-'anasIdentityAnchor' WHERE id='" + session_id + "'"
        updated = run("docker", "exec", prefix + "postgres", "psql", "-U", "postgres", "-d", os.environ.get("ANAS_TEST_LLNG_DB", "llng_anchor"), "-c", statement)
        assert updated == "UPDATE 1", "missing-anchor counterexample did not alter exactly one SSO session"
        # LLNG materializes sessions from its local cache before the DB. Evict
        # precisely this test SID so the next request reads the altered record.
        eviction = run("docker", "exec", "-u", "www-data", llng, "perl", "-MCache::FileCache", "-MLemonldap::NG::Common::Conf", "-e", r"""
            my $conf = Lemonldap::NG::Common::Conf->new()->getConf();
            die "unexpected cache backend" unless $conf->{localSessionStorage} eq "Cache::FileCache";
            my $cache = Cache::FileCache->new($conf->{localSessionStorageOptions});
            die "missing counterexample cache entry" unless defined $cache->get($ARGV[0]);
            $cache->remove($ARGV[0]);
            die "cache entry survived" if defined $cache->get($ARGV[0]);
            print "session_cache_evict=passed";
        """, session_id)
        assert eviction == "session_cache_evict=passed"
        authorize(missing_client, missing, None)
        print("llng_missing_anchor=passed authorization_code=denied", flush=True)
    finally:
        server.shutdown()
        for name in (username, renamed, peer, missing):
            samba("user", "delete", name, check=False)
        for uid in fixture_uids:
            run("docker", "exec", "-u", "www-data", nc, "php", "occ", "user:delete", uid, check=False)
        labels = ",".join("'" + name + "'" for name in (username, renamed, peer, missing))
        cleanup = f"""DELETE FROM oidcsessions WHERE a_session->>'user_session_id' IN
            (SELECT id FROM sessions WHERE a_session->>'_whatToTrace' IN ({labels}));
            DELETE FROM sessions WHERE a_session->>'_whatToTrace' IN ({labels});
            DELETE FROM psessions WHERE a_session->>'_session_uid' IN ({labels});"""
        run("docker", "exec", prefix + "postgres", "psql", "-U", "postgres", "-d", os.environ.get("ANAS_TEST_LLNG_DB", "llng_anchor"), "-c", cleanup, check=False)


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        # Never expose tokens or callback query parameters through exception text.
        detail = str(error) if isinstance(error, AssertionError) else type(error).__name__
        if isinstance(error, KeyError) and re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]{0,64}", str(error.args[0])):
            detail += " field=" + str(error.args[0])
        raise SystemExit("FAIL: " + detail)
