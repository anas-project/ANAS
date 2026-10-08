#!/usr/bin/env python3
"""Run fixed upstream subject/signing/notification methods with ORM doubles.

This verifies actual 2026.5.6 method bodies and PyJWT signatures at image build;
it does not replace real LDAP, Authentik HTTP, or Consumer acceptance.
"""

import ast
from dataclasses import asdict, dataclass, field
from datetime import datetime, timedelta, timezone as datetime_timezone
from hashlib import sha256
import json
from pathlib import Path
import sys
from types import ModuleType, SimpleNamespace
from typing import Any
from uuid import uuid4

import jwt


def find_node(root, relative, kind, name):
    parsed = ast.parse((root / relative).read_text())
    matches = [node for node in ast.walk(parsed) if isinstance(node, kind) and node.name == name]
    if len(matches) != 1:
        raise RuntimeError(f"fixed upstream method drift: {relative}:{name}")
    return matches[0]


def compile_nodes(nodes, namespace):
    future = ast.ImportFrom(module="__future__", names=[ast.alias(name="annotations")], level=0)
    tree = ast.fix_missing_locations(ast.Module(body=[future, *nodes], type_ignores=[]))
    exec(compile(tree, "<fixed authentik 2026.5.6 methods>", "exec"), namespace)


def verify(root):
    source = (root / "providers/oauth2/id_token.py").read_text()
    if "# ANAS 2026.5.6: mapped sub" not in source:
        raise RuntimeError("canonical subject patch is missing")
    module = ModuleType("anas_fixed_authentik_subject_probe")
    sys.modules[module.__name__] = module
    ns = module.__dict__
    ns.update({
        "Any": Any, "asdict": asdict, "dataclass": dataclass, "field": field,
        "sha256": sha256, "json": json, "uuid": SimpleNamespace(uuid4=uuid4),
        "timezone": SimpleNamespace(now=lambda: datetime.now(datetime_timezone.utc)),
        "now": lambda: datetime.now(datetime_timezone.utc),
        "default_token_duration": lambda: datetime.now(datetime_timezone.utc) + timedelta(minutes=5),
        "generate_id": lambda: str(uuid4()), "get_login_event": lambda _: None,
        "ACR_AUTHENTIK_DEFAULT": "fixture", "encode": jwt.encode,
        "timedelta_from_string": lambda _: timedelta(minutes=5),
        "from_dict": lambda cls, value: cls(**value),
        "SubModes": SimpleNamespace(HASHED_USER_ID="hashed", USER_ID="id", USER_UUID="uuid", USER_EMAIL="email", USER_USERNAME="username", USER_UPN="upn"),
        "LOGGER": SimpleNamespace(debug=lambda *args, **kwargs: None),
    })
    compile_nodes([
        find_node(root, "providers/oauth2/id_token.py", ast.FunctionDef, "hash_session_key"),
        find_node(root, "providers/oauth2/id_token.py", ast.ClassDef, "IDToken"),
    ], ns)
    sys.modules["authentik.providers.oauth2.id_token"] = module
    provider_class = find_node(root, "providers/oauth2/models.py", ast.ClassDef, "OAuth2Provider")
    encode_method = [node for node in provider_class.body if isinstance(node, ast.FunctionDef) and node.name == "encode"]
    if len(encode_method) != 1:
        raise RuntimeError("fixed provider encode method drift")
    compile_nodes([ast.ClassDef(name="FixedProvider", bases=[], keywords=[], body=encode_method, decorator_list=[])], ns)
    grant_class = find_node(root, "providers/oauth2/models.py", ast.ClassDef, "AccessToken")
    grant_properties = [node for node in grant_class.body if isinstance(node, ast.FunctionDef) and node.name == "id_token"]
    if len(grant_properties) != 2:
        raise RuntimeError("fixed AccessToken serialization drift")
    compile_nodes([ast.ClassDef(name="FixedAccessToken", bases=[], keywords=[], body=grant_properties, decorator_list=[])], ns)
    compile_nodes([find_node(root, "providers/oauth2/utils.py", ast.FunctionDef, "create_logout_token")], ns)
    signal = find_node(root, "providers/oauth2/signals.py", ast.FunctionDef, "user_session_deleted_oauth_backchannel_logout_and_tokens_removal")
    signal.decorator_list = []
    compile_nodes([signal], ns)

    profile = {"sub": "immutable-directory-anchor", "email": "fixture@example.test"}
    userinfo = ModuleType("authentik.providers.oauth2.views.userinfo")
    class UserInfoView:
        def get_claims(self, *_):
            return dict(profile)
    userinfo.UserInfoView = UserInfoView
    sys.modules[userinfo.__name__] = userinfo

    provider = ns["FixedProvider"]()
    provider.client_id = "immich"
    provider.sub_mode = "uuid"
    provider.include_claims_in_id_token = True
    provider.signing_key = None
    provider.encryption_key = None
    provider.jwt_key = ("fixed-native-callpath-fixture-key-32-bytes", "HS256")
    provider.get_issuer = lambda _: "https://idp.example.test/application/o/immich/"
    provider.logout_method = "backchannel"
    provider.logout_uri = "https://photos.example.test/api/oauth/backchannel-logout"
    provider.access_token_validity = "minutes=5"
    user = SimpleNamespace(uuid="authentik-internal-uuid")
    auth_session = SimpleNamespace(session=SimpleNamespace(session_key="native-session-key"))
    token = SimpleNamespace(user=user, session=auth_session, expires=None, auth_time=datetime.now(datetime_timezone.utc), provider=provider)
    id_token = ns["IDToken"].new(provider, token, SimpleNamespace())
    assert id_token.sub == profile["sub"] and "sub" not in id_token.claims
    encoded = id_token.to_jwt(provider)
    assert jwt.decode(encoded, provider.jwt_key[0], algorithms=["HS256"], audience="immich")["sub"] == profile["sub"]

    saved = ns["FixedAccessToken"]()
    saved.provider = provider
    saved.scope = ["openid", "profile", "email"]
    saved.provider_id = 7
    saved.id_token = id_token
    assert saved.id_token.sub == profile["sub"]
    assert json.loads(saved._id_token)["sub"] == profile["sub"]
    assert "sub" not in json.loads(saved._id_token)["claims"]
    captured = []
    class TokenRows(list):
        def delete(self):
            pass
    rows = TokenRows([saved])
    ns["AccessToken"] = SimpleNamespace(objects=SimpleNamespace(select_related=lambda _: SimpleNamespace(filter=lambda **_: rows)))
    ns["OAuth2LogoutMethod"] = SimpleNamespace(BACKCHANNEL="backchannel")
    ns["backchannel_logout_notification_dispatch"] = SimpleNamespace(send=lambda **kwargs: captured.extend(kwargs["revocations"]))
    ns["user_session_deleted_oauth_backchannel_logout_and_tokens_removal"](None, SimpleNamespace(user=user, session=auth_session.session))
    assert len(captured) == 1
    _, issuer, subject, session_key = captured[0]
    assert subject == profile["sub"]
    logout_token = ns["create_logout_token"](provider, issuer, subject, session_key)
    logout = jwt.decode(logout_token, provider.jwt_key[0], algorithms=["HS256"], issuer=issuer, audience="immich")
    assert logout["sub"] == profile["sub"] and logout["sid"] == sha256(session_key.encode("ascii")).hexdigest()
    assert "http://schemas.openid.net/event/backchannel-logout" in logout["events"]
    assert jwt.get_unverified_header(logout_token)["typ"] == "logout+jwt"
    profile.pop("sub")
    assert ns["IDToken"].new(provider, token, SimpleNamespace()).sub == user.uuid
    for invalid in (None, "", " ", 42, []):
        profile["sub"] = invalid
        try:
            ns["IDToken"].new(provider, token, SimpleNamespace())
        except ValueError:
            continue
        raise AssertionError("invalid mapped subject was accepted")
    print("PASS: fixed native IDToken.new/encode/AccessToken serialization/session-delete signal/create_logout_token preserve canonical anchor; invalid mapping rejected")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: verify-canonical-oidc-sub.py PATCHED_AUTHENTIK_PACKAGE_ROOT")
    verify(Path(sys.argv[1]))
