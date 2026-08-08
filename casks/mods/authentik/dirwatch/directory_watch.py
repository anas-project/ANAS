#!/usr/bin/env python3
"""Trigger the Samba AD source sync when the directory actually changes.

authentik refreshes its copy of the directory on a schedule, and nothing in the
login path re-reads LDAP: the password is verified with a bind, but groups and
attributes come from whatever the last sync stored. So a user added to a group
stays unauthorized until the next scheduled run -- up to two hours, which is
exactly how a Nextcloud login failed on 2026-08-08 with the group already
correct in AD.

This subscribes to the normalized directory event journal that samba_dc
publishes and asks the scheduler to run early. It is an accelerator, not a
source of truth: the ordinary schedule stays in place, so if this process is
down the deployment simply falls back to its previous behaviour.

Triggering is deliberately coarse. authentik has no per-user refresh -- the
only entry point is a full source sync -- so a burst of changes must collapse
into one run, and consecutive runs need a floor between them.
"""

from __future__ import annotations

import json
import logging
import os
import sys
import time
from dataclasses import dataclass, field
from pathlib import Path

LOG = logging.getLogger("dirwatch")

DEFAULT_ACTOR = "authentik.sources.ldap.tasks.ldap_sync"


def split_csv(value: str) -> tuple[str, ...]:
    return tuple(item.strip() for item in value.split(",") if item.strip())


@dataclass
class Settings:
    event_file: Path
    cursor_file: Path
    health_file: Path
    source_slug: str
    actor_name: str
    operations: tuple[str, ...]
    attributes: tuple[str, ...]
    debounce_seconds: float
    min_interval_seconds: float
    poll_seconds: float

    @classmethod
    def from_env(cls) -> "Settings":
        return cls(
            event_file=Path(
                os.environ.get(
                    "AUTHENTIK_DIRWATCH_EVENT_FILE",
                    "/var/lib/anas-directory-events/events.jsonl",
                )
            ),
            cursor_file=Path(
                os.environ.get("AUTHENTIK_DIRWATCH_CURSOR_FILE", "/data/anas-dirwatch/cursor.json")
            ),
            health_file=Path(
                os.environ.get("AUTHENTIK_DIRWATCH_HEALTH_FILE", "/data/anas-dirwatch/health.json")
            ),
            source_slug=os.environ.get("AUTHENTIK_DIRWATCH_SOURCE_SLUG", "samba-ad"),
            actor_name=os.environ.get("AUTHENTIK_DIRWATCH_ACTOR_NAME", DEFAULT_ACTOR),
            operations=split_csv(
                os.environ.get("AUTHENTIK_DIRWATCH_OPERATIONS", "Add,Modify,Delete")
            ),
            attributes=split_csv(os.environ.get("AUTHENTIK_DIRWATCH_ATTRIBUTES", "")),
            debounce_seconds=float(os.environ.get("AUTHENTIK_DIRWATCH_DEBOUNCE_SECONDS", "5")),
            min_interval_seconds=float(
                os.environ.get("AUTHENTIK_DIRWATCH_MIN_INTERVAL_SECONDS", "60")
            ),
            poll_seconds=float(os.environ.get("AUTHENTIK_DIRWATCH_POLL_SECONDS", "1")),
        )


def interesting(event: dict, settings: Settings) -> bool:
    """Does this subscriber care about the event the producer published?

    The producer already dropped the machine-account churn. This is the
    narrower, subscriber-side interest: an empty attribute list means "any
    attribute the producer considered publishable".
    """
    if event.get("op") not in settings.operations:
        return False
    if not settings.attributes:
        return True
    if event.get("op") in ("Add", "Delete"):
        return True
    watched = {name.casefold() for name in settings.attributes}
    return any(name.casefold() in watched for name in event.get("attributes") or [])


class JournalReader:
    """Follow the append-only journal across restarts and rotations.

    The byte offset is what makes a poll cheap; `seq` is what makes it correct.
    After a rotation the offset means nothing, so the reader starts over at zero
    and lets the sequence number filter out everything already handled.
    """

    def __init__(self, path: Path, cursor: int):
        self.path = path
        self.cursor = cursor
        self.handle = None
        self.inode = None

    def close(self) -> None:
        if self.handle:
            self.handle.close()
        self.handle = None
        self.inode = None

    def _open(self) -> bool:
        try:
            self.handle = self.path.open("r", encoding="utf-8")
            self.inode = os.fstat(self.handle.fileno()).st_ino
        except FileNotFoundError:
            self.handle = None
            self.inode = None
            return False
        return True

    def events(self) -> list[dict]:
        if self.handle is None and not self._open():
            return []
        try:
            current = self.path.stat()
        except FileNotFoundError:
            current = None
        if current is None or current.st_ino != self.inode:
            # Drain what is left of the file we still hold open, then pick up
            # the replacement from the beginning.
            records = self._read_open_handle()
            self.close()
            if self._open():
                records.extend(self._read_open_handle())
            return records
        return self._read_open_handle()

    def _read_open_handle(self) -> list[dict]:
        if self.handle is None:
            return []
        records = []
        for line in self.handle.readlines():
            line = line.strip()
            if not line:
                continue
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                LOG.warning("skipping malformed journal line")
                continue
            seq = event.get("seq")
            if not isinstance(seq, int) or seq <= self.cursor:
                continue
            records.append(event)
        return records


