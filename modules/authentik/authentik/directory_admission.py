"""Fixed Samba AD/OIDC admission-loss extension; uses native durable tasks.

Only the negotiated CAEP session-revoked event is implemented. This is not an
SSF stream manager or a separate directory scheduler.
"""

import math
import os

from django.db import connection, transaction

from authentik.core.models import Application
from authentik.lib.expression.evaluator import BaseEvaluator
from authentik.policies.expression.models import ExpressionPolicy
from authentik.policies.types import PolicyRequest
from authentik.providers.oauth2.models import OAuth2LogoutMethod, OAuth2Provider
from authentik.sources.ldap.models import UserLDAPSourceConnection

CAEP_EVENT = "https://schemas.openid.net/secevent/caep/event-type/session-revoked"


def split_csv(value):
    return [part.strip() for part in value.split(",") if part.strip()]


def registration(application):
    """Read the same provider-neutral registration the module already consumes."""
    selected = {name.lower().replace("_", "-") for name in split_csv(os.environ.get("ANAS_IDENTITY_OIDC_CLIENTS", ""))}
    if application.slug not in selected:
        return None
    name = application.slug.upper().replace("-", "_")
    prefix = f"ANAS_IAM_CLIENT__{name}__"
    events = split_csv(os.environ.get(prefix + "OIDC_CAEP_EVENTS", ""))
    if not events:
        return None
    binding = f"ANAS_IAM_BINDING__{name}__"
    anchor = os.environ.get("SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE", "")
    sub_source = ""
    for attribute in split_csv(os.environ.get(prefix + "ATTRIBUTES", "")):
        parts = attribute.split(":")
        if len(parts) > 1 and parts[0].strip() == "sub":
            sub_source = parts[1].strip()
    issuer = os.environ.get(binding + "OIDC_ISSUER_URL", "")
    if (
        events != ["session-revoked"]
        or split_csv(os.environ.get(binding + "OIDC_CAEP_EVENTS", "")) != ["session-revoked"]
        or os.environ.get(binding + "INTERFACE") != "oidc"
        or "backchannel" not in split_csv(os.environ.get(prefix + "OIDC_LOGOUT_METHODS", ""))
        or not os.environ.get(prefix + "OIDC_LOGOUT_URI")
        or os.environ.get(prefix + "OIDC_LOGOUT_SESSION_REQUIRED") == "true"
        or not anchor
        or sub_source.casefold() != anchor.casefold()
        or not issuer.startswith("https://")
    ):
        raise ValueError(f"Invalid negotiated CAEP registration for {application.slug}")
    provider = application.get_provider()
    if (
        not isinstance(provider, OAuth2Provider)
        or provider.client_id != os.environ.get(prefix + "CLIENT_ID")
        or provider.logout_method != OAuth2LogoutMethod.BACKCHANNEL
        or provider.logout_uri != os.environ.get(prefix + "OIDC_LOGOUT_URI")
    ):
        raise ValueError(f"CAEP registration/provider mismatch for {application.slug}")
    return provider, issuer


def registered_applications(source):
    if source.slug != "samba-ad":
        return []
    result = []
    for application in Application.objects.with_provider():
        if (value := registration(application)) is not None:
            result.append((application, *value))
    if result and source.object_uniqueness_field.casefold() != os.environ.get("SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE", "").casefold():
        raise ValueError("CAEP source uniqueness field must be the verified directory anchor")
    return result


def revocation_time():
    # The consumers use the same PostgreSQL Provider; avoid application-clock
    # drift and preserve this value as a task argument across every retry.
    with connection.cursor() as cursor:
        cursor.execute("SELECT EXTRACT(EPOCH FROM clock_timestamp())::double precision")
        return float(cursor.fetchone()[0])


def enqueue(provider, issuer, subject, timestamp):
    if not isinstance(subject, str) or not subject.strip():
        raise ValueError("Directory revocation requires a stable non-empty subject")
    from authentik.providers.oauth2.tasks import send_backchannel_logout_request

    # PostgresBroker.enqueue's nested transaction persists the native Task in
    # the surrounding directory transaction. No grant/token row is needed.
    send_backchannel_logout_request.send_with_options(
        args=(provider.pk, issuer, subject, None, timestamp), rel_obj=provider
    )


