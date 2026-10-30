import copy
import importlib.util
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "samba_dc/root/usr/local/bin/install-mail-schema.py"
loader = importlib.util.spec_from_file_location("install_mail_schema", SOURCE)
schema = importlib.util.module_from_spec(loader)
loader.loader.exec_module(schema)


class FakeDirectory:
    def __init__(self):
        self.rows = []
        self.writes = 0
        self.fail_after = None

    def context(self):
        return "CN=Schema,CN=Configuration,DC=example,DC=test"

    def find(self, item, _schema_dn):
        keys = {"ldapdisplayname": item["name"].encode(), "attributeid": item["oid"].encode(), "governsid": item["oid"].encode(), "schemaidguid": schema.uuid.UUID(item["guid"]).bytes_le}
        return [copy.deepcopy(row) for row in self.rows if any(value in row.get(key, []) for key, value in keys.items())]

    def add(self, values):
        if self.fail_after == self.writes:
            raise ValueError("mail schema directory operation failed: injected interruption")
        self.rows.append(copy.deepcopy(values))
        self.writes += 1


class MailSchemaTests(unittest.TestCase):
    def setUp(self):
        self.spec = schema.load_spec()

    def test_default_check_never_writes_and_repeated_install_is_idempotent(self):
        directory = FakeDirectory()
        with self.assertRaisesRegex(ValueError, "incomplete"):
            schema.install(directory, self.spec)
        self.assertEqual(directory.writes, 0)
        self.assertEqual(schema.install(directory, self.spec, True), 15)
        self.assertEqual(schema.install(directory, self.spec), 0)
        self.assertEqual(schema.install(directory, self.spec, True), 0)
        self.assertEqual(directory.writes, 15)

    def test_name_oid_guid_inactive_and_definition_drift_fail_before_any_write(self):
        for kind in ("name", "oid", "guid", "inactive", "syntax", "class fields", "duplicates"):
            with self.subTest(kind=kind):
                directory = FakeDirectory()
                item = self.spec["classes"][-1] if kind == "class fields" else self.spec["attributes"][-1]
                row = schema.expected(item, "classSchema" if kind == "class fields" else "attributeSchema", directory.context())
                if kind == "name": row["attributeid"] = [b"1.2.3.4"]
                if kind == "oid": row["ldapdisplayname"] = [b"OtherName"]
                if kind == "guid": row["ldapdisplayname"] = [b"OtherName"]; row["attributeid"] = [b"1.2.3.4"]
                if kind == "inactive": row["isdefunct"] = [b"TRUE"]
                if kind == "syntax": row["attributesyntax"] = [b"2.5.5.10"]
                if kind == "class fields": row["maycontain"] += [b"anasIdentityAnchor"]
                directory.rows.append(row)
                if kind == "duplicates": directory.rows.append(copy.deepcopy(row))
                with self.assertRaisesRegex(ValueError, "collision or drift"):
                    schema.install(directory, self.spec, True)
                self.assertEqual(directory.writes, 0)

    def test_interrupted_install_resumes_only_exact_existing_definitions(self):
        directory = FakeDirectory()
        directory.fail_after = 7
        with self.assertRaisesRegex(ValueError, "injected interruption"):
            schema.install(directory, self.spec, True)
        self.assertEqual(len(directory.rows), 7)
        directory.fail_after = None
        self.assertEqual(schema.install(directory, self.spec, True), 8)
        self.assertEqual(directory.writes, 15)
        self.assertEqual(schema.install(directory, self.spec), 0)

    def test_ldif_unfolding_binary_and_reference_rejection(self):
        raw = bytes(range(16))
        encoded = schema.base64.b64encode(raw).decode()
        record = schema.parse_ldif("# comment\ndn: CN=one,DC=test\nschemaIDGUID:: " + encoded[:12] + "\n " + encoded[12:] + "\n\n")
        self.assertEqual(len(record), 1)
        self.assertEqual(record[0]["schemaidguid"], [raw])
        with self.assertRaises(ValueError):
            schema.parse_ldif("dn: CN=one,DC=test\nvalue:< file:///private/key\n")
        with self.assertRaises(ValueError):
            schema.parse_ldif("dn: CN=one,DC=test\nvalue:: invalid!\n")

    def test_ldb_printable_guid_and_raw_guid_verify_the_same_schema(self):
        item = self.spec["attributes"][0]
        wanted = schema.expected(item, "attributeSchema", FakeDirectory().context())
        row = copy.deepcopy(wanted)
        row["schemaidguid"] = [item["guid"].encode()]
        self.assertTrue(schema.matches(row, wanted))
        row["schemaidguid"] = [b"11111111-1111-1111-1111-111111111111"]
        self.assertFalse(schema.matches(row, wanted))

    def test_oid_registry_and_non_login_boundary(self):
        registry = (ROOT.parents[1] / "docs/governance/oid-registry.md").read_text()
        for item in self.spec["attributes"] + self.spec["classes"]:
            self.assertIn("`" + item["oid"] + "` | `" + item["name"] + "`", registry)
        for item in self.spec["classes"]:
            fields = set(item["must"] + item["may"])
            self.assertNotIn("anasIdentityAnchor", fields)
            self.assertIn("anasMailOwnerAnchor", fields)
            self.assertIn("anasMailboxID", fields)
            self.assertFalse(fields.intersection({"sAMAccountName", "userPrincipalName", "unicodePwd", "objectSid"}))
            self.assertTrue(item["security_descriptor"].startswith("O:DAG:DAD:"))
            self.assertNotIn(";;;AU)", item["security_descriptor"])


if __name__ == "__main__":
    unittest.main()
