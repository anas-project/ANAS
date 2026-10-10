#!/usr/bin/env python3
"""Fresh-VM lease network acceptance (INCUS-R-112--R-164) through real Core.

Reuse the installed approval fixture: the release anas/anasd/anas-hostd,
HTTPS owner enrollment and confirmed install/configure/enroll jobs. The Go
stages in internal/runner/compute_lease_network_native_linux_test.go then
deploy the real Traefik and lego Modules beside five leases of four synthetic
consumers, create guests through the shared client and probe every tier from
inside them. This driver adds the console steps between those stages: the
lease network view (its API, and the rendered page where the VM has Chromium),
and a console plan/apply that revokes a lease.

Two phases, because one requirement needs a host restart:

  --phase before-reboot   everything up to an installed early port squatter
  --phase after-reboot    the boot order and restored policy after the restart,
                          then removal of every lease and the approved uninstall

The outer owner restarts the VM between them. Neither phase touches anything
outside the disposable VM.
"""
import argparse
from collections import Counter
import importlib.util
import json
import os
from pathlib import Path
import re
import secrets
import sys

INPUTS = Path('/opt/anas-core-inputs')
HOST_INPUTS = Path('/opt/anas-host-action-inputs')
WORKSPACES = (('native', '/srv/anas/host-action-native'),)
REPORTS = Path('/opt/anas-core-projection/reports')
PHASE_ONE = REPORTS/'lease-network-phase1.json'
CHROMIUM = Path('/usr/bin/chromium')
SCHEMA = 'anas.native-lease-network/v1'


def subtests(parent, *names):
    return (parent, *(parent + '/' + name for name in names))


DEPLOY = subtests('TestNativeLeaseNetworkDeploy',
                  'approved_host_and_dedicated_docker', 'lan_internet_and_module_stand_ins',
                  'module_sources_images_and_bootable_guest', 'cli_render_and_apply',
                  'frozen_networks_and_reported_bridges', 'instances_through_shared_client',
                  'traefik_address_set_follows_container', 'port_bindings_applied_by_hostd',
                  'http_publication_through_traefik')
MATRIX = subtests('TestNativeLeaseNetworkMatrix',
                  'static_forwarding_rules_after_docker', 'reachability_matrix',
                  'port_binding_holds_and_host_addresses')
RUNTIME = subtests('TestNativeLeaseNetworkRuntime',
                   'traefik_address_change_and_anasd_start', 'late_docker_network_is_not_lan',
                   'mediator_down_routes_stay_then_reconcile', 'request_files_cannot_widen_publication',
                   'explicit_port_conflicts_fail_apply', 'auto_port_skips_taken_ports', 'forward_auth_frozen_at_apply',
                   'binding_drift_recorded_by_anasd', 'admin_forward_rules_keep_precedence',
                   'cli_activation_prunes_changed_routes', 'cli_revocation_ends_connections')
BEFORE_CONSOLE = subtests('TestNativeLeaseNetworkBeforeConsole', 'held_connection_and_removed_module_config')
AFTER_CONSOLE = subtests('TestNativeLeaseNetworkAfterConsole', 'console_revocation_ends_connections')
BEFORE_REBOOT = subtests('TestNativeLeaseNetworkBeforeReboot', 'early_port_squatter_installed')
AFTER_REBOOT = subtests('TestNativeLeaseNetworkAfterReboot',
                        'services_back_after_restart', 'squatted_binding_left_out_and_recorded',
                        'policy_and_traefik_after_restart')
TEARDOWN = subtests('TestNativeLeaseNetworkTeardown', 'consumers_removed_through_cli', 'operator_removes_retained_leases')
AFTER_UNINSTALL = subtests('TestNativeLeaseNetworkAfterUninstall', 'host_network_rules_removed')

PHASE_TWO_STAGES = ('after_reboot', 'teardown', 'confirmed_uninstall', 'after_uninstall')


def phase_one_stages(page):
    """The console page stage runs where the VM has a Chromium package
    (Debian); Ubuntu ships Chromium only as a snap."""
    return (('approved_host_install_configure_enroll', 'deploy', 'matrix', 'runtime', 'console_lease_view') +
            (('console_lease_page',) if page else ()) +
            ('before_console', 'console_revocation_apply', 'after_console', 'console_lease_view_after_revocation',
             'before_reboot'))


