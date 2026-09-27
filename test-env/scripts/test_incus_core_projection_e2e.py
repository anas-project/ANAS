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
        for required in (NATIVE.CORE_TESTS, NATIVE.CLEANUP_TESTS, NATIVE.REVOKED_TESTS):
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


class PruneVerdict(unittest.TestCase):
    STATE = {'pin_r1': 'a' * 64, 'pin_r2': 'b' * 64}

    def planned(self, delete=None):
        return {'delete': delete if delete is not None else [{'project': 'anas-core-one', 'fingerprint': 'b' * 64}],
                'retained': [{'project': 'anas-core-one', 'fingerprint': 'a' * 64, 'reasons': ['current_deployment', 'previous_deployment']},
                             {'project': 'anas-core-two', 'fingerprint': 'a' * 64, 'reasons': ['previous_deployment']}]}

    def applied(self, **change):
        return {'deleted': [{'project': 'anas-core-one', 'fingerprint': 'b' * 64, 'verified': True}], 'partial': False, **change}

    def test_only_the_failed_revision_may_be_deleted(self):
        self.assertTrue(NATIVE.prune_verdicts(self.STATE, self.planned(), self.applied()))
        # Deleting a rollback target, or nothing, or an unverified delete is a failure.
        self.assertFalse(NATIVE.prune_verdicts(self.STATE, self.planned([{'project': 'anas-core-one', 'fingerprint': 'a' * 64}]), self.applied()))
        self.assertFalse(NATIVE.prune_verdicts(self.STATE, self.planned([]), self.applied()))
        self.assertFalse(NATIVE.prune_verdicts(self.STATE, self.planned(), self.applied(partial=True)))
        unverified = self.applied(deleted=[{'project': 'anas-core-one', 'fingerprint': 'b' * 64, 'verified': False}])
        self.assertFalse(NATIVE.prune_verdicts(self.STATE, self.planned(), unverified))

    def test_rollback_targets_must_be_named_as_retained(self):
        planned = self.planned()
        planned['retained'] = planned['retained'][:1]
        self.assertFalse(NATIVE.prune_verdicts(self.STATE, planned, self.applied()))

    def test_only_the_core_workspace_is_registered(self):
        self.assertEqual(NATIVE.CORE_WORKSPACES, (('native', '/srv/anas/host-action-native'),))


if __name__ == '__main__':
    unittest.main()
