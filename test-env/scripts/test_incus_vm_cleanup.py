import errno
import socket
import subprocess
import unittest
from unittest.mock import patch

from incus_vm_cleanup import finalize_vm, require_no_loopback_listeners


class Process:
    def __init__(self, calls, waits=(0,)):
        self.calls = calls
        self.waits = iter(waits)
        self.returncode = None

    def poll(self):
        return self.returncode

    def wait(self, timeout):
        self.calls.append('wait')
        result = next(self.waits)
        if isinstance(result, Exception):
            raise result
        self.returncode = result
        return result


class VMCleanupTests(unittest.TestCase):
    def cleanup(self, fail=None, waits=(0,)):
        calls = []
        def step(name):
            def run():
                calls.append(name)
                if name == fail:
                    raise RuntimeError('private-session-token-must-not-escape')
            return run
        result = finalize_vm(Process(calls, waits), step('shutdown'), step('quit'),
                             stop_tunnel=step('tunnel'), after_exit=step('resources'), baseline=step('baseline'))
        return result, calls

    def test_tunnel_error_cannot_bypass_shutdown_reaping_or_baseline(self):
        result, calls = self.cleanup('tunnel')
        self.assertEqual(calls, ['tunnel', 'shutdown', 'wait', 'resources', 'baseline'])
        self.assertEqual(result['qemu_exit'], 0)
        self.assertFalse(result['cleanup_passed'])
        self.assertNotIn('private-session', str(result))

    def test_resource_error_still_collects_physical_host_baseline(self):
        result, calls = self.cleanup('resources')
        self.assertEqual(calls[-1], 'baseline')
        self.assertFalse(result['cleanup_passed'])

    def test_graceful_success_requires_actual_exit_and_every_check(self):
        result, calls = self.cleanup()
        self.assertTrue(result['cleanup_passed'])
        self.assertNotIn('quit', calls)
        result, _ = self.cleanup('baseline')
        self.assertFalse(result['cleanup_passed'])
        result, _ = self.cleanup(waits=(1,))
        self.assertFalse(result['cleanup_passed'])

    def test_timeout_forces_only_verified_quit_and_is_not_graceful_success(self):
        result, calls = self.cleanup(waits=(subprocess.TimeoutExpired('private-command', 1), 0))
        self.assertEqual(calls, ['tunnel', 'shutdown', 'wait', 'quit', 'wait', 'resources', 'baseline'])
        self.assertTrue(result['forced_quit_requested'])
        self.assertEqual(result['qemu_exit'], 0)
        self.assertFalse(result['cleanup_passed'])
        self.assertNotIn('private-command', str(result))

    def test_missing_exit_never_claims_resources_released(self):
        timeout = subprocess.TimeoutExpired('not-logged', 1)
        result, calls = self.cleanup(waits=(timeout, timeout))
        self.assertIsNone(result['qemu_exit'])
        self.assertNotIn('resources', calls)
        self.assertEqual(calls[-1], 'baseline')
        self.assertFalse(result['cleanup_passed'])

    def test_refused_connection_not_rebinding_decides_listener_absence(self):
        with patch('incus_vm_cleanup.socket.socket') as factory:
            connection = factory.return_value.__enter__.return_value
            connection.connect_ex.return_value = errno.ECONNREFUSED
            connection.bind.side_effect = OSError(errno.EADDRINUSE, 'TIME_WAIT')
            require_no_loopback_listeners([22251])
            connection.bind.assert_not_called()
            for status in (0, errno.ETIMEDOUT, errno.ENETUNREACH, errno.EACCES):
                connection.connect_ex.return_value = status
                with self.subTest(status=status), self.assertRaises(RuntimeError):
                    require_no_loopback_listeners([22251])

    def test_actual_loopback_listener_is_not_reported_absent(self):
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', 0))
            listener.listen(1)
            with self.assertRaises(RuntimeError):
                require_no_loopback_listeners([listener.getsockname()[1]])

    def test_invalid_ports_are_rejected(self):
        for port in (False, '22251', 22, 65536):
            with self.subTest(port=port), self.assertRaises(ValueError):
                require_no_loopback_listeners([port])


if __name__ == '__main__':
    unittest.main()
