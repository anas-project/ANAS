#!/usr/bin/env python3
"""Owner script for one disposable lifecycle VM on the lab host.

  prepare <round> <base-qcow2> <base-sha256> <port> <install-script>
  run <round> <generation>      (generation "hold-*" keeps the VM for diagnosis)

The script lives in an experiment root next to incus_vm_cleanup.py and an
`inputs/` directory holding the precompiled test inputs. Only files under the
named round directory are created or removed. QEMU runs as the unprivileged
lab user through setpriv with user-mode NAT and a loopback SSH forward; no TAP,
bridge, business Docker socket, block device or shared volume is attached. One
lab VM runs at a time. Inside it the lifecycle harness runs twice against the
same fresh Incus daemon -- the container tier first, then the VM tier on nested
KVM, so both timing baselines come from one environment -- followed by the
dual-stack egress harness.

The physical host is only read (Docker inventory, services, nft and routes)
before and after, and the two snapshots must match.
"""
from pathlib import Path
import hashlib, json, os, re, secrets, shlex, socket, stat, subprocess, sys, time

sys.path.insert(0, str(Path(__file__).resolve().parent))
from incus_vm_cleanup import finalize_vm, require_no_loopback_listeners

BASE_ROOT = Path(__file__).resolve().parent
ROUND = re.compile(r'r[1-9][0-9]?-[a-z0-9.-]{3,40}')
INPUTS = ('server-incus-lifecycle-e2e.py', 'server-incus-network-e2e.py', 'provider', 'computeclient.test', 'anas-fixture',
          'test2json', 'vm-incus.tar.xz', 'vm-disk.qcow2', 'ct-incus.tar.xz', 'ct-root.tar.xz', 'install-common.sh')
STAGES = ('container', 'vm', 'network')
VM_FILES = ('lab.qcow2', 'guest-key', 'guest-key.pub', 'known_hosts', 'seed.iso', 'meta-data', 'network-config', 'user-data')
QEMU_UID, QEMU_GID = 1000, 108
GUEST_INPUTS = '/opt/anas-lifecycle-inputs'


def sha256(path):
    h = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1 << 20), b''):
            h.update(block)
    return h.hexdigest()


def round_dir(name):
    root = BASE_ROOT/name
    if not ROUND.fullmatch(name) or root.parent != BASE_ROOT:
        raise SystemExit('invalid round name')
    return root


