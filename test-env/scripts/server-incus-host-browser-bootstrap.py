#!/usr/bin/env python3
"""Trusted bootstrap/readback for the disposable installed-UI acceptance VM.

The `prepare` output contains a real session credential and MUST flow privately
to the browser driver's stdin, never to a report or terminal log. Owner creation
uses the ordinary installed HTTPS enrollment flow, not an authentication fixture.
Only `finish` produces public evidence. No browser package is installed in the VM.
"""
import argparse
import base64
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import sys

INPUTS = Path('/opt/anas-host-action-inputs')
REQUIRED = ('pinned_test_origin', 'embedded_product_assets', 'initial_consent_required',
            'actual_expiry_clears_consent', 'expiry_never_confirms_or_applies', 'fresh_consent_uses_new_plan',
            'browser_job_succeeded', 'no_secret_in_storage_or_dom')


class BootstrapFailure(RuntimeError):
    def __init__(self):
        super().__init__('native browser bootstrap or evidence validation failed')


def check(condition):
    if not condition:
        raise BootstrapFailure()


def browser_envelope(identity, source, session, public_spki):
    asset = source.get('sources', {}).get('internal/webui/dist/main/assets/main.js')
    check(re.fullmatch(r'anas-incus-host-[a-f0-9]{6}', identity) and
          isinstance(asset, str) and re.fullmatch(r'[a-f0-9]{64}', asset) and
          isinstance(session, dict) and session.get('schema') == 'anas.console-session/v1' and
          session.get('origin') == 'https://anas.native.test:18445' and
          session.get('session', {}).get('source') == 'local' and public_spki)
    return {'schema': 'anas.incus-host-browser/v1', 'vm_id': identity,
            'server_spki_sha256': base64.b64encode(hashlib.sha256(public_spki).digest()).decode(),
            'main_asset_sha256': asset, 'session': session}


def validate_browser_result(result, identity):
    check(isinstance(result, dict) and result.get('schema') == 'anas.incus-host-browser/v1' and
          result.get('vm_id') == identity and result.get('passed') is True)
    events = result.get('events')
    check(isinstance(events, list) and len(events) == len(REQUIRED) and
          {event.get('stage') for event in events} == set(REQUIRED) and
          all(event.get('status') == 'passed' for event in events))


def public_result(result, identity):
    check(isinstance(result, dict) and set(result) <= {'schema', 'vm_id', 'passed', 'events', 'scope'} and
          result.get('schema') == 'anas.incus-host-browser/v1' and result.get('vm_id') == identity and
          isinstance(result.get('passed'), bool))
    events = result.get('events')
    check(isinstance(events, list) and 0 < len(events) <= len(REQUIRED))
    clean = []
    for event in events:
        check(isinstance(event, dict) and set(event) <= {'stage', 'status', 'code', 'browser_version',
              'plan_job_id', 'old_plan_job_id', 'new_plan_job_id', 'apply_job_id', 'job_id', 'elapsed_ms'} and
              event.get('stage') in REQUIRED and event.get('status') in ('passed', 'failed'))
        for key, value in event.items():
            if key.endswith('job_id'):
                check(isinstance(value, str) and re.fullmatch(r'job_[a-f0-9]{32}', value))
            elif key == 'code':
                check(isinstance(value, str) and re.fullmatch(r'[a-z_]{1,64}', value))
            elif key == 'browser_version':
                check(isinstance(value, str) and re.fullmatch(r'[0-9.]{1,64}', value))
            elif key == 'elapsed_ms':
                check(isinstance(value, int) and 0 <= value <= 700000)
        clean.append(event)
    projected = {'schema': result['schema'], 'vm_id': identity, 'passed': result['passed'], 'events': clean}
    if projected['passed']:
        validate_browser_result(projected, identity)
    return projected


def load_native(identity):
    check(os.geteuid() == 0 and re.fullmatch(r'anas-incus-host-[a-f0-9]{6}', identity) and
          Path('/var/lib/cloud/data/instance-id').read_text().strip() == identity and
          Path('/sys/devices/virtual/dmi/id/sys_vendor').read_text().strip() == 'QEMU')
    path = INPUTS/'server-incus-host-action-e2e.py'
    check(path.is_file() and not path.is_symlink() and path.stat().st_uid == 0 and not path.stat().st_mode & 0o022)
    spec = importlib.util.spec_from_file_location('installed_native_browser_owner', path)
    native = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(native)
    native.digest_input(INPUTS/'source-manifest.json')
    native.digest_input(INPUTS/'browser-source-manifest.json')
    source = json.loads((INPUTS/'source-manifest.json').read_bytes())
    browser = json.loads((INPUTS/'browser-source-manifest.json').read_bytes())
    check(native.digest_input(path) == source['runner_sha256'] and
          native.digest_input(INPUTS/'source-manifest.json') == browser['product_manifest_sha256'] and
          native.digest_input(Path(__file__).resolve()) == browser['bootstrap_sha256'])
    return native, source


