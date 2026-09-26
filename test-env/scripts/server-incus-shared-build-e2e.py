#!/usr/bin/env python3
"""Build the three compute images from source and build-only staging layouts.

Requires an explicitly identified disposable QEMU VM and an already isolated
test Docker daemon. Never starts a deployment or reads its runtime .env. The
staging fixture contains the actual module build inputs, not a fake Go module;
it does not claim to test the complete `anas build/apply` lifecycle.
"""
import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import stat
import subprocess
import sys
import time
from urllib.parse import urlsplit

MODULES = {
    'incus': ('./provisioner', 'anas-incus-provisioner', 1),
    'forgejo': ('./actions-controller', 'anas-forgejo-actions-controller', 2),
    'ai_agent': ('./orchestrator', 'anas-ai-agent', 1),
}
SCHEMA = 'anas.shared-build-inputs/v1'
LABEL = 'io.anas.test.shared-build'
INCUS_MODULE_VERSION = 'v7.3.0'
# The pinned upstream module prints "7.3", not its Go module tag "v7.3.0".
INCUS_CLI_VERSIONS = {'7.3', '7.3.0'}


def require_vm(identity):
    if (os.geteuid() != 0 or not re.fullmatch(r'anas-incus-build-[a-z0-9]{6}', identity)
            or Path('/var/lib/cloud/data/instance-id').read_text().strip() != identity
            or Path('/sys/class/dmi/id/sys_vendor').read_text().strip() != 'QEMU'):
        raise RuntimeError('exact disposable QEMU build identity and root are required')


def validate_transport(registry, proxy):
    if len(registry) > 512 or not re.fullmatch(r'[a-z0-9][a-z0-9.-]*(?::[0-9]{1,5})?(?:/[a-z0-9][a-z0-9._-]*)*', registry):
        raise RuntimeError('registry must be a hostname and optional repository prefix, not a credential or URL')
    registry_url = urlsplit('https://'+registry)
    if registry_url.port is not None and not 1 <= registry_url.port <= 65535:
        raise RuntimeError('registry port is outside the valid range')
    components = proxy.split(',')
    if len(components) not in (1, 2) or (len(components) == 2 and components[1] != 'direct'):
        raise RuntimeError('module proxy must be HTTPS with an optional direct fallback')
    parsed = urlsplit(components[0])
    if (parsed.scheme != 'https' or not parsed.hostname or not re.fullmatch(r'[a-zA-Z0-9][a-zA-Z0-9.-]*', parsed.hostname)
            or (parsed.port is not None and not 1 <= parsed.port <= 65535) or parsed.username or parsed.password
            or parsed.query or parsed.fragment or any(c.isspace() for c in proxy)):
        raise RuntimeError('module proxy must not contain credentials, query or fragments')


def select_builds(report, source, build_root):
    if (report.get('schema') != SCHEMA or report.get('docker_executed') is not False
            or report.get('source_root') != str(source) or report.get('build_root') != str(build_root)):
        raise RuntimeError('build report does not identify the requested source and layout')
    grouped = {name: [] for name in MODULES}
    for item in report.get('images', []):
        module = item.get('module')
        if module not in grouped:
            raise RuntimeError('unexpected shared-context image in native acceptance')
        if (item.get('compose_directory') != str(build_root/'modules'/module)
                or item.get('shared_root') != str(source)
                or not re.fullmatch(r'[a-z0-9_]+', item.get('service', ''))
                or not re.fullmatch(r'[0-9a-f]{64}', item.get('input_digest', ''))
                or item.get('build', {}).get('context') != MODULES[module][0]
                or item['build'].get('additional_contexts') != {'shared': '${ANAS_SHARED_BUILD_CONTEXT:-../..}'}):
            raise RuntimeError('build metadata escaped the selected module or shared input')
        grouped[module].append(item)
    selected = {}
    for module, items in grouped.items():
        if len(items) != MODULES[module][2] or len({item['service'] for item in items}) != len(items):
            raise RuntimeError('required Compose builds are missing or duplicated')
        first = sorted(items, key=lambda item: item['service'])[0]
        if any(item['build'] != first['build'] or item['input_digest'] != first['input_digest'] for item in items):
            raise RuntimeError('services sharing an image have different build inputs')
        selected[module] = first
    return selected