def host_baseline(root, phase):
    """Read-only physical-host snapshot; 'after' must equal 'before'."""
    def run(argv):
        return subprocess.check_output(argv, stderr=subprocess.PIPE, timeout=20)

    def text(argv):
        return run(argv).decode().strip()

    def normalize(value):
        if isinstance(value, dict):
            return {k: normalize(v) for k, v in value.items() if k not in ('expires', 'cache')}
        if isinstance(value, list):
            return sorted((normalize(v) for v in value), key=lambda v: json.dumps(v, sort_keys=True))
        return value
    docker = ['sudo', '-n', 'docker', '-H', 'unix:///var/run/docker.sock']
    containers = []
    for ident in sorted(text(docker + ['ps', '-a', '-q', '--no-trunc']).split()):
        template = ('{{json .Id}} {{json .Name}} {{json .State.Status}} {{json .State.Pid}} {{json .State.StartedAt}} '
                    '{{json .RestartCount}} {{with index .State "Health"}}{{json .Status}}{{else}}"none"{{end}}')
        containers.append(text(docker + ['inspect', '--format', template, ident]))
    nft = run(['sudo', '-n', 'nft', '-s', 'list', 'ruleset'])
    data = {'containers': containers,
            'docker_services': text(['systemctl', 'show', 'docker.service', 'docker.socket', '-p', 'Id', '-p', 'MainPID',
                                     '-p', 'InvocationID', '-p', 'ExecMainStartTimestampMonotonic', '-p', 'NRestarts',
                                     '-p', 'ActiveState']),
            'networks': sorted(text(docker + ['network', 'ls', '--no-trunc', '--format', '{{.ID}} {{.Name}} {{.Driver}}']).splitlines()),
            'volumes': sorted(text(docker + ['volume', 'ls', '--format', '{{.Name}} {{.Driver}}']).splitlines()),
            'docker_unit_digest': hashlib.sha256(run(['systemctl', 'cat', 'docker.service', 'docker.socket'])).hexdigest(),
            'docker_config': (text(['sudo', '-n', 'sha256sum', '/etc/docker/daemon.json'])
                              if Path('/etc/docker/daemon.json').exists() else 'absent'),
            'nft_sha256': hashlib.sha256(nft).hexdigest(),
            'routes4': normalize(json.loads(run(['ip', '-j', '-4', 'route', 'show', 'table', 'all']))),
            'routes6': normalize(json.loads(run(['ip', '-j', '-6', 'route', 'show', 'table', 'all']))),
            'named_netns': text(['sudo', '-n', 'ip', 'netns', 'list'])}
    with (root/'reports'/('baseline-' + phase + '.json')).open('x') as out:
        json.dump(data, out, sort_keys=True, indent=2)
    if phase == 'after':
        before = json.loads((root/'reports/baseline-before.json').read_text())
        comparison = {key: before[key] == data[key] for key in data}
        (root/'reports/baseline-comparison.json').write_text(json.dumps(comparison, sort_keys=True, indent=2))
        if not all(comparison.values()):
            raise RuntimeError('physical host baseline changed')


def prepare(name, base, base_sha, port, install):
    root = round_dir(name)
    root.mkdir(mode=0o700)
    for sub in ('vm', 'src', 'reports'):
        (root/sub).mkdir(mode=0o700)
    subprocess.run(['chattr', '+C', str(root/'vm')], check=True)
    base = Path(base)
    if sha256(base) != base_sha:
        raise SystemExit('base image checksum mismatch')
    info = json.loads(subprocess.check_output(['qemu-img', 'info', '--output=json', str(base)]))
    if info['format'] != 'qcow2' or info.get('backing-filename'):
        raise SystemExit('unexpected base image chain')
    port = int(port)
    with socket.socket() as probe:
        probe.bind(('127.0.0.1', port))
    os.umask(0o077)
    for item in INPUTS + (install,):
        (root/'src'/item).write_bytes((BASE_ROOT/'inputs'/item).read_bytes())
    manifest = {'install': install, 'artifacts': {p.name: sha256(p) for p in sorted((root/'src').iterdir())},
                'source': json.loads((BASE_ROOT/'inputs/source.json').read_text())}
    (root/'src/manifest.json').write_text(json.dumps(manifest, sort_keys=True, indent=1) + '\n')
    identity = 'anas-incus-lifecycle-' + secrets.token_hex(3)
    vm = root/'vm'
    subprocess.run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-C', identity, '-f', str(vm/'guest-key')], check=True)
    public = (vm/'guest-key.pub').read_text().strip()
    (vm/'user-data').write_text('#cloud-config\nusers:\n  - name: anas-test\n    groups: [sudo]\n    shell: /bin/bash\n'
                                '    sudo: ["ALL=(ALL) NOPASSWD:ALL"]\n    lock_passwd: true\n    ssh_authorized_keys:\n      - '
                                + public + '\nssh_pwauth: false\ndisable_root: true\npackage_update: false\n')
    (vm/'meta-data').write_text('instance-id: ' + identity + '\nlocal-hostname: anas-incus-lifecycle\n')
    (vm/'network-config').write_text('version: 2\nethernets:\n  enp1s0:\n    match:\n      name: "en*"\n    dhcp4: true\n    dhcp6: false\n')
    with (root/'reports/seed.log').open('xb') as out:
        subprocess.run(['xorriso', '-as', 'mkisofs', '-output', str(vm/'seed.iso'), '-volid', 'cidata', '-joliet', '-rock',
                        str(vm/'user-data'), str(vm/'meta-data'), str(vm/'network-config')], stdout=out, stderr=subprocess.STDOUT, check=True)
    subprocess.run(['qemu-img', 'create', '-q', '-f', 'qcow2', '-F', 'qcow2', '-b', str(base), str(vm/'lab.qcow2'), '56G'], check=True)
    lab = {'base': str(base), 'base_sha256': base_sha, 'instance_id': identity, 'port': port, 'cpus': 3, 'memory_mib': 3584,
           'root': str(root), 'qemu_uid': QEMU_UID, 'qemu_gid': QEMU_GID}
    (root/'lab.json').write_text(json.dumps(lab, sort_keys=True) + '\n')
    (root/'guest-ssh').write_text('#!/bin/bash\nset -euo pipefail\nroot=' + str(root) + '\nexec ssh -i "$root/vm/guest-key" '
                                  '-o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=accept-new '
                                  '-o UserKnownHostsFile="$root/vm/known_hosts" -p ' + str(port) + ' anas-test@127.0.0.1 "$@"\n')
    os.chmod(root/'guest-ssh', 0o700)
    print(json.dumps(lab, sort_keys=True), flush=True)


