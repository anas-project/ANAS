import json
import tempfile
import unittest
from pathlib import Path

from directory_watch import Debouncer, JournalReader, Settings, Watcher, interesting, split_csv

PEOPLE = "OU=People,DC=finance,DC=hlong,DC=wang"


class FakeTrigger:
    def __init__(self):
        self.fires = 0

    def fire(self) -> int:
        self.fires += 1
        return 1


def settings(root: Path, **overrides) -> Settings:
    values = dict(
        event_file=root / "events.jsonl",
        cursor_file=root / "cursor.json",
        health_file=root / "health.json",
        source_slug="samba-ad",
        actor_name="authentik.sources.ldap.tasks.ldap_sync",
        operations=("Add", "Modify", "Delete"),
        attributes=("member", "memberOf", "userAccountControl", "sAMAccountName"),
        debounce_seconds=5.0,
        min_interval_seconds=60.0,
        poll_seconds=1.0,
    )
    values.update(overrides)
    return Settings(**values)


def event(seq: int, **overrides) -> dict:
    record = {
        "seq": seq,
        "ts": "2026-08-08T12:46:12.072033+0800",
        "op": "Modify",
        "dn": "CN=APP_nextcloud,OU=Apps,OU=Groups,DC=finance,DC=hlong,DC=wang",
        "attributes": ["member"],
    }
    record.update(overrides)
    return record


class InterestTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.settings = settings(Path(self.tmp.name))

    def test_membership_change_is_interesting(self):
        self.assertTrue(interesting(event(1), self.settings))

    def test_cosmetic_attribute_is_not_worth_a_full_sync(self):
        self.assertFalse(interesting(event(1, attributes=["displayName"]), self.settings))

    def test_add_and_delete_bypass_the_attribute_filter(self):
        for operation in ("Add", "Delete"):
            self.assertTrue(interesting(event(1, op=operation, attributes=[]), self.settings))

    def test_operation_filter_is_honoured(self):
        narrowed = settings(Path(self.tmp.name), operations=("Delete",))
        self.assertFalse(interesting(event(1), narrowed))

    def test_empty_attribute_filter_accepts_anything_published(self):
        wide = settings(Path(self.tmp.name), attributes=())
        self.assertTrue(interesting(event(1, attributes=["displayName"]), wide))

    def test_split_csv_trims_and_drops_blanks(self):
        self.assertEqual(split_csv(" Add , ,Delete "), ("Add", "Delete"))


class DebouncerTest(unittest.TestCase):
    def test_waits_for_the_window_to_close(self):
        debouncer = Debouncer(5.0, 60.0)
        debouncer.note(100.0)
        self.assertFalse(debouncer.due(103.0))
        self.assertTrue(debouncer.due(105.0))

    def test_a_burst_collapses_into_one_trigger(self):
        debouncer = Debouncer(5.0, 60.0)
        for moment in (100.0, 100.5, 101.0, 102.0):
            debouncer.note(moment)
        self.assertTrue(debouncer.due(105.0))
        debouncer.fired(105.0)
        self.assertFalse(debouncer.due(106.0))

    def test_minimum_interval_throttles_consecutive_triggers(self):
        debouncer = Debouncer(5.0, 60.0)
        debouncer.note(100.0)
        debouncer.fired(105.0)
        debouncer.note(106.0)
        self.assertFalse(debouncer.due(120.0))
        self.assertTrue(debouncer.due(166.0))


class JournalReaderTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name) / "events.jsonl"

    def reader(self, cursor: int) -> JournalReader:
        instance = JournalReader(self.path, cursor)
        self.addCleanup(instance.close)
        return instance

    def write(self, *records, path=None):
        target = path or self.path
        with target.open("a", encoding="utf-8") as handle:
            for record in records:
                handle.write(json.dumps(record) + "\n")

    def test_missing_journal_is_not_an_error(self):
        self.assertEqual(self.reader(0).events(), [])

    def test_reads_only_records_past_the_cursor(self):
        self.write(event(1), event(2), event(3))
        reader = self.reader(2)
        self.assertEqual([record["seq"] for record in reader.events()], [3])

    def test_follows_appends(self):
        self.write(event(1))
        reader = self.reader(0)
        self.assertEqual(len(reader.events()), 1)
        self.write(event(2))
        self.assertEqual([record["seq"] for record in reader.events()], [2])

    def test_survives_rotation_without_replaying(self):
        self.write(event(1), event(2))
        reader = self.reader(0)
        reader.cursor = max(record["seq"] for record in reader.events())
        self.path.replace(self.path.with_name(self.path.name + ".1"))
        self.write(event(3))
        self.assertEqual([record["seq"] for record in reader.events()], [3])

    def test_skips_malformed_lines(self):
        self.write(event(1))
        with self.path.open("a", encoding="utf-8") as handle:
            handle.write("{not json\n")
        self.write(event(2))
        self.assertEqual([record["seq"] for record in self.reader(0).events()], [1, 2])


class WatcherTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.settings = settings(self.root)
        self.trigger = FakeTrigger()

    def write(self, *records):
        with self.settings.event_file.open("a", encoding="utf-8") as handle:
            for record in records:
                handle.write(json.dumps(record) + "\n")

    def watcher(self) -> Watcher:
        instance = Watcher(self.settings, trigger=self.trigger)
        self.addCleanup(instance.reader.close)
        return instance

    def test_a_membership_burst_produces_exactly_one_sync(self):
        # authentik has no per-user refresh, so five members added at once must
        # not become five full source syncs.
        self.write(*(event(seq) for seq in range(1, 6)))
        watcher = self.watcher()
        self.assertFalse(watcher.poll(100.0))
        self.assertTrue(watcher.poll(106.0))
        self.assertEqual(self.trigger.fires, 1)

    def test_cursor_is_only_committed_after_a_successful_trigger(self):
        self.write(event(1))
        watcher = self.watcher()
        watcher.poll(100.0)
        self.assertEqual(watcher.read_cursor(), 0)
        watcher.poll(106.0)
        self.assertEqual(watcher.read_cursor(), 1)

    def test_uninteresting_events_advance_the_cursor_without_a_sync(self):
        self.write(event(1, attributes=["displayName"]))
        watcher = self.watcher()
        self.assertFalse(watcher.poll(100.0))
        self.assertEqual(self.trigger.fires, 0)
        self.assertEqual(watcher.read_cursor(), 1)

    def test_a_restart_does_not_replay_committed_events(self):
        self.write(event(1))
        first = self.watcher()
        first.poll(100.0)
        first.poll(106.0)
        self.assertEqual(self.trigger.fires, 1)

        second = self.watcher()
        second.poll(200.0)
        second.poll(206.0)
        self.assertEqual(self.trigger.fires, 1)

    def test_a_restart_replays_an_event_that_never_triggered(self):
        # The cursor is held back on purpose: a crash between reading an event
        # and acting on it must re-deliver rather than swallow it.
        self.write(event(1))
        crashed = self.watcher()
        crashed.poll(100.0)
        self.assertEqual(crashed.read_cursor(), 0)

        restarted = self.watcher()
        restarted.poll(200.0)
        restarted.poll(206.0)
        self.assertEqual(self.trigger.fires, 1)

    def test_second_burst_is_throttled_by_the_minimum_interval(self):
        self.write(event(1))
        watcher = self.watcher()
        watcher.poll(100.0)
        watcher.poll(106.0)
        self.write(event(2))
        watcher.poll(110.0)
        self.assertFalse(watcher.poll(120.0))
        self.assertTrue(watcher.poll(170.0))
        self.assertEqual(self.trigger.fires, 2)

    def test_health_file_reports_the_cursor(self):
        self.write(event(1))
        watcher = self.watcher()
        watcher.poll(100.0)
        watcher.poll(106.0)
        watcher.write_health()
        health = json.loads(self.settings.health_file.read_text(encoding="utf-8"))
        self.assertTrue(health["ready"])
        self.assertEqual(health["cursor"], 1)


if __name__ == "__main__":
    unittest.main()
