"""Offline contracts for the disposable default-Docker forwarding experiment."""
import importlib.util
from pathlib import Path
import unittest
from unittest import mock
import subprocess
import sys

SPEC = importlib.util.spec_from_file_location('native_forwarding', Path(__file__).with_name('server-incus-forwarding-e2e.py'))
NATIVE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(NATIVE)


class ForwardingGuards(unittest.TestCase):
    def test_preparation_installs_bridge_helper_without_changing_forward_policy(self):
        script = Path(__file__).with_name('prepare-incus-forwarding-native.sh').read_text()
        packages = script.split('install -y --no-install-recommends', 1)[1].split('\ntest ', 1)[0].split()
        self.assertIn('dnsmasq-base', packages)
        self.assertIn("dpkg-query -W -f='${Status}' dnsmasq-base", script)
        self.assertNotIn('sysctl -w', script)
        self.assertNotIn('iptables -P', script)
        self.assertNotIn('ip-forward-no-drop', script)

    def test_exact_disposable_vm_and_empty_experimental_docker_only(self):
        facts = {'uid': 0, 'vendor': 'QEMU', 'vm_id': 'anas-incus-host-abcdef',
                 'docker_root': '/var/lib/anas-forwarding-docker', 'containers': []}
        self.assertTrue(NATIVE.valid_environment('anas-incus-host-abcdef', facts))
        for key, value in [('uid', 1000), ('vendor', 'physical'), ('vm_id', 'anas-incus-host-123456'),
                           ('docker_root', '/var/lib/docker'), ('containers', ['business'])]:
            self.assertFalse(NATIVE.valid_environment('anas-incus-host-abcdef', {**facts, key: value}))
        self.assertFalse(NATIVE.valid_environment('anas-incus-host-abcdef\n', facts))

    def test_missing_duplicated_or_skipped_evidence_is_not_a_pass(self):
        events = [{'stage': name, 'status': 'passed'} for name in NATIVE.REQUIRED]
        self.assertTrue(NATIVE.complete(events))
        for rows in ([], events[:-1], events+events[:1], events+[{'stage': 'extra', 'status': 'passed'}],
                     [*events[:-1], {'stage': events[-1]['stage'], 'status': 'skipped'}]):
            self.assertFalse(NATIVE.complete(rows))

    def test_experimental_permits_are_only_for_one_source_and_endpoint(self):
        rules = NATIVE.permit_rules()
        self.assertEqual(len(rules), 2)
        joined = ' '.join(' '.join(row) for row in rules)
        for expected in ('10.231.77.2/32', '198.18.77.2/32', '18080', 'ESTABLISHED', 'an-fw-up', 'an-fw-br'):
            self.assertIn(expected, joined)
        for forbidden in ('-P', '0.0.0.0/0', '10.231.77.0/24', 'RELATED', '+', 'privileged'):
            self.assertNotIn(forbidden, joined)
        self.assertNotIn('18081', joined)

    def test_nft_transaction_has_explicit_block_and_statement_boundaries(self):
        body = NATIVE.early_accept_rules()
        self.assertIsInstance(body, bytes)
        self.assertTrue(body.endswith(b'\n'))
        self.assertIn(b'\n  }\n}\n', body)
        self.assertIn(b'priority -110;', body)
        self.assertIn(b'counter accept\n', body)
        self.assertNotIn(b'} }', body)
        self.assertNotIn(b'flush', body)
        self.assertNotIn(b'policy drop', body)

    def test_command_diagnostics_do_not_echo_arbitrary_stderr(self):
        self.assertEqual(NATIVE.command_diagnostic('/usr/sbin/nft', b'private-path: syntax error, secret-value'), 'nft_syntax_error')
        self.assertEqual(NATIVE.command_diagnostic('/usr/sbin/nft', b'private-path: Operation not permitted'), 'permission_denied')
        self.assertEqual(NATIVE.command_diagnostic('/usr/sbin/nft', b'private-token'), 'command_error')
        self.assertEqual(NATIVE.command_diagnostic('/usr/bin/other', b'syntax error'), 'command_error')

    def test_policy_counter_requires_the_actual_filter_forward_chain(self):
        body = '*filter\n:INPUT ACCEPT [0:0]\n:FORWARD DROP [3:180]\n:OUTPUT ACCEPT [0:0]\nCOMMIT\n'
        self.assertEqual(NATIVE.forward_policy(body), ('DROP', 3))
        for bad in ('', body+body, body.replace('DROP [3:180]', 'MISSING [3:180]'),
                    body.replace(':FORWARD', ':OTHER'), body.replace('[3:180]', '[x:180]')):
            with self.assertRaises(NATIVE.Failure):
                NATIVE.forward_policy(bad)

    def test_native_events_require_exact_package_order_and_terminal(self):
        package = 'github.com/anas-project/ANAS/internal/incusprovision'
        name = 'TestNativeForwardingDiagnostics'
        good = [{'Package': package, 'Test': name, 'Action': 'run'},
                {'Package': package, 'Test': name, 'Action': 'pass'},
                {'Package': package, 'Action': 'pass'}]
        self.assertTrue(NATIVE.native_events_passed(good, 0))
        for bad in ([], good[1:], good[:-1], good+good[-1:], list(reversed(good)),
                    [*good[:1], {'Package': package, 'Test': name, 'Action': 'skip'}, *good[1:]],
                    [{**row, 'Package': 'other'} for row in good],
                    [{**row, 'Test': 'different'} for row in good], [*good, 'invalid']):
            self.assertFalse(NATIVE.native_events_passed(bad, 0))
        for code in (1, False, '0', None):
            self.assertFalse(NATIVE.native_events_passed(good, code))

    def test_local_endpoint_does_not_depend_on_reverse_dns(self):
        namespace = {'__name__': 'offline_endpoint_test'}
        exec(NATIVE.endpoint_program(), namespace)
        with mock.patch('socket.getfqdn', side_effect=AssertionError('DNS must not be consulted')):
            server = namespace['LocalEndpoint'](('127.0.0.1', 0), namespace['Handler'])
            try:
                self.assertEqual(server.server_name, 'fixed-local-endpoint')
                self.assertGreater(server.server_port, 0)
            finally:
                server.server_close()

    def test_noninteractive_command_gets_eof_even_when_supervisor_stdin_is_open(self):
        # Keep the parent's pipe open while waiting for completion. communicate
        # would close it and hide the same bug in SSH-supervised native calls.
        path = str(Path(__file__).with_name('server-incus-forwarding-e2e.py').resolve())
        program = f'''import importlib.util,sys
spec=importlib.util.spec_from_file_location('forwarding_capture',{path!r})
native=importlib.util.module_from_spec(spec);spec.loader.exec_module(native)
result=native.capture([sys.executable,'-c','import sys; print(len(sys.stdin.buffer.read()))'],timeout=1)
assert result.stdout.strip()==b'0'
'''
        child = subprocess.Popen([sys.executable, '-c', program], stdin=subprocess.PIPE,
                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        try:
            self.assertEqual(child.wait(timeout=5), 0, 'noninteractive command inherited the live supervisor input stream')
        finally:
            if child.poll() is None:
                child.kill(); child.wait(timeout=5)
            for stream in (child.stdin, child.stdout, child.stderr):
                stream.close()


if __name__ == '__main__':
    unittest.main()