class Trigger:
    """Ask the scheduler to run the LDAP source sync now."""

    def __init__(self, settings: Settings):
        self.settings = settings

    def schedules(self):
        from authentik.tasks.schedules.models import Schedule

        found = []
        for schedule in Schedule.objects.filter(actor_name=self.settings.actor_name):
            if getattr(schedule.rel_obj, "slug", None) != self.settings.source_slug:
                continue
            if schedule.paused:
                LOG.warning("schedule for %s is paused; not triggering", self.settings.source_slug)
                continue
            found.append(schedule)
        return found

    def fire(self) -> int:
        sent = 0
        for schedule in self.schedules():
            schedule.send()
            sent += 1
        if not sent:
            LOG.warning(
                "no schedule found for actor=%s source=%s",
                self.settings.actor_name,
                self.settings.source_slug,
            )
        return sent


@dataclass
class Debouncer:
    """Collapse a burst of events into a single, rate-limited trigger."""

    debounce_seconds: float
    min_interval_seconds: float
    pending_since: float | None = field(default=None)
    last_fired_at: float | None = field(default=None)

    def note(self, now: float) -> None:
        if self.pending_since is None:
            self.pending_since = now

    def due(self, now: float) -> bool:
        if self.pending_since is None:
            return False
        if now - self.pending_since < self.debounce_seconds:
            return False
        if self.last_fired_at is not None:
            if now - self.last_fired_at < self.min_interval_seconds:
                return False
        return True

    def fired(self, now: float) -> None:
        self.pending_since = None
        self.last_fired_at = now


class Watcher:
    def __init__(self, settings: Settings, trigger: Trigger | None = None):
        self.settings = settings
        self.trigger = trigger if trigger is not None else Trigger(settings)
        self.cursor = self.read_cursor()
        self.reader = JournalReader(settings.event_file, self.cursor)
        self.debouncer = Debouncer(settings.debounce_seconds, settings.min_interval_seconds)
        # Held back until a trigger succeeds, so a crash between reading an
        # event and acting on it replays rather than silently swallows it.
        self.uncommitted = self.cursor
        self.health = {
            "ready": True,
            "started_at": int(time.time()),
            "cursor": self.cursor,
            "last_trigger_at": 0,
            "last_error": "",
        }

    def read_cursor(self) -> int:
        try:
            data = json.loads(self.settings.cursor_file.read_text(encoding="utf-8"))
        except (FileNotFoundError, json.JSONDecodeError, OSError):
            return 0
        seq = data.get("seq") if isinstance(data, dict) else None
        return seq if isinstance(seq, int) and seq > 0 else 0

    def write_cursor(self, seq: int) -> None:
        self.settings.cursor_file.parent.mkdir(parents=True, exist_ok=True)
        temporary = self.settings.cursor_file.with_suffix(".tmp")
        temporary.write_text(json.dumps({"seq": seq}), encoding="utf-8")
        temporary.replace(self.settings.cursor_file)
        self.cursor = seq
        self.reader.cursor = seq
        self.health["cursor"] = seq

    def write_health(self) -> None:
        try:
            self.settings.health_file.parent.mkdir(parents=True, exist_ok=True)
            temporary = self.settings.health_file.with_suffix(".tmp")
            temporary.write_text(json.dumps(self.health, sort_keys=True), encoding="utf-8")
            temporary.replace(self.settings.health_file)
        except OSError as exc:
            LOG.warning("cannot write health file: %s", exc)

    def poll(self, now: float) -> bool:
        """Read pending events and fire when the debouncer allows. Returns
        whether a trigger was sent."""
        matched = False
        for event in self.reader.events():
            seq = event["seq"]
            if seq > self.uncommitted:
                self.uncommitted = seq
            if interesting(event, self.settings):
                matched = True
                LOG.info(
                    "directory change seq=%s op=%s dn=%s attrs=%s",
                    seq,
                    event.get("op"),
                    event.get("dn"),
                    ",".join(event.get("attributes") or []),
                )
        if matched:
            self.debouncer.note(now)
        elif self.uncommitted > self.cursor and self.debouncer.pending_since is None:
            # Nothing here will ever cause a trigger, so it is safe to bank.
            self.write_cursor(self.uncommitted)

        if not self.debouncer.due(now):
            return False
        self.trigger.fire()
        self.debouncer.fired(now)
        self.health["last_trigger_at"] = int(time.time())
        self.write_cursor(self.uncommitted)
        return True

    def run(self) -> None:
        self.write_health()
        while True:
            try:
                self.poll(time.monotonic())
                self.health["ready"] = True
                self.health["last_error"] = ""
            except Exception as exc:  # noqa: BLE001 - keep following the journal
                LOG.exception("poll failed")
                self.health["ready"] = False
                self.health["last_error"] = str(exc)
                self.reader.close()
            self.write_health()
            time.sleep(self.settings.poll_seconds)


def setup_django() -> None:
    # Running a file puts that file's directory on sys.path, not the working
    # directory, so the authentik package the image installs at the filesystem
    # root is not importable from /app without saying so explicitly.
    app_root = os.environ.get("AUTHENTIK_DIRWATCH_APP_ROOT", "/")
    if app_root not in sys.path:
        sys.path.insert(0, app_root)

    import django

    os.environ.setdefault("DJANGO_SETTINGS_MODULE", "authentik.root.settings")
    django.setup()


def healthcheck(settings: Settings) -> int:
    try:
        data = json.loads(settings.health_file.read_text(encoding="utf-8"))
    except (FileNotFoundError, json.JSONDecodeError, OSError):
        return 1
    return 0 if data.get("ready") else 1


def main() -> int:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    settings = Settings.from_env()
    if "--healthcheck" in sys.argv:
        return healthcheck(settings)
    setup_django()
    LOG.info(
        "watching %s for source=%s (debounce=%ss min-interval=%ss)",
        settings.event_file,
        settings.source_slug,
        settings.debounce_seconds,
        settings.min_interval_seconds,
    )
    Watcher(settings).run()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