def build_projection(item, image, scope):
    build = copy.deepcopy(item['build'])
    if set(build) - {'context', 'dockerfile', 'additional_contexts', 'args', 'network'}:
        raise RuntimeError('unsupported build input in native projection')
    build['labels'] = {LABEL: scope}
    return {'services': {item['service']: {'image': image, 'build': build}}}


def parse_probe_output(module, output):
    if module not in MODULES:
        raise RuntimeError('unexpected module probe')
    lines = output.decode('utf-8').splitlines()
    if len(lines) != (3 if module == 'forgejo' else 1):
        raise RuntimeError('image probe returned incomplete or extra evidence')

    def digest(line, binary):
        fields = line.split()
        if (len(fields) != 2 or not re.fullmatch(r'[0-9a-f]{64}', fields[0])
                or fields[1] != '/usr/local/bin/'+binary):
            raise RuntimeError('image probe digest is not bound to the expected binary')
        return fields[0]

    result = {'binary_sha256': digest(lines[0], MODULES[module][1])}
    if module == 'forgejo':
        result['incus_cli_sha256'] = digest(lines[1], 'incus')
        key, separator, version = lines[2].partition('=')
        if key != 'incus_cli_version' or separator != '=' or version not in INCUS_CLI_VERSIONS:
            raise RuntimeError('image probe contains an unexpected Incus CLI version')
        result['incus_cli_version'] = version
        result['incus_module_version'] = INCUS_MODULE_VERSION
    return result


def results_passed(results, missing_override_rejected, containers_removed, inputs_rechecked):
    required = {(phase, module) for phase in ('source', 'staging') for module in MODULES}
    actual = {(item.get('phase'), item.get('module')) for item in results}
    if (len(results) != len(required) or actual != required or missing_override_rejected is not True
            or containers_removed is not True or inputs_rechecked is not True):
        return False
    by_key = {(item['phase'], item['module']): item for item in results}
    return all(
        item.get('passed') is True
        and re.fullmatch(r'[0-9a-f]{64}', item.get('binary_sha256', '')) is not None
        and re.fullmatch(r'[0-9a-f]{64}', item.get('input_digest', '')) is not None
        and item['binary_sha256'] == by_key[('source', item['module'])]['binary_sha256']
        and item.get('input_digest') == by_key[('source', item['module'])].get('input_digest')
        and (item['module'] != 'forgejo' or (
            item.get('incus_module_version') == INCUS_MODULE_VERSION
            and item.get('incus_cli_version') in INCUS_CLI_VERSIONS
            and item['incus_cli_version'] == by_key[('source', 'forgejo')].get('incus_cli_version')
            and re.fullmatch(r'[0-9a-f]{64}', item.get('incus_cli_sha256', '')) is not None
            and item['incus_cli_sha256'] == by_key[('source', 'forgejo')].get('incus_cli_sha256')))
        for item in results)


def missing_context_failure(result, diagnostic):
    text = diagnostic.decode('utf-8', errors='replace').lower()
    # A generic nonzero status, network error, cancellation, or an unavailable
    # base image cannot substitute for this negative path-resolution control.
    return result != 0 and re.search(
        r'["\s/](?:go\.(?:mod|sum)|internal/(?:computeclient|computeingress|computeimage|securefs))'
        r'["\s:][^\n]*(?:not found|no such file)', text) is not None


def limit_client_output():
    # This limit belongs to the short-lived client, not the Docker daemon or
    # compiler. It bounds log files while a timed build is still running.
    import resource
    resource.setrlimit(resource.RLIMIT_FSIZE, (32 << 20, 32 << 20))