PHASE_ONE_STAGES = phase_one_stages(True)

# The lease view must never carry key material; these mark it.
FORBIDDEN = ('PRIVATE KEY', 'BEGIN CERTIFICATE', 'CLIENT_KEY', 'LEASE_SECRET')


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


def validate_lease_view(view, expected):
    """INCUS-R-128: one entry per active lease, frozen tiers, publications,
    bindings with slot addresses, live instances, derived directions."""
    problems = []
    body = json.dumps(view)
    if any(marker in body for marker in FORBIDDEN):
        problems.append('credential_marker')
    leases = {item.get('consumer', '') + '.' + item.get('resource', ''): item for item in view.get('leases', [])}
    if set(leases) != set(expected):
        problems.append('lease_set')
        return problems
    for key, want in expected.items():
        item = leases[key]
        for field in ('egress', 'module_access', 'intra_lease', 'ingress'):
            if item.get(field) != want[field]:
                problems.append(key + ':' + field)
        if not item.get('bridge', '').startswith('lease') or not item.get('ipv4_subnet') or not item.get('ipv4_gateway'):
            problems.append(key + ':bridge')
        if item.get('instances_error') or len(item.get('instances', [])) != want['instances']:
            problems.append(key + ':instances')
        if any(not instance.get('addresses') for instance in item.get('instances', []) if instance.get('status') == 'Running'):
            problems.append(key + ':instance_addresses')
        bindings = sorted((b.get('protocol'), b.get('guest_port'), bool(b.get('ipv4'))) for b in item.get('port_bindings', []))
        if bindings != sorted(want['bindings']):
            problems.append(key + ':port_bindings')
        if sorted(p.get('instance') for p in item.get('http_publications', [])) != sorted(want['published']):
            problems.append(key + ':http_publications')
        directions = {(d.get('flow'), d.get('peer')): d.get('allowed') for d in item.get('directions', [])}
        for direction, allowed in want['directions'].items():
            if directions.get(direction) is not allowed:
                problems.append(key + ':direction:' + '/'.join(direction))
    return problems


def validate_lease_page(page, view):
    """INCUS-R-129: the rendered page shows, for every lease, the directions
    the API derived (allowed and denied told apart by class, arrow and
    colour), and offers no way to change anything: no form, no input, only
    the workspace selector and the reload button, and only reads sent."""
    problems = []
    rendered = {item.get('key'): item for item in (page.get('page') or {}).get('leases', [])}
    expected = {item['consumer'] + '.' + item['resource']: item for item in view.get('leases', [])}
    if set(rendered) != set(expected):
        return ['page_lease_set']
    strokes = {True: set(), False: set()}
    for key, item in expected.items():
        for flow in ('egress', 'ingress'):
            want = [d['allowed'] for d in item.get('directions', []) if d.get('flow') == flow]
            edges = rendered[key].get(flow, [])
            if [e.get('allowed') for e in edges] != want:
                problems.append(key + ':' + flow)
            for edge in edges:
                allowed = edge.get('allowed') is True
                if edge.get('denied') is allowed or edge.get('marker') != ('url(#arrow-allowed)' if allowed else 'url(#arrow-denied)') or \
                        not edge.get('label') or not edge.get('title'):
                    problems.append(key + ':' + flow + ':edge')
                strokes[allowed].add(edge.get('stroke'))
    if len(strokes[True]) != 1 or len(strokes[False]) != 1 or strokes[True] == strokes[False]:
        problems.append('allowed_and_denied_not_distinct')
    controls = sorted((c.get('tag'), c.get('type')) for c in (page.get('page') or {}).get('controls', []))
    # The reload button, and the workspace selector only where there is more
    # than one workspace: nothing that changes anything (INCUS-R-129).
    if controls not in ([('button', 'button')], [('button', 'button'), ('select', '')]) or (page.get('page') or {}).get('forms') != 0:
        problems.append('page_controls')
    api = [r for r in page.get('requests', []) if '/api/' in r.get('url', '')]
    if any(r.get('method') != 'GET' for r in api):
        problems.append('page_sent_a_change')
    if sum(1 for r in api if r.get('url', '').endswith('/api/v1/workspaces/native/compute/leases')) < 2:
        problems.append('page_reload_not_seen')
    if not page.get('screenshot_bytes'):
        problems.append('screenshot')
    return problems


