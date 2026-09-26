"""No server, real credential or browser is started by these guard tests."""
import importlib.util
from pathlib import Path
import sys
import unittest

SPEC = importlib.util.spec_from_file_location('native_browser_bootstrap',
    Path(__file__).with_name('server-incus-host-browser-bootstrap.py'))
MODULE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


class BrowserBootstrapGuards(unittest.TestCase):
    def test_native_browser_envelope_requires_bound_product_asset_and_test_origin(self):
        source = {'sources': {'internal/webui/dist/main/assets/main.js': 'a' * 64}}
        session = {'schema': 'anas.console-session/v1', 'origin': 'https://anas.native.test:18445',
                   'ca_pem': 'public-test-ca', 'session': {'source': 'local', 'session_token': 'secret-session',
                                                         'csrf_token': 'secret-csrf'}}
        result = MODULE.browser_envelope('anas-incus-host-abcdef', source, session, b'public-spki')
        self.assertEqual(result['schema'], 'anas.incus-host-browser/v1')
        self.assertEqual(result['main_asset_sha256'], 'a' * 64)
        self.assertEqual(result['vm_id'], 'anas-incus-host-abcdef')
        for bad in ({'sources': {}}, {'sources': {'internal/webui/dist/main/assets/main.js': 'bad'}}):
            with self.assertRaises(MODULE.BootstrapFailure):
                MODULE.browser_envelope('anas-incus-host-abcdef', bad, session, b'public-spki')
        with self.assertRaises(MODULE.BootstrapFailure):
            MODULE.browser_envelope('anas-incus-host-abcdef', source,
                {**session, 'origin': 'https://production.example'}, b'public-spki')

    def test_native_browser_summary_cannot_report_subset_as_complete(self):
        result = {'schema': 'anas.incus-host-browser/v1', 'vm_id': 'anas-incus-host-abcdef', 'passed': True,
                  'events': [{'stage': stage, 'status': 'passed'} for stage in MODULE.REQUIRED]}
        MODULE.validate_browser_result(result, 'anas-incus-host-abcdef')
        for changed in ({**result, 'events': result['events'][:-1]}, {**result, 'passed': False},
                        {**result, 'vm_id': 'anas-incus-host-123456'},
                        {**result, 'events': result['events'] + result['events'][:1]}):
            with self.assertRaises(MODULE.BootstrapFailure):
                MODULE.validate_browser_result(changed, 'anas-incus-host-abcdef')


if __name__ == '__main__':
    unittest.main()
