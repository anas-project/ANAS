#!/usr/bin/env python3
"""Installed approval chain on an unadapted distribution (INCUS-R-094, INCUS-R-107).

Runs inside the owner's disposable QEMU VM on a distribution that is not a row
of the compiled recipe table. It reuses the installed-host fixture of
server-incus-host-action-e2e.py (actual CLI/HTTPS owner, shared jobs and a
socket-activated systemd hostd) and asserts that:

* the installed preflight reports `disabled` with `distribution_not_adapted`
  and a manual guide, never `compute_ready`;
* the install plan contains no package or service step, only "leave compute
  disabled";
* confirming and applying it records a disabled receipt and changes nothing:
  the dpkg inventory, APT sources and the absence of any Incus daemon are the
  same afterwards;
* the experiment Docker endpoint is unchanged.

It never adds a package source, installs Incus or touches a physical host.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import sys

HOST_INPUTS = Path('/opt/anas-host-action-inputs')
FIRST_TIER = {('debian', '13'), ('ubuntu', '24.04'), ('ubuntu', '26.04')}


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def os_release():
    fields = {}
    for line in Path('/etc/os-release').read_text().splitlines():
        if '=' in line:
            key, value = line.split('=', 1)
            fields[key] = value.strip().strip('"')
    return fields.get('ID', ''), fields.get('VERSION_ID', ''), fields.get('VERSION_CODENAME', '')


def apt_sources_digest():
    digest = hashlib.sha256()
    roots = [Path('/etc/apt/sources.list'), Path('/etc/apt/sources.list.d'), Path('/etc/apt/preferences.d'),
             Path('/etc/apt/keyrings'), Path('/etc/anas')]
    for root in roots:
        paths = [root] if root.is_file() else sorted(p for p in root.rglob('*') if p.is_file()) if root.exists() else []
        for path in paths:
            if path.is_relative_to(Path('/etc/anas')) and 'incus' not in path.name:
                continue
            digest.update(str(path).encode() + b'\0' + path.read_bytes() + b'\0')
    return digest.hexdigest()


def unadapted_preflight_verdict(value):
    return (isinstance(value, dict) and value.get('disposition') == 'disabled' and value.get('compute_ready') is False
            and 'distribution_not_adapted' in (value.get('blockers') or []) and bool(value.get('manual_guide')))


def unadapted_plan_verdict(plan):
    steps = plan.get('steps') or []
    return (plan.get('disposition') == 'disabled' and plan.get('compute_ready') is False
            and 'distribution_not_adapted' in (plan.get('blockers') or [])
            and [(s.get('id'), s.get('skipped')) for s in steps] == [('unsupported', True)])


def unadapted_apply_verdict(value):
    return (isinstance(value, dict) and value.get('phase') == 'install' and value.get('disposition') == 'disabled'
            and value.get('compute_ready') is False and value.get('blockers') == ['distribution_not_adapted']
            # No recipe row takes the disabled path; a matched but unsupported
            # host (architecture, init) takes the unsupported one.
            and any(r.get('step') in ('install.disabled', 'install.unsupported') and r.get('status') == 'ok'
                    for r in value.get('receipts') or []))


def run(identity):
    if (os.geteuid() != 0 or not re.fullmatch(r'anas-incus-host-[a-f0-9]{6}', identity) or
            Path('/var/lib/cloud/data/instance-id').read_text().strip() != identity or
            Path('/sys/devices/virtual/dmi/id/sys_vendor').read_text().strip() != 'QEMU'):
        raise RuntimeError('isolated native VM required')
    release = os_release()
    if (release[0], release[1]) in FIRST_TIER:
        raise RuntimeError('this gate needs a distribution outside the first-tier recipe table')
    driver = HOST_INPUTS/'server-incus-host-action-e2e.py'
    info = driver.lstat()
    if driver.is_symlink() or not driver.is_file() or info.st_uid != 0 or info.st_mode & 0o022:
        raise RuntimeError('trusted installed-host fixture required')
    native = load_module('unadapted_installed_host_actions', driver)
    native.digest_input(HOST_INPUTS/'source-manifest.json')
    manifest = json.loads((HOST_INPUTS/'source-manifest.json').read_bytes())
    native.require(native.digest_input(driver) == manifest['runner_sha256'], 'host_driver_identity')
    baseline = native.guarded_vm(identity)
    packages_before, sources_before = native.installed_packages(), apt_sources_digest()
    native.require(not Path('/usr/bin/incus').exists() and not Path('/opt/incus').exists() and
                   not Path('/var/lib/incus').exists(), 'preexisting_incus')
    token = native.install_fixture(manifest)
    events = []
    stage = 'installed_release_identity'

    def passed(name, **facts):
        record = {'stage': name, 'status': 'passed', **facts}
        events.append(record)
        print(json.dumps(record, sort_keys=True), flush=True)

    try:
        native.systemd_identity(manifest)
        passed(stage, os_id=release[0], version_id=release[1], codename=release[2])
        stage = 'https_owner_enrollment'
        client = native.Console()
        client.enroll(token)
        passed(stage)
        stage = 'installed_preflight_disabled'
        job = client.wait_job(client.cli('incus-preflight', '-w', 'native', '--session-json', '-'))
        value = job['result']['value']
        native.require(unadapted_preflight_verdict(value), 'preflight_not_disabled')
        passed(stage, blockers=value.get('blockers'))
        stage = 'install_plan_leaves_compute_disabled'
        plan = client.wait_job(client.cli('incus-plan', '-w', 'native', '--phase', 'install',
                                          '--request-json', json.dumps({}), '--session-json', '-'))
        planned = plan['result']['value']
        native.require(unadapted_plan_verdict(planned.get('inspect', {}).get('plan', {})), 'install_plan_not_disabled')
        passed(stage, job_id=plan['id'])
        stage = 'confirmed_install_records_disabled'
        confirmation = client.cli('incus-confirm', '-w', 'native', '--plan-job', plan['id'],
                                  '--action', 'incus.install', '--session-json', '-')
        client.credentials.append(confirmation['token'])
        envelope = {'session': client.envelope(), 'plan_job_id': plan['id'], 'confirmation_token': confirmation['token'],
                    'parameters': planned['parameters']}
        applied = client.wait_job(client.cli('incus-apply', '-w', 'native', '--phase', 'install',
                                             '--request-json', '-', request=envelope))
        native.require(unadapted_apply_verdict(applied['result']['value']), 'install_not_disabled')
        passed(stage, job_id=applied['id'])
        stage = 'host_unchanged'
        native.require(native.installed_packages() == packages_before, 'package_inventory_changed')
        native.require(apt_sources_digest() == sources_before, 'package_sources_changed')
        native.require(not Path('/usr/bin/incus').exists() and not Path('/opt/incus').exists() and
                       not Path('/var/lib/incus').exists(), 'incus_installed')
        native.require(native.docker_identity() == baseline, 'docker_baseline_changed')
        passed(stage)
    except Exception as error:
        events.append({'stage': stage, 'status': 'failed', **native.public_failure(error)})
    complete = len(events) == 6 and all(event['status'] == 'passed' for event in events)
    result = {'schema': 'anas.native-unadapted-host/v1', 'vm_id': identity, 'passed': complete, 'events': events,
              'scope': 'installed approval chain on one unadapted distribution; not other architectures or Core consumer rendering'}
    native.write_new(native.ROOT/'reports/unadapted-summary.json', json.dumps(result, sort_keys=True)+'\n')
    print(json.dumps(result, sort_keys=True), flush=True)
    return 0 if complete else 1


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--vm-id', required=True)
    args = parser.parse_args()
    os.umask(0o077)
    try:
        sys.exit(run(args.vm_id))
    except Exception as error:
        # Only the driver's fixed gate codes or an exception type, never text
        # that could carry a response or path.
        code = getattr(error, 'code', None)
        detail = code if isinstance(code, str) and re.fullmatch(r'[a-z_]{1,64}', code) else type(error).__name__
        print(json.dumps({'passed': False, 'stage': 'fixture_preparation', 'code': 'unadapted_fixture_rejected',
                          'detail': detail}), flush=True)
        sys.exit(1)