def notify_admission_loss(source):
    applications = registered_applications(source)
    if not applications:
        return
    with transaction.atomic():
        timestamp = revocation_time()
        for application, provider, issuer in applications:
            # Evaluate the generated, existing access policy directly. A failed
            # evaluation is not authoritative evidence of directory revocation.
            policy = ExpressionPolicy.objects.get(name=f"anas-access-{application.slug}")
            for link in UserLDAPSourceConnection.objects.filter(source=source).select_related("user").iterator():
                result = policy.passes(PolicyRequest(link.user))
                if not isinstance(result.raw_result, bool):
                    raise ValueError(f"Directory access policy failed for {application.slug}")
                if not link.user.is_active or not result.passing:
                    enqueue(provider, issuer, link.identifier, timestamp)


def capture_deleted_users(source, user_pks):
    if not registered_applications(source):
        return []
    return list(UserLDAPSourceConnection.objects.filter(source=source, user_id__in=user_pks).values_list("identifier", flat=True))


def notify_deleted_users(source, subjects):
    if not subjects:
        return
    applications = registered_applications(source)
    timestamp = revocation_time()
    for _, provider, issuer in applications:
        for subject in subjects:
            enqueue(provider, issuer, subject, timestamp)


def trusted_role_applications(source):
    result = []
    for application, provider, issuer in registered_applications(source):
        prefix = "ANAS_IAM_CLIENT__" + application.slug.upper().replace("-", "_") + "__"
        attributes = split_csv(os.environ.get(prefix + "ATTRIBUTES", ""))
        if any(len(parts := attribute.split(":")) > 1 and parts[1].strip() == "anasRole" for attribute in attributes):
            result.append((application, provider, issuer))
    return result


def is_trusted_admin(user):
    # Match the exact reserved anasRole claim mapping, including recursive
    # groups. Native is_superuser also accepts other marked groups.
    return BaseEvaluator.expr_is_group_member(
        user, name=os.environ.get("SAMBA_DC_ADMIN_GROUP_NAME") or "Admins"
    )


def capture_trusted_role_holders(source, groups):
    if not trusted_role_applications(source):
        return []
    return [
        (link.user_id, link.identifier)
        for link in UserLDAPSourceConnection.objects.filter(
            source=source, user__groups__in=groups
        ).distinct().select_related("user").iterator()
        if is_trusted_admin(link.user)
    ]


def notify_trusted_role_loss(source, holders):
    if not holders:
        return
    applications = trusted_role_applications(source)
    if not applications:
        return
    timestamp = revocation_time()
    previous = dict(holders)
    for link in UserLDAPSourceConnection.objects.filter(
        source=source, user_id__in=previous
    ).select_related("user").iterator():
        if link.identifier != previous[link.user_id]:
            raise ValueError("Directory subject changed during trusted-role update")
        if not is_trusted_admin(link.user):
            for _, provider, issuer in applications:
                enqueue(provider, issuer, link.identifier, timestamp)


def add_caep_session_revocation(payload, provider, issuer, subject, session_key, timestamp):
    """Augment an already native logout payload only for an opted-in provider."""
    value = registration(provider.application)
    if (
        value is None
        or value[0].pk != provider.pk
        or value[1] != issuer
        or session_key is not None
        or not isinstance(subject, str)
        or not subject.strip()
        or isinstance(timestamp, bool)
        or not isinstance(timestamp, (int, float))
        or not math.isfinite(timestamp)
        or timestamp <= 0
        or timestamp > payload["iat"] + 5
    ):
        raise ValueError("Invalid CAEP directory revocation arguments")
    payload["sub_id"] = {"format": "iss_sub", "iss": issuer, "sub": subject}
    payload["events"][CAEP_EVENT] = {
        "initiating_entity": "policy", "event_timestamp": timestamp,
    }
