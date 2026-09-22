"""Offline admission checks for the fixed native image fixture replayer."""
import importlib.util
from pathlib import Path
import tempfile
import types
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('image_replay', Path(__file__).with_name('replay-incus-image-native.py'))
lab = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lab)


class ReplaySafety(unittest.TestCase):
    def test_wrong_vm_precedes_private_input_and_effects(self):
        with patch.object(lab.lab, 'require_vm', side_effect=RuntimeError('wrong VM')), patch.object(lab, 'private') as read, patch.object(lab.subprocess, 'run') as run:
            with self.assertRaises(RuntimeError):
                lab.main(types.SimpleNamespace(vm_id='wrong'))
            read.assert_not_called()
            run.assert_not_called()

    def test_unknown_case_has_no_effects(self):
        with patch.object(lab.lab, 'require_vm'), patch.object(lab, 'private') as read, patch.object(lab.subprocess, 'run') as run:
            with self.assertRaises(RuntimeError):
                lab.main(types.SimpleNamespace(vm_id='anas-runner-bake-abc123', case='shell'))
            read.assert_not_called()
            run.assert_not_called()

    def test_private_reader_rejects_links_and_broad_modes(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root/'fixture'; source.write_bytes(b'not-a-secret'); source.chmod(0o644)
            with self.assertRaises(RuntimeError):
                lab.private(source)
            link = root/'link'; link.symlink_to(source)
            with self.assertRaises(RuntimeError):
                lab.private(link)


if __name__ == '__main__':
    unittest.main()
