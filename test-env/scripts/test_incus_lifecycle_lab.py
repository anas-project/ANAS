"""Offline gates for the lifecycle lab owner script (no QEMU is started)."""
import importlib.util
import json
import tempfile
from pathlib import Path
import unittest
from unittest.mock import patch

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
        container, volumes, vm, network = (owner.harness(lab, stage) for stage in owner.STAGES)
        self.assertIn('--interface container', container)
        self.assertIn('--interface volumes', volumes)
        self.assertNotIn('--vm-base-disk', volumes)
        self.assertNotIn('--vm-base-disk', container)
        self.assertIn('--interface vm', vm)
        self.assertIn('--vm-base-disk', vm)
        self.assertIn('server-incus-network-e2e.py', network)
        self.assertNotIn('--interface', network)
        for command in (container, volumes, vm, network):
            self.assertIn('anas-incus-lifecycle-abc123', command)
            self.assertTrue(command.startswith('sudo -n timeout'))

    def test_inputs_include_every_harness_and_image(self):
        for item in ('server-incus-lifecycle-e2e.py', 'server-incus-network-e2e.py', 'vm-disk.qcow2', 'ct-root.tar.xz'):
            self.assertIn(item, owner.INPUTS)


class LifecycleLabMemoryAdmission(unittest.TestCase):
    def test_free_swap_does_not_substitute_for_available_ram(self):
        info = 'MemTotal: 8388608 kB\nMemAvailable: 1048576 kB\nSwapFree: 16777216 kB\n'
        with patch.object(owner.Path, 'read_text', return_value=info), self.assertRaisesRegex(RuntimeError, 'RAM'):
            owner.require_memory_budget(2048)

    def test_available_ram_includes_room_for_the_hypervisor(self):
        info = 'MemTotal: 8388608 kB\nMemAvailable: 2621440 kB\n'
        with patch.object(owner.Path, 'read_text', return_value=info):
            owner.require_memory_budget(2048)
        info = 'MemTotal: 8388608 kB\nMemAvailable: 2097152 kB\n'
        with patch.object(owner.Path, 'read_text', return_value=info), self.assertRaises(RuntimeError):
            owner.require_memory_budget(2048)

    def test_low_memory_rejects_before_host_snapshot_or_qemu_start(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for name in ('vm', 'reports', 'src'):
                (root/name).mkdir()
            (root/'lab.json').write_text(json.dumps({'root': str(root), 'instance_id': 'anas-incus-lifecycle-abc123', 'memory_mib': 3584}))
            (root/'src/manifest.json').write_text(json.dumps({'artifacts': {}}))
            with patch.object(owner, 'round_dir', return_value=root), \
                    patch.object(owner.subprocess, 'run', return_value=owner.subprocess.CompletedProcess([], 1)), \
                    patch.object(owner, 'require_memory_budget', side_effect=RuntimeError('insufficient available RAM')), \
                    patch.object(owner, 'host_baseline') as baseline, \
                    patch.object(owner.subprocess, 'Popen', side_effect=AssertionError('QEMU must not be started')) as start:
                with self.assertRaisesRegex(RuntimeError, 'RAM'):
                    owner.run('r1-memory', 'memory-admission')
                baseline.assert_not_called()
                start.assert_not_called()

    def test_missing_memory_information_fails_closed(self):
        with patch.object(owner.Path, 'read_text', return_value='SwapFree: 16777216 kB\n'), self.assertRaises(RuntimeError):
            owner.require_memory_budget(2048)


if __name__ == '__main__':
    unittest.main()
