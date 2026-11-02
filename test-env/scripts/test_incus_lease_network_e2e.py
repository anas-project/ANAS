"""Offline guards for the lease network native gate."""
import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location('lease_native', Path(__file__).with_name('server-incus-lease-network-e2e.py'))
NATIVE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(NATIVE)


class LeaseNetworkEvidence(unittest.TestCase):
    def events(self, expected):
        return [{'Action': action, 'Test': name} for name in expected for action in ('run', 'pass')]

    def test_exact_complete_native_runs_only(self):
        for required in (NATIVE.DEPLOY, NATIVE.MATRIX, NATIVE.RUNTIME, NATIVE.BEFORE_CONSOLE,
                         NATIVE.AFTER_CONSOLE, NATIVE.BEFORE_REBOOT, NATIVE.AFTER_REBOOT,
                         NATIVE.TEARDOWN, NATIVE.AFTER_UNINSTALL):
            events = self.events(required)
            self.assertTrue(NATIVE.events_passed(events, required, 0))
            self.assertFalse(NATIVE.events_passed(events, required, 1))
            self.assertFalse(NATIVE.events_passed(events[:-1], required, 0))
            self.assertFalse(NATIVE.events_passed(events + [{'Action': 'skip', 'Test': required[0]}], required, 0))

    def test_stage_lists_are_ordered_and_distinct(self):
        for page in (True, False):
            stages = NATIVE.phase_one_stages(page) + NATIVE.PHASE_TWO_STAGES
            self.assertEqual(len(stages), len(set(stages)))
            self.assertEqual('console_lease_page' in stages, page)


class LeaseView(unittest.TestCase):
    def view(self, with_inet=True):
        leases = []
        for key, want in NATIVE.expected_view(with_inet).items():
            consumer, resource = key.split('.')
            directions = [{'flow': flow, 'peer': peer, 'allowed': allowed} for (flow, peer), allowed in want['directions'].items()]
            leases.append({'consumer': consumer, 'resource': resource, 'egress': want['egress'],
                           'module_access': want['module_access'], 'intra_lease': want['intra_lease'],
                           'ingress': want['ingress'], 'bridge': 'lease0123456789', 'ipv4_subnet': '10.1.2.0/24',
                           'ipv4_gateway': '10.1.2.1',
                           'instances': [{'name': 'i%d' % i, 'status': 'Running', 'addresses': ['10.1.2.%d' % (i + 2)]}
                                         for i in range(want['instances'])],
                           'port_bindings': [{'protocol': p, 'guest_port': g, 'ipv4': '10.1.2.254' if v else ''}
                                             for p, g, v in want['bindings']],
                           'http_publications': [{'instance': name} for name in want['published']],
                           'directions': directions})
        return {'leases': leases}

    def test_matching_view_passes(self):
        self.assertEqual(NATIVE.validate_lease_view(self.view(), NATIVE.expected_view(True)), [])
        self.assertEqual(NATIVE.validate_lease_view(self.view(False), NATIVE.expected_view(False)), [])

    def test_wrong_tier_missing_lease_or_key_material_fail(self):
        wrong = self.view()
        wrong['leases'][0]['egress'] = 'internet'
        self.assertTrue(NATIVE.validate_lease_view(wrong, NATIVE.expected_view(True)))
        self.assertIn('lease_set', NATIVE.validate_lease_view(self.view(False), NATIVE.expected_view(True)))
        leaked = self.view()
        leaked['leases'][0]['bridge'] = '-----BEGIN PRIVATE KEY-----'
        self.assertIn('credential_marker', NATIVE.validate_lease_view(leaked, NATIVE.expected_view(True)))
        flipped = copy.deepcopy(self.view())
        flipped['leases'][0]['directions'][0]['allowed'] = not flipped['leases'][0]['directions'][0]['allowed']
        self.assertTrue(NATIVE.validate_lease_view(flipped, NATIVE.expected_view(True)))


class HostInterface(unittest.TestCase):
    def test_vm_consumers_request_the_vm_interface(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)/'lease-tiers.json'
            self.assertEqual(NATIVE.host_interface(path), 'incus_container')
            path.write_text(json.dumps({'vm_consumers': []}))
            self.assertEqual(NATIVE.host_interface(path), 'incus_container')
            path.write_text(json.dumps({'vm_consumers': ['lnlan', 'lnhost']}))
            self.assertEqual(NATIVE.host_interface(path), 'incus_vm')


class LeasePage(unittest.TestCase):
    def view(self):
        return LeaseView().view()

    def page(self, view):
        leases = []
        for item in view['leases']:
            edges = lambda flow: [{'flow': flow, 'label': d['peer'], 'allowed': d['allowed'], 'denied': not d['allowed'],
                                   'marker': 'url(#arrow-allowed)' if d['allowed'] else 'url(#arrow-denied)',
                                   'title': d['peer'], 'stroke': 'rgb(0, 128, 0)' if d['allowed'] else 'rgb(200, 0, 0)'}
                                  for d in item['directions'] if d['flow'] == flow]
            leases.append({'key': item['consumer'] + '.' + item['resource'], 'egress': edges('egress'), 'ingress': edges('ingress')})
        url = 'https://anas.native.test:18445/api/v1/workspaces/native/compute/leases'
        return {'page': {'leases': leases, 'forms': 0,
                         'controls': [{'tag': 'select', 'type': ''}, {'tag': 'button', 'type': 'button'}]},
                'requests': [{'method': 'GET', 'url': 'https://anas.native.test:18445/api/v1/system'},
                             {'method': 'GET', 'url': url}, {'method': 'GET', 'url': url}],
                'screenshot_bytes': 1000}

    def test_matching_page_passes(self):
        view = self.view()
        self.assertEqual(NATIVE.validate_lease_page(self.page(view), view), [])
        # One workspace: the page shows no workspace selector.
        single = self.page(view)
        single['page']['controls'] = [{'tag': 'button', 'type': 'button'}]
        self.assertEqual(NATIVE.validate_lease_page(single, view), [])

    def test_flipped_edge_control_change_or_same_colour_fail(self):
        view = self.view()
        flipped = self.page(view)
        flipped['page']['leases'][0]['egress'][0]['allowed'] = not flipped['page']['leases'][0]['egress'][0]['allowed']
        self.assertTrue(NATIVE.validate_lease_page(flipped, view))
        editable = self.page(view)
        editable['page']['controls'].append({'tag': 'input', 'type': 'text'})
        self.assertIn('page_controls', NATIVE.validate_lease_page(editable, view))
        writing = self.page(view)
        writing['requests'].append({'method': 'POST', 'url': 'https://anas.native.test:18445/api/v1/workspaces/native/plans'})
        self.assertIn('page_sent_a_change', NATIVE.validate_lease_page(writing, view))
        plain = self.page(view)
        for lease in plain['page']['leases']:
            for edge in lease['egress'] + lease['ingress']:
                edge['stroke'] = 'rgb(0, 0, 0)'
        self.assertIn('allowed_and_denied_not_distinct', NATIVE.validate_lease_page(plain, view))


if __name__ == '__main__':
    unittest.main()