def expected_view(with_inet):
    lease = lambda egress, module_access, intra, ingress, instances, bindings, published, directions: {
        'egress': egress, 'module_access': module_access, 'intra_lease': intra, 'ingress': ingress,
        'instances': instances, 'bindings': bindings, 'published': published, 'directions': directions}
    view = {
        'lnlan.workers': lease('internet_lan', True, True, 'published', 2, [('tcp', 7022, True)],
                               ['anas-lnlan-a', 'anas-lnlan-b'],
                               {('egress', 'lan'): True, ('egress', 'host'): False, ('egress', 'modules'): True,
                                ('egress', 'other_leases'): False, ('egress', 'same_lease'): True,
                                ('ingress', 'traefik'): True, ('ingress', 'port_binding_clients'): True}),
        'lnhost.workers': lease('internet_lan_host', False, False, 'published', 1,
                                [('tcp', 7022, True), ('udp', 7053, True)], [],
                                {('egress', 'host'): True, ('egress', 'docker_published'): True,
                                 ('egress', 'docker_unpublished'): False, ('egress', 'link_local'): False}),
        'lnmods.workers': lease('modules_only', False, False, 'none', 1, [], [],
                                {('egress', 'internet'): False, ('egress', 'modules'): True,
                                 ('egress', 'gateway_dns_dhcp'): True}),
    }
    if with_inet:
        view['lninet.workers'] = lease('internet', False, False, 'none', 2, [], [],
                                       {('egress', 'internet'): True, ('egress', 'lan'): False,
                                        ('egress', 'same_lease'): False, ('ingress', 'docker_containers'): False})
    return view


def confirm_host_action(native, client, host_phase):
    """One host action through the console approval: plan, owner
    confirmation, then apply with the confirmed parameters."""
    request = {'interface': 'incus_container', 'storage_size_gib': 24}
    plan = client.wait_job(client.cli('incus-plan', '-w', 'native', '--phase', host_phase,
                                      '--request-json', json.dumps(request), '--session-json', '-'))
    proof = client.cli('incus-confirm', '-w', 'native', '--plan-job', plan['id'],
                       '--action', 'incus.' + host_phase, '--session-json', '-')
    client.credentials.append(proof['token'])
    envelope = {'session': client.envelope(), 'plan_job_id': plan['id'], 'confirmation_token': proof['token'],
                'parameters': plan['result']['value']['parameters']}
    applied = client.wait_job(client.cli('incus-apply', '-w', 'native', '--phase', host_phase,
                                         '--request-json', '-', request=envelope))
    native.validate_phase_result(applied['result']['value'], host_phase, skip=False)
    return applied['id']


