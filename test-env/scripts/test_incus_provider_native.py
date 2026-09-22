"""Protocol-level checks for the isolated daemon harness; no host access."""
import importlib.util
import json
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('incus_native', Path(__file__).with_name('incus-provider-native.py'))
native = importlib.util.module_from_spec(spec)
spec.loader.exec_module(native)


class NativeHarnessProtocol(unittest.TestCase):
    def response(self, method, status, body):
        with patch.object(native, 'require_isolation'), patch.object(native, 'UnixConnection') as connection:
            response = connection.return_value.getresponse.return_value
            response.status = status
            response.read.return_value = body
            try:
                return native.api(method, '/1.0/storage-pools', {})
            finally:
                connection.return_value.close.assert_called_once()

    def test_completed_post_and_read_have_distinct_transport_statuses(self):
        value = json.dumps({'type': 'sync', 'status_code': 200, 'metadata': None}).encode()
        self.assertIsNone(self.response('POST', 201, value))
        self.assertIsNone(self.response('GET', 200, value))
        for method, status in (('GET', 201), ('DELETE', 201), ('POST', 202), ('POST', 204)):
            with self.subTest(method=method, status=status), self.assertRaises(RuntimeError):
                self.response(method, status, value)

    def test_conflicting_envelope_cannot_be_reported_as_success(self):
        for extra in ({'type': 'async'}, {'status_code': 103}, {'error_code': 500}, {'error': 'private-marker'}, {'operation': '/1.0/operations/unknown'}):
            value = {'type': 'sync', 'status_code': 200, 'metadata': None, **extra}
            with self.subTest(extra=extra), self.assertRaises(RuntimeError) as caught:
                self.response('POST', 201, json.dumps(value).encode())
            self.assertNotIn('private-marker', str(caught.exception))

    def test_oversized_response_is_rejected_and_closed(self):
        with self.assertRaises(RuntimeError):
            self.response('GET', 200, b' ' * ((4 << 20) + 1))

    def test_missing_isolation_cannot_open_a_socket(self):
        with patch.object(native, 'require_isolation', side_effect=RuntimeError('not isolated')), patch.object(native, 'UnixConnection') as connection:
            with self.assertRaises(RuntimeError):
                native.api('POST', '/1.0/storage-pools', {})
            connection.assert_not_called()


if __name__ == '__main__':
    unittest.main()
