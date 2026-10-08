#!/usr/bin/env python3
"""Attach negotiated admission-loss events to fixed authentik 2026.5.6."""

from pathlib import Path
import sys


def replace_once(path, old, new):
    source = path.read_text()
    if source.count(old) != 1:
        raise RuntimeError(f"fixed authentik 2026.5.6 directory target drift: {path}")
    path.write_text(source.replace(old, new))


def patch(root):
    tasks = root / "sources/ldap/tasks.py"
    replace_once(tasks, '''        deletion_tasks.run().wait(
            timeout=60 * 60 * CONFIG.get_int("ldap.task_timeout_hours") * 1000,
        )

    if source.sync_outgoing_trigger_mode''', '''        deletion_tasks.run().wait(
            timeout=60 * 60 * CONFIG.get_int("ldap.task_timeout_hours") * 1000,
        )
        # ANAS 2026.5.6: only completed source sync can emit admission loss.
        from authentik.sources.ldap.anas_admission import notify_admission_loss

        notify_admission_loss(source)

    if source.sync_outgoing_trigger_mode''')
    replace_once(tasks, '''            LOGGER.warning(error_message)
            self.error(error_message)
            return
        cache.touch(page_cache_key)''', '''            LOGGER.warning(error_message)
            self.error(error_message)
            # ANAS 2026.5.6: an incomplete negotiated source must not reach
            # the completion hook through a successful group result.
            from authentik.sources.ldap.anas_admission import registered_applications

            if registered_applications(source):
                raise RuntimeError(error_message)
            return
        cache.touch(page_cache_key)''')

    deletion = root / "sources/ldap/sync/forward_delete_users.py"
    replace_once(deletion, "from ldap3 import SUBTREE", "from django.db import transaction\nfrom ldap3 import SUBTREE")
    replace_once(deletion, '''        _, deleted_per_type = User.objects.filter(pk__in=user_pks).delete()
        return deleted_per_type.get(User._meta.label, 0)''', '''        # ANAS 2026.5.6: capture the immutable LDAP subject before cascade.
        from authentik.sources.ldap.anas_admission import capture_deleted_users, notify_deleted_users

        with transaction.atomic():
            subjects = capture_deleted_users(self._source, user_pks)
            _, deleted_per_type = User.objects.filter(pk__in=user_pks).delete()
            notify_deleted_users(self._source, subjects)
        return deleted_per_type.get(User._meta.label, 0)''')

    membership = root / "sources/ldap/sync/membership.py"
    replace_once(membership, "from django.db.models import Q", "from django.db import transaction\nfrom django.db.models import Q")
    replace_once(membership, '''            group.users.set(users)
            group.save()
        self._logger.debug''', '''            # ANAS 2026.5.6: capture the actual anasRole predicate before
            # mutation and persist any loss with the same membership commit.
            from authentik.sources.ldap.anas_admission import (
                capture_trusted_role_holders, notify_trusted_role_loss,
            )

            with transaction.atomic():
                holders = capture_trusted_role_holders(self._source, [group])
                group.users.set(users)
                group.save()
                notify_trusted_role_loss(self._source, holders)
        self._logger.debug''')

    group_deletion = root / "sources/ldap/sync/forward_delete_groups.py"
    replace_once(group_deletion, "from ldap3 import SUBTREE", "from django.db import transaction\nfrom ldap3 import SUBTREE")
    replace_once(group_deletion, '''        _, deleted_per_type = Group.objects.filter(pk__in=group_pks).delete()
        return deleted_per_type.get(Group._meta.label, 0)''', '''        from authentik.sources.ldap.anas_admission import (
            capture_trusted_role_holders, notify_trusted_role_loss,
        )

        with transaction.atomic():
            groups = Group.objects.filter(pk__in=group_pks).with_descendants()
            holders = capture_trusted_role_holders(self._source, groups)
            _, deleted_per_type = Group.objects.filter(pk__in=group_pks).delete()
            notify_trusted_role_loss(self._source, holders)
        return deleted_per_type.get(Group._meta.label, 0)''')

    groups = root / "sources/ldap/sync/groups.py"
    replace_once(groups, "from django.db.utils import IntegrityError", "from django.db import transaction\nfrom django.db.utils import IntegrityError")
    replace_once(groups, '''                if action in (Action.AUTH, Action.LINK):
                    group = connection.group
                    group.update_attributes(defaults)
                elif action == Action.DENY:''', '''                if action in (Action.AUTH, Action.LINK):
                    group = connection.group
                    from authentik.sources.ldap.anas_admission import (
                        capture_trusted_role_holders, notify_trusted_role_loss,
                    )

                    # Name is part of the trusted-role predicate; a directory
                    # rename must persist loss with the same native update.
                    with transaction.atomic():
                        affected = Group.objects.filter(pk=group.pk).with_descendants()
                        holders = capture_trusted_role_holders(self._source, affected)
                        group.update_attributes(defaults)
                        notify_trusted_role_loss(self._source, holders)
                elif action == Action.DENY:''')

    oauth_tasks = root / "providers/oauth2/tasks.py"
    replace_once(oauth_tasks, '''    session_key: str | None = None,
) -> bool:''', '''    session_key: str | None = None,
    event_timestamp: float | None = None,
) -> bool:''')
    replace_once(oauth_tasks, "logout_token = create_logout_token(provider, iss, sub, session_key)", "logout_token = create_logout_token(provider, iss, sub, session_key, event_timestamp)")
    replace_once(oauth_tasks, '''    if provider is None:
        return

    # Generate the logout token''', '''    if provider is None:
        if event_timestamp is not None:
            raise ValueError("CAEP provider disappeared before notification")
        return

    # Generate the logout token''')
    replace_once(oauth_tasks, '''        allow_redirects=True,
    )
    response.raise_for_status()''', '''        allow_redirects=event_timestamp is None,
    )
    if event_timestamp is not None and response.status_code not in (200, 204):
        raise ValueError("CAEP receiver did not acknowledge the notification")
    response.raise_for_status()''')

    utils = root / "providers/oauth2/utils.py"
    replace_once(utils, '''    session_key: str | None = None,
) -> str:''', '''    session_key: str | None = None,
    event_timestamp: float | None = None,
) -> str:''')
    replace_once(utils, '''    _now = now()
    # Create the logout token payload''', '''    # ANAS 2026.5.6: CAEP issue/expiry and event time share PostgreSQL's clock.
    if event_timestamp is not None:
        from datetime import datetime, timezone
        from authentik.sources.ldap.anas_admission import revocation_time

        _now = datetime.fromtimestamp(revocation_time(), timezone.utc)
    else:
        _now = now()
    # Create the logout token payload''')
    replace_once(utils, '''    # Encode the token
    return provider.encode(payload, jwt_type="logout+jwt")''', '''    # ANAS 2026.5.6: ordinary logout retains the native payload and scope.
    if event_timestamp is not None:
        from authentik.sources.ldap.anas_admission import add_caep_session_revocation

        add_caep_session_revocation(payload, provider, iss, sub, session_key, event_timestamp)
    # Encode the token
    return provider.encode(payload, jwt_type="logout+jwt")''')


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: patch-directory-admission.py AUTHENTIK_PACKAGE_ROOT")
    patch(Path(sys.argv[1]))
