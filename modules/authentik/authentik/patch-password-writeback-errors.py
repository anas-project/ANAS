#!/usr/bin/env python3
"""Add ANAS-safe LDAP password writeback messages to the pinned authentik release."""

from pathlib import Path
import sys


def replace_once(path: Path, old: str, new: str) -> None:
    content = path.read_text()
    if content.count(old) != 1:
        raise RuntimeError(f"expected exactly one patch target in {path}")
    path.write_text(content.replace(old, new))


def patch(root: Path) -> None:
    password = root / "sources/ldap/password.py"
    replace_once(
        password,
        '            if password.lower() in user_attributes["sAMAccountName"].lower():',
        '            if user_attributes["sAMAccountName"].lower() in password.lower():',
    )

    signals = root / "sources/ldap/signals.py"
    replace_once(
        signals,
        "from django.utils.translation import gettext_lazy as _",
        "from django.utils.translation import get_language, gettext_lazy as _",
    )
    replace_once(
        signals,
        "LOGGER = get_logger()\n\n\n@receiver(password_validate)",
        '''LOGGER = get_logger()


def _anas_password_writeback_message(exc: LDAPOperationResult) -> str:
    """Return a useful message without exposing LDAP diagnostics to the user."""
    language = get_language() or "en"
    result = getattr(exc, "result", None)
    diagnostic = " ".join(
        str(getattr(exc, field, "")) for field in ("message", "description")
    ).lower()
    rejected_by_policy = result in (19, 53) or any(
        marker in diagnostic
        for marker in ("constraint", "unwilling to perform", "password restriction", "0000052d")
    )
    insufficient_access = result == 50 or "insufficient access" in diagnostic
    missing_user = result == 32 or "no such object" in diagnostic

    if language.lower().startswith("zh"):
        if rejected_by_policy:
            return (
                "Samba 域拒绝了这个新密码。请确认长度、复杂度、用户名或姓名限制、"
                "历史密码和最小改密间隔均符合页面说明。"
            )
        if insufficient_access:
            return "无法写回 Samba 域密码：密码服务账号权限不足，请联系管理员。"
        if missing_user:
            return "无法写回 Samba 域密码：目录中找不到此用户，请联系管理员。"
        return "无法写回 Samba 域密码。目录服务可能暂时不可用，请稍后重试或联系管理员。"

    if rejected_by_policy:
        return (
            "The Samba domain rejected this password. Check its length, complexity, "
            "username or display-name content, password history, and minimum age."
        )
    if insufficient_access:
        return "The Samba password service account cannot update this user. Contact an administrator."
    if missing_user:
        return "The user no longer exists in the Samba directory. Contact an administrator."
    return "The password could not be written to the Samba directory. Try again later or contact an administrator."


@receiver(password_validate)''',
    )
    replace_once(
        signals,
        '        raise ValidationError("Failed to set password") from exc',
        "        raise ValidationError(_anas_password_writeback_message(exc)) from exc",
    )

    user_write = root / "stages/user_write/stage.py"
    replace_once(
        user_write,
        '''        except ValidationError as exc:
            self.logger.warning("failed to update user", exc=exc)
            return self.executor.stage_invalid(_("Failed to update user. Please try again later."))''',
        '''        except ValidationError as exc:
            self.logger.warning("failed to update user", exc=exc)
            # Password source integrations raise deliberately user-safe
            # ValidationErrors. Preserve that message while keeping every
            # other user-write failure behind authentik's generic response.
            if "password" in data and isinstance(exc.detail, list) and len(exc.detail) == 1:
                return self.executor.stage_invalid(str(exc.detail[0]))
            return self.executor.stage_invalid(_("Failed to update user. Please try again later."))''',
    )


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: patch-password-writeback-errors.py AUTHENTIK_PACKAGE_ROOT")
    patch(Path(sys.argv[1]))
