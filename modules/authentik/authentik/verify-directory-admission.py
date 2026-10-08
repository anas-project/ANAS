#!/usr/bin/env python3
"""Exercise fixed native sync/deletion/signing/sending paths with ORM doubles.

Image-build verification is intentionally separate from real LDAP/HTTP E2E.
The database queue and directory server still require joint deployment tests.
"""

import ast
from contextlib import contextmanager
from datetime import datetime, timedelta, timezone
from hashlib import sha256
import importlib.util
import math
import os
from pathlib import Path
import sys
from types import ModuleType, SimpleNamespace
from uuid import uuid4


ENV = {
    "ANAS_IDENTITY_OIDC_CLIENTS": "immich",
    "SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE": "anasIdentityAnchor",
    "SAMBA_DC_ADMIN_GROUP_NAME": "Admins",
    "ANAS_IAM_CLIENT__IMMICH__CLIENT_ID": "immich",
    "ANAS_IAM_CLIENT__IMMICH__ATTRIBUTES": "sub:anasIdentityAnchor:1,anas_role:anasRole:1",
    "ANAS_IAM_CLIENT__IMMICH__ALLOW_GROUPS": "APP_immich,APP_all,Admins",
    "ANAS_IAM_CLIENT__IMMICH__OIDC_CAEP_EVENTS": "session-revoked",
    "ANAS_IAM_CLIENT__IMMICH__OIDC_LOGOUT_METHODS": "backchannel",
    "ANAS_IAM_CLIENT__IMMICH__OIDC_LOGOUT_URI": "https://photos.example/api/oauth/backchannel-logout",
    "ANAS_IAM_CLIENT__IMMICH__OIDC_LOGOUT_SESSION_REQUIRED": "false",
    "ANAS_IAM_BINDING__IMMICH__INTERFACE": "oidc",
    "ANAS_IAM_BINDING__IMMICH__OIDC_CAEP_EVENTS": "session-revoked",
    "ANAS_IAM_BINDING__IMMICH__OIDC_ISSUER_URL": "https://idp.example/application/o/immich/",
}


def compile_nodes(nodes, namespace):
    future = ast.ImportFrom(module="__future__", names=[ast.alias(name="annotations")], level=0)
    tree = ast.fix_missing_locations(ast.Module(body=[future, *nodes], type_ignores=[]))
    exec(compile(tree, "<fixed directory admission callpaths>", "exec"), namespace)


def find_node(root, relative, kind, name):
    parsed = ast.parse((root / relative).read_text())
    found = [node for node in ast.walk(parsed) if isinstance(node, kind) and node.name == name]
    if len(found) != 1:
        raise RuntimeError(f"fixed native callpath drift: {relative}:{name}")
    return found[0]


class Links(list):
    def distinct(self):
        return self

    def select_related(self, *_):
        return self

    def iterator(self):
        return iter(self)

    def values_list(self, field, flat=False):
        assert field == "identifier" and flat
        return [link.identifier for link in self]