def run(identity, phase):
    if (os.geteuid() != 0 or not re.fullmatch(r'anas-incus-host-[a-f0-9]{6}', identity) or
            Path('/var/lib/cloud/data/instance-id').read_text().strip() != identity or
            Path('/sys/devices/virtual/dmi/id/sys_vendor').read_text().strip() != 'QEMU'):
        raise RuntimeError('isolated native VM required')
    driver = HOST_INPUTS/'server-incus-host-action-e2e.py'
    info = driver.lstat()
    if driver.is_symlink() or not driver.is_file() or info.st_uid != 0 or info.st_mode & 0o022:
        raise RuntimeError('trusted installed-host fixture required')
    native = load_module('lease_installed_host_actions', driver)
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
                   manifest['files']['anas'] == source['artifacts']['anas'] and
                   'lease-net-agent' in manifest['files'], 'core_product_identity')
    supervisor = load_module('lease_native_process', INPUTS/'server-incus-host-provision-e2e.py')
    events = []
    stage = 'fixture_preparation'
    page = CHROMIUM.is_file()

    def passed(name, **facts):
        event = {'stage': name, 'status': 'passed', **facts}
        events.append(event)
        print(json.dumps(event, sort_keys=True), flush=True)

    def native_test(label, expected, minutes):
        report = native.ROOT/'reports'
        command = [str(INPUTS/'test2json'), '-t', '-p', 'github.com/anas-project/ANAS/internal/runner',
                   str(INPUTS/'core.test'), '-test.v=test2json', '-test.count=1', '-test.timeout=%dm' % minutes,
                   '-test.run=^' + expected[0] + '$']
        env = {'PATH': '/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin', 'HOME': '/root', 'LC_ALL': 'C',
               'ANAS_REQUIRE_CORE_COMPUTE_NATIVE': '1'}
        code, timed_out, log_limit = supervisor.run_native_process(
            command, env, report/(label + '.jsonl'), report/(label + '.stderr'), (minutes + 1) * 60, 16 << 20)
        try:
            test_events = [json.loads(line) for line in (report/(label + '.jsonl')).read_text().splitlines()]
        except (ValueError, UnicodeError):
            test_events = []
        native.require(not timed_out and not log_limit and events_passed(test_events, expected, code),
                       'lease_native_test_failed')
        passed(label, required_test_passes=len(expected))

    try:
        if phase == 'before-reboot':
            baseline = native.guarded_vm(identity)

            def init_workspace(path):
                native.require(not path.exists(), 'workspace_preexists')
                path.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
                native.capture(['/usr/local/bin/anas', 'init', str(path), '--yes', '--json'])

            token = native.install_fixture(source, WORKSPACES, init_workspace)
            stage = 'approved_host_install_configure_enroll'
            native.systemd_identity(source)
            client = native.Console()
            client.enroll(token)
            # Phase two logs in again after the restart for the uninstall.
            native.write_new(native.ROOT/'private'/'lease-console-password', client.credentials[1])

            def confirmed(host_phase):
                return confirm_host_action(native, client, host_phase)

            passed(stage, job_ids=[confirmed(item) for item in ('install', 'configure', 'enroll')], docker_id=baseline['id'])
            for stage, tests, minutes in (('deploy', DEPLOY, 60), ('matrix', MATRIX, 15), ('runtime', RUNTIME, 45)):
                native_test(stage, tests, minutes)

            def lease_view(label, with_inet):
                view = client.expected('GET', '/api/v1/workspaces/native/compute/leases')
                native.check_public_response(view, client.credentials)
                native.write_new(REPORTS/(label + '.json'), json.dumps(view, sort_keys=True, indent=1))
                problems = validate_lease_view(view, expected_view(with_inet))
                if problems:
                    native.write_new(native.ROOT/'reports'/(label + '-problems.json'), json.dumps(problems))
                native.require(not problems, 'lease_view_mismatch')
                passed(label, leases=len(view['leases']))
                return view

            stage = 'console_lease_view'
            view = lease_view(stage, True)
            if page:
                stage = 'console_lease_page'
                shot = REPORTS/'console-lease-page.png'
                probe = {'chromium': str(CHROMIUM), 'origin': native.ORIGIN, 'path': '/#/network',
                         'cookies': [{'name': name, 'value': value} for name, value in client.cookies.items()],
                         'leases': len(view['leases']), 'screenshot': str(shot), 'timeout_ms': 120000}
                rendered = json.loads(native.capture([INPUTS/'console-page-probe'], body=json.dumps(probe).encode(), timeout=180))
                native.check_public_response(rendered, client.credentials)
                native.write_new(REPORTS/'console-lease-page.json', json.dumps(rendered, sort_keys=True, indent=1))
                problems = validate_lease_page(rendered, view)
                if problems:
                    native.write_new(native.ROOT/'reports'/'console-lease-page-problems.json', json.dumps(problems))
                native.require(not problems, 'lease_page_mismatch')
                passed(stage, leases=len(view['leases']), screenshot_bytes=rendered['screenshot_bytes'])
                # The page refreshed the shared session on load, which issues
                # a new CSRF token; take the current one for the writes below.
                client.csrf = client.expected('GET', '/api/v1/auth/session')['csrf_token']
                client.credentials.append(client.csrf)
            stage = 'before_console'
            native_test(stage, BEFORE_CONSOLE, 10)
            stage = 'console_revocation_apply'
            password = client.credentials[1]
            step_up = client.expected('POST', '/api/v1/auth/step-up',
                                      {'password': password, 'action': 'deployment.apply', 'workspace_id': 'native'})
            client.credentials.append(step_up['proof'])
            planned = client.expected('POST', '/api/v1/workspaces/native/plans', {'step_up_proof': step_up['proof']})
            confirmation = planned['confirmation']
            client.credentials.append(confirmation['token'])
            applied = client.expected('POST', '/api/v1/workspaces/native/actions/apply', {
                'plan_job_id': confirmation['plan_job_id'], 'confirmation_token': confirmation['token'],
                'step_up_proof': step_up['proof'], 'expected_config_validator': confirmation['config_validator'],
                'expected_plan_digest': confirmation['plan_digest'], 'allow_risky': False, 'no_snapshot': True},
                status=202, headers={'Idempotency-Key': 'lnet-' + secrets.token_hex(16)})
            job = client.wait_job(applied)
            passed(stage, plan_job=confirmation['plan_job_id'], apply_job=job['id'])
            stage = 'after_console'
            native_test(stage, AFTER_CONSOLE, 15)
            stage = 'console_lease_view_after_revocation'
            lease_view(stage, False)
            stage = 'before_reboot'
            native_test(stage, BEFORE_REBOOT, 5)
            native.write_new(PHASE_ONE, json.dumps({'events': events}, sort_keys=True))
            expected_stages = phase_one_stages(page)
        else:
            stage = 'phase_one_evidence'
            prior = json.loads(PHASE_ONE.read_text())['events']
            native.require([event['stage'] for event in prior] == list(phase_one_stages(page)) and
                           all(event['status'] == 'passed' for event in prior), 'phase_one_incomplete')
            events.extend(prior)
            stage = 'after_reboot'
            native_test(stage, AFTER_REBOOT, 20)
            stage = 'teardown'
            native_test(stage, TEARDOWN, 30)
            stage = 'confirmed_uninstall'
            client = native.Console()
            client.csrf = client.ready_get('/api/v1/auth/csrf')['csrf_token']
            password = (native.ROOT/'private'/'lease-console-password').read_text()
            session = client.expected('POST', '/api/v1/auth/login', {'password': password})
            native.require(session.get('state') == 'full', 'owner_login')
            client.csrf = session['csrf_token']
            client.credentials = [password, client.cookies['__Host-anas_local_session'], client.csrf]
            passed(stage, job_id=confirm_host_action(native, client, 'uninstall'))
            stage = 'after_uninstall'
            native_test(stage, AFTER_UNINSTALL, 10)
            expected_stages = phase_one_stages(page) + PHASE_TWO_STAGES
    except Exception as error:
        events.append({'stage': stage, 'status': 'failed', **native.public_failure(error)})
        expected_stages = None
    complete = (expected_stages is not None and [event['stage'] for event in events] == list(expected_stages) and
                all(event['status'] == 'passed' for event in events))
    result = {'schema': SCHEMA, 'vm_id': identity, 'phase': phase, 'passed': complete, 'events': events,
              'scope': 'real Core CLI and console apply, production Hook/Provider/anasd/hostd, real Traefik and lego, '
                       'Debian container guests through the shared client; container tier and amd64 only'}
    native.write_new(native.ROOT/'reports'/('lease-network-' + phase + '.json'), json.dumps(result, sort_keys=True) + '\n')
    print(json.dumps(result, sort_keys=True), flush=True)
    return 0 if complete else 1


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--vm-id', required=True)
    parser.add_argument('--phase', required=True, choices=('before-reboot', 'after-reboot'))
    args = parser.parse_args()
    os.umask(0o077)
    try:
        sys.exit(run(args.vm_id, args.phase))
    except Exception:
        print(json.dumps({'passed': False, 'stage': 'fixture_preparation', 'code': 'lease_fixture_rejected'}), flush=True)
        sys.exit(1)
