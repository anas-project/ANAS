#!/usr/bin/env python3
"""Fresh-VM actual CLI render/apply, Incus Hook, Provider and two consumers.

Reuse the installed approval fixture and bounded native-process supervisor.
This test adds no authentication/backend escape hatch and never reads a physical
host Docker socket. The fixture image is explicitly not a signed/bootable guest.
"""
import argparse
from collections import Counter
import http.client
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import socket
import sys

INPUTS = Path('/opt/anas-core-inputs')
HOST_INPUTS = Path('/opt/anas-host-action-inputs')
CORE_PARENT = 'TestNativeCoreComputeProjection'
CORE_TESTS = (CORE_PARENT, *(CORE_PARENT + '/' + name for name in (
    'approved_host_and_empty_experiment', 'trusted_fixture_and_actual_provider_compose',
    'cli_render_without_manual_host_connection', 'frozen_target_and_private_resource_projections',
    'real_compose_apply_and_nonroot_image_import', 'two_existing_projects_are_mutually_restricted',
    'repeat_cli_render_and_apply_preserves_credentials', 'removed_consumer_lease_is_revoked',
    'failed_activation_leaves_new_revision_outside_rollback', 'rollback_restores_lost_image_from_frozen_artifact',
    'lost_frozen_artifact_fails_without_rebuild', 'stopped_for_explicit_prune')))
CLEANUP_TESTS = ('TestNativeCoreComputeCleanup',)
REVOKED_TESTS = ('TestNativeCoreRejectsRevokedAutomaticConnection',)
# Image prune reads every registered workspace, so register only the one the
# Core test creates.
CORE_WORKSPACES = (('native', '/srv/anas/host-action-native'),)
STATE = Path('/opt/anas-core-projection/private/core-state.json')


def incus_images(project):
    """Read one project's image fingerprints straight from the local daemon."""
    connection = http.client.HTTPConnection('localhost', timeout=30)
    connection.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    connection.sock.settimeout(30)
    connection.sock.connect('/var/lib/incus/unix.socket')
    connection.request('GET', '/1.0/images?recursion=1&project=' + project)
    response = connection.getresponse()
    body = json.loads(response.read(4 << 20))
    connection.close()
    if response.status != 200 or body.get('type') != 'sync':
        raise RuntimeError('image inventory unavailable')
    return sorted(item['fingerprint'] for item in body['metadata'])


def prune_verdicts(state, planned, applied):
    """Exactly the failed deployment's revision is deleted; rollback targets stay,
    including one whose image the daemon has lost."""
    retained = {(item['project'], item['fingerprint']): item['reasons'] for item in planned.get('retained') or []}
    target = {'project': 'anas-core-one', 'fingerprint': state['pin_r2']}
    lost = retained.get(('anas-core-one', state['pin_r3']), [])
    return (planned.get('delete') == [target]
            and 'current_deployment' in retained.get(('anas-core-one', state['pin_r1']), [])
            and 'previous_deployment' in retained.get(('anas-core-two', state['pin_r1']), [])
            and 'previous_deployment' in lost and 'not_present' in lost
            and applied.get('deleted') == [{**target, 'verified': True}] and applied.get('partial') is False)


