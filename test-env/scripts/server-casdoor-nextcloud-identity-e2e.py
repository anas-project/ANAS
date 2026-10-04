#!/usr/bin/env python3
"""Real Nextcloud OIDC cookies, LDAP identity and file ownership; isolated Docker only."""
import argparse
import html
import http.cookiejar
import json
import os
from pathlib import Path
import re
import secrets
import socket
import ssl
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request


def run(*args, check=True):
    result = subprocess.run(args, capture_output=True, text=True)
    if check and result.returncode:
        raise RuntimeError(f"fixture command failed: {args[0]}")
    return result.stdout.strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--entry-ip", required=True)
    args = parser.parse_args()
    run("bash", str(Path(__file__).with_name("server-require-isolated-docker.sh")))
    prefix = os.environ["ANAS_TEST_CONTAINER_PREFIX"]
    nc, cd, dc = (prefix + name for name in ("nextcloud", "casdoor", "samba_dc"))
    env = lambda container, key: run("docker", "exec", container, "printenv", key)
    base = env(nc, "NEXTCLOUD_DOMAIN_FULL").rstrip("/")
    issuer = env(nc, "NEXTCLOUD_OIDC_ISSUER_URL").rstrip("/")
    client_id = env(nc, "NEXTCLOUD_OIDC_CLIENT_ID")
    domain = urllib.parse.urlsplit(base).hostname
    issuer_domain = urllib.parse.urlsplit(issuer).hostname
    original_resolve = socket.getaddrinfo

    def resolve(host, port, *rest, **kw):
        if host in (domain, issuer_domain):
            host = args.entry_ip
        return original_resolve(host, port, *rest, **kw)

    socket.getaddrinfo = resolve
    tls = ssl._create_unverified_context()  # Independent test CA, never the business deployment.
    username = "nci" + time.strftime("%H%M%S")
    renamed = "ncr" + username[3:]
    password = "Anas-Native-" + secrets.token_hex(12) + "!"
    asset = "casdoor-identity-e2e-" + username + ".txt"
    content = secrets.token_hex(24).encode()

    def samba(*words, check=True):
        return run("docker", "exec", dc, "bash", "-c", """
            set -e
            auth=$(mktemp)
            trap 'rm -f "$auth"' EXIT
            chmod 0600 "$auth"
            printf 'username = %s\npassword = %s\n' "$SAMBA_DC_ADMIN_NAME" "$SAMBA_DC_ADMIN_PASSWORD" > "$auth"
            samba-tool "$@" -H ldap://127.0.0.1 -A "$auth"
            """, "native-nextcloud-fixture", *words, check=check)

    def profile(name):
        value = run("docker", "exec", cd, "/opt/anas/bin/casdoor-helper", "directory-watch", "--get-user", "anas/" + name, check=False)
        return json.loads(value or "null")

    def wait_profile(name):
        deadline = time.monotonic() + 420
        while time.monotonic() < deadline:
            value = profile(name)
            if value and value.get("externalId") and not value.get("isForbidden") and "anas/APP_nextcloud" in value.get("groups", []):
                return value
            time.sleep(2)
        raise RuntimeError("directory profile did not converge")

    def new_client():
        return urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()), urllib.request.HTTPSHandler(context=tls))

    def request(client, url, method="GET", data=None, headers=None):
        req = urllib.request.Request(url, data=data, method=method, headers=headers or {})
        try:
            response = client.open(req, timeout=45)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            return response.status, response.geturl(), response.read()

    def login(name):
        client = new_client()
        status, authorization, _ = request(client, base + "/index.php/apps/user_oidc/login/1")
        if urllib.parse.urlsplit(authorization).hostname != issuer_domain:
            raise RuntimeError(f"Nextcloud did not initiate OIDC: HTTP {status}")
        params = urllib.parse.parse_qs(urllib.parse.urlsplit(authorization).query)

        def parameter(snake, camel=None):
            return params.get(snake, params.get(camel or snake, [""]))[0]

        callback = parameter("redirect_uri", "redirectUri")
        state = parameter("state")
        assert parameter("client_id", "clientId") == client_id
        query = urllib.parse.urlencode({"clientId": client_id, "responseType": "code", "redirectUri": callback, "state": state, "nonce": parameter("nonce"), "scope": parameter("scope"), "challenge": parameter("code_challenge", "codeChallenge")})
        payload = json.dumps({"application": "app-anas-nextcloud", "organization": "anas", "username": name, "password": password, "type": "code", "signinMethod": "Password", "autoSignin": False}).encode()
        status, _, body = request(client, issuer + "/api/login?" + query, "POST", payload, {"Content-Type": "application/json"})
        answer = json.loads(body)
        if status != 200 or answer.get("status") != "ok" or not answer.get("data"):
            raise RuntimeError("Casdoor refused the real Nextcloud authorization")
        callback += "?" + urllib.parse.urlencode({"code": answer["data"], "state": state})
        status, _, body = request(client, callback)
        text = body.decode(errors="replace")
        token = re.search(r'data-requesttoken=["\']([^"\']+)', text)
        if not token:
            raise RuntimeError(f"Nextcloud callback did not establish a page session: HTTP {status}")
        request_token = html.unescape(token.group(1))
        status, _, body = request(client, base + "/ocs/v2.php/cloud/user?format=json", headers={"OCS-APIRequest": "true"})
        value = json.loads(body).get("ocs", {}).get("data", {})
        uid = value.get("id") if isinstance(value, dict) else None
        if status != 200 or not uid:
            raise RuntimeError("original application cookies did not authenticate OCS")
        return client, uid, request_token

    def mapping(uid, anchor):
        assert re.fullmatch(r"nc[ir][0-9]{6}", uid)
        statement = f"SELECT owncloud_name, directory_uuid FROM oc_ldap_user_mapping WHERE owncloud_name='{uid}'"
        result = run("docker", "exec", prefix + "postgres", "psql", "-U", "postgres", "-d", "nextcloud", "-At", "-F", "|", "-c", statement)
        assert result == uid + "|" + anchor, "Nextcloud persisted a different identity binding"

    try:
        samba("user", "add", username, password, "--userou=OU=People", "--mail-address=" + username + "@" + domain.removeprefix("nc."))
        samba("group", "addmembers", "APP_nextcloud", username)
        first = wait_profile(username)
        client, uid, csrf = login(username)
        assert uid == username and uid != first["externalId"]
        mapping(uid, first["externalId"])
        path = base + "/remote.php/dav/files/" + urllib.parse.quote(uid) + "/" + asset
        status, _, _ = request(client, path, "PUT", content, {"requesttoken": csrf})
        assert status in (201, 204), f"real file creation failed: HTTP {status}"
        print("nextcloud_initial_login=passed ldap_anchor=verified uid_is_label=true file=created", flush=True)
        samba("user", "rename", username, "--samaccountname=" + renamed)
        current = wait_profile(renamed)
        assert current["id"] == first["id"] and current["externalId"] == first["externalId"]
        client_after, uid_after, csrf_after = login(renamed)
        assert uid_after == uid, "rename created a second Nextcloud account"
        mapping(uid_after, first["externalId"])
        status, _, body = request(client_after, path)
        assert status == 200 and body == content, "rename lost access to original file"
        status, _, _ = request(client_after, path, "DELETE", headers={"requesttoken": csrf_after})
        assert status == 204
        print("nextcloud_rename=passed same_ldap_account=true original_file=preserved", flush=True)
    finally:
        samba("user", "delete", username, check=False)
        samba("user", "delete", renamed, check=False)
        # This fixture owns the entire account and its single test file.
        run("docker", "exec", nc, "php", "occ", "user:delete", username, check=False)
        run("docker", "exec", nc, "php", "occ", "user:delete", renamed, check=False)


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        raise SystemExit("FAIL: " + str(error))
