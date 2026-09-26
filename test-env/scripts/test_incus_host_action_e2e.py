"""Unit checks for the VM-only installed-host-action acceptance runner."""
import importlib.util
import inspect
import json
from pathlib import Path
import sys
import unittest
from unittest import mock
import ssl

SPEC = importlib.util.spec_from_file_location(
    'installed_host_action_native',
    Path(__file__).with_name('server-incus-host-action-e2e.py'))
NATIVE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = NATIVE
SPEC.loader.exec_module(NATIVE)


class HostActionNativeContract(unittest.TestCase):
    def test_only_exact_disposable_vm_identity_is_accepted(self):
        facts = {'uid': 0, 'vendor': 'QEMU', 'instance': 'anas-incus-host-abcdef',
                 'kernel': 'Linux', 'docker_root': '/var/lib/anas-host-provision-test',
                 'containers': []}
        NATIVE.validate_environment('anas-incus-host-abcdef', facts)
        for field, value in [('uid', 1000), ('vendor', 'Dell Inc.'),
                             ('instance', 'anas-incus-host-123456'),
                             ('kernel', 'Darwin'), ('docker_root', '/var/lib/docker'),
                             ('containers', [{'Id': 'existing-business-container'}])]:
            with self.subTest(field=field), self.assertRaises(NATIVE.GateFailure):
                NATIVE.validate_environment('anas-incus-host-abcdef', {**facts, field: value})
        for identity in ('', 'i-production', 'anas-incus-host-abcdef/../other',
                         'anas-incus-host-abcdef\n'):
            with self.subTest(identity=identity), self.assertRaises(NATIVE.GateFailure):
                NATIVE.validate_environment(identity, facts)

    def test_lab_release_never_claims_formal_release(self):
        NATIVE.validate_lab_release({'version': '0.0.0-native.20260923.1', 'commit': 'a' * 40})
        for release in ({'version': 'dev', 'commit': 'unknown'},
                        {'version': '1.2.3', 'commit': 'a' * 40},
                        {'version': '0.0.0-native.1', 'commit': 'a' * 39},
                        {'version': '0.0.0-native.1', 'commit': 'A' * 40}):
            with self.subTest(release=release), self.assertRaises(NATIVE.GateFailure):
                NATIVE.validate_lab_release(release)

    def test_required_gate_set_is_not_replaced_by_a_successful_command(self):
        events = [{'stage': stage, 'status': 'passed'} for stage in NATIVE.REQUIRED]
        self.assertTrue(NATIVE.passed(events, True))
        self.assertFalse(NATIVE.passed(events[:-1], True))
        self.assertFalse(NATIVE.passed(events + events[:1], True))
        self.assertFalse(NATIVE.passed(events, False))
        self.assertFalse(NATIVE.passed([*events[:-1], {'stage': events[-1]['stage'], 'status': 'skipped'}], True))
        self.assertFalse(NATIVE.passed([{'stage': 'local-unit-tests', 'status': 'passed'}], True))

    def test_installation_policy_matches_compiled_canonical_key_order(self):
        # Source manifests are normally saved with sort_keys=True. Reusing
        # their nested release object directly produces commit-before-version,
        # which the actual compiled installation decoder correctly rejects.
        release = json.loads(json.dumps({'version': '0.0.0-native.1', 'commit': 'a' * 40}, sort_keys=True))
        actual = NATIVE.installation_policy_bytes(release)
        expected = ('{"schema":"anas.host-action-installation/v2",'
                    '"release":{"version":"0.0.0-native.1","commit":"' + 'a' * 40 + '"},'
                    '"service_mode":"systemd-root-service","service_unit":"anasd.service",'
                    '"socket_gid":0}\n').encode()
        self.assertEqual(actual, expected)
        self.assertEqual(list(release), ['commit', 'version'])

    def test_public_diagnostics_do_not_echo_responses_or_credentials(self):
        failure = NATIVE.GateFailure('http_status', 'SECRET_PRIVATE_RESPONSE')
        self.assertNotIn('SECRET', str(failure))
        self.assertEqual(NATIVE.public_failure(failure), {'code': 'http_status'})
        self.assertEqual(NATIVE.public_failure(RuntimeError('PRIVATE KEY SECRET')),
                         {'code': 'unexpected_failure'})

    def test_secret_scan_checks_exact_tokens_as_well_as_private_keys(self):
        NATIVE.check_public_response({'job': {'status': 'succeeded'}}, ['opaque-token'])
        for value in ({'value': 'opaque-token'}, {'value': '-----BEGIN PRIVATE KEY-----'}):
            with self.assertRaises(NATIVE.GateFailure):
                NATIVE.check_public_response(value, ['opaque-token'])
        self.assertEqual(json.loads(json.dumps(NATIVE.REQUIRED)), list(NATIVE.REQUIRED))

    def test_owner_enrollment_requires_the_actual_created_response(self):
        client = NATIVE.Console.__new__(NATIVE.Console)
        client.cookies, client.csrf, client.credentials = {}, '', []
        visited = []

        def expected(method, path, body=None, **options):
            visited.append(path)
            if path.endswith('/csrf'):
                return {'csrf_token': 'csrf-fixture'}
            if path.endswith('/bootstrap/exchange'):
                return {'state': 'enrollment', 'csrf_token': 'bootstrap-csrf'}
            if path.endswith('/handoffs'):
                return {'target_origin': NATIVE.ORIGIN, 'handoff': 'handoff-fixture'}
            if path.endswith('/enrollment/exchange'):
                client.cookies['__Host-anas_enrollment_csrf'] = 'owner-csrf'
                return {}
            if path.endswith('/enrollment/owner'):
                self.assertEqual(options.get('status', 200), 201)
                return {'state': 'full'}
            if path.endswith('/login'):
                client.cookies['__Host-anas_local_session'] = 'session-fixture'
                return {'state': 'full', 'csrf_token': 'session-csrf'}
            self.fail('unexpected enrollment step')

        client.expected = expected
        client.enroll('bootstrap-fixture')
        self.assertIn('/api/v1/auth/enrollment/owner', visited)
        self.assertEqual(client.csrf, 'session-csrf')

    def test_confirmation_negatives_require_the_actual_public_error(self):
        NATIVE.require_confirmation_rejection(400, {'code': 'invalid_json'}, replay=False)
        NATIVE.require_confirmation_rejection(409, {'code': 'confirmation_consumed'}, replay=True)
        for status, body, replay in (
                (201, {'token': 'unexpected-grant'}, False),
                (400, {'code': 'invalid_json', 'token': 'unexpected-grant'}, False),
                (503, {'code': 'host_actions_unavailable'}, False),
                (403, {'code': 'forbidden'}, False),
                (409, {'code': 'confirmation_expired'}, True),
                (400, {'code': 'invalid_json'}, True)):
            with self.subTest(status=status, replay=replay), self.assertRaises(NATIVE.GateFailure):
                NATIVE.require_confirmation_rejection(status, body, replay=replay)

    def test_problem_json_keeps_the_rejection_code(self):
        raw = b'{"status":400,"code":"invalid_json"}'
        for media in ('application/json', 'application/problem+json',
                      'Application/Problem+JSON; charset=utf-8'):
            self.assertEqual(NATIVE.decode_http_response(raw, media)['code'], 'invalid_json')
        self.assertEqual(NATIVE.decode_http_response(b'', ''), {})
        for raw, media in ((b'<html>failure</html>', 'text/html'),
                           (b'{}', 'application/json-unexpected'),
                           (b'null', 'application/json'), (b'[]', 'application/json'),
                           (b'{PRIVATE', 'application/problem+json')):
            with self.subTest(media=media), self.assertRaises(NATIVE.GateFailure):
                NATIVE.decode_http_response(raw, media)

    def test_restart_readiness_retries_only_a_read_and_not_tls_validation(self):
        client = NATIVE.Console.__new__(NATIVE.Console)
        client.expected = mock.Mock(side_effect=[ConnectionRefusedError(), {'job': {'id': 'job_fixture'}}])
        with mock.patch.object(NATIVE.time, 'sleep'):
            self.assertEqual(client.ready_get('/api/v1/jobs/job_fixture')['job']['id'], 'job_fixture')
        self.assertEqual(client.expected.call_count, 2)
        for call in client.expected.call_args_list:
            self.assertEqual(call.args, ('GET', '/api/v1/jobs/job_fixture'))
        client.expected = mock.Mock(side_effect=ssl.SSLCertVerificationError('private certificate diagnostic'))
        with mock.patch.object(NATIVE.time, 'sleep') as sleep:
            with self.assertRaises(ssl.SSLCertVerificationError):
                client.ready_get('/api/v1/jobs/job_fixture')
            sleep.assert_not_called()

    def test_control_probe_container_cannot_inherit_host_privileges(self):
        container = {'Id': 'b' * 64, 'Image': 'sha256:' + 'a' * 64,
                     'Config': {'User': '65534:65534', 'Labels': {'dev.anas.native-control': 'anas-incus-host-abcdef'}},
                     'HostConfig': {'Privileged': False, 'ReadonlyRootfs': True, 'CapAdd': None, 'CapDrop': ['ALL'],
                                    'Binds': None, 'PidMode': '', 'NetworkMode': 'c' * 64,
                                    'SecurityOpt': ['no-new-privileges:true']},
                     'Mounts': [], 'State': {'Running': False, 'Status': 'created', 'Pid': 0},
                     'NetworkSettings': {'Networks': {'anas-incus-control': {'NetworkID': 'c' * 64}}}}
        args = ('b' * 64, 'sha256:' + 'a' * 64, 'anas-incus-host-abcdef', 'c' * 64)
        NATIVE.validate_probe_container(container, *args)
        pending = {**container, 'NetworkSettings': {'Networks': {'anas-incus-control': {'NetworkID': ''}}}}
        NATIVE.validate_probe_container(pending, *args)
        for state in ({'Running': True, 'Status': 'running', 'Pid': 123},
                      {'Running': False, 'Status': 'exited', 'Pid': 0},
                      {'Running': False, 'Status': 'created', 'Pid': 123}):
            with self.subTest(state=state), self.assertRaises(NATIVE.GateFailure):
                NATIVE.validate_probe_container({**pending, 'State': state}, *args)
        for field, value in [('Privileged', True), ('ReadonlyRootfs', False), ('CapAdd', ['NET_ADMIN']),
                             ('CapDrop', []), ('PidMode', 'host'), ('Binds', ['/run/docker.sock:/run/docker.sock']),
                             ('SecurityOpt', []), ('NetworkMode', 'host')]:
            changed = {**container, 'HostConfig': {**container['HostConfig'], field: value}}
            with self.subTest(field=field), self.assertRaises(NATIVE.GateFailure):
                NATIVE.validate_probe_container(changed, *args)
        for field, value in [('Id', 'd' * 64), ('Image', 'sha256:' + 'd' * 64),
                             ('Mounts', [{'Source': '/run/docker.sock'}]),
                             ('NetworkSettings', {'Networks': {}}),
                             ('Config', {'User': '0', 'Labels': container['Config']['Labels']})]:
            with self.subTest(field=field), self.assertRaises(NATIVE.GateFailure):
                NATIVE.validate_probe_container({**container, field: value}, *args)

    def test_control_probe_positive_and_negative_results_are_distinct(self):
        for mode in ('trusted', 'network_blocked', 'pin_rejected', 'untrusted'):
            good = {'schema': 'anas.control-probe/v1', 'mode': mode, 'passed': True}
            NATIVE.validate_probe_result(good, mode)
            for bad in ({}, {**good, 'passed': False}, {**good, 'mode': 'other'},
                        {**good, 'certificate': 'private-fixture'}):
                with self.subTest(mode=mode), self.assertRaises(NATIVE.GateFailure):
                    NATIVE.validate_probe_result(bad, mode)

    def test_real_expiry_uses_the_five_minute_plan_deadline(self):
        target = NATIVE.confirmation_expiry_target('2026-09-23T00:00:00Z', '2026-09-23T00:05:00Z')
        self.assertIsInstance(target, float)
        for expires in ('2026-09-23T00:04:59Z', '2026-09-23T00:06:00Z',
                        '2026-09-23T00:05:00', 'bad-private-input'):
            with self.subTest(expires=expires), self.assertRaises(NATIVE.GateFailure):
                NATIVE.confirmation_expiry_target('2026-09-23T00:00:00Z', expires)
        NATIVE.require_expired_rejection(409, {'code': 'confirmation_expired'})
        for status, body in ((409, {'code': 'confirmation_consumed'}),
                             (503, {'code': 'host_actions_unavailable'}),
                             (409, {'code': 'confirmation_expired', 'token': 'new-grant'})):
            with self.subTest(body=body), self.assertRaises(NATIVE.GateFailure):
                NATIVE.require_expired_rejection(status, body)

    def test_distribution_run_cannot_treat_disabled_or_partial_as_installation(self):
        for phase, disposition in (('install', 'installed'), ('configure', 'configured'),
                                   ('enroll', 'connection_ready'), ('uninstall', 'uninstalled')):
            result = {'schema': 'anas.incus-host-provision/v1', 'phase': phase,
                      'disposition': disposition, 'compute_ready': False}
            NATIVE.validate_phase_result(result, phase, skip=False)
            for update in ({'disposition': 'disabled'}, {'disposition': 'partial'},
                           {'phase': 'other'}, {'compute_ready': True}, {'schema': 'wrong'}):
                with self.subTest(phase=phase, update=update), self.assertRaises(NATIVE.GateFailure):
                    NATIVE.validate_phase_result({**result, **update}, phase, skip=False)
        NATIVE.validate_phase_result({'schema': 'anas.incus-host-provision/v1', 'phase': 'install',
                                     'disposition': 'disabled', 'compute_ready': False}, 'install', skip=True)

    def test_effect_diagnostic_projects_only_fixed_stage_identifiers(self):
        private = {'schema': 'anas.incus-host-state/v1', 'disabled': True,
                   'credential': {'private_key_pem': 'PRIVATE KEY secret-key'},
                   'bundle': {'endpoint': 'private.example', 'token': 'secret-token'},
                   'intents': [{'phase': 'enroll', 'step': 'enroll.trust', 'status': 'failed',
                                'digest': 'secret-digest', 'extra': 'secret-token'}]}
        projected = NATIVE.public_effect_summary(private)
        self.assertEqual(projected, {'schema': 'anas.native-effect-summary/v1', 'disabled': True,
                                    'effects': [{'phase': 'enroll', 'step': 'enroll.trust', 'status': 'failed'}]})
        self.assertNotIn('secret', json.dumps(projected))
        self.assertNotIn('private.example', json.dumps(projected))
        for update in ({'step': 'enroll.secret-token'}, {'phase': 'unknown'}, {'status': 'secret-token'}):
            with self.subTest(update=update), self.assertRaises(NATIVE.GateFailure):
                NATIVE.public_effect_summary({**private, 'intents': [{**private['intents'][0], **update}]})

    def test_package_inventory_cannot_hide_unfinished_triggers_or_aliases(self):
        observed = NATIVE.parse_installed_packages(
            b'incus:amd64\tinstalled\tok\nincus-base\tinstalled\tok\nold\tconfig-files\tok\n')
        self.assertEqual(observed, {'incus', 'incus-base'})
        for raw in (b'', b'incus\thalf-configured\tok\n', b'initramfs-tools\ttriggers-pending\tok\n',
                    b'incus\tinstalled\treinstreq\n', b'incus\tinstalled\tok',
                    b'incus\tinstalled\tok\nincus\tinstalled\tok\n',
                    b'incus:amd64\tinstalled\tok\nincus:arm64\tinstalled\tok\n',
                    b'../../etc/passwd\tinstalled\tok\n', b'incus\tinstalled\tok\tPRIVATE\n'):
            with self.subTest(raw=raw), self.assertRaises(NATIVE.GateFailure):
                NATIVE.parse_installed_packages(raw)

    def test_explicit_removal_proves_exact_owned_set_and_preserves_all_other_packages(self):
        original = {'docker.io', 'docker-cli', 'nftables', 'initramfs-tools'}
        installed = original | {'incus', 'incus-base', 'incus-client', 'qemu-system-x86'}
        owned = ['incus', 'incus-base', 'incus-client']
        remaining = original | {'qemu-system-x86'}
        NATIVE.validate_removed_packages(original, installed, remaining, owned)
        for before, after, names in (
                (original, remaining | {'incus-base'}, owned),
                (original, remaining - {'docker.io'}, owned),
                (original, original, owned),  # Unowned dependency was autoremove'd.
                (original, remaining, owned + ['qemu-system-x86']),
                (original | {'incus-base'}, remaining, owned),
                (original, remaining, owned + ['incus']),
                (original, remaining, [])):
            with self.subTest(after=after, names=names), self.assertRaises(NATIVE.GateFailure):
                NATIVE.validate_removed_packages(before, installed, after, names)

    def test_restart_replay_proof_cannot_be_confused_with_expiry(self):
        created = '2026-09-23T00:00:00Z'
        start = NATIVE.datetime.fromisoformat(created.replace('Z', '+00:00')).timestamp()
        with mock.patch.object(NATIVE.time, 'time', return_value=start + 15):
            NATIVE.require_live_confirmation_plan(created)
        for now in (start - 1, start + 300, start + 1500):
            with self.subTest(now=now), mock.patch.object(NATIVE.time, 'time', return_value=now):
                with self.assertRaises(NATIVE.GateFailure):
                    NATIVE.require_live_confirmation_plan(created)
        for stamp in ('2026-09-23T00:00:00', None, 'private-invalid-value'):
            with self.subTest(stamp=stamp), self.assertRaises(NATIVE.GateFailure):
                NATIVE.require_live_confirmation_plan(stamp)
        # Real package acquisition can legitimately exceed the five-minute
        # plan lifetime. Test consumption across restart BEFORE that work,
        # while retaining exact consumed (not expired) response assertions.
        source = inspect.getsource(NATIVE.run)
        self.assertLess(source.index("stage = 'service_restart_persists_consumption'"),
                        source.index("expiry_plan = plan('uninstall', request)"))
        self.assertEqual(source.count('require_live_confirmation_plan('), 2)

    def test_package_inventory_snapshots_are_never_rebound(self):
        # The expiry wait once reused an inventory snapshot's name for seconds
        # left, so the repeat-uninstall comparison saw a float, not a set.
        import ast
        tree = ast.parse(Path(NATIVE.__file__).read_text())
        run = next(node for node in ast.walk(tree) if isinstance(node, ast.FunctionDef) and node.name == 'run')
        bindings = {}
        for node in ast.walk(run):
            targets = node.targets if isinstance(node, ast.Assign) else [node.target] if isinstance(node, (ast.AugAssign, ast.AnnAssign, ast.For)) else []
            for target in targets:
                for name in ast.walk(target):
                    if isinstance(name, ast.Name):
                        bindings.setdefault(name.id, []).append(node)
        snapshots = {target.id for node in ast.walk(run) if isinstance(node, ast.Assign) and isinstance(node.value, ast.Call)
                     and getattr(node.value.func, 'id', '') == 'installed_packages' for target in node.targets if isinstance(target, ast.Name)}
        self.assertTrue({'original_packages', 'installed', 'after_removal'} <= snapshots, snapshots)
        for name in snapshots:
            self.assertEqual(len(bindings[name]), 1, name)


if __name__ == '__main__':
    unittest.main()
