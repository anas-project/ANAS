"""Offline guards for the actual Core/Compose native gate."""
import importlib.util
from pathlib import Path
import unittest

SPEC = importlib.util.spec_from_file_location('core_native', Path(__file__).with_name('server-incus-core-projection-e2e.py'))
NATIVE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(NATIVE)


class CoreProjectionEvidence(unittest.TestCase):
    def events(self, expected):
        return [{'Action': action, 'Test': name} for name in expected for action in ('run', 'pass')]

    def test_exact_complete_native_runs_only(self):
        for required in (NATIVE.CORE_TESTS, NATIVE.REVOKED_TESTS):
            events = self.events(required)
            self.assertTrue(NATIVE.events_passed(events, required, 0))
            self.assertFalse(NATIVE.events_passed(events, required, 1))
            self.assertFalse(NATIVE.events_passed(events[:-1], required, 0))
            self.assertFalse(NATIVE.events_passed(events+events[-1:], required, 0))
            self.assertFalse(NATIVE.events_passed([], required, 0))

    def test_skip_failure_or_different_tests_never_substitute(self):
        events = self.events(NATIVE.CORE_TESTS)
        for extra in ({'Action': 'skip'}, {'Action': 'fail'}, {'Action': 'pass', 'Test': 'mock-core'}, 'malformed'):
            self.assertFalse(NATIVE.events_passed(events+[extra], NATIVE.CORE_TESTS, 0))


if __name__ == '__main__':
    unittest.main()
