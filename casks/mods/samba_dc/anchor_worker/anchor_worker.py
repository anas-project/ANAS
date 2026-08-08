#!/usr/bin/env python3
"""Keep the Samba AD identity anchor populated for business users and groups."""

from __future__ import annotations

import json
import logging
import os
import stat
import sys
import time
import uuid
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable

from event_journal import (
    DEFAULT_ATTRIBUTES,
    DEFAULT_MAX_BYTES,
    EventJournal,
    normalize_change,
    parse_audit_line,
    split_attributes,
    successful_change,
)

try:
    import ldap
    from ldap.controls import SimplePagedResultsControl
except ImportError:  # Unit tests exercise the pure helpers without python3-ldap.
    ldap = None
    SimplePagedResultsControl = None


LOG = logging.getLogger("anchor-worker")
DEFAULT_USER_FILTER = (
    "(&(objectClass=user)"
    "(userAccountControl:1.2.840.113556.1.4.803:=512)"
    "(!(isCriticalSystemObject=TRUE))"
    "(!(sAMAccountName=krbtgt*)))"
)
DEFAULT_GROUP_FILTER = "(&(objectClass=group)(!(isCriticalSystemObject=TRUE)))"


def split_bases(value: str) -> list[str]:
    return [item.strip() for item in value.split(";") if item.strip()]


def printable_anchor(value: bytes) -> str:
    """Return AD's canonical GUID text for a 16-byte little-endian GUID."""
    if len(value) != 16:
        raise ValueError("binary identity anchor is not exactly 16 bytes")
    return str(uuid.UUID(bytes_le=value))


def extract_audit_event(line: str) -> str | None:
    """Return a successful DSDB Add DN from a possibly prefixed log line."""
    record = parse_audit_line(line)
    if record is None:
        return None
    return extract_added_dn(record)


def extract_added_dn(record: dict) -> str | None:
    """Return the DN of a successful DSDB Add, for an already parsed record."""
    change = successful_change(record)
    if change is None or change.get("operation") != "Add":
        return None
    return change["dn"]


@dataclass(frozen=True)
class Settings:
    ldap_url: str
    bind_dn: str
    bind_password: str
    user_bases: tuple[str, ...]
    group_bases: tuple[str, ...]
    binary_anchor_attribute: str
    anchor_attribute: str
    audit_file: Path
    health_file: Path
    tls_cacert: str
    scan_interval: int
    page_size: int
    event_file: Path | None = None
    event_attributes: tuple[str, ...] = DEFAULT_ATTRIBUTES
    event_max_bytes: int = DEFAULT_MAX_BYTES

    @classmethod
    def from_env(cls) -> "Settings":
        required = [
            "ANCHOR_LDAP_URL",
            "ANCHOR_BIND_DN",
            "ANCHOR_BIND_PASSWORD",
            "ANCHOR_USER_BASES",
            "ANCHOR_GROUP_BASES",
            "ANCHOR_TLS_CACERT",
        ]
        missing = [name for name in required if not os.environ.get(name)]
        if missing:
            raise ValueError("missing required environment variables: " + ", ".join(missing))
        return cls(
            ldap_url=os.environ["ANCHOR_LDAP_URL"],
            bind_dn=os.environ["ANCHOR_BIND_DN"],
            bind_password=os.environ["ANCHOR_BIND_PASSWORD"],
            user_bases=tuple(split_bases(os.environ["ANCHOR_USER_BASES"])),
            group_bases=tuple(split_bases(os.environ["ANCHOR_GROUP_BASES"])),
            binary_anchor_attribute=os.environ.get(
                "ANCHOR_BINARY_ATTRIBUTE", "mS-DS-ConsistencyGuid"
            ),
            anchor_attribute=os.environ.get("ANCHOR_ATTRIBUTE", "anasIdentityAnchor"),
            audit_file=Path(os.environ.get("ANCHOR_AUDIT_FILE", "/var/log/samba-audit/dsdb.json")),
            health_file=Path(os.environ.get("ANCHOR_HEALTH_FILE", "/run/anchor-worker/health.json")),
            tls_cacert=os.environ["ANCHOR_TLS_CACERT"],
            scan_interval=max(30, int(os.environ.get("ANCHOR_SCAN_INTERVAL", "300"))),
            page_size=max(1, int(os.environ.get("ANCHOR_PAGE_SIZE", "500"))),
            # Publishing is optional: an empty path leaves the worker doing
            # exactly what it did before, which keeps the deployment that has
            # no subscribers from carrying a journal it never reads.
            event_file=(
                Path(os.environ["ANCHOR_EVENT_FILE"])
                if os.environ.get("ANCHOR_EVENT_FILE")
                else None
            ),
            event_attributes=(
                split_attributes(os.environ["ANCHOR_EVENT_ATTRIBUTES"])
                if os.environ.get("ANCHOR_EVENT_ATTRIBUTES")
                else DEFAULT_ATTRIBUTES
            ),
            event_max_bytes=max(
                64 * 1024, int(os.environ.get("ANCHOR_EVENT_MAX_BYTES", str(DEFAULT_MAX_BYTES)))
            ),
        )


