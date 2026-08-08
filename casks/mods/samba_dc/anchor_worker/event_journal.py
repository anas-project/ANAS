#!/usr/bin/env python3
"""Normalize Samba's dsdb audit stream into a journal subscribers can tail.

Samba's audit log is a Samba-shaped, high-noise stream: on 2026-08-08 it held
3977 records, 3958 of which were lastLogon/logonCount churn from a single
machine account. Subscribers should not have to know that shape or filter that
noise, and they must never need write access to the domain controller's own
log. So the anchor worker -- already the one process following the audit file,
with one cursor -- also republishes the interesting records here.

The journal is append-only JSON Lines. Each record carries a monotonic `seq`,
so a subscriber persists the last seq it handled and resumes from there across
restarts on either side.
"""

from __future__ import annotations

import json
import logging
import os
from pathlib import Path
from typing import Callable, Iterable

LOG = logging.getLogger("event-journal")

# Attributes whose change is worth waking a subscriber for. Everything outside
# this set is dropped at the producer, which is what keeps the machine-account
# logon churn out of the journal entirely.
DEFAULT_ATTRIBUTES = (
    "member",
    "memberOf",
    "userAccountControl",
    "sAMAccountName",
    "userPrincipalName",
    "displayName",
    "mail",
    "anasIdentityAnchor",
)

# A safety valve, not a retention policy. At the observed rate -- a couple of
# dozen real changes a day at ~150 bytes each -- this is years of headroom, and
# a subscriber that has been offline long enough to be cut by a rotation is
# already relying on its own periodic reconciliation.
DEFAULT_MAX_BYTES = 5 * 1024 * 1024


def split_attributes(value: str) -> tuple[str, ...]:
    return tuple(item.strip() for item in value.split(",") if item.strip())


def parse_audit_line(line: str) -> dict | None:
    """Return the JSON object from a possibly prefixed audit log line."""
    start = line.find("{")
    if start < 0:
        return None
    try:
        record = json.loads(line[start:])
    except json.JSONDecodeError:
        return None
    return record if isinstance(record, dict) else None


def successful_change(record: dict) -> dict | None:
    """Return the dsdbChange body of a successful change, if this is one."""
    if record.get("type") != "dsdbChange":
        return None
    change = record.get("dsdbChange")
    if not isinstance(change, dict):
        return None
    if change.get("statusCode") not in (None, 0):
        return None
    if change.get("status") not in (None, "Success"):
        return None
    dn = change.get("dn")
    if not isinstance(dn, str) or not dn:
        return None
    return change


def normalize_change(
    record: dict,
    attributes: Iterable[str],
    in_scope: Callable[[str], bool],
) -> dict | None:
    """Turn one audit record into a subscriber event, or None to drop it.

    Add and Delete are always published for an in-scope object: the object
    appearing or disappearing is the event, whatever it happens to carry.
    Modify is published only when it touched a watched attribute.
    """
    change = successful_change(record)
    if change is None:
        return None
    dn = change["dn"]
    if not in_scope(dn):
        return None
    operation = change.get("operation")
    if operation not in ("Add", "Modify", "Delete"):
        return None

    watched = {name.casefold() for name in attributes}
    changed = change.get("attributes")
    touched = sorted(
        name for name in (changed or {}) if name.casefold() in watched
    ) if isinstance(changed, dict) else []

    if operation == "Modify" and not touched:
        return None
    return {
        "ts": record.get("timestamp"),
        "op": operation,
        "dn": dn,
        "attributes": touched,
    }


class EventJournal:
    """Append-only writer for the normalized event stream."""

    def __init__(self, path: Path, max_bytes: int = DEFAULT_MAX_BYTES):
        self.path = Path(path)
        self.rotated = self.path.with_name(self.path.name + ".1")
        self.max_bytes = max_bytes
        self.seq = self._resume()

    def _resume(self) -> int:
        """Continue the sequence across restarts so subscriber cursors hold.

        The rotated file is consulted too: a restart that lands right after a
        rotation would otherwise find an empty journal, restart the sequence at
        zero, and leave every subscriber's stored cursor ahead of the stream --
        silently dropping everything until the sequence caught back up.
        """
        for candidate in (self.path, self.rotated):
            last = self._last_seq(candidate)
            if last:
                return last
        return 0

    @staticmethod
    def _last_seq(path: Path) -> int:
        last = 0
        try:
            with path.open("r", encoding="utf-8") as handle:
                for line in handle:
                    line = line.strip()
                    if not line:
                        continue
                    try:
                        seq = json.loads(line).get("seq")
                    except (json.JSONDecodeError, AttributeError):
                        continue
                    if isinstance(seq, int) and seq > last:
                        last = seq
        except FileNotFoundError:
            return 0
        except OSError as exc:
            LOG.warning("cannot read journal %s: %s", path, exc)
            return 0
        return last

    def _rotate_if_needed(self, incoming: int) -> None:
        try:
            size = self.path.stat().st_size
        except FileNotFoundError:
            return
        if size + incoming <= self.max_bytes:
            return
        LOG.info("rotating event journal at %d bytes", size)
        self.path.replace(self.rotated)

    def append(self, event: dict) -> dict:
        """Write one event and return it with its assigned sequence number."""
        self.seq += 1
        record = {"seq": self.seq, **event}
        line = json.dumps(record, sort_keys=True, ensure_ascii=False)
        self._rotate_if_needed(len(line) + 1)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        with self.path.open("a", encoding="utf-8") as handle:
            handle.write(line + "\n")
            handle.flush()
            # A subscriber acts on what it reads, so the record has to outlive a
            # crash. At this volume the fsync costs nothing.
            os.fsync(handle.fileno())
        return record
