"""Offline gates for the lifecycle lab owner script (no QEMU is started)."""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('owner', Path(__file__).with_name('server-incus-lifecycle-lab.py'))
owner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(owner)


class LifecycleLabOwner(unittest.TestCase):
    def test_round_names_cannot_escape_the_experiment_root(self):
        for name in ('../r1-x', 'r1-../../etc', 'r0-lab', 'x1-lab', 'r1-UPPER', 'r1-a/b'):
            with self.subTest(name=name), self.assertRaises(SystemExit):
                owner.round_dir(name)
        self.assertEqual(owner.round_dir('r12-debian13-7.0').parent, owner.BASE_ROOT)

    def test_every_stage_runs_its_own_harness(self):
        lab = {'instance_id': 'anas-incus-lifecycle-abc123'}
        container, vm, network = (owner.harness(lab, stage) for stage in owner.STAGES)
        self.assertIn('--interface container', container)
        self.assertNotIn('--vm-base-disk', container)
        self.assertIn('--interface vm', vm)
        self.assertIn('--vm-base-disk', vm)
        self.assertIn('server-incus-network-e2e.py', network)
        self.assertNotIn('--interface', network)
        for command in (container, vm, network):
            self.assertIn('anas-incus-lifecycle-abc123', command)
            self.assertTrue(command.startswith('sudo -n timeout'))

    def test_inputs_include_every_harness_and_image(self):
        for item in ('server-incus-lifecycle-e2e.py', 'server-incus-network-e2e.py', 'vm-disk.qcow2', 'ct-root.tar.xz'):
            self.assertIn(item, owner.INPUTS)


if __name__ == '__main__':
    unittest.main()