class Fixture:
    def __init__(self, source_path):
        self.queued = []
        self.history = []
        self.depth = 0
        self.time = datetime.now(timezone.utc).timestamp() - 10
        self.provider = self.Provider()
        self.provider.pk = 7
        self.provider.client_id = "immich"
        self.provider.logout_method = "backchannel"
        self.provider.logout_uri = ENV["ANAS_IAM_CLIENT__IMMICH__OIDC_LOGOUT_URI"]
        self.app = SimpleNamespace(slug="immich", get_provider=lambda: self.provider)
        self.provider.application = self.app
        self.source = SimpleNamespace(slug="samba-ad", pk="source", object_uniqueness_field="anasIdentityAnchor", sync_users=True, delete_not_found_objects=True, sync_outgoing_trigger_mode="none", sync_lock=self.lock())
        self.links = Links([
            SimpleNamespace(user_id=1, identifier="disabled-anchor", user=SimpleNamespace(pk=1, is_active=False, admitted=True, trusted_admin=False)),
            SimpleNamespace(user_id=2, identifier="denied-anchor", user=SimpleNamespace(pk=2, is_active=True, admitted=False, trusted_admin=False)),
            SimpleNamespace(user_id=3, identifier="protected-anchor", user=SimpleNamespace(pk=3, is_active=True, admitted=True, trusted_admin=False)),
        ])
        self.policy_result_override = None
        fixture = self

        class Policy:
            def passes(self, request):
                value = fixture.policy_result_override
                if value is None:
                    value = request.user.admitted
                return SimpleNamespace(raw_result=value, passing=bool(value))

        def get_policy(**kwargs):
            assert kwargs == {"name": "anas-access-immich"}
            return Policy()

        def filter_links(**kwargs):
            assert kwargs["source"] is self.source
            selected = self.links
            if "user_id__in" in kwargs:
                selected = Links(link for link in selected if link.user.pk in kwargs["user_id__in"])
            if "user__groups__in" in kwargs:
                members = set().union(*(group.member_ids for group in kwargs["user__groups__in"]))
                selected = Links(link for link in selected if link.user.pk in members)
            return selected

        self.module = ModuleType("authentik.sources.ldap.anas_admission")
        self.ns = self.module.__dict__
        self.ns.update({
            "math": math, "os": os,
            "transaction": SimpleNamespace(atomic=self.atomic), "connection": SimpleNamespace(cursor=self.cursor),
            "Application": SimpleNamespace(objects=SimpleNamespace(with_provider=lambda: [self.app, SimpleNamespace(slug="other", get_provider=lambda: None)])),
            "ExpressionPolicy": SimpleNamespace(objects=SimpleNamespace(get=get_policy)),
            "PolicyRequest": lambda user: SimpleNamespace(user=user),
            "BaseEvaluator": SimpleNamespace(expr_is_group_member=lambda user, **kwargs: user.trusted_admin if kwargs == {"name": "Admins"} else False),
            "OAuth2Provider": self.Provider,
            "OAuth2LogoutMethod": SimpleNamespace(BACKCHANNEL="backchannel"),
            "UserLDAPSourceConnection": SimpleNamespace(objects=SimpleNamespace(filter=filter_links)),
        })
        parsed = ast.parse(source_path.read_text())
        compile_nodes([node for node in parsed.body if isinstance(node, (ast.FunctionDef, ast.Assign))], self.ns)
        self.tasks_module = ModuleType("authentik.providers.oauth2.tasks")
        self.tasks_module.send_backchannel_logout_request = SimpleNamespace(send_with_options=self.enqueue)

    class Provider:
        pass

    @contextmanager
    def installed(self):
        names = {self.module.__name__: self.module, self.tasks_module.__name__: self.tasks_module}
        saved_modules = {name: sys.modules.get(name) for name in names}
        saved_env = {key: os.environ.get(key) for key in ENV}
        sys.modules.update(names)
        os.environ.update(ENV)
        try:
            yield self
        finally:
            for name, value in saved_modules.items():
                if value is None:
                    sys.modules.pop(name, None)
                else:
                    sys.modules[name] = value
            for key, value in saved_env.items():
                if value is None:
                    os.environ.pop(key, None)
                else:
                    os.environ[key] = value

    @contextmanager
    def atomic(self):
        self.depth += 1
        self.history.append("begin")
        initial = len(self.queued)
        try:
            yield
        except BaseException:
            del self.queued[initial:]
            self.history.append("rollback")
            raise
        finally:
            self.depth -= 1

    @contextmanager
    def cursor(self):
        yield SimpleNamespace(execute=lambda sql: self.check_clock(sql), fetchone=lambda: [self.time])

    def check_clock(self, sql):
        assert sql == "SELECT EXTRACT(EPOCH FROM clock_timestamp())::double precision"
        self.history.append("clock")

    @contextmanager
    def lock(self):
        yield True

    def enqueue(self, *, args, rel_obj):
        assert self.depth > 0 and rel_obj is self.provider
        self.history.append("enqueue")
        self.queued.append(args)