def harness(lab, stage):
    inputs = GUEST_INPUTS
    prefix = 'sudo -n timeout --signal=TERM --kill-after=20s 1500 python3 '
    if stage == 'network':
        return (prefix + inputs + '/server-incus-network-e2e.py --vm-id ' + lab['instance_id'] +
                ' --provider ' + inputs + '/provider --ct-metadata ' + inputs + '/ct-incus.tar.xz'
                ' --ct-rootfs ' + inputs + '/ct-root.tar.xz --report-root /opt/anas-lifecycle-report-network')
    command = (prefix + inputs + '/server-incus-lifecycle-e2e.py'
               ' --vm-id ' + lab['instance_id'] + ' --interface ' + stage +
               ' --provider ' + inputs + '/provider --tests ' + inputs + '/computeclient.test'
               ' --guest-binary ' + inputs + '/anas-fixture --test2json ' + inputs + '/test2json'
               ' --report-root /opt/anas-lifecycle-report-' + stage)
    if stage == 'vm':
        command += ' --vm-base-metadata ' + inputs + '/vm-incus.tar.xz --vm-base-disk ' + inputs + '/vm-disk.qcow2'
    return command


def run(name, generation):
    root = round_dir(name)
    lab = json.loads((root/'lab.json').read_text())
    vm, reports, src = root/'vm', root/'reports', root/'src'
    if lab['root'] != str(root) or not re.fullmatch(r'anas-incus-lifecycle-[a-f0-9]{6}', lab['instance_id']):
        raise SystemExit('lab identity mismatch')
    if (vm/'qmp.sock').exists() or (reports/'qemu.log').exists():
        raise SystemExit('round already used')
    manifest = json.loads((src/'manifest.json').read_text())
    for item, digest in manifest['artifacts'].items():
        if sha256(src/item) != digest:
            raise SystemExit('input changed after preparation: ' + item)
    if subprocess.run(['pgrep', '-x', 'qemu-system-x86'], stdout=subprocess.DEVNULL).returncode == 0:
        raise SystemExit('another QEMU is running; one lab VM at a time')
    os.umask(0o077)
    host_baseline(root, 'before')
    with socket.socket() as probe:
        probe.bind(('127.0.0.1', lab['port']))
    argv = ['timeout', '--signal=TERM', '--kill-after=15s', '7200', 'sudo', '-n', '/usr/bin/setpriv',
            '--reuid=%d' % QEMU_UID, '--regid=%d' % QEMU_GID,
            '--clear-groups', '--bounding-set=-all', '--inh-caps=-all', '--ambient-caps=-all', '--no-new-privs',
            '/usr/bin/qemu-system-x86_64', '-name', lab['instance_id'], '-machine', 'q35,accel=kvm', '-cpu', 'host',
            '-smp', str(lab['cpus']), '-m', str(lab['memory_mib']),
            '-drive', 'file=' + str(vm/'lab.qcow2') + ',format=qcow2,if=virtio',
            '-drive', 'file=' + str(vm/'seed.iso') + ',format=raw,media=cdrom,readonly=on',
            '-netdev', 'user,id=net0,hostfwd=tcp:127.0.0.1:' + str(lab['port']) + '-:22', '-device', 'virtio-net-pci,netdev=net0',
            '-device', 'virtio-rng-pci', '-display', 'none', '-serial', 'file:' + str(reports/'serial.log'), '-monitor', 'none',
            '-qmp', 'unix:' + str(vm/'qmp.sock') + ',server=on,wait=off', '-no-reboot']
    log = (reports/'qemu.log').open('xb')
    qemu = subprocess.Popen(argv, stdout=log, stderr=subprocess.STDOUT)

    def guest(command, **options):
        return subprocess.run([str(root/'guest-ssh'), command], **options)

    def qmp(command):
        with socket.socket(socket.AF_UNIX) as connection:
            connection.settimeout(10)
            connection.connect(str(vm/'qmp.sock'))
            stream = connection.makefile('rwb')
            assert 'QMP' in json.loads(stream.readline())

            def send(name):
                stream.write((json.dumps({'execute': name}) + '\r\n').encode())
                stream.flush()
                while True:
                    reply = json.loads(stream.readline())
                    if 'event' in reply:
                        continue
                    assert 'error' not in reply
                    return reply['return']
            send('qmp_capabilities')
            assert send('query-name')['name'] == lab['instance_id']
            send(command)

    codes, failure, archive = {}, None, None
    try:
        deadline = time.monotonic() + 300
        while time.monotonic() < deadline:
            assert qemu.poll() is None, 'QEMU exited during boot'
            ready = guest('test "$(cat /var/lib/cloud/data/instance-id)" = ' + lab['instance_id'] +
                          ' && test -f /var/lib/cloud/instance/boot-finished',
                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=10)
            if ready.returncode == 0:
                break
            time.sleep(2)
        else:
            raise RuntimeError('VM not ready')
        guest('umask 077; mkdir /home/anas-test/lifecycle-inputs', check=True, timeout=15)
        scp = ['scp', '-q', '-i', str(vm/'guest-key'), '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes',
               '-o', 'UserKnownHostsFile=' + str(vm/'known_hosts'), '-P', str(lab['port'])]
        subprocess.run(scp + [str(p) for p in sorted(src.iterdir())] + ['anas-test@127.0.0.1:/home/anas-test/lifecycle-inputs/'],
                       check=True, timeout=900)
        # The input directory is root-only; every step inside it runs as root.
        guest('sudo -n install -d -m 0700 ' + GUEST_INPUTS + ' && sudo -n sh -c "mv /home/anas-test/lifecycle-inputs/* ' + GUEST_INPUTS +
              '/ && cd ' + GUEST_INPUTS + ' && chown root:root ./* && chmod 0700 provider computeclient.test anas-fixture test2json"',
              check=True, timeout=60)
        # Inputs are re-verified inside the guest before any root action uses them.
        expected = '\n'.join('%s  %s' % (digest, item) for item, digest in sorted(manifest['artifacts'].items())) + '\n'
        check = guest('sudo -n sh -c "cd ' + GUEST_INPUTS + ' && sha256sum -c --strict --quiet -"', input=expected.encode(), timeout=300)
        if check.returncode != 0:
            raise RuntimeError('inputs changed in transit')
        with (reports/'install.log').open('xb') as out:
            step = guest('sudo -n timeout --signal=TERM --kill-after=20s 1800 bash ' + GUEST_INPUTS + '/' + manifest['install'] + ' ' +
                         lab['instance_id'], stdout=out, stderr=subprocess.STDOUT, timeout=1850)
        (reports/'install.exit').write_text(str(step.returncode) + '\n')
        if step.returncode != 0:
            raise RuntimeError('guest installation failed')
        if generation.startswith('hold-'):
            # Diagnostic mode: keep the installed VM for owner-driven guest-ssh
            # commands until <round>/release appears or two hours pass. The
            # round is then recorded as not passed.
            deadline = time.monotonic() + 7200
            while not (root/'release').exists() and time.monotonic() < deadline and qemu.poll() is None:
                time.sleep(5)
            raise RuntimeError('diagnostic hold round; no stage verdict')
        for stage in STAGES:
            with (reports/('lifecycle-' + stage + '.log')).open('xb') as out:
                step = guest(harness(lab, stage), stdout=out, stderr=subprocess.STDOUT, timeout=1550)
            codes[stage] = step.returncode
            (reports/('lifecycle-' + stage + '.exit')).write_text(str(step.returncode) + '\n')
            print((reports/('lifecycle-' + stage + '.log')).read_text()[-12000:], flush=True)
            # The harness keeps its private runtime root; move it aside so the
            # next stage starts from a fresh one without deleting evidence.
            if stage != 'network':
                guest('sudo -n mv /run/anas-incus-lifecycle /run/anas-incus-lifecycle.' + stage, timeout=30)
            if step.returncode != 0:
                break
        with (reports/'lifecycle-evidence.tgz.partial').open('xb') as out:
            packed = guest('cd /opt && sudo -n tar -czf - anas-lifecycle-report-*', stdout=out, stderr=subprocess.PIPE, timeout=120)
        if packed.returncode == 0:
            (reports/'lifecycle-evidence.tgz.partial').rename(reports/'lifecycle-evidence.tgz')
            archive = sha256(reports/'lifecycle-evidence.tgz')
    except Exception as error:
        failure = type(error).__name__ + ': ' + str(error)[:200]
        print(json.dumps({'supervisor_error': failure}), flush=True)
    finally:
        def absent():
            assert not (vm/'qmp.sock').exists()
            require_no_loopback_listeners([lab['port']])
        cleanup = finalize_vm(qemu, lambda: qmp('system_powerdown'), lambda: qmp('quit'), stop_tunnel=lambda: None,
                              after_exit=absent, baseline=lambda: host_baseline(root, 'after'), shutdown_timeout=150)
        log.close()
        (reports/'qemu.exit').write_text(str(cleanup['qemu_exit']) + '\n')
        removed = []
        passed = codes == {stage: 0 for stage in STAGES} and not failure
        if cleanup.get('cleanup_passed') and passed and not (vm/'qmp.sock').exists():
            for item in VM_FILES:
                path = vm/item
                if path.exists():
                    info = path.lstat()
                    if not stat.S_ISREG(info.st_mode) or info.st_uid != QEMU_UID or info.st_nlink != 1:
                        raise SystemExit('unexpected disposable file ownership: ' + item)
                    path.unlink()
                    removed.append(item)
        result = {'generation': generation, 'test_exits': codes, 'supervisor_error': failure, 'archive_sha256': archive,
                  'removed_vm_files': removed, **cleanup}
        (reports/'supervisor-result.json').write_text(json.dumps(result, sort_keys=True) + '\n')
        print(json.dumps(result, sort_keys=True), flush=True)
    if codes != {stage: 0 for stage in STAGES} or failure or not cleanup.get('cleanup_passed'):
        raise SystemExit(1)


if __name__ == '__main__':
    if sys.argv[1:2] == ['prepare'] and len(sys.argv) == 7:
        prepare(*sys.argv[2:])
    elif sys.argv[1:2] == ['run'] and len(sys.argv) == 4:
        run(*sys.argv[2:])
    else:
        raise SystemExit(__doc__)