def events_passed(events, expected, code):
    if code != 0 or not isinstance(events, list):
        return False
    runs, passes = [], []
    for event in events:
        if not isinstance(event, dict) or event.get('Action') in ('fail', 'skip'):
            return False
        name = event.get('Test')
        if name is not None and name not in expected:
            return False
        if event.get('Action') == 'run' and name:
            runs.append(name)
        if event.get('Action') == 'pass' and name:
            passes.append(name)
    return Counter(runs) == Counter(expected) and Counter(passes) == Counter(expected)


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def run(identity):
    # Check exact disposable VM identity before even importing the driver that
    # is allowed to install this experiment's root services.
    if (os.geteuid() != 0 or not re.fullmatch(r'anas-incus-host-[a-f0-9]{6}', identity) or
            Path('/var/lib/cloud/data/instance-id').read_text().strip() != identity or
            Path('/sys/devices/virtual/dmi/id/sys_vendor').read_text().strip() != 'QEMU'):
        raise RuntimeError('isolated native VM required')
    driver = HOST_INPUTS/'server-incus-host-action-e2e.py'
    info = driver.lstat()
    if driver.is_symlink() or not driver.is_file() or info.st_uid != 0 or info.st_mode & 0o022:
        raise RuntimeError('trusted installed-host fixture required')
    native = load_module('core_installed_host_actions', driver)
    native.digest_input(HOST_INPUTS/'source-manifest.json')
    source = json.loads((HOST_INPUTS/'source-manifest.json').read_bytes())
    native.require(native.digest_input(driver) == source['runner_sha256'], 'host_driver_identity')
    native.digest_input(INPUTS/'core-manifest.json')
    manifest = json.loads((INPUTS/'core-manifest.json').read_bytes())
    native.require(manifest.get('schema') == 'anas.native-core-inputs/v1' and
                   10 <= len(manifest.get('files', {})) <= 4096, 'core_input_manifest')
    for path, digest in manifest['files'].items():
        relative = Path(path)
        native.require(not relative.is_absolute() and '..' not in relative.parts and
                       native.digest_input(INPUTS/relative) == digest, 'core_input_digest')
    native.require(native.digest_input(Path(__file__).resolve()) == manifest['files'][Path(__file__).name] and
                   manifest['files']['anas'] == source['artifacts']['anas'], 'core_product_identity')
    supervisor = load_module('core_native_process', INPUTS/'server-incus-host-provision-e2e.py')
    baseline = native.guarded_vm(identity)
    def init_workspace(path):
        # The installed product CLI creates the one workspace this test uses;
        # the fixture never manufactures its state.
        require_fresh = not path.exists()
        native.require(require_fresh, 'core_workspace_preexists')
        path.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
        native.capture(['/usr/local/bin/anas', 'init', str(path), '--yes', '--json'])

    token = native.install_fixture(source, CORE_WORKSPACES, init_workspace)
    events = []
    stage = 'approved_host_install_configure_enroll'

    def passed(name, **facts):
        event = {'stage': name, 'status': 'passed', **facts}
        events.append(event)
        print(json.dumps(event, sort_keys=True), flush=True)

    def native_test(label, expected):
        report = native.ROOT/'reports'
        command = [str(INPUTS/'test2json'), '-t', '-p', 'github.com/anas-project/ANAS/internal/runner',
                   str(INPUTS/'core.test'), '-test.v=test2json', '-test.count=1', '-test.timeout=10m',
                   '-test.run=^'+expected[0]+'$']
        env = {'PATH': '/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin', 'HOME': '/root', 'LC_ALL': 'C',
               'ANAS_REQUIRE_CORE_COMPUTE_NATIVE': '1'}
        code, timed_out, log_limit = supervisor.run_native_process(
            command, env, report/(label+'.jsonl'), report/(label+'.stderr'), 11*60, 4 << 20)
        try:
            test_events = [json.loads(line) for line in (report/(label+'.jsonl')).read_text().splitlines()]
        except (ValueError, UnicodeError):
            test_events = []
        native.require(not timed_out and not log_limit and events_passed(test_events, expected, code),
                       'core_native_test_failed')
        passed(label, required_test_passes=len(expected))

    try:
        native.systemd_identity(source)
        client = native.Console()
        client.enroll(token)
        request = {'interface': 'incus_container', 'storage_size_gib': 16}

        def confirmed(phase):
            plan = client.wait_job(client.cli('incus-plan', '-w', 'native', '--phase', phase,
                                              '--request-json', json.dumps(request), '--session-json', '-'))
            proof = client.cli('incus-confirm', '-w', 'native', '--plan-job', plan['id'],
                               '--action', 'incus.'+phase, '--session-json', '-')
            client.credentials.append(proof['token'])
            envelope = {'session': client.envelope(), 'plan_job_id': plan['id'], 'confirmation_token': proof['token'],
                        'parameters': plan['result']['value']['parameters']}
            applied = client.wait_job(client.cli('incus-apply', '-w', 'native', '--phase', phase,
                                                 '--request-json', '-', request=envelope))
            native.validate_phase_result(applied['result']['value'], phase, skip=False)
            return applied['id']

        setup = [confirmed(phase) for phase in ('install', 'configure', 'enroll')]
        passed(stage, job_ids=setup)
        stage = 'core_cli_compose_projection'
        native_test(stage, CORE_TESTS)
        stage = 'image_prune_confirmed_delete'
        state = json.loads(STATE.read_text())
        native.require(incus_images('anas-core-one') == sorted([state['pin_r1'], state['pin_r2']]), 'prune_precondition')
        plan = client.wait_job(client.cli('incus-prune-plan', '-w', 'native', '--session-json', '-'))
        planned = plan['result']['value']['plan']
        proof = client.cli('incus-confirm', '-w', 'native', '--plan-job', plan['id'],
                           '--action', 'incus.image-prune', '--session-json', '-')
        client.credentials.append(proof['token'])
        envelope = {'session': client.envelope(), 'plan_job_id': plan['id'], 'confirmation_token': proof['token']}
        applied = client.wait_job(client.cli('incus-prune-apply', '-w', 'native', '--request-json', '-', request=envelope))
        native.require(prune_verdicts(state, planned, applied['result']['value']), 'prune_result')
        native.require(incus_images('anas-core-one') == [state['pin_r1']] and
                       incus_images('anas-core-two') == [state['pin_r1']], 'prune_readback')
        passed(stage, plan_job=plan['id'], apply_job=applied['id'], deleted=1)
        stage = 'core_owned_resources_cleaned'
        native_test(stage, CLEANUP_TESTS)
        stage = 'approved_host_connection_revocation'
        removed = confirmed('uninstall')
        passed(stage, job_id=removed)
        stage = 'revoked_automatic_connection_rejected'
        native_test(stage, REVOKED_TESTS)
        stage = 'experiment_docker_baseline_restored'
        native.require(native.docker_identity() == baseline, 'docker_baseline_changed')
        passed(stage)
        shutil.copyfile('/opt/anas-core-projection/reports/core-stage.json', native.ROOT/'reports/core-stage.json')
    except Exception as error:
        events.append({'stage': stage, 'status': 'failed', **native.public_failure(error)})
    unchanged = native.docker_identity() == baseline
    complete = len(events) == 7 and all(event['status'] == 'passed' for event in events) and unchanged
    result = {'schema': 'anas.native-core-projection/v1', 'vm_id': identity, 'passed': complete,
              'events': events, 'docker_baseline_unchanged': unchanged,
              'scope': 'actual CLI render/apply, production Hook/Provider and two synthetic Compose consumers; not full Forgejo/AI Agent or bootable/signed guest release'}
    native.write_new(native.ROOT/'reports/core-summary.json', json.dumps(result, sort_keys=True)+'\n')
    print(json.dumps(result, sort_keys=True), flush=True)
    return 0 if complete else 1


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--vm-id', required=True)
    args = parser.parse_args()
    os.umask(0o077)
    try:
        sys.exit(run(args.vm_id))
    except Exception:
        print(json.dumps({'passed': False, 'stage': 'fixture_preparation', 'code': 'core_fixture_rejected'}), flush=True)
        sys.exit(1)
