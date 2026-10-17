#!/usr/bin/env python3
# TEST_CASES: PORTAL-T-006
"""Exercise provisioning guards without importing Samba or writing a directory."""
import ast
import pathlib
import unittest


class Row(dict):
    dn = 'CN=svc_portal_profile,OU=Service Accounts,DC=example'


class WriterBoundaryTests(unittest.TestCase):
    def setUp(self):
        source = pathlib.Path(__file__).with_name('provision-directory.py').read_text()
        tree = ast.parse(source)
        guard = next(node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == 'validate_writer')
        scope = {
            'under': lambda dn, parent: dn.endswith(',' + parent),
            'service_base': 'OU=Service Accounts,DC=example',
            'owned': 'ANAS user portal scoped writer',
        }
        exec(compile(ast.Module(body=[guard], type_ignores=[]), source, 'exec'), scope)
        self.guard = scope['validate_writer']
        self.row = Row(description=[scope['owned']], primaryGroupID=['513'], userAccountControl=['512'])

    def test_ordinary_writer(self):
        self.guard(self.row)

    def test_primary_domain_role_without_memberof_is_rejected(self):
        for rid in ['512', '519', '9999']:
            with self.subTest(rid=rid), self.assertRaises(AssertionError):
                self.guard(Row(self.row, primaryGroupID=[rid]))

    def test_additional_group_is_rejected(self):
        with self.assertRaises(AssertionError):
            self.guard(Row(self.row, memberOf=['CN=Domain Admins,DC=example']))

    def test_trust_and_computer_subjects_are_rejected(self):
        for flags in [2048, 4096, 8192, 512 | 8192]:
            with self.subTest(flags=flags), self.assertRaises(AssertionError):
                self.guard(Row(self.row, userAccountControl=[str(flags)]))

    def test_unowned_account_is_rejected(self):
        with self.assertRaises(AssertionError):
            self.guard(Row(self.row, description=['unrelated account']))


if __name__ == '__main__':
    unittest.main()