def prepare(identity):
    native, source = load_native(identity)
    baseline = native.guarded_vm(identity)
    token = native.install_fixture(source)
    owner = native.systemd_identity(source)
    client = native.Console()
    client.enroll(token)
    jobs = []
    request = {'interface': 'incus_container', 'storage_size_gib': 16}
    # The browser will confirm a real teardown, not a fabricated no-op result.
    # Set up its resources through the same installed public CLI/job/approval
    # channel; do not write backend state or invoke internal methods directly.
    for phase in ('install', 'configure', 'enroll'):
        planned = client.wait_job(client.cli('incus-plan', '-w', 'native', '--phase', phase,
                                             '--request-json', json.dumps(request), '--session-json', '-'))
        jobs.append(planned['id'])
        proof = client.cli('incus-confirm', '-w', 'native', '--plan-job', planned['id'],
                           '--action', 'incus.' + phase, '--session-json', '-')
        envelope = {'session': client.envelope(), 'plan_job_id': planned['id'], 'confirmation_token': proof['token'],
                    'parameters': planned['result']['value']['parameters']}
        client.credentials.append(proof['token'])
        applied = client.wait_job(client.cli('incus-apply', '-w', 'native', '--phase', phase,
                                             '--request-json', '-', request=envelope))
        native.validate_phase_result(applied['result']['value'], phase, skip=False)
        jobs.append(applied['id'])
    public_key = native.capture(['openssl', 'x509', '-in', native.TLS/'server.crt', '-noout', '-pubkey'])
    public_spki = native.capture(['openssl', 'pkey', '-pubin', '-outform', 'DER'], body=public_key)
    native.write_new(native.ROOT/'reports/browser-environment.json', json.dumps({
        'schema': 'anas.native-browser-environment/v1', 'vm_id': identity, 'release': source['release'],
        'service': owner, 'docker_baseline': baseline, 'owner_enrolled_via_actual_https': True,
        'preparation_job_ids': jobs, 'actual_install_configure_enroll_passed': True}))
    # Intentionally credential-bearing private output, consumed only through
    # the separately supervised browser process's stdin. Do not log this.
    return browser_envelope(identity, source, client.envelope(), public_spki)


def finish(identity, incoming):
    native, source = load_native(identity)
    check(len(incoming) <= 65536)
    result = public_result(json.loads(incoming), identity)
    before = json.loads((native.ROOT/'reports/browser-environment.json').read_text())
    unchanged = native.docker_identity() == before['docker_baseline']
    service = native.systemd_identity(source)
    active = native.capture(['systemctl', 'list-units', '--state=active,activating,deactivating',
                             '--no-legend', '--plain', 'anas-hostd@*.service'])
    no_executors = not active.strip()
    raw = native.capture(['journalctl', '--no-pager', '-o', 'json', '-u', 'anas-hostd@*'])
    activations = set()
    for line in raw.splitlines():
        event = json.loads(line)
        unit = event.get('UNIT') or event.get('_SYSTEMD_UNIT', '')
        if unit.startswith('anas-hostd@') and unit.endswith('.service'):
            activations.add(unit)
    if result['passed']:
        check(before.get('actual_install_configure_enroll_passed') is True and
              len(before.get('preparation_job_ids', [])) == 6 and unchanged and no_executors and
              len(activations) == len(before['preparation_job_ids']) + 3)
    native.write_new(native.ROOT/'reports/browser-host-readback.json', json.dumps({
        'schema': 'anas.native-browser-readback/v1', 'vm_id': identity,
        'docker_baseline_unchanged': unchanged, 'no_active_host_executors': no_executors,
        'observed_activations': len(activations), 'service': service}))
    native.write_new(native.ROOT/'reports/browser-summary.json', json.dumps(result, sort_keys=True))
    return {'passed': result['passed'], 'vm_id': identity, 'browser_gates': len(result['events']),
            'docker_baseline_unchanged': unchanged, 'observed_activations': len(activations)}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('operation', choices=('prepare', 'finish'))
    parser.add_argument('--vm-id', required=True)
    args = parser.parse_args()
    os.umask(0o077)
    try:
        result = prepare(args.vm_id) if args.operation == 'prepare' else finish(args.vm_id, sys.stdin.buffer.read(65537))
        sys.stdout.write(json.dumps(result) + '\n')
    except Exception:
        # Raw exceptions may reference TLS files or requests. Only this fixed
        # diagnostic escapes, even when the private prepare channel fails.
        sys.stderr.write('native browser bootstrap or evidence validation failed\n')
        sys.exit(1)
