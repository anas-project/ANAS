"""Offline guards for the explicit VM source/staging Docker acceptance."""
import copy
import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('shared_build', Path(__file__).with_name('server-incus-shared-build-e2e.py'))
lab = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lab)


class SharedBuildSafety(unittest.TestCase):
    def report(self, source=Path('/source'), stage=Path('/source')):
        images = []
        for module, (context, _, count) in lab.MODULES.items():
            for index in range(count):
                images.append({'module': module, 'service': module+'_'+str(index),
                               'compose_directory': str(stage/'modules'/module), 'shared_root': str(source),
                               'input_digest': 'a'*64, 'build': {'context': context,
                               'additional_contexts': {'shared': '${ANAS_SHARED_BUILD_CONTEXT:-../..}'}}})
        return {'schema': lab.SCHEMA, 'docker_executed': False, 'source_root': str(source),
                'build_root': str(stage), 'images': images}

    def test_host_or_wrong_vm_rejected_before_subprocess(self):
        with patch.object(lab.os, 'geteuid', return_value=1000), patch.object(lab.Path, 'read_text') as read, patch.object(lab.subprocess, 'Popen') as run:
            with self.assertRaises(RuntimeError):
                lab.require_vm('anas-incus-build-abc123')
            read.assert_not_called()
            run.assert_not_called()
        for identity, vendor in [('other-machine', 'QEMU'), ('anas-incus-build-abc123', 'Dell')]:
            with patch.object(lab.os, 'geteuid', return_value=0), patch.object(lab.Path, 'read_text', side_effect=[identity, vendor]), patch.object(lab.subprocess, 'Popen') as run:
                with self.assertRaises(RuntimeError):
                    lab.require_vm('anas-incus-build-abc123')
                run.assert_not_called()

    def test_transport_rejects_credentials_and_checksum_disabling_shortcuts(self):
        lab.validate_transport('docker.io', 'https://proxy.golang.org,direct')
        lab.validate_transport('m.daocloud.io/docker.io', 'https://goproxy.cn,direct')
        lab.validate_transport('registry.example.test:5443', 'https://proxy.example.test/modules')
        for registry, proxy in [('user:secret@example.test', 'https://proxy.golang.org'),
                                ('docker.io', 'https://user:secret@example.test'),
                                ('docker.io', 'http://proxy.example.test'),
                                ('docker.io', 'https://proxy.example.test/?token=private'),
                                ('docker.io', 'off'), ('docker.io', 'https://proxy.example.test|direct'),
                                ('registry.test/../private', 'https://proxy.golang.org'),
                                ('registry.test:99999/cache', 'https://proxy.golang.org')]:
            with self.subTest(registry=registry, proxy=proxy), self.assertRaises((RuntimeError, ValueError)):
                lab.validate_transport(registry, proxy)

    def test_requires_all_four_matching_compose_services_and_both_layouts(self):
        for stage in (Path('/source'), Path('/stage')):
            selected = lab.select_builds(self.report(stage=stage), Path('/source'), stage)
            self.assertEqual(set(selected), set(lab.MODULES))
        original = self.report()
        for index in range(len(original['images'])):
            report = copy.deepcopy(original)
            report['images'].pop(index)
            with self.assertRaises(RuntimeError):
                lab.select_builds(report, Path('/source'), Path('/source'))

    def test_rejects_swapped_source_and_extra_or_changed_builds(self):
        for mutation in ('source', 'shared', 'digest', 'context', 'duplicate', 'extra', 'executed'):
            report = self.report()
            if mutation == 'source': report['source_root'] = '/different'
            elif mutation == 'shared': report['images'][0]['shared_root'] = '/different'
            elif mutation == 'digest': report['images'][2]['input_digest'] = 'b'*64
            elif mutation == 'context': report['images'][0]['build']['context'] = '../private'
            elif mutation == 'duplicate': report['images'].append(report['images'][0])
            elif mutation == 'extra': report['images'][0]['module'] = 'unexpected'
            elif mutation == 'executed': report['docker_executed'] = True
            with self.subTest(mutation=mutation), self.assertRaises(RuntimeError):
                lab.select_builds(report, Path('/source'), Path('/source'))

    def test_projection_keeps_actual_build_and_never_includes_runtime_environment(self):
        item = self.report()['images'][0]
        item['environment'] = {'SECRET': 'never-forward'}
        before = copy.deepcopy(item)
        projected = lab.build_projection(item, 'anas-test-image:verify', 'test-scope')
        service = projected['services'][item['service']]
        self.assertEqual(set(service), {'image', 'build'})
        self.assertEqual(service['build']['context'], item['build']['context'])
        self.assertEqual(service['build']['additional_contexts'], item['build']['additional_contexts'])
        self.assertEqual(service['build']['labels'], {lab.LABEL: 'test-scope'})
        self.assertEqual(item, before)
        item['build']['secrets'] = ['do-not-forward']
        with self.assertRaises(RuntimeError):
            lab.build_projection(item, 'anas-test-image:verify', 'test-scope')

    def test_empty_missing_mismatched_or_failed_builds_never_pass(self):
        results = [{'phase': phase, 'module': module, 'passed': True,
                    'binary_sha256': 'b'*64, 'input_digest': 'a'*64}
                   for phase in ('source', 'staging') for module in lab.MODULES]
        for item in results:
            if item['module'] == 'forgejo':
                item.update(incus_module_version='v7.3.0', incus_cli_version='7.3', incus_cli_sha256='c'*64)
        self.assertTrue(lab.results_passed(results, True, True, True))
        self.assertFalse(lab.results_passed(results, False, True, True))
        self.assertFalse(lab.results_passed(results, True, False, True))
        self.assertFalse(lab.results_passed(results, True, True, False))
        self.assertFalse(lab.results_passed([], True, True, True))
        for index in range(len(results)):
            self.assertFalse(lab.results_passed(results[:index]+results[index+1:], True, True, True))
        for field, value in [('passed', False), ('binary_sha256', 'c'*64), ('input_digest', 'd'*64)]:
            changed = copy.deepcopy(results)
            changed[-1][field] = value
            self.assertFalse(lab.results_passed(changed, True, True, True))
        for item in results:
            item['input_digest'] = ''
        self.assertFalse(lab.results_passed(results, True, True, True))

    def test_probe_binds_binary_paths_and_actual_upstream_version(self):
        prefix = ('a'*64+'  /usr/local/bin/anas-forgejo-actions-controller\n'
                  +'b'*64+'  /usr/local/bin/incus\n')
        for version in ('7.3', '7.3.0'):
            result = lab.parse_probe_output('forgejo', (prefix+'incus_cli_version='+version+'\n').encode())
            self.assertEqual(result['incus_module_version'], 'v7.3.0')
            self.assertEqual(result['incus_cli_version'], version)
            self.assertEqual(result['incus_cli_sha256'], 'b'*64)
        for output in (prefix+'incus_cli_version=6.0.5\n', prefix+'incus_cli_version=7.4\n',
                       prefix+'incus_cli_version=7.3-dev\n', prefix,
                       prefix.replace('/usr/local/bin/incus', '/tmp/incus')+'incus_cli_version=7.3\n',
                       prefix+'incus_cli_version=7.3\nextra\n'):
            with self.assertRaises(RuntimeError):
                lab.parse_probe_output('forgejo', output.encode())
        for module in ('incus', 'ai_agent'):
            result = lab.parse_probe_output(module, ('c'*64+'  /usr/local/bin/'+lab.MODULES[module][1]+'\n').encode())
            self.assertEqual(result, {'binary_sha256': 'c'*64})
        dockerfile = Path(__file__).resolve().parents[2]/'modules/forgejo/actions-controller/Dockerfile'
        self.assertIn('go install github.com/lxc/incus/v7/cmd/incus@'+lab.INCUS_MODULE_VERSION, dockerfile.read_text())

    def test_complete_result_rejects_missing_or_different_cli_evidence(self):
        results = []
        for phase in ('source', 'staging'):
            for module in lab.MODULES:
                output = 'a'*64+'  /usr/local/bin/'+lab.MODULES[module][1]+'\n'
                if module == 'forgejo':
                    output += 'b'*64+'  /usr/local/bin/incus\nincus_cli_version=7.3\n'
                results.append(dict(phase=phase, module=module, passed=True, input_digest='c'*64,
                                    **lab.parse_probe_output(module, output.encode())))
        self.assertTrue(lab.results_passed(results, True, True, True))
        for field, value in [('incus_cli_sha256', ''), ('incus_cli_sha256', 'd'*64),
                             ('incus_cli_version', '7.3.0'), ('incus_module_version', 'v7.4.0')]:
            changed = copy.deepcopy(results)
            next(item for item in changed if item['phase'] == 'staging' and item['module'] == 'forgejo')[field] = value
            self.assertFalse(lab.results_passed(changed, True, True, True))

    def test_missing_override_requires_a_real_missing_shared_input_error(self):
        for path in ('go.mod', 'go.sum', 'internal/securefs', 'internal/computeclient'):
            diagnostic = ('failed to calculate checksum: "/'+path+'": not found').encode()
            self.assertTrue(lab.missing_context_failure(1, diagnostic))
            self.assertFalse(lab.missing_context_failure(0, diagnostic))
        for diagnostic in (b'COPY --from=shared go.mod go.sum ./\nbase image: not found',
                           b'failed to solve: context canceled', b'TLS handshake timeout',
                           b'registry authorization failed', b''):
            self.assertFalse(lab.missing_context_failure(1, diagnostic))


if __name__ == '__main__':
    unittest.main()
