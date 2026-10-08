#!/usr/bin/env python3
"""Keep mapped OIDC subjects canonical in fixed authentik 2026.5.6."""

from pathlib import Path
import sys


MARKER = "# ANAS 2026.5.6: mapped sub must also identify backchannel notifications."
TARGET = "        return id_token\n\n    def to_dict(self) -> dict[str, Any]:"
REPLACEMENT = '''        # ANAS 2026.5.6: mapped sub must also identify backchannel notifications.
        if "sub" in id_token.claims:
            canonical_sub = id_token.claims.pop("sub")
            if not isinstance(canonical_sub, str) or not canonical_sub.strip():
                raise ValueError("Mapped OIDC subject must be a non-empty string")
            id_token.sub = canonical_sub
        return id_token

    def to_dict(self) -> dict[str, Any]:'''


def patch_source(source: str) -> str:
    if MARKER in source or source.count(TARGET) != 1:
        raise RuntimeError("expected exactly one unpatched authentik 2026.5.6 IDToken target")
    return source.replace(TARGET, REPLACEMENT)


def patch(root: Path) -> None:
    target = root / "providers/oauth2/id_token.py"
    target.write_text(patch_source(target.read_text()))


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: patch-canonical-oidc-sub.py AUTHENTIK_PACKAGE_ROOT")
    patch(Path(sys.argv[1]))
