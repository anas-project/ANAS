"""Offline admission and process ownership checks for the one-job fixture."""
import ast
import importlib.util
from pathlib import Path
import types
import unittest
from unittest.mock import patch

path = Path(__file__).with_name('server-forgejo-onejob-e2e.py')
spec = importlib.util.spec_from_file_location('onejob_fixture', path)
lab = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lab)


class OneJobFixtureSafety(unittest.TestCase):
    def test_guest_trust_must_use_the_product_stdin_path(self):
        source = path.read_text()
        controller = (path.parents[2] / 'modules/forgejo/actions-controller/onejob_native_linux_test.go').read_text()
        self.assertIn('cfg.RunnerTrustPEM, err = normalizeRunnerTrust(publicCA, time.Now())', controller)
        self.assertNotIn('nativePublicTrustCompute', controller)
        self.assertNotIn('"file", "push"', controller)
        self.assertNotIn('"/usr/sbin/update-ca-certificates"', controller)
        self.assertIn("'public_trust_via_controller_stdin': True", source)
        self.assertIn("'guest_system_trust_modified': False", source)
        self.assertNotIn("'public_fixture_ca_installed': True", source)

    def test_pinned_forgejo_observation_uses_real_fields_and_readonly_steps(self):
        source = path.read_text()
        self.assertIn("row.get('commit_sha') == commit", source)
        self.assertNotIn("row.get('head_sha')", source)
        self.assertIn("'?mode=ro'", source)
        self.assertIn('WHERE j.run_id=? AND t.id=j.task_id', source)
        self.assertNotIn("REPO+'/actions/runs/'+str(run_id)+'/jobs'", source)
        self.assertNotIn("REPO+'/actions/runs/'+str(run_id)+'/cancel'", source)
        self.assertIn("'cancellation_mode': 'controller-context-SIGTERM'", source)

    def test_terminal_results_need_explicit_consistent_status(self):
        for state in ('success', 'failure', 'cancelled'):
            self.assertEqual(lab.workflow_result({'status': state}), state)
            self.assertEqual(lab.workflow_result({'status': 'completed', 'conclusion': state}), state)
        self.assertIsNone(lab.workflow_result({'status': 'waiting', 'conclusion': None}))
        self.assertIsNone(lab.workflow_result({'status': 'running'}))
        for value in (None, {}, {'status': 'completed'}, {'status': 'waiting', 'conclusion': 'success'},
                      {'status': 'success', 'conclusion': 'failure'}, {'status': 'unknown'}, {'status': []}):
            with self.subTest(value=value), self.assertRaises(RuntimeError):
                lab.workflow_result(value)

    def test_workflows_are_closed_and_do_not_expose_guest_token(self):
        for name in ('normal', 'failure', 'cancel', 'crash', 'unapproved'):
            source = lab.workflow_source(name)
            self.assertIn('test ! -e /run/anas-actions-token/runner-token', source)
            self.assertIn('runs-on: anas-native', source)
            self.assertNotIn(':host', source)
        self.assertIn('exit 23', lab.workflow_source('failure'))
        self.assertIn('sleep 120', lab.workflow_source('cancel'))
        with self.assertRaises(RuntimeError):
            lab.workflow_source('arbitrary')

    def test_missing_git_is_rejected_without_process_or_network(self):
        with patch.object(lab.shutil, 'which', side_effect=lambda name, **kw: None if name == 'git' else '/usr/bin/'+name), \
                patch.object(lab.subprocess, 'run') as run, patch.object(lab.Path, 'mkdir') as mkdir:
            with self.assertRaisesRegex(RuntimeError, 'git'):
                lab.require_test_programs()
            run.assert_not_called()
            mkdir.assert_not_called()

    def test_test_program_admission_does_not_install_packages(self):
        with patch.object(lab.shutil, 'which', side_effect=lambda name, **kw: '/usr/bin/'+name) as which, \
                patch.object(lab.subprocess, 'run') as run:
            lab.require_test_programs()
            self.assertIn('git', [call.args[0] for call in which.call_args_list])
            run.assert_not_called()

    def test_vm_rejection_precedes_paths_processes_and_network(self):
        with patch.object(lab.image_lab, 'require_vm', side_effect=RuntimeError('wrong VM')), \
                patch.object(lab.Path, 'mkdir') as mkdir, patch.object(lab.subprocess, 'Popen') as spawn, \
                patch.object(lab.socket, 'socket') as connect:
            with self.assertRaises(RuntimeError):
                lab.run(types.SimpleNamespace(vm_id='wrong'))
            mkdir.assert_not_called()
            spawn.assert_not_called()
            connect.assert_not_called()

    def test_report_outside_dedicated_owner_directory_is_rejected(self):
        with patch.object(lab.image_lab, 'require_vm'), patch.object(lab.Path, 'mkdir') as mkdir, \
                patch.object(lab.subprocess, 'run') as command, patch.object(lab.socket, 'socket') as connect:
            with self.assertRaises(RuntimeError):
                lab.run(types.SimpleNamespace(vm_id='anas-runner-bake-abc123', report_root='/etc/onejob'))
            mkdir.assert_not_called()
            command.assert_not_called()
            connect.assert_not_called()

    def test_controller_owns_actual_child_not_a_decoder_or_shell(self):
        tree = ast.parse(path.read_text())
        spawn = [node for node in ast.walk(tree) if isinstance(node, ast.Call)
                 and isinstance(node.func, ast.Attribute) and node.func.attr == 'Popen']
        self.assertEqual(len(spawn), 2)
        first_arguments = [node.args[0].elts[0] for node in spawn]
        self.assertEqual({node.attr for node in first_arguments}, {'tests', 'forgejo'})
        self.assertTrue(all(isinstance(node.value, ast.Name) and node.value.id == 'args' for node in first_arguments))
        for node in spawn:
            self.assertNotIn('shell', [kw.arg for kw in node.keywords])

    def test_fixture_endpoints_and_scope_are_fixed(self):
        self.assertEqual(lab.BASE, 'https://10.0.2.15:13001')
        self.assertEqual(lab.PROJECT, 'anas-onejob-fixture')
        self.assertEqual(lab.POOL, 'anas-onejob-btrfs')
        self.assertEqual(lab.REPO, '/api/v1/repos/anas-onejob-lab/onejob')
        self.assertNotIn(':host', lab.LABEL)
        self.assertIn('busybox@sha256:', lab.LABEL)
        self.assertEqual(len(lab.LABEL.rsplit(':', 1)[1]), 64)


if __name__ == '__main__':
    unittest.main()