def main(args):
    require_vm(args.vm_id)  # Before any Docker command or source execution.
    validate_transport(args.registry, args.go_module_proxy)
    source, report, checker = Path(args.source_root), Path(args.report_root), Path(args.checker)
    if (not source.is_absolute() or source.resolve() != source or not source.is_dir()
            or not checker.is_absolute() or checker.is_symlink() or not checker.is_file()
            or not report.is_absolute() or report.exists() or report.parent.resolve() != report.parent
            or report == source or report in source.parents or source in report.parents):
        raise RuntimeError('explicit source, checker and separate fresh report roots are required')
    sock = Path(args.docker_socket)
    if not re.fullmatch(r'/run/anas-[a-z0-9-]+-test\.sock', str(sock)):
        raise RuntimeError('an explicit ANAS test Docker socket is required')
    info = sock.lstat()
    if not stat.S_ISSOCK(info.st_mode) or info.st_uid != 0:
        raise RuntimeError('test Docker endpoint must be a root-owned socket, not a symlink')
    os.umask(0o077)
    report.mkdir(mode=0o700)
    for name in ('home', 'docker-config', 'logs', 'projections'):
        (report/name).mkdir(mode=0o700)
    (report/'empty.env').write_text('')
    (report/'docker-config/config.json').write_text('{}\n')
    env = {'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'HOME': str(report/'home'),
           'DOCKER_HOST': 'unix://'+str(sock), 'DOCKER_CONFIG': str(report/'docker-config'),
           'DOCKER_BUILDKIT': '1', 'COMPOSE_BAKE': 'false', 'COMPOSE_PARALLEL_LIMIT': '1',
           'BUILDKIT_PROGRESS': 'plain', 'DOCKER_BUILD_NETWORK': 'host',
           # Exercise the repository's existing transport settings and the
           # actual nested Compose fallbacks, not test-only direct overrides.
           'DOCKER_HUB_REGISTRY': args.registry, 'GOPROXY_URL': args.go_module_proxy}
    sequence = 0

    def call(argv, *, extra=None, timeout=90, check=True):
        nonlocal sequence
        sequence += 1
        child_env = env.copy()
        child_env.update(extra or {})
        output_path = report/'logs'/f'{sequence:03d}.log'
        error_path = report/'logs'/f'{sequence:03d}.stderr.log'
        with output_path.open('xb') as output, error_path.open('xb') as errors:
            process = subprocess.Popen(argv, env=child_env, stdin=subprocess.DEVNULL,
                                       stdout=output, stderr=errors, start_new_session=True,
                                       preexec_fn=limit_client_output)
            try:
                result = process.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                try:
                    os.killpg(process.pid, signal.SIGTERM)
                    process.wait(timeout=5)
                except (ProcessLookupError, subprocess.TimeoutExpired):
                    try:
                        os.killpg(process.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                    process.wait(timeout=5)
                raise RuntimeError(f'test command timed out; private log {sequence:03d}') from None
        if output_path.stat().st_size + error_path.stat().st_size > 32 << 20:
            raise RuntimeError('native build output exceeded its limit')
        if check and result:
            raise RuntimeError(f'test command failed; private log {sequence:03d}')
        body = output_path.read_bytes()
        # JSON stays on stdout; even a benign CLI warning on stderr must not
        # corrupt machine-readable checker/Compose/Docker responses.
        if result:
            body += error_path.read_bytes()
        return result, body

    guard = source/'test-env/scripts/server-require-isolated-docker.sh'
    call(['/bin/sh', '-c', '. "$1"', 'guard', str(guard)])
    docker = ['/usr/bin/docker', '--host', env['DOCKER_HOST']]
    daemon = json.loads(call(docker+['info', '--format', '{{json .}}'])[1])
    docker_root = Path(daemon['DockerRootDir'])
    if (not re.fullmatch(r'/var/lib/anas-[a-z0-9-]+-test', str(docker_root))
            or docker_root.resolve() != docker_root or not daemon.get('ID')):
        raise RuntimeError('daemon must use a separate canonical ANAS test data root')
    daemon_id = daemon['ID']

    def confirm_daemon():
        current = json.loads(call(docker+['info', '--format', '{{json .}}'])[1])
        if current.get('ID') != daemon_id or current.get('DockerRootDir') != str(docker_root):
            raise RuntimeError('test Docker identity changed')

    if call(docker+['ps', '-aq'])[1].strip():
        raise RuntimeError('native build fixture requires a daemon with no existing containers')
    scope = args.vm_id+'-'+os.urandom(6).hex()
    metadata = {'schema': 'anas.shared-build-native/v1', 'vm_id': args.vm_id,
                'docker_version': daemon.get('ServerVersion'), 'docker_id': daemon_id,
                'docker_root': str(docker_root), 'scope': scope,
                'checker_sha256': hashlib.sha256(checker.read_bytes()).hexdigest(),
                'registry': args.registry, 'module_proxy': args.go_module_proxy,
                'build_only_staging': True}
    (report/'environment.json').write_text(json.dumps(metadata, sort_keys=True)+'\n')

    def plan(build_root, phase):
        argv = [str(checker), '--source-root', str(source), '--json']
        extra = {}
        if build_root != source:
            argv += ['--staging-root', str(build_root)]
            extra['ANAS_SHARED_BUILD_CONTEXT'] = str(source)
        document = json.loads(call(argv, extra=extra)[1])
        (report/(phase+'-inputs.json')).write_text(json.dumps(document, sort_keys=True)+'\n')
        return select_builds(document, source, build_root)

    selected = plan(source, 'source')
    stage = report/'staging'
    stage.mkdir(mode=0o700)
    for module, item in selected.items():
        destination = stage/'modules'/module
        destination.mkdir(mode=0o700, parents=True)
        original = Path(item['compose_directory'])
        shutil.copy2(original/'docker-compose.yml', destination/'docker-compose.yml')
        shutil.copytree(original/item['build']['context'], destination/item['build']['context'], symlinks=True)
    staged = plan(stage, 'staging')
    if any(selected[name]['input_digest'] != staged[name]['input_digest'] for name in MODULES):
        raise RuntimeError('staging input identity differs from source')
    # A missing override must fail the real checker as well as Docker later.
    rejected, _ = call([str(checker), '--source-root', str(source), '--staging-root', str(stage)], check=False)
    if not rejected:
        raise RuntimeError('static staging check accepted a missing shared override')
    results, images, containers = [], [], []
    missing_override_rejected = False
    inputs_rechecked = False

    def compose(item, path, project):
        return docker+['compose', '--project-directory', item['compose_directory'],
                       '--env-file', str(report/'empty.env'), '--file', str(path), '--project-name', project]

    try:
        for phase, items in (('source', selected), ('staging', staged)):
            for module in MODULES:
                item = items[module]
                image = 'anas-test-shared-'+scope+'-'+phase+'-'+module+':verify'
                project = 'anas-test-shared-'+scope+'-'+phase+'-'+module
                projection = report/'projections'/(phase+'-'+module+'.json')
                projection.write_text(json.dumps(build_projection(item, image, scope), sort_keys=True)+'\n')
                command = compose(item, projection, project)
                extra = {'ANAS_SHARED_BUILD_CONTEXT': str(source)} if phase == 'staging' else {}
                resolved = json.loads(call(command+['config', '--format', 'json'], extra=extra)[1])
                actual = resolved['services'][item['service']]['build']
                if (Path(actual['context']) != Path(item['compose_directory'])/item['build']['context']
                        or actual['additional_contexts'] != {'shared': str(source)}):
                    raise RuntimeError('Compose resolved a different module or shared context')
                if (actual['args'].get('DOCKER_HUB_REGISTRY') != args.registry
                        or actual['args'].get('GO_BUILDER_REGISTRY') != args.registry
                        or (module != 'incus' and actual['args'].get('GO_MODULE_PROXY') != args.go_module_proxy)):
                    raise RuntimeError('Compose did not resolve the repository transport policy')
                (report/'projections'/(phase+'-'+module+'-resolved.json')).write_text(json.dumps(resolved, sort_keys=True)+'\n')
                confirm_daemon()
                if phase == 'staging' and module == 'incus':
                    rejected, diagnostic = call(command+['build', item['service']], check=False, timeout=240)
                    if not missing_context_failure(rejected, diagnostic):
                        raise RuntimeError('missing-override build did not fail specifically on missing shared inputs')
                    missing_override_rejected = True
                images.append(image)
                started = time.monotonic()
                call(command+['build', '--no-cache', item['service']], extra=extra, timeout=1200)
                inspected = json.loads(call(docker+['image', 'inspect', image])[1])
                if (len(inspected) != 1 or inspected[0]['Config'].get('Labels', {}).get(LABEL) != scope
                        or inspected[0]['Config'].get('User') != '65532:65532'
                        or inspected[0]['Config'].get('Entrypoint') != ['/usr/local/bin/'+MODULES[module][1]]
                        or inspected[0].get('Os') != 'linux'):
                    raise RuntimeError('built image identity or non-root runtime contract differs')
                container = project+'-probe'
                containers.append(container)
                probe = 'test "$(id -u)" = 65532; test -x /usr/local/bin/'+MODULES[module][1]+'; sha256sum /usr/local/bin/'+MODULES[module][1]
                if module == 'forgejo':
                    probe += '; sha256sum /usr/local/bin/incus; version=$(incus --version); printf "incus_cli_version=%s\\n" "$version"'
                _, output = call(docker+['run', '--rm', '--name', container, '--label', LABEL+'='+scope,
                                         '--network', 'none', '--read-only', '--cap-drop', 'ALL',
                                         '--security-opt', 'no-new-privileges', '--pids-limit', '32', '--memory', '128m',
                                         '--cpus', '1', '--entrypoint', '/bin/sh', image, '-eu', '-c', probe])
                record = {'phase': phase, 'module': module, 'passed': True, 'image_id': inspected[0]['Id'],
                          'architecture': inspected[0]['Architecture'], **parse_probe_output(module, output),
                          'input_digest': item['input_digest'], 'build_probe_ms': int((time.monotonic()-started)*1000)}
                results.append(record)
                print(json.dumps(record, sort_keys=True), flush=True)
        after_source, after_stage = plan(source, 'source-after'), plan(stage, 'staging-after')
        if any(selected[name]['input_digest'] != after_source[name]['input_digest']
               or selected[name]['input_digest'] != after_stage[name]['input_digest'] for name in MODULES):
            raise RuntimeError('build inputs changed during the native run')
        inputs_rechecked = True
    finally:
        confirm_daemon()
        existing = call(docker+['ps', '-aq'])[1].decode().split()
        for identifier in existing:
            item = json.loads(call(docker+['container', 'inspect', identifier])[1])[0]
            if item['Config'].get('Labels', {}).get(LABEL) != scope or item['Name'].lstrip('/') not in containers:
                raise RuntimeError('unexpected container; refusing native fixture cleanup')
            call(docker+['container', 'rm', '--force', identifier])
        for image in images:
            code, output = call(docker+['image', 'inspect', image], check=False)
            if code:
                # A failed build may not have created its expected tag. Verify
                # absence via inventory instead of treating every inspect error
                # as proof of absence.
                if call(docker+['image', 'ls', '-q', image])[1].strip():
                    raise RuntimeError('cannot confirm ownership of a built image')
                continue
            item = json.loads(output)[0]
            if item['Config'].get('Labels', {}).get(LABEL) != scope:
                raise RuntimeError('image ownership changed; refusing cleanup')
            call(docker+['image', 'rm', image])
        containers_removed = not call(docker+['ps', '-aq'])[1].strip()
        summary = dict(metadata, results=results, missing_override_rejected=missing_override_rejected,
                       containers_removed=containers_removed, inputs_rechecked=inputs_rechecked,
                       passed=results_passed(results, missing_override_rejected, containers_removed, inputs_rechecked))
        (report/'summary.json').write_text(json.dumps(summary, sort_keys=True)+'\n')
    if not summary['passed']:
        raise RuntimeError('required source/staging build and cleanup evidence is incomplete')
    print(json.dumps({'passed': True, 'builds': len(results), 'missing_override_rejected': True,
                      'owned_images_and_containers_removed': True}), flush=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('vm-id', 'source-root', 'report-root', 'checker', 'docker-socket'):
        parser.add_argument('--'+name, required=True)
    parser.add_argument('--registry', default='docker.io')
    parser.add_argument('--go-module-proxy', default='https://proxy.golang.org,direct')
    try:
        main(parser.parse_args())
    except Exception as exc:
        print(json.dumps({'passed': False, 'error_type': type(exc).__name__,
                          'message': str(exc) if type(exc) is RuntimeError else 'private native build operation failed'}), file=sys.stderr)
        sys.exit(1)
