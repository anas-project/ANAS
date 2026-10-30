# TEST_CASES: MAILMOD-T-007 MAILMOD-T-008
"""Run the real DMS driver against a process fixture; never access Docker."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


class RuntimeFailure(unittest.TestCase):
    def run_driver(self, mode, name="server-mail-dms-runtime-e2e.py"):
        with tempfile.TemporaryDirectory(prefix='anas-mail-runtime-test-') as directory:
            root = Path(directory)
            source = root / 'source/test-env/scripts'
            source.mkdir(parents=True)
            driver = source / name
            driver.write_bytes(Path(__file__).with_name(driver.name).read_bytes())
            docker = root / 'docker-fixture'
            docker.write_text('''#!/usr/bin/env python3
import json,os,sys
args=sys.argv[1:]
with open(os.environ['CALLS'],'a') as f:f.write(json.dumps(args)+'\\n')
if args[0]=='info':
 print('/wrong-root' if os.environ['MODE']=='empty-assertion' else os.environ['EXPECTED_ROOT'])
elif args[:2]==['image','inspect']:
 if args[-1]=='{{.Id}}':print('sha256:4b35cb5d547b3cb7e2de20ce5c0687f4f4bd3694665277dda319246be115b527' if os.environ['MODE']=='offline-config' else 'sha256:'+'a'*64)
 else:print(json.dumps([] if os.environ['MODE'] in ['offline-config','untrusted-archive'] else ['ghcr.io/docker-mailserver/docker-mailserver@sha256:'+('b'*64 if os.environ['MODE']=='wrong-digest' else 'd0fe7668defe157aad57ea31b1707ad1e2fb57d7a91bdf17cbdf876549946c86')]))
else:sys.exit(1)
''')
            docker.chmod(0o700)
            parent = root / 'anas-mail-runtime-test-fixture'
            if name != 'server-mail-store-runtime-e2e.py': parent.mkdir()
            env = {k: v for k, v in os.environ.items() if not k.startswith(('MAIL_', 'DOCKER_', 'ANAS_TEST_'))}
            env.update(MAIL_RUNTIME_TEST_ROOT=str(parent), DOCKER_CMD=str(docker), ANAS_TEST_DOCKER_ROOT='/tmp/anas-mail-go-test-fixture/docker', EXPECTED_ROOT='/tmp/anas-mail-go-test-fixture/docker', CALLS=str(root/'calls'), MODE=mode)
            if name == 'server-mail-store-runtime-e2e.py':
                artifacts = source.parents[1] / 'test-artifacts'
                artifacts.mkdir()
                for artifact in ['store.test','mail-service.test','mail-ingress.test']:
                    (artifacts/artifact).write_bytes(b'not executed')
                env['MAIL_STORE_TEST_BINARY'] = str(artifacts/'store.test')
            result = subprocess.run(['python3', str(driver)], capture_output=True, text=True, env=env, timeout=15,
                                    preexec_fn=lambda: os.umask(0o077))
            if name == 'server-mail-store-runtime-e2e.py':
                self.assertEqual((parent/'data').stat().st_mode & 0o777, 0o755)
            final = json.loads((parent/('evidence/final.json' if name == 'server-mail-store-runtime-e2e.py' else 'dms-fixture/evidence/final.json')).read_text())
            calls = [json.loads(row) for row in (root/'calls').read_text().splitlines()]
            return result, final, calls

    def test_empty_assertion_is_a_failed_run(self):
        result, final, _ = self.run_driver('empty-assertion')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(final['failure'], 'AssertionError: ')

    def test_store_empty_assertion_is_a_failed_run(self):
        result, final, _ = self.run_driver('empty-assertion','server-mail-store-runtime-e2e.py')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(final['failure'], 'AssertionError: ')
        self.assertTrue(final['runtime_credentials_removed'])

    def test_wrong_manifest_is_rejected(self):
        result, final, _ = self.run_driver('wrong-digest')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(final['failure'], 'RuntimeError: fixed DMS repository digest mismatch')

    def test_classic_docker_config_id_uses_pinned_repository_digest(self):
        result, final, calls = self.run_driver('classic-docker')
        self.assertNotEqual(result.returncode, 0)
        # Exact repository digest passed; absent fixture ELF fails the next
        # assertion. No build, container, network or native command was issued.
        self.assertEqual(final['failure'], 'AssertionError: ')
        self.assertEqual(sum(row[:2] == ['image', 'inspect'] for row in calls), 2)
        self.assertFalse(any(row[0] in ['run', 'build', 'exec', 'rm'] for row in calls))

    def test_verified_offline_config_passes_without_repository_digest(self):
        result, final, calls = self.run_driver('offline-config')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(final['failure'], 'AssertionError: ')
        self.assertFalse(any(row[0] in ['run', 'build', 'exec', 'rm'] for row in calls))

    def test_untrusted_archive_without_repository_digest_is_rejected(self):
        result, final, _ = self.run_driver('untrusted-archive')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(final['failure'], 'RuntimeError: fixed DMS repository digest mismatch')


class RoundcubeImageIdentity(unittest.TestCase):
    def run_driver(self, trusted):
        with tempfile.TemporaryDirectory(prefix='anas-mail-runtime-test-') as directory:
            root = Path(directory)
            parent = root / 'dms-fixture'
            parent.mkdir()
            driver = root / 'server-mail-roundcube-runtime-e2e.py'
            driver.write_bytes(Path(__file__).with_name(driver.name).read_bytes())
            docker = root / 'docker-fixture'
            docker.write_text('''#!/usr/bin/env python3
import json,os,sys
a=sys.argv[1:]
with open(os.environ['CALLS'],'a') as f:f.write(json.dumps(a)+'\\n')
if a[:2]==['image','inspect']:
 if a[-1]!='{{.Id}}':print('[]')
 elif a[2].startswith('ghcr.io/anas-project/anas-postgres:'):print('sha256:9dde9de534a1962ecc0a66bb46ba8ca8727eccbe42ab846d2c7c2ffecd43c244')
 else:print('sha256:0fe4fa023dcd7d3d4bd0e31f331b3c052a62689681b7fc519cdb32444e0c2a4c' if os.environ['TRUSTED']=='1' else 'sha256:'+'a'*64)
elif a[0]=='inspect' and a[-1]=='fixture-dms':print(os.environ['MAIL_DMS_FIXTURE_ROOT'])
else:sys.exit(1)
''')
            docker.chmod(0o700)
            env = {k: v for k, v in os.environ.items() if not k.startswith(('MAIL_', 'DOCKER_', 'ANAS_TEST_'))}
            env.update(MAIL_DMS_FIXTURE_ROOT=str(parent), MAIL_DMS_FIXTURE_CONTAINER='fixture-dms',
                       MAIL_DMS_FIXTURE_NETWORK='fixture-network', DOCKER_CMD=str(docker),
                       TRUSTED='1' if trusted else '0', CALLS=str(root/'calls'))
            result = subprocess.run(['python3', str(driver)], capture_output=True, env=env, timeout=15,
                                    preexec_fn=lambda: os.umask(0o077))
            self.assertEqual((parent/'roundcube-fixture/pgdata').stat().st_mode & 0o777, 0o755)
            self.assertEqual((parent/'roundcube-fixture').stat().st_mode & 0o777, 0o700)
            final = json.loads((parent/'roundcube-fixture/evidence/final.json').read_text())
            calls = [json.loads(row) for row in (root/'calls').read_text().splitlines()]
            return result, final, calls

    def test_verified_offline_configs_reach_build(self):
        result, final, calls = self.run_driver(True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(final['failure'], 'RuntimeError: native Roundcube fixture command failed; private log preserved')
        self.assertEqual(sum(row[0]=='build' for row in calls), 1)
        self.assertFalse(any(row[0] in ['run', 'exec', 'rm'] for row in calls))

    def test_unknown_roundcube_config_is_rejected_before_build(self):
        result, final, calls = self.run_driver(False)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(final['failure'], 'RuntimeError: fixed native dependency repository digest mismatch')
        self.assertFalse(any(row[0] in ['build', 'run', 'exec', 'rm'] for row in calls))


if __name__ == '__main__':
    unittest.main()
