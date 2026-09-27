"""Offline verdict gates for the unadapted-distribution harness."""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('unadapted', Path(__file__).with_name('server-incus-unadapted-e2e.py'))
lab = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lab)


class UnadaptedVerdicts(unittest.TestCase):
    def test_preflight_must_be_disabled_with_guide(self):
        good = {'disposition': 'disabled', 'compute_ready': False, 'blockers': ['distribution_not_adapted'],
                'manual_guide': 'https://linuxcontainers.org/incus/docs/main/installing/'}
        self.assertTrue(lab.unadapted_preflight_verdict(good))
        for change in ({'compute_ready': True}, {'disposition': 'pending'}, {'blockers': []}, {'manual_guide': ''}):
            with self.subTest(change=change):
                self.assertFalse(lab.unadapted_preflight_verdict({**good, **change}))

    def test_plan_may_only_leave_compute_disabled(self):
        good = {'disposition': 'disabled', 'compute_ready': False, 'blockers': ['distribution_not_adapted'],
                'steps': [{'id': 'unsupported', 'skipped': True}]}
        self.assertTrue(lab.unadapted_plan_verdict(good))
        with_install = {**good, 'steps': good['steps'] + [{'id': 'packages', 'skipped': False}]}
        self.assertFalse(lab.unadapted_plan_verdict(with_install))
        self.assertFalse(lab.unadapted_plan_verdict({**good, 'disposition': 'pending'}))

    def test_apply_must_record_the_unsupported_receipt(self):
        good = {'phase': 'install', 'disposition': 'disabled', 'compute_ready': False,
                'blockers': ['distribution_not_adapted'], 'receipts': [{'step': 'install.disabled', 'status': 'ok'}]}
        self.assertTrue(lab.unadapted_apply_verdict(good))
        self.assertTrue(lab.unadapted_apply_verdict({**good, 'receipts': [{'step': 'install.unsupported', 'status': 'ok'}]}))
        self.assertFalse(lab.unadapted_apply_verdict({**good, 'receipts': [{'step': 'install.packages', 'status': 'ok'}]}))
        self.assertFalse(lab.unadapted_apply_verdict({**good, 'receipts': [{'step': 'install.disabled', 'status': 'failed'}]}))
        self.assertFalse(lab.unadapted_apply_verdict({**good, 'disposition': 'installed'}))

    def test_first_tier_rows_are_the_compiled_table(self):
        self.assertEqual(lab.FIRST_TIER, {('debian', '13'), ('ubuntu', '24.04'), ('ubuntu', '26.04')})


if __name__ == '__main__':
    unittest.main()