class Directory:
    def __init__(self, settings: Settings):
        if ldap is None:
            raise RuntimeError("python3-ldap is required")
        self.settings = settings
        self.connection = None

    def close(self) -> None:
        if self.connection is not None:
            try:
                self.connection.unbind_s()
            except ldap.LDAPError:
                pass
            self.connection = None

    def connect(self):
        if self.connection is not None:
            return self.connection
        ldap.set_option(ldap.OPT_X_TLS_CACERTFILE, self.settings.tls_cacert)
        ldap.set_option(ldap.OPT_X_TLS_REQUIRE_CERT, ldap.OPT_X_TLS_DEMAND)
        connection = ldap.initialize(self.settings.ldap_url)
        connection.set_option(ldap.OPT_PROTOCOL_VERSION, 3)
        connection.set_option(ldap.OPT_NETWORK_TIMEOUT, 10)
        connection.set_option(ldap.OPT_TIMEOUT, 15)
        connection.simple_bind_s(self.settings.bind_dn, self.settings.bind_password)
        self.connection = connection
        return connection

    def _retry(self, operation):
        try:
            return operation(self.connect())
        except (ldap.SERVER_DOWN, ldap.TIMEOUT):
            self.close()
            return operation(self.connect())

    @property
    def attributes(self) -> list[str]:
        return [
            "objectGUID",
            self.settings.binary_anchor_attribute,
            self.settings.anchor_attribute,
            "objectClass",
        ]

    def read_target(self, dn: str):
        target_filter = f"(|{DEFAULT_USER_FILTER}{DEFAULT_GROUP_FILTER})"

        def search(connection):
            rows = connection.search_s(dn, ldap.SCOPE_BASE, target_filter, self.attributes)
            return rows[0] if rows else None

        try:
            return self._retry(search)
        except (ldap.NO_SUCH_OBJECT, ldap.FILTER_ERROR):
            return None

    def iter_missing(self, base: str, object_filter: str) -> Iterable[tuple[str, dict]]:
        missing_filter = (
            f"(&{object_filter}(|"
            f"(!({self.settings.binary_anchor_attribute}=*))"
            f"(!({self.settings.anchor_attribute}=*))))"
        )
        yield from self.iter_entries(base, missing_filter)

    def iter_entries(self, base: str, search_filter: str) -> Iterable[tuple[str, dict]]:
        cookie = b""
        while True:
            control = SimplePagedResultsControl(True, size=self.settings.page_size, cookie=cookie)

            def search(connection):
                msgid = connection.search_ext(
                    base,
                    ldap.SCOPE_SUBTREE,
                    search_filter,
                    self.attributes,
                    serverctrls=[control],
                )
                return connection.result3(msgid)

            _rtype, rows, _msgid, controls = self._retry(search)
            for dn, attributes in rows:
                if dn:
                    yield dn, attributes
            cookie = b""
            for response_control in controls:
                if response_control.controlType == SimplePagedResultsControl.controlType:
                    cookie = response_control.cookie
                    break
            if not cookie:
                return

    def stamp(self, dn: str, attributes: dict | None = None) -> bool:
        if attributes is None:
            row = self.read_target(dn)
            if row is None:
                return False
            _dn, attributes = row
        binary_values = attributes.get(self.settings.binary_anchor_attribute, [])
        object_guids = attributes.get("objectGUID", [])
        if len(object_guids) != 1 or len(object_guids[0]) != 16:
            raise ValueError(f"{dn}: objectGUID is not exactly 16 bytes")
        if binary_values:
            if len(binary_values) != 1 or len(binary_values[0]) != 16:
                raise ValueError(f"{dn}: binary identity anchor is not exactly 16 bytes")
            binary_value = binary_values[0]
        else:
            binary_value = object_guids[0]

        text_value = printable_anchor(binary_value)
        text_values = attributes.get(self.settings.anchor_attribute, [])
        if text_values:
            if len(text_values) != 1:
                raise ValueError(f"{dn}: printable identity anchor is not single-valued")
            try:
                current_text = text_values[0].decode("ascii")
            except (AttributeError, UnicodeDecodeError) as exc:
                raise ValueError(f"{dn}: printable identity anchor is not ASCII") from exc
            if current_text != text_value:
                raise ValueError(
                    f"{dn}: printable identity anchor does not match the binary anchor"
                )

        changes = []
        if not binary_values:
            changes.append(
                (ldap.MOD_ADD, self.settings.binary_anchor_attribute, [binary_value])
            )
        if not text_values:
            changes.append(
                (ldap.MOD_ADD, self.settings.anchor_attribute, [text_value.encode("ascii")])
            )
        if not changes:
            return False

        def modify(connection):
            return connection.modify_s(dn, changes)

        try:
            self._retry(modify)
        except (ldap.TYPE_OR_VALUE_EXISTS, ldap.CONSTRAINT_VIOLATION):
            pass
        row = self.read_target(dn)
        if row is None:
            raise RuntimeError(f"{dn}: identity anchor verification failed")
        verified = row[1]
        if verified.get(self.settings.binary_anchor_attribute) != [binary_value]:
            raise RuntimeError(f"{dn}: binary identity anchor verification failed")
        if verified.get(self.settings.anchor_attribute) != [text_value.encode("ascii")]:
            raise RuntimeError(f"{dn}: printable identity anchor verification failed")
        LOG.info("reconciled binary and printable identity anchors on %s", dn)
        return True


