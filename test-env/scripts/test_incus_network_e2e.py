"""Offline safety and verdict gates for the dual-stack egress harness."""
import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('network', Path(__file__).with_name('server-incus-network-e2e.py'))
lab = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lab)


class NetworkHarness(unittest.TestCase):
    def test_masquerade_requires_exactly_one_record_from_the_lab_address(self):
        record = {'token': 'p-v6', 'family': 6, 'peer': lab.HOST_V6}
        self.assertTrue(lab.masquerade_verdict([record], 'p-v6', 6, lab.HOST_V6, ['fd42::10']))
        # The guest's own bridge address reaching upstream is the failure this gate exists for.
        leaked = dict(record, peer='fd42::10')
        self.assertFalse(lab.masquerade_verdict([leaked], 'p-v6', 6, lab.HOST_V6, ['fd42::10']))
        self.assertFalse(lab.masquerade_verdict([], 'p-v6', 6, lab.HOST_V6, []))
        self.assertFalse(lab.masquerade_verdict([record, record], 'p-v6', 6, lab.HOST_V6, []))
        self.assertFalse(lab.masquerade_verdict([dict(record, family=4)], 'p-v6', 6, lab.HOST_V6, []))

    def test_bridge_names_follow_computeclient(self):
        # computeclient.NetworkName / LegacyNetworkName for "anas-network-v6".
        digest = lab.hashlib.sha256(b'anas-network-v6').hexdigest()[:10]
        self.assertEqual(lab.lease_bridge('anas-network-v6'), 'lease' + digest)
        self.assertEqual(lab.legacy_bridge('anas-network-v6'), 'anas' + digest)
        for project in lab.LEASES:
            self.assertLessEqual(len(lab.lease_bridge(project)), 15)
            self.assertFalse(lab.lease_bridge(project).startswith('anas'))

    def test_ensure_must_report_the_lease_network(self):
        flags = {'exists': True, 'ready': True, 'restricted': True, 'quota_enforced': True}
        v4 = {'bridge': 'lease0123456789', 'ipv4_subnet': '10.1.2.0/24', 'ipv4_gateway': '10.1.2.1'}
        v6 = dict(v4, ipv6_subnet='fd42:1::/64', ipv6_gateway='fd42:1::1')
        self.assertEqual(lab.provider_confirmed({**flags, 'network': v4}, 'ensure', 'lease0123456789', False), ['10.1.2.0/24'])
        self.assertEqual(lab.provider_confirmed({**flags, 'network': v6}, 'ensure', 'lease0123456789', True),
                         ['10.1.2.0/24', 'fd42:1::/64'])
        self.assertEqual(lab.provider_confirmed(dict(flags), 'inspect', 'lease0123456789', True), [])
        for result, ipv6 in (({**flags}, False),                                   # no network report
                             ({**flags, 'network': v6}, False),                   # IPv6 on a disabled lease
                             ({**flags, 'network': v4}, True),                    # IPv6 missing
                             ({**flags, 'network': dict(v4, bridge='anas0123456789')}, False),
                             ({**flags, 'ready': False, 'network': v4}, False),
                             ({**flags, 'network': v4, 'extra': 1}, False)):
            self.assertIsNone(lab.provider_confirmed(result, 'ensure', 'lease0123456789', ipv6))
        self.assertIsNone(lab.provider_confirmed({**flags, 'network': v4}, 'inspect', 'lease0123456789', False))

    def lease_acl(self):
        config = {'security.acls': 'lease0123456789', 'security.acls.default.egress.action': 'drop',
                  'security.acls.default.ingress.action': 'drop', 'ipv4.address': '10.1.2.1/24', 'ipv6.address': 'fd42:1::1/64'}
        acl = {'egress': [{'action': 'allow', 'source': 'fd42:1::/64,10.1.2.0/24', 'destination': '1.0.0.0/8,2000::/3', 'state': 'enabled'},
                          {'action': 'drop', 'destination': '10.9.9.0/24', 'state': 'enabled'}],
               'ingress': []}
        return config, acl

    def test_lease_acl_sources_every_allow_from_the_bridge_subnets(self):
        subnets = ['10.1.2.0/24', 'fd42:1::/64']
        config, acl = self.lease_acl()
        self.assertTrue(lab.lease_acl_verdict(config, acl, 'lease0123456789', subnets))
        # The 2026-09-27 design allowed ingress by default; M10a drops it.
        for key, value in (('security.acls', 'other'), ('security.acls.default.egress.action', 'allow'),
                           ('security.acls.default.ingress.action', 'allow'), ('ipv4.address', '10.1.3.1/24')):
            self.assertFalse(lab.lease_acl_verdict(dict(config, **{key: value}), acl, 'lease0123456789', subnets), key)
        unsourced = {'action': 'allow', 'destination': '2000::/3', 'state': 'enabled'}
        self.assertFalse(lab.lease_acl_verdict(config, dict(acl, egress=acl['egress'] + [unsourced]), 'lease0123456789', subnets))
        narrowed = dict(acl['egress'][0], source='10.1.2.0/24')
        self.assertFalse(lab.lease_acl_verdict(config, dict(acl, egress=[narrowed]), 'lease0123456789', subnets))
        self.assertFalse(lab.lease_acl_verdict(config, dict(acl, egress=acl['egress'][1:]), 'lease0123456789', subnets))
        # A disabled rule allows nothing.
        self.assertTrue(lab.lease_acl_verdict(config, dict(acl, egress=acl['egress'] + [dict(unsourced, state='disabled')]),
                                              'lease0123456789', subnets))

    def test_upstream_uses_documentation_prefixes_only(self):
        for address in (lab.HOST_V4, lab.UPSTREAM_V4):
            self.assertTrue(address.startswith('198.51.100.'))
        for address in (lab.HOST_V6, lab.UPSTREAM_V6):
            self.assertTrue(address.startswith('2001:db8:'))

    def test_forged_sources_are_outside_every_lease_and_upstream_prefix(self):
        sources = [a for pair in lab.SPOOF.values() for a in pair]
        self.assertEqual(len(set(sources)), 4)
        self.assertEqual(set(lab.SPOOF), set(lab.LEASES))
        for v4, v6 in lab.SPOOF.values():
            self.assertTrue(v4.startswith('192.0.2.') and v6.startswith('2001:db8:') and not v6.startswith('2001:db8:99:'))
        ruleset = lab.probe_ruleset()
        for address in sources:
            self.assertIn('saddr %s counter name "%s"' % (address, lab.counter_name(address)), ruleset)

    def test_spoof_verdict_requires_zero_upstream_and_a_reached_host_in_the_ipv6_lease(self):
        sources = lab.SPOOF['anas-network-v6']
        clean = {a: 0 for a in sources}
        seen = {sources[0]: 0, sources[1]: 3}
        self.assertTrue(lab.spoof_verdict(seen, clean, sources, True))
        # Nothing reached the lab VM: the test proves nothing about the fence.
        self.assertFalse(lab.spoof_verdict(clean, clean, sources, True))
        self.assertTrue(lab.spoof_verdict(clean, clean, sources, False))
        for address in sources:
            self.assertFalse(lab.spoof_verdict(seen, dict(clean, **{address: 1}), sources, True))
            self.assertFalse(lab.spoof_verdict(seen, dict(clean, **{address: 1}), sources, False))

    def test_drift_must_leak_be_reported_and_be_repaired(self):
        good = {'inspect_ready': False, 'leaked_upstream': 2, 'ensure_rc': 0, 'repaired_ready': True, 'after_repair_upstream': 0}
        self.assertTrue(lab.drift_verdict(good))
        for key, value in (('inspect_ready', True), ('inspect_ready', None), ('leaked_upstream', 0), ('ensure_rc', 1),
                           ('repaired_ready', False), ('after_repair_upstream', 1)):
            self.assertFalse(lab.drift_verdict(dict(good, **{key: value})), key)

    def test_read_counters_ignores_foreign_tables(self):
        items = [{'metainfo': {}}] + [{'counter': {'table': lab.PROBE_TABLE, 'name': lab.counter_name(a), 'packets': 1}}
                                      for pair in lab.SPOOF.values() for a in pair]
        items.append({'counter': {'table': 'other', 'name': lab.counter_name('192.0.2.66'), 'packets': 9}})
        counters = lab.read_counters(lab.json.dumps({'nftables': items}))
        self.assertEqual(set(counters.values()), {1})

    def test_both_postures_are_exercised(self):
        self.assertEqual(sorted(lab.LEASES.values()), [False, True])

    def test_unprivileged_or_foreign_identity_rejected_before_any_command(self):
        with patch.object(lab.os, 'geteuid', return_value=1000), patch.object(lab.subprocess, 'run') as run:
            with self.assertRaises(RuntimeError):
                lab.require_vm('anas-incus-lifecycle-abc123')
            run.assert_not_called()
        with patch.object(lab.os, 'geteuid', return_value=0), patch.object(lab.subprocess, 'run') as run:
            with self.assertRaises(RuntimeError):
                lab.require_vm('some-business-host')
            run.assert_not_called()


if __name__ == '__main__':
    unittest.main()
