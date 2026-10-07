#!/usr/bin/env python3
"""Build and check Lego on an explicitly isolated Docker daemon.

The alternate issuer is a local ACME fixture, not a public CA/DNS-01 test.
Raw logs and execution.json are written outside the source tree by default.
"""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import uuid
import sys

ROOT = Path(__file__).resolve().parents[2]


def main():
    subprocess.run(['sh', '-c', '. "$1"', 'sh', str(ROOT / 'test-env/scripts/server-require-isolated-docker.sh')], check=True)
    evidence = Path(os.environ.get('ANAS_LEGO_EVIDENCE_DIR', tempfile.mkdtemp(prefix='anas-lego-e2e-'))).resolve()
    evidence.mkdir(parents=True, exist_ok=True)
    scope = 'anas-lego-e2e-' + uuid.uuid4().hex[:12]
    image, volume, container = scope + ':candidate', scope + '-certs', scope
    report = {'scope': scope, 'evidence': str(evidence), 'alternate_issuer': 'local ACME fixture; no public DNS-01 issuance', 'checks': []}

    def docker(*args, input=None):
        result = subprocess.run(['docker', *args], input=input, text=True, capture_output=True, timeout=600 if args[0] == 'build' else 90)
        with (evidence / 'docker.log').open('a') as log:
            log.write(result.stdout + result.stderr)
        if result.returncode:
            raise RuntimeError('docker command failed: ' + ' '.join(args[:3]) + '; see docker.log')
        return result.stdout.strip()

    def shell(script):
        return docker('exec', '-i', container, 'sh', '-eu', input=script)

    def check(name, condition):
        if not condition:
            raise AssertionError(name)
        report['checks'].append({'name': name, 'passed': True})
        print(name + ': PASS', flush=True)

    def pair_digest(prefix):
        return shell('sha256sum /certs/certificates/' + prefix + '.crt /certs/certificates/' + prefix + '.key')

    def tls_check(name):
        shell('''if [ -f /tmp/tls.pid ]; then kill "$(cat /tmp/tls.pid)" 2>/dev/null || true; fi''')
        time.sleep(0.2)
        docker('exec', '-d', container, 'sh', '-c', 'echo $$ >/tmp/tls.pid; exec openssl s_server -quiet -accept 8443 -cert /certs/certificates/example.test.crt -key /certs/certificates/example.test.key -www >/tmp/tls.log 2>&1')
        # The second container consumes the shared trust bundle read-only.
        for attempt in range(10):
            try:
                output = docker('run', '--rm', '--network', 'container:' + container,
                                '-v', volume + ':/certs:ro', '--entrypoint', 'sh', image,
                                '-c', "set -eu; printf 'GET / HTTP/1.0\\r\\n\\r\\n' | openssl s_client -connect 127.0.0.1:8443 -servername svc.example.test -verify_hostname svc.example.test -verify_return_error -CAfile /certs/certificates/anas-trust-bundle.crt -showcerts -ign_eof >/tmp/peer; openssl x509 -in /tmp/peer -outform DER | openssl dgst -sha256 >/tmp/actual; openssl x509 -in /certs/certificates/example.test.crt -outform DER | openssl dgst -sha256 >/tmp/expected; cmp /tmp/actual /tmp/expected; cat /tmp/peer")
                check(name, 'HTTP/1.0 200' in output)
                return
            except RuntimeError:
                if attempt == 9:
                    raise
                time.sleep(0.2)

    def wait_ready():
        for attempt in range(60):
            try:
                shell('test -s /certs/certificates/example.test.key; test -s /certs/certificates/anas-trust-bundle.crt; pgrep crond >/dev/null')
                break
            except RuntimeError:
                if attempt == 59:
                    raise
                time.sleep(0.5)

    volume_created = False
    container_created = False
    try:
        for name in ('ca.sh', 'run.sh', 'cert.sh', 'Dockerfile'):
            report.setdefault('source_sha256', {})[name] = hashlib.sha256((ROOT / 'modules/lego/lego' / name).read_bytes()).hexdigest()
        report['test_script_sha256'] = hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
        base = os.environ.get('ANAS_LEGO_BASE_IMAGE')
        build_args = []
        if base:
            # Explicit offline runtime validation; never pretend this rebuilt upstream layers.
            report['build_mode'] = 'cached Lego runtime with current certificate scripts'
            report['base_image'] = base
            report['base_image_id'] = docker('image', 'inspect', '--format', '{{.Id}}', base)
            candidate_dockerfile = evidence / 'Candidate.Dockerfile'
            candidate_dockerfile.write_text('FROM ' + base + '\nWORKDIR /certs\nCOPY cert.sh run.sh ca.sh /root/\nRUN chmod +x /root/cert.sh /root/run.sh /root/ca.sh\nENTRYPOINT ["/root/run.sh"]\n')
            build_args = ['-f', str(candidate_dockerfile)]
        else:
            report['build_mode'] = 'repository Dockerfile'
            report['build_arguments'] = {}
            for key in ('DOCKER_HUB_REGISTRY', 'CHINESE_BUILD_SPEEDUP', 'APK_MIRROR_URL'):
                if os.environ.get(key):
                    report['build_arguments'][key] = os.environ[key]
                    build_args += ['--build-arg', key + '=' + os.environ[key]]
            if os.environ.get('DOCKER_BUILD_NETWORK'):
                build_args += ['--network', os.environ['DOCKER_BUILD_NETWORK']]
        docker('build', *build_args, '--iidfile', str(evidence / 'image-id'), '-t', image, str(ROOT / 'modules/lego/lego'))
        report['image_id'] = (evidence / 'image-id').read_text().strip()
        report['lego_version'] = docker('run', '--rm', '--network', 'none', '--entrypoint', '/lego', image, '--version')
        check('candidate Lego version', '5.3.1' in report['lego_version'])
        report['platform'] = docker('image', 'inspect', '--format', '{{.Os}}/{{.Architecture}}', image)
        docker('volume', 'create', '--label', 'anas.test=' + scope, volume)
        volume_created = True
        docker('run', '-d', '--name', container, '--label', 'anas.test=' + scope,
               '--network', 'none', '-v', volume + ':/certs', '-e', 'BASE_DOMAIN=example.test', '-e', 'VIRTUAL_DOMAIN=true',
               '-e', 'LEGO_DNS_SERVER=127.0.0.1', '-e', 'LEGO_CERT_NAME=example.test.crt',
               '-e', 'LEGO_KEY_NAME=example.test.key', '-e', 'LEGO_CA_CERT_NAME=example.test.issuer.crt', image)
        container_created = True
        wait_ready()
        internal = pair_digest('anas-internal')
        serving = pair_digest('example.test')
        docker('restart', container)
        wait_ready()
        check('restart preserves internal pair and serving pair', internal == pair_digest('anas-internal') and serving == pair_digest('example.test'))
        check('private key permissions', shell('stat -c %a /certs/certificates/anas-internal.key /certs/ca/ca.key /certs/certificates/example.test.key').splitlines() == ['600'] * 3)
        tls_check('consumer verifies internal TLS')
        shell('''mkdir -p /fixture
openssl req -x509 -newkey rsa:2048 -nodes -days 365 -keyout /fixture/ca.key -out /fixture/ca.crt -subj /CN=Alternate-test-CA -addext basicConstraints=critical,CA:TRUE
openssl req -newkey rsa:2048 -nodes -keyout /fixture/leaf.key -out /fixture/leaf.csr -subj /CN=example.test
printf 'subjectAltName=DNS:example.test,DNS:*.example.test\n' >/fixture/ext
openssl x509 -req -in /fixture/leaf.csr -CA /fixture/ca.crt -CAkey /fixture/ca.key -CAcreateserial -days 30 -extfile /fixture/ext -out /fixture/leaf.crt
cat /fixture/ca.crt >>/etc/ssl/certs/ca-certificates.crt
/root/ca.sh bootstrap
mv /lego /lego.original
cat >/lego <<'STUB'
#!/bin/sh
set -eu
cp /fixture/leaf.crt /certs/certificates/example.test.acme.crt
cp /fixture/leaf.key /certs/certificates/example.test.acme.key
cp /fixture/ca.crt /certs/certificates/example.test.acme.issuer.crt
printf '{"domain":"example.test"}\n' >/certs/certificates/example.test.acme.json
STUB
chmod 755 /lego
''')
        docker('exec', '-e', 'VIRTUAL_DOMAIN=false', '-e', 'LEGO_PROVIDER_CODE=fixture', '-e', 'LEGO_EMAIL=test@example.test', container, '/root/cert.sh')
        alternate = pair_digest('example.test')
        check('ACME adoption replaces serving pair only', alternate != serving and internal == pair_digest('anas-internal') and shell('cat /certs/certificates/.issuer') == 'acme')
        shell('/root/ca.sh bootstrap; /root/ca.sh renew')
        check('valid short-lived alternate pair survives bootstrap and renew', alternate == pair_digest('example.test'))
        tls_check('consumer verifies alternate TLS with same trust bundle path')
        shell('''openssl x509 -in /certs/certificates/example.test.crt -signkey /certs/certificates/example.test.key -days 0 -out /tmp/expired.crt
mv /tmp/expired.crt /certs/certificates/example.test.crt
/root/ca.sh renew''')
        check('expired ACME-marked certificate falls back', shell('cmp /certs/certificates/anas-internal.crt /certs/certificates/example.test.crt; cat /certs/certificates/.issuer') == 'internal')
        check('fallback retains internal pair', internal == pair_digest('anas-internal'))
        tls_check('consumer verifies fallback TLS after reload')
        shell('rm /certs/certificates/example.test.key; /root/ca.sh bootstrap')
        check('missing serving key restored', serving == pair_digest('example.test'))
        shell('cp /fixture/leaf.key /certs/certificates/example.test.key; /root/ca.sh bootstrap')
        check('mismatched serving key restored', serving == pair_digest('example.test'))
        shell('openssl x509 -in /certs/certificates/anas-internal.crt -signkey /certs/certificates/anas-internal.key -days 0 -out /tmp/expired-internal.crt; mv /tmp/expired-internal.crt /certs/certificates/anas-internal.crt; /root/ca.sh renew')
        check('expired retained internal pair is reissued', internal != pair_digest('anas-internal'))
        shell('openssl verify -CAfile /certs/certificates/anas-internal-ca.crt /certs/certificates/anas-internal.crt')
        old_ca = shell('sha256sum /certs/certificates/anas-internal-ca.crt')
        shell('cp /fixture/ca.key /certs/ca/ca.key; /root/ca.sh renew')
        check('invalid internal CA key rebuilds issuer and serving certificate', old_ca != shell('sha256sum /certs/certificates/anas-internal-ca.crt') and shell('cmp /certs/certificates/anas-internal.crt /certs/certificates/example.test.crt; cat /certs/certificates/.issuer') == 'internal')
        tls_check('consumer verifies rebuilt internal CA through updated trust bundle')
        report['passed'] = True
    except Exception as exc:
        report['passed'] = False
        report['error'] = str(exc)
        raise
    finally:
        if container_created:
            with (evidence / 'container.log').open('w') as log:
                subprocess.run(['docker', 'logs', container], stdout=log, stderr=subprocess.STDOUT, timeout=90)
        cleanup = []
        if container_created:
            cleanup.append(subprocess.run(['docker', 'rm', '-f', container], capture_output=True, timeout=90))
        if volume_created:
            cleanup.append(subprocess.run(['docker', 'volume', 'rm', volume], capture_output=True, timeout=90))
        report['cleanup_passed'] = all(result.returncode == 0 for result in cleanup)
        if not report['cleanup_passed']:
            report['passed'] = False
            report['cleanup_error'] = 'Container or volume cleanup failed; inspect the named resources.'
        report['candidate_image'] = image  # Keep the candidate image for inspection.
        (evidence / 'execution.json').write_text(json.dumps(report, indent=2) + '\n')
        print('Evidence: ' + str(evidence), flush=True)
        if not report['cleanup_passed'] and sys.exc_info()[0] is None:
            raise RuntimeError(report['cleanup_error'])


if __name__ == '__main__':
    main()