def verify(root):
    import jwt

    fixture = Fixture(root / "sources/ldap/anas_admission.py")
    with fixture.installed():
        fixture.ns["notify_admission_loss"](fixture.source)
        assert [args[2] for args in fixture.queued] == ["disabled-anchor", "denied-anchor"]
        assert all(args[3] is None and args[4] == fixture.time for args in fixture.queued)
        fixture.queued.clear()

        # Execute the installed native LDAP task body. The hook must follow all
        # three waits and must never run after a failed phase.
        phases = []
        fail_phase = [None]
        class Group:
            def __init__(self, phase):
                self.phase = phase[0]
            def run(self):
                return self
            def wait(self, **_):
                phases.append(self.phase)
                if fail_phase[0] == self.phase:
                    raise RuntimeError("failed native sync phase")

        logger = SimpleNamespace(debug=lambda *args, **kwargs: None)
        ns = {
            "group": Group, "ldap_sync_paginator": lambda task, source, cls: [cls],
            "CurrentTask": SimpleNamespace(get_task=lambda: SimpleNamespace(info=lambda *_: None)),
            "LDAPSource": SimpleNamespace(objects=SimpleNamespace(filter=lambda **_: SimpleNamespace(first=lambda: fixture.source))),
            "UserLDAPSynchronizer": "users", "GroupLDAPSynchronizer": "groups", "MembershipLDAPSynchronizer": "membership",
            "UserLDAPForwardDeletion": "deletions", "GroupLDAPForwardDeletion": "group-deletions",
            "CONFIG": SimpleNamespace(get_int=lambda _: 1), "LOGGER": logger,
            "SyncOutgoingTriggerMode": SimpleNamespace(DEFERRED_END="deferred"),
        }
        sync = find_node(root, "sources/ldap/tasks.py", ast.FunctionDef, "ldap_sync")
        sync.decorator_list = []
        compile_nodes([sync], ns)
        ns["ldap_sync"]("source")
        assert phases == ["users", "membership", "deletions"] and len(fixture.queued) == 2
        fixture.queued.clear()
        phases.clear()
        fixture.source.sync_lock = fixture.lock()
        fail_phase[0] = "membership"
        try:
            ns["ldap_sync"]("source")
        except RuntimeError:
            pass
        else:
            raise AssertionError("native sync failure must propagate")
        assert not fixture.queued

        # A logged cache miss previously returned None, which Results treated
        # as success. Exercise the real fixed page body and installed Dramatiq
        # result middleware/group, rather than only a synthetic failed wait.
        from dramatiq.broker import MessageProxy
        from dramatiq.composition import group as native_group
        from dramatiq.message import Message
        from dramatiq.results import ResultFailure, ResultMissing
        from dramatiq.results.backends.stub import StubBackend
        from dramatiq.results.middleware import Results

        errors = []
        page_ns = {
            "CurrentTask": SimpleNamespace(get_task=lambda: SimpleNamespace(error=errors.append)),
            "LDAPSource": SimpleNamespace(objects=SimpleNamespace(filter=lambda **_: SimpleNamespace(first=lambda: fixture.source))),
            "path_to_class": lambda _: lambda source, task: SimpleNamespace(),
            "cache": SimpleNamespace(get=lambda _: None),
            "LOGGER": SimpleNamespace(warning=lambda *args, **kwargs: None),
            "LDAPException": type("LDAPException", (Exception,), {}),
            "StopSync": type("StopSync", (Exception,), {}),
        }
        page = find_node(root, "sources/ldap/tasks.py", ast.FunctionDef, "ldap_sync_page")
        page.decorator_list = []
        compile_nodes([page], page_ns)
        try:
            page_ns["ldap_sync_page"]("source", "sync", "missing-page")
        except RuntimeError as exc:
            page_exception = exc
        else:
            raise AssertionError("negotiated cache miss must fail the native page task")
        assert len(errors) == 1 and not fixture.queued
        backend = StubBackend()
        middleware = Results(backend=backend)
        broker = SimpleNamespace(get_actor=lambda _: SimpleNamespace(options={"store_results": True}), get_results_backend=lambda: backend)
        message = Message("default", "ldap_sync_page", (), {}, {})
        middleware.after_process_message(broker, message, exception=page_exception)
        try:
            backend.get_result(message)
        except ResultMissing:
            pass
        else:
            raise AssertionError("failed page must not store a successful None result")
        rejected = MessageProxy(message)
        rejected.stuff_exception(page_exception)
        rejected.fail()
        middleware.after_nack(broker, rejected)
        try:
            native_group([message], broker=broker).wait(timeout=10)
        except ResultFailure:
            pass
        else:
            raise AssertionError("native group must propagate exhausted page failure")
        os.environ["ANAS_IDENTITY_OIDC_CLIENTS"] = "other"
        assert page_ns["ldap_sync_page"]("source", "sync", "missing-page") is None
        os.environ.update(ENV)

        # Execute the native named-group predicate and all_groups method used
        # by the generated anasRole mapping. is_superuser is deliberately not
        # the source of this claim.
        role_ns = {}
        predicate = find_node(root, "lib/expression/evaluator.py", ast.FunctionDef, "expr_is_group_member")
        predicate.decorator_list = []
        compile_nodes([predicate, find_node(root, "core/models.py", ast.FunctionDef, "all_groups")], role_ns)
        fixture.ns["BaseEvaluator"] = SimpleNamespace(expr_is_group_member=role_ns["expr_is_group_member"])
        for link in fixture.links:
            user = link.user
            query = SimpleNamespace()
            query.with_ancestors = lambda query=query: query
            query.filter = lambda user=user, **kwargs: SimpleNamespace(exists=lambda: user.trusted_admin if kwargs == {"name": "Admins"} else False)
            user.groups = SimpleNamespace(all=lambda query=query: query)
            user.all_groups = role_ns["all_groups"].__get__(user)

        # Native membership update: capture and notification are within the
        # same transaction, so a retry cannot lose a previous admin snapshot.
        fixture.source.sync_groups = True
        fixture.source.lookup_groups_from_user = False
        fixture.source.group_membership_field = "member"
        fixture.source.user_membership_attribute = "distinguishedName"
        fixture.links[1].user.admitted = True
        fixture.links[1].user.trusted_admin = True
        fixture.links[1].user.is_superuser = True
        fixture.links[2].user.trusted_admin = True
        role_group = SimpleNamespace(member_ids={2, 3})
        new_members = [set([3])]
        def set_members(_):
            assert fixture.depth > 0
            fixture.history.append("set-members")
            role_group.member_ids = new_members[0].copy()
            for link in fixture.links:
                link.user.trusted_admin = link.user.pk in role_group.member_ids
        role_group.users = SimpleNamespace(set=set_members)
        role_group.save = lambda: fixture.history.append("save")
        users = SimpleNamespace(count=lambda: len(new_members[0]), distinct=lambda: users)
        class Query:
            def __init__(self, **_):
                pass
            def __or__(self, _):
                return self
        membership_ns = {"Q": Query, "User": SimpleNamespace(objects=SimpleNamespace(filter=lambda *_: users)), "transaction": SimpleNamespace(atomic=fixture.atomic)}
        compile_nodes([find_node(root, "sources/ldap/sync/membership.py", ast.FunctionDef, "sync")], membership_ns)
        synchronizer = SimpleNamespace(_source=fixture.source, _logger=logger, get_attributes=lambda _: {"member": list(new_members[0])}, get_group=lambda _: role_group)
        fixture.history.clear()
        assert membership_ns["sync"](synchronizer, [{}]) == 2
        assert [args[2] for args in fixture.queued] == ["denied-anchor"]
        assert fixture.links[1].user.admitted and fixture.links[1].user.is_superuser
        assert fixture.history == ["begin", "set-members", "save", "clock", "enqueue"]
        fixture.queued.clear()
        assert membership_ns["sync"](synchronizer, [{}]) == 2 and not fixture.queued

        # The same predicate depends on the directory group's name. Execute
        # the fixed native group attribute update too, including descendants.
        role_group.pk = 7
        role_group.name = "Admins"
        role_group.attributes = {}
        def update_group(defaults):
            assert fixture.depth > 0
            fixture.history.append("update-group")
            role_group.name = defaults["name"]
            for link in fixture.links:
                link.user.trusted_admin = link.user.pk in role_group.member_ids and role_group.name == "Admins"
        role_group.update_attributes = update_group
        group_query = SimpleNamespace(with_descendants=lambda: [role_group])
        errors_ns = {name: type(name, (Exception,), {}) for name in ["SkipObjectException", "PropertyMappingExpressionException", "IntegrityError", "FieldError", "StopSync"]}
        group_ns = {**errors_ns, "transaction": SimpleNamespace(atomic=fixture.atomic), "Group": SimpleNamespace(objects=SimpleNamespace(filter=lambda **_: group_query)), "flatten": lambda value: value, "Action": SimpleNamespace(AUTH="auth", LINK="link", ENROLL="enroll", DENY="deny"), "LDAP_UNIQUENESS": "ldap_uniq"}
        compile_nodes([find_node(root, "sources/ldap/sync/groups.py", ast.FunctionDef, "sync")], group_ns)
        group_synchronizer = SimpleNamespace(_source=fixture.source, _logger=logger, _task=SimpleNamespace(info=lambda *args, **kwargs: None), get_attributes=lambda _: {}, get_identifier=lambda _: "group-anchor", mapper=SimpleNamespace(build_object_properties=lambda **_: {"name": "Renamed Admins"}), manager=None, matcher=SimpleNamespace(get_group_action=lambda *_: ("auth", SimpleNamespace(group=role_group))))
        fixture.history.clear()
        assert group_ns["sync"](group_synchronizer, [{"dn": "CN=Admins"}]) == 1
        assert [args[2] for args in fixture.queued] == ["protected-anchor"]
        assert fixture.history == ["begin", "update-group", "clock", "enqueue"]
        fixture.queued.clear()
        role_group.name = "Admins"
        fixture.links[2].user.trusted_admin = True

        # Native deletion includes descendants for indirect Admins membership.
        def delete_groups():
            assert fixture.depth > 0
            fixture.history.append("delete-group")
            fixture.links[2].user.trusted_admin = False
            return 1, {"authentik_core.Group": 1}
        groups = SimpleNamespace(with_descendants=lambda: [role_group], delete=delete_groups)
        group_deletion_ns = {"transaction": SimpleNamespace(atomic=fixture.atomic), "Group": SimpleNamespace(objects=SimpleNamespace(filter=lambda **_: groups), _meta=SimpleNamespace(label="authentik_core.Group"))}
        compile_nodes([find_node(root, "sources/ldap/sync/forward_delete_groups.py", ast.FunctionDef, "sync")], group_deletion_ns)
        fixture.history.clear()
        assert group_deletion_ns["sync"](SimpleNamespace(_source=fixture.source, _logger=logger), (7,)) == 1
        assert [args[2] for args in fixture.queued] == ["protected-anchor"]
        assert fixture.history == ["begin", "delete-group", "clock", "enqueue"]
        fixture.queued.clear()

        # Native deletion captures the anchor first, then deletes and enqueues
        # before the transaction closes. OAuth grant absence is irrelevant.
        def delete_users():
            fixture.history.append("delete")
            fixture.links[:] = [link for link in fixture.links if link.user.pk != 2]
            return 1, {"authentik_core.User": 1}
        deletion_ns = {"transaction": SimpleNamespace(atomic=fixture.atomic), "User": SimpleNamespace(objects=SimpleNamespace(filter=lambda **_: SimpleNamespace(delete=delete_users)), _meta=SimpleNamespace(label="authentik_core.User"))}
        compile_nodes([find_node(root, "sources/ldap/sync/forward_delete_users.py", ast.FunctionDef, "sync")], deletion_ns)
        fixture.history.clear()
        assert deletion_ns["sync"](SimpleNamespace(_source=fixture.source, _logger=logger), (2,)) == 1
        assert fixture.queued[-1][2] == "denied-anchor"
        assert fixture.history == ["begin", "delete", "clock", "enqueue"]

        # Native OIDC authorize/token checks already bypass result cache. Keep
        # that fixed source behavior instead of adding another policy layer.
        for relative, name in [("policies/views.py", "user_has_access"), ("providers/oauth2/views/token.py", "__check_policy_access")]:
            method = find_node(root, relative, ast.FunctionDef, name)
            body = ast.unparse(method)
            if ".use_cache = False" not in body or body.index(".use_cache = False") > body.index(".build()"):
                raise RuntimeError(f"fixed authorization cache bypass drift: {relative}:{name}")

        ns = {"LOGGER": logger, "now": lambda: datetime.now(timezone.utc) + timedelta(days=1), "timedelta_from_string": lambda _: timedelta(minutes=5), "uuid": SimpleNamespace(uuid4=uuid4), "hash_session_key": lambda value: sha256(value.encode("ascii")).hexdigest()}
        compile_nodes([find_node(root, "providers/oauth2/utils.py", ast.FunctionDef, "create_logout_token")], ns)
        provider_class = find_node(root, "providers/oauth2/models.py", ast.ClassDef, "OAuth2Provider")
        encoder = [node for node in provider_class.body if isinstance(node, ast.FunctionDef) and node.name == "encode"]
        if len(encoder) != 1:
            raise RuntimeError("fixed native encoder drift")
        compile_nodes(encoder, ns)
        native_encoder = ns["encode"]
        ns["encode"] = jwt.encode
        fixture.provider.encode = native_encoder.__get__(fixture.provider)
        fixture.provider.signing_key = None
        fixture.provider.encryption_key = None
        fixture.provider.jwt_key = ("fixed-admission-chain-key-32-bytes", "HS256")
        fixture.provider.access_token_validity = "minutes=5"
        fixture.Provider.objects = SimpleNamespace(filter=lambda **_: SimpleNamespace(first=lambda: fixture.provider))
        sent = []
        fail = [True]
        response_status = [204]
        def post(uri, *, data, **_):
            assert uri == fixture.provider.logout_uri
            claims = jwt.decode(data["logout_token"], fixture.provider.jwt_key[0], algorithms=["HS256"], audience="immich", issuer=ENV["ANAS_IAM_BINDING__IMMICH__OIDC_ISSUER_URL"])
            sent.append(claims)
            def status():
                if fail[0]:
                    raise RuntimeError("receiver unavailable")
            assert _["allow_redirects"] is False
            return SimpleNamespace(raise_for_status=status, status_code=response_status[0])
        ns.update({"OAuth2Provider": fixture.Provider, "OAuth2LogoutMethod": SimpleNamespace(BACKCHANNEL="backchannel"), "CurrentTask": SimpleNamespace(get_task=lambda: SimpleNamespace(info=lambda *args, **kwargs: None)), "get_http_session": lambda: SimpleNamespace(post=post)})
        sender = find_node(root, "providers/oauth2/tasks.py", ast.FunctionDef, "send_backchannel_logout_request")
        sender.decorator_list = []
        compile_nodes([sender], ns)
        args = fixture.queued[-1]
        try:
            ns["send_backchannel_logout_request"](*args)
        except RuntimeError:
            pass
        else:
            raise AssertionError("native send failure must reach persistent retry middleware")
        fail[0] = False
        assert ns["send_backchannel_logout_request"](*args)
        for rejected_status in (302, 202, 400, 503):
            response_status[0] = rejected_status
            try:
                ns["send_backchannel_logout_request"](*args)
            except ValueError:
                pass
            else:
                raise AssertionError("unacknowledged CAEP delivery must reach native retry")
        response_status[0] = 204
        fixture.Provider.objects = SimpleNamespace(filter=lambda **_: SimpleNamespace(first=lambda: None))
        try:
            ns["send_backchannel_logout_request"](*args)
        except ValueError:
            pass
        else:
            raise AssertionError("a removed CAEP provider must not acknowledge delivery")
        assert ns["send_backchannel_logout_request"](*args[:4]) is None
        fixture.Provider.objects = SimpleNamespace(filter=lambda **_: SimpleNamespace(first=lambda: fixture.provider))
        os.environ["ANAS_IDENTITY_OIDC_CLIENTS"] = "other"
        try:
            ns["send_backchannel_logout_request"](*args)
        except ValueError:
            pass
        else:
            raise AssertionError("an unselected CAEP consumer must not receive events")
        os.environ.update(ENV)
        for token in sent:
            assert token["sub_id"] == {"format": "iss_sub", "iss": args[1], "sub": args[2]}
            assert token["events"][fixture.ns["CAEP_EVENT"]] == {"initiating_entity": "policy", "event_timestamp": args[4]}
            assert token["events"]["http://schemas.openid.net/event/backchannel-logout"] == {}
            assert "sid" not in token and "nonce" not in token
            assert token["iat"] == int(fixture.time) and token["exp"] == int(fixture.time + 300)
        assert sent[0]["jti"] != sent[1]["jti"]
        # Installed native retry middleware keeps the original durable message
        # arguments/ID while rescheduling it; it never recomputes event time.
        from dramatiq.middleware.retries import Retries
        retried = []
        retry_broker = SimpleNamespace(get_actor=lambda _: SimpleNamespace(options={}), enqueue=lambda message, **kwargs: retried.append((message, kwargs)))
        retry_message = Message("default", "authentik.providers.oauth2.tasks.send_backchannel_logout_request", tuple(args), {}, {})
        retry_proxy = MessageProxy(retry_message)
        native_retries = Retries(max_retries=5, min_backoff=1, max_backoff=1)
        native_retries.after_process_message(retry_broker, retry_proxy, exception=RuntimeError("receiver unavailable"))
        assert len(retried) == 1 and retried[0][0].args == tuple(args)
        assert retried[0][0].message_id == retry_message.message_id and retry_proxy.options["retries"] == 1
        native_retries.after_process_message(retry_broker, retry_proxy, result=True)
        assert len(retried) == 1
        retry_proxy.options["retries"] = 5
        native_retries.after_process_message(retry_broker, retry_proxy, exception=RuntimeError("receiver unavailable"))
        assert retry_proxy.failed and len(retried) == 1
        ns["now"] = lambda: datetime.now(timezone.utc)
        ordinary = ns["create_logout_token"](fixture.provider, args[1], args[2], "native-session")
        ordinary_claims = jwt.decode(ordinary, fixture.provider.jwt_key[0], algorithms=["HS256"], audience="immich")
        assert fixture.ns["CAEP_EVENT"] not in ordinary_claims["events"] and "sub_id" not in ordinary_claims
        assert ordinary_claims["sid"] == sha256(b"native-session").hexdigest()
    print("PASS: fixed native LDAP completion, user deletion and trusted-role membership/rename/deletion transactions; signed backchannel CAEP subject/time and failure propagation; ordinary logout and fresh authorization remain native")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: verify-directory-admission.py PATCHED_AUTHENTIK_PACKAGE_ROOT")
    verify(Path(sys.argv[1]))