class AuditFollower:
    def __init__(self, path: Path):
        self.path = path
        self.handle = None
        self.inode = None

    def close(self) -> None:
        if self.handle is not None:
            self.handle.close()
        self.handle = None
        self.inode = None

    def open_at_end(self) -> bool:
        try:
            self.handle = self.path.open("r", encoding="utf-8", errors="replace")
            self.inode = os.fstat(self.handle.fileno()).st_ino
            self.handle.seek(0, os.SEEK_END)
            return True
        except FileNotFoundError:
            return False

    def lines(self) -> list[str]:
        if self.handle is None:
            self.open_at_end()
            return []
        lines = self.handle.readlines()
        try:
            current = self.path.stat()
        except FileNotFoundError:
            current = None
        if current is None or current.st_ino != self.inode:
            # Drain the renamed file before opening the replacement at offset 0.
            lines.extend(self.handle.readlines())
            self.close()
            if current is not None and stat.S_ISREG(current.st_mode):
                self.handle = self.path.open("r", encoding="utf-8", errors="replace")
                self.inode = os.fstat(self.handle.fileno()).st_ino
        elif self.handle.tell() > current.st_size:
            self.handle.seek(0)
        return lines


class Worker:
    def __init__(self, settings: Settings):
        self.settings = settings
        self.directory = Directory(settings)
        self.follower = AuditFollower(settings.audit_file)
        self.journal = (
            EventJournal(settings.event_file, settings.event_max_bytes)
            if settings.event_file
            else None
        )
        self.health = {
            "ready": False,
            "started_at": int(time.time()),
            "last_scan_at": 0,
            "last_event_at": 0,
            "last_error": "",
            "missing_after_scan": None,
            "last_integrity_at": 0,
            "duplicate_anchors": None,
            "invalid_anchors": None,
            "last_published_seq": self.journal.seq if self.journal else None,
        }

    def write_health(self) -> None:
        temporary = self.settings.health_file.with_suffix(".tmp")
        temporary.write_text(json.dumps(self.health, sort_keys=True), encoding="utf-8")
        temporary.replace(self.settings.health_file)

    def reconcile(self) -> None:
        stamped = 0
        for base in self.settings.user_bases:
            for dn, attributes in self.directory.iter_missing(base, DEFAULT_USER_FILTER):
                stamped += int(self.directory.stamp(dn, attributes))
        for base in self.settings.group_bases:
            for dn, attributes in self.directory.iter_missing(base, DEFAULT_GROUP_FILTER):
                stamped += int(self.directory.stamp(dn, attributes))
        # A second pass is both the readiness gate and protection against a
        # concurrent create near the end of the first pass.
        remaining = 0
        for base in self.settings.user_bases:
            remaining += sum(1 for _ in self.directory.iter_missing(base, DEFAULT_USER_FILTER))
        for base in self.settings.group_bases:
            remaining += sum(1 for _ in self.directory.iter_missing(base, DEFAULT_GROUP_FILTER))
        if not self.health["last_integrity_at"] or int(time.time()) - int(self.health["last_integrity_at"]) >= 86400:
            self.audit_integrity()
        healthy = (
            remaining == 0
            and self.health["duplicate_anchors"] == 0
            and self.health["invalid_anchors"] == 0
        )
        self.health.update(
            ready=healthy,
            last_scan_at=int(time.time()),
            last_error="" if healthy else self.integrity_error(remaining),
            missing_after_scan=remaining,
        )
        self.write_health()
        LOG.info("reconciliation complete: stamped=%d remaining=%d", stamped, remaining)

    def audit_integrity(self) -> None:
        seen_dns: set[str] = set()
        seen_values: set[bytes] = set()
        duplicate = 0
        invalid = 0
        scopes = [
            *((base, DEFAULT_USER_FILTER) for base in self.settings.user_bases),
            *((base, DEFAULT_GROUP_FILTER) for base in self.settings.group_bases),
        ]
        for base, object_filter in scopes:
            for dn, attributes in self.directory.iter_entries(base, object_filter):
                normalized_dn = dn.casefold()
                if normalized_dn in seen_dns:
                    continue
                seen_dns.add(normalized_dn)
                binary_values = attributes.get(self.settings.binary_anchor_attribute, [])
                text_values = attributes.get(self.settings.anchor_attribute, [])
                if len(binary_values) != 1 or len(binary_values[0]) != 16:
                    invalid += 1
                    continue
                expected_text = printable_anchor(binary_values[0]).encode("ascii")
                if text_values != [expected_text]:
                    invalid += 1
                    continue
                if binary_values[0] in seen_values:
                    duplicate += 1
                seen_values.add(binary_values[0])
        self.health.update(
            last_integrity_at=int(time.time()),
            duplicate_anchors=duplicate,
            invalid_anchors=invalid,
        )
        LOG.info("integrity audit complete: duplicates=%d invalid=%d", duplicate, invalid)

    def integrity_error(self, remaining: int) -> str:
        problems = []
        if remaining:
            problems.append(f"{remaining} entries lack an identity anchor")
        if self.health["duplicate_anchors"]:
            problems.append(f"{self.health['duplicate_anchors']} duplicate identity anchors")
        if self.health["invalid_anchors"]:
            problems.append(f"{self.health['invalid_anchors']} invalid identity anchors")
        return "; ".join(problems)

    def handle_dn(self, dn: str) -> None:
        if not self.dn_in_scope(dn):
            return
        for delay in (0.2, 0.5, 1, 2, 5, 10):
            row = self.directory.read_target(dn)
            if row is not None:
                self.directory.stamp(dn, row[1])
                self.health["last_event_at"] = int(time.time())
                # Recheck uniqueness soon. This matters for an explicitly
                # restored migration anchor, which the writer correctly leaves
                # untouched but still needs duplicate detection.
                self.health["last_integrity_at"] = 0
                self.write_health()
                return
            time.sleep(delay)
        LOG.info("discarding Add event for absent or out-of-scope object %s", dn)

    def handle_audit_line(self, line: str) -> dict | None:
        """Feed one audit line to both sinks, parsing it exactly once.

        Anchoring runs first: a subscriber woken by the event should already be
        able to see the anchor that every downstream consumer keys on.
        """
        record = parse_audit_line(line)
        if record is None:
            return None
        dn = extract_added_dn(record)
        if dn:
            self.handle_dn(dn)
        if self.journal is None:
            return None
        event = normalize_change(record, self.settings.event_attributes, self.dn_in_scope)
        if event is None:
            return None
        published = self.journal.append(event)
        self.health["last_published_seq"] = published["seq"]
        return published

    def dn_in_scope(self, dn: str) -> bool:
        normalized = dn.casefold()
        for base in (*self.settings.user_bases, *self.settings.group_bases):
            normalized_base = base.casefold()
            if normalized == normalized_base or normalized.endswith("," + normalized_base):
                return True
        return False

    def run(self) -> None:
        # Establish the audit cursor before scanning, so creates concurrent with
        # the startup scan remain in the event stream.
        self.follower.open_at_end()
        next_scan = 0.0
        while True:
            try:
                now = time.monotonic()
                if now >= next_scan:
                    self.reconcile()
                    next_scan = time.monotonic() + self.settings.scan_interval
                for line in self.follower.lines():
                    self.handle_audit_line(line)
                time.sleep(0.25)
            except (ldap.LDAPError, OSError, RuntimeError, ValueError) as exc:
                LOG.exception("worker operation failed")
                self.health["ready"] = False
                self.health["last_error"] = str(exc)
                self.write_health()
                self.directory.close()
                next_scan = 0.0
                time.sleep(5)


def healthcheck(settings: Settings) -> int:
    try:
        state = json.loads(settings.health_file.read_text(encoding="utf-8"))
    except (FileNotFoundError, json.JSONDecodeError, OSError):
        return 1
    max_age = max(settings.scan_interval * 3, 900)
    if not state.get("ready") or int(time.time()) - int(state.get("last_scan_at", 0)) > max_age:
        return 1
    return 0


def main() -> int:
    logging.basicConfig(
        level=os.environ.get("ANCHOR_LOG_LEVEL", "INFO"),
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
    )
    try:
        settings = Settings.from_env()
    except (KeyError, ValueError) as exc:
        LOG.error("invalid configuration: %s", exc)
        return 2
    if len(sys.argv) == 2 and sys.argv[1] == "--healthcheck":
        return healthcheck(settings)
    if len(sys.argv) != 1:
        LOG.error("usage: anchor_worker.py [--healthcheck]")
        return 2
    Worker(settings).run()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
