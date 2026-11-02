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
    def test_inflight_vm_inventory_is_not_verified_or_empty(self):
        self.assertTrue(lab.runner_inventory_ready([], 'vm'))
        row = {'name': 'anas-fj-test', 'type': 'virtual-machine', 'config': None}
        self.assertFalse(lab.runner_inventory_ready([row], 'vm'))
        self.assertTrue(lab.runner_inventory_ready([{**row, 'expanded_config': {'security.secureboot': 'true'}}], 'vm'))

    def test_actual_wrong_tier_or_insecure_vm_cannot_be_retried_into_success(self):
        for row in ({'type': 'container', 'config': {'security.secureboot': 'true'}},
                    {'type': 'virtual-machine', 'config': {'security.secureboot': 'false'}},
                    {'type': 'virtual-machine', 'config': []}, None):
            with self.subTest(row=row), self.assertRaises(RuntimeError):
                lab.runner_inventory_ready([row], 'vm')
        self.assertTrue(lab.runner_inventory_ready([{'type': 'container'}], 'container'))

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

    def test_pinned_forgejo_observation_uses_current_api_and_postgres(self):
        source = path.read_text()
        self.assertIn("row.get('commit_sha') == commit", source)
        self.assertNotIn("row.get('head_sha')", source)
        self.assertNotIn('sqlite3', source)
        self.assertIn('DB_TYPE = postgres', source)
        self.assertEqual(lab.FORGEJO_VERSION, '17.0.1')
        self.assertIn("REPO+'/actions/runs/'+str(run_id)+'/jobs'", source)
        self.assertNotIn("REPO+'/actions/runs/'+str(run_id)+'/cancel'", source)
        self.assertIn("'cancellation_mode': None if container_build else 'controller-context-SIGTERM'", source)

    def test_single_job_api_supplies_steps_and_normalizes_dates(self):
        calls = []
        def api(method, path):
            calls.append((method, path))
            if path.endswith('/runs/7/jobs'):
                return [{'id': 12}]
            return {'id': 12, 'run_id': 7, 'status': 'running', 'steps': [
                {'name': 'entered-test-payload', 'status': 'running',
                 'started': '2026-10-30T14:00:00Z', 'stopped': '0001-01-01T00:00:00Z'}]}
        rows = lab.fixture_job_steps(api, 7)
        self.assertGreater(rows[0]['started'], 0)
        self.assertEqual(rows[0]['stopped'], 0)
        self.assertEqual(rows[0]['job_status'], 'running')
        self.assertEqual(calls[-1], ('GET', lab.REPO+'/actions/jobs/12'))

    def test_org_cleanup_observes_org_and_unapproved_repo_stays_separate(self):
        calls = []
        def api(method, path):
            calls.append((method, path))
            return []
        lab.fixture_registrations(api, lab.REPO, org_scope=True)
        self.assertEqual(calls[-1], ('GET', '/api/v1/orgs/anas-onejob-org/actions/runners'))
        denied = '/api/v1/repos/'+lab.OWNER+'/unapproved'
        lab.fixture_registrations(api, denied, org_scope=True)
        self.assertEqual(calls[-1], ('GET', denied+'/actions/runners'))
        lab.fixture_registrations(api, lab.REPO)
        self.assertEqual(calls[-1], ('GET', lab.REPO+'/actions/runners'))
        with self.assertRaises(RuntimeError):
            lab.fixture_registrations(lambda *args: {}, lab.REPO, org_scope=True)

    def test_optional_api_dates_do_not_claim_payload_started(self):
        job = {'id': 12, 'run_id': 7, 'status': 'running', 'steps': [
            {'name': 'entered-test-payload', 'status': 'waiting', 'started': None, 'stopped': None}]}
        api = lambda method, path: [{'id': 12}] if path.endswith('/jobs') else job
        row = lab.fixture_job_steps(api, 7)[0]
        self.assertEqual((row['started'], row['stopped']), (0, 0))
        job['steps'][0]['started'] = '2026-10-31T01:02:03Z'
        row = lab.fixture_job_steps(api, 7)[0]
        self.assertGreater(row['started'], 0)
        self.assertEqual(row['stopped'], 0)
        del job['steps'][0]['stopped']
        with self.assertRaises(RuntimeError):
            lab.fixture_job_steps(api, 7)

    def test_job_observation_rejects_ambiguous_or_wrong_identity_and_dates(self):
        for jobs in ({}, [{'id': True}], [{'id': 1}, {'id': 2}]):
            with self.subTest(jobs=jobs), self.assertRaises(RuntimeError):
                lab.fixture_job_steps(lambda *args: jobs, 7)
        for job in ({'id': 1, 'run_id': 8},
                    {'id': 1, 'run_id': 7, 'status': 'running', 'steps': [{'name': 'payload', 'started': '2026-10-30', 'stopped': ''}]}):
            with self.subTest(job=job), self.assertRaises(RuntimeError):
                lab.fixture_job_steps(lambda method, path: [{'id': 1}] if path.endswith('/jobs') else job, 7)
        self.assertEqual(lab.fixture_job_steps(lambda *args: [], 7), [])

    def test_idle_gate_requires_full_duration_and_real_sparse_requests(self):
        hz = lab.os.sysconf('SC_CLK_TCK')
        samples = [{'at': 0, 'ticks_including_reaped_children': 0, 'rss_bytes': 32<<20},
                   {'at': 1800, 'ticks_including_reaped_children': 9*hz, 'rss_bytes': 40<<20}]
        stamps = list(range(0, 1800_000_000_001, 15_000_000_000))
        metrics = lab.validate_idle_metrics(samples, stamps, 0, 1800_000_000_000)
        self.assertEqual(metrics['duration_seconds'], 1800)
        self.assertEqual(metrics['average_single_core_cpu_percent'], .5)
        for field, value in [('at', 1799), ('rss_bytes', (128<<20)+1),
                             ('ticks_including_reaped_children', 19*hz)]:
            bad = [dict(s) for s in samples]
            bad[-1][field] = value
            with self.subTest(field=field), self.assertRaises(RuntimeError):
                lab.validate_idle_metrics(bad, stamps, 0, 1800_000_000_000)
        for timestamps in ([], [1], [15_000_000_000, 14_000_000_000],
                           [1, 5_000_000_000], list(range(0, 1800_000_000_001, 8_000_000_000))):
            with self.subTest(timestamps=timestamps[:3]), self.assertRaises(RuntimeError):
                lab.validate_idle_metrics(samples, timestamps, 0, 1800_000_000_000)

    def test_resource_sample_counts_reaped_child_cpu_and_handles_process_names(self):
        fields = ['0']*22
        fields[0] = 'S'
        for index, value in ((11, 2), (12, 3), (13, 5), (14, 7), (21, 10)):
            fields[index] = str(value)
        process = types.SimpleNamespace(pid=123, poll=lambda: None)
        with patch.object(lab.Path, 'read_text', return_value='123 (name with ) spaces) '+' '.join(fields)), \
                patch.object(lab.os, 'sysconf', return_value=4096):
            sample = lab.process_sample(process)
        self.assertEqual(sample['ticks_including_reaped_children'], 17)
        self.assertEqual(sample['rss_bytes'], 40960)
        with self.assertRaises(RuntimeError):
            lab.process_sample(types.SimpleNamespace(pid=123, poll=lambda: 1))

    def test_failed_queue_cycles_cannot_count_as_healthy_idle(self):
        self.assertTrue(lab.idle_log_healthy('=== RUN TestNativeOneJobControllerProcess\n'))
        self.assertFalse(lab.idle_log_healthy('anas-forgejo-actions: reconcile: list waiting jobs: HTTP 503\n'))

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
