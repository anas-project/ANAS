import json
import tempfile
import unittest
from pathlib import Path

from event_journal import (
    DEFAULT_ATTRIBUTES,
    EventJournal,
    normalize_change,
    parse_audit_line,
    split_attributes,
)

PEOPLE = "OU=People,DC=finance,DC=hlong,DC=wang"
GROUPS = "OU=Groups,DC=finance,DC=hlong,DC=wang"


def in_scope(dn: str) -> bool:
    lowered = dn.casefold()
    return lowered.endswith("," + PEOPLE.casefold()) or lowered.endswith("," + GROUPS.casefold())


def change(**overrides) -> dict:
    body = {
        "operation": "Modify",
        "statusCode": 0,
        "status": "Success",
        "dn": "CN=APP_nextcloud,OU=Apps," + GROUPS,
        "attributes": {"member": {"actions": [{"action": "add", "values": []}]}},
    }
    body.update(overrides)
    return {"timestamp": "2026-08-08T12:46:12.072033+0800", "type": "dsdbChange", "dsdbChange": body}


class NormalizeTest(unittest.TestCase):
    def normalize(self, record, attributes=DEFAULT_ATTRIBUTES):
        return normalize_change(record, attributes, in_scope)

    def test_publishes_the_group_membership_change_that_broke_the_login(self):
        # Verbatim shape of the record Samba wrote when hailongwang was added
        # to APP_nextcloud, eight minutes before the scheduled sync picked it up.
        line = (
            '  {"timestamp": "2026-08-08T12:46:12.072033+0800", "type": "dsdbChange", '
            '"dsdbChange": {"statusCode": 0, "status": "Success", "operation": "Modify", '
            '"dn": "CN=APP_nextcloud,OU=Apps,OU=Groups,DC=finance,DC=hlong,DC=wang", '
            '"attributes": {"member": {"actions": [{"action": "add", "values": '
            '[{"value": "<GUID=b2004878-ef3d-4a7e-861e-e8e70cddfa23>;CN=hailongwang,'
            'OU=People,DC=finance,DC=hlong,DC=wang"}]}]}}}}'
        )
        event = self.normalize(parse_audit_line(line))
        self.assertEqual(event["op"], "Modify")
        self.assertEqual(event["attributes"], ["member"])
        self.assertEqual(event["ts"], "2026-08-08T12:46:12.072033+0800")
        self.assertTrue(event["dn"].startswith("CN=APP_nextcloud,"))

    def test_drops_machine_account_logon_churn(self):
        # 3958 of 3977 records on the day this was written. If this ever starts
        # being published, every subscriber gets woken every few seconds.
        record = change(
            dn="CN=SAMBAFS,CN=Computers,DC=finance,DC=hlong,DC=wang",
            attributes={
                "lastLogon": {"actions": [{"action": "replace", "values": []}]},
                "logonCount": {"actions": [{"action": "replace", "values": []}]},
            },
        )
        self.assertIsNone(self.normalize(record))

    def test_drops_watched_attribute_outside_the_configured_scope(self):
        record = change(dn="CN=SAMBAFS,CN=Computers,DC=finance,DC=hlong,DC=wang")
        self.assertIsNone(self.normalize(record))

    def test_drops_modify_that_touched_nothing_watched(self):
        record = change(attributes={"whenChanged": {"actions": []}})
        self.assertIsNone(self.normalize(record))

    def test_publishes_add_and_delete_regardless_of_attributes(self):
        for operation in ("Add", "Delete"):
            record = change(
                operation=operation,
                dn="CN=newuser," + PEOPLE,
                attributes={"whenCreated": {"actions": []}},
            )
            event = self.normalize(record)
            self.assertIsNotNone(event, operation)
            self.assertEqual(event["op"], operation)
            self.assertEqual(event["attributes"], [])

    def test_drops_failed_changes(self):
        self.assertIsNone(self.normalize(change(statusCode=53, status="Unwilling to perform")))

    def test_matches_attribute_names_case_insensitively(self):
        record = change(attributes={"MEMBEROF": {"actions": []}})
        self.assertEqual(self.normalize(record)["attributes"], ["MEMBEROF"])

    def test_honours_a_narrowed_attribute_set(self):
        self.assertIsNone(self.normalize(change(), attributes=("displayName",)))

    def test_ignores_non_change_records(self):
        self.assertIsNone(self.normalize({"type": "dsdbTransaction"}))

    def test_parse_audit_line_tolerates_prefix_and_garbage(self):
        self.assertEqual(parse_audit_line('  {"a": 1}'), {"a": 1})
        self.assertIsNone(parse_audit_line("not json at all"))
        self.assertIsNone(parse_audit_line('{"broken": '))


class JournalTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name) / "events.jsonl"

    def read(self, path=None) -> list[dict]:
        target = path or self.path
        return [json.loads(line) for line in target.read_text(encoding="utf-8").splitlines() if line]

    def test_assigns_monotonic_sequence_numbers(self):
        journal = EventJournal(self.path)
        journal.append({"op": "Add", "dn": "CN=a," + PEOPLE, "attributes": []})
        journal.append({"op": "Add", "dn": "CN=b," + PEOPLE, "attributes": []})
        self.assertEqual([record["seq"] for record in self.read()], [1, 2])

    def test_resumes_the_sequence_across_a_restart(self):
        EventJournal(self.path).append({"op": "Add", "dn": "CN=a," + PEOPLE, "attributes": []})
        self.assertEqual(EventJournal(self.path).seq, 1)

    def test_resumes_from_the_rotated_file_when_the_journal_is_fresh(self):
        # A restart landing right after a rotation must not restart at zero:
        # every subscriber cursor would then be ahead of the stream and they
        # would silently ignore events until the sequence caught back up.
        journal = EventJournal(self.path, max_bytes=200)
        for index in range(6):
            journal.append({"op": "Add", "dn": f"CN=u{index}," + PEOPLE, "attributes": []})
        self.assertTrue(journal.rotated.exists())
        self.assertGreaterEqual(EventJournal(self.path, max_bytes=200).seq, journal.seq)

    def test_rotation_preserves_the_previous_generation(self):
        journal = EventJournal(self.path, max_bytes=200)
        for index in range(6):
            journal.append({"op": "Add", "dn": f"CN=u{index}," + PEOPLE, "attributes": []})
        self.assertTrue(journal.rotated.exists())
        self.assertTrue(self.read(journal.rotated))

    def test_creates_the_directory_on_first_write(self):
        nested = Path(self.tmp.name) / "missing" / "events.jsonl"
        EventJournal(nested).append({"op": "Add", "dn": "CN=a," + PEOPLE, "attributes": []})
        self.assertTrue(nested.exists())

    def test_split_attributes_trims_and_drops_blanks(self):
        self.assertEqual(split_attributes(" member , ,memberOf "), ("member", "memberOf"))


if __name__ == "__main__":
    unittest.main()
