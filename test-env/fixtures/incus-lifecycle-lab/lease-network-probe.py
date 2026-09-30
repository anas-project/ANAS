#!/usr/bin/env python3
"""Disposable lab VM only: lease network checks before M10a/M11b/M11c and the
control-listener simplification (2026-09-30).

The lab VM plays an Incus host that also runs Docker. The real Provider creates
two leases (probe-a with IPv6, probe-b without); Docker stand-ins play Traefik
(a published port) and another Module; a network namespace plays a machine on
the LAN. Listeners answer with a tag and the peer address they saw.

  setup      Docker from the distribution, leases, containers, LAN namespace
  acl        ACL rule order, address sets both ways, default-deny ingress
  docker     direct access to unpublished container ports (tier internet_lan_host)
  isolation  security.port_isolation inside one lease
  slots      fixed slot addresses through the restricted lease certificate, IPv6
  ports      Docker-like port binding chain, reservation, Docker publish conflicts
  control    Incus listening on a Docker bridge gateway; start order
  dockerce   replace docker.io with Docker CE, then rerun setup/docker/ports

Run as root: python3 - <cloud-instance-id> <stage> < this-file. Each stage prints
one JSON report; shared state lives in /root/lease-probe/state.json.
"""
import base64
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time

IDENTITY, STAGE = sys.argv[1], sys.argv[2]
INPUTS = Path('/opt/anas-lifecycle-inputs')
WORK = Path('/root/lease-probe')
STATE_PATH = WORK/'state.json'
UNITS = Path('/run/systemd/system')
POOL, PROFILE, PREFIX = 'probe-btrfs', 'anas-lease', 'anas-native-'
LEASES = {'probe-a': True, 'probe-b': False}
LAN_NS = 'probe-lan'
LAN_HOST4, LAN_CLIENT4 = '198.51.100.1', '198.51.100.2'
LAN_HOST6, LAN_CLIENT6 = '2001:db8:51::1', '2001:db8:51::2'
DOCKER_NET, DOCKER_SUBNET = 'probe-mod', '172.30.0.0/24'
CONTROL_NET, CONTROL_SUBNET, CONTROL_GW, CONTROL_BRIDGE = 'probe-control', '172.29.0.0/24', '172.29.0.1', 'probectl0'
TRAEFIK_HOST_PORT, TRAEFIK_PORT = 18443, 8443
OTHER_HOST_PORT, OTHER_PORT, OTHER_UNPUBLISHED = 18444, 9001, 9002
HTTP_PORT, CLOSED_PORT, LAN_PORT = 8080, 9090, 18080
BIND_TCP, BIND_UDP, SLOT_TCP, SLOT_UDP = 2222, 2253, 7022, 7053
DOCKER_KEY_FPR = '9DC858229FC7DD38854AE2D88D81803C0EBFCD88'

LISTENER = r'''
# lease-probe-listener
import socket, sys, threading
tag, port = sys.argv[1], int(sys.argv[2])
def norm(a):
    return a[7:] if a.startswith("::ffff:") else a
def make(kind):
    try:
        s = socket.socket(socket.AF_INET6, kind)
        s.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 0)
        s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        s.bind(("::", port))
        return s
    except OSError:
        s = socket.socket(socket.AF_INET, kind)
        s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        s.bind(("0.0.0.0", port))
        return s
def tcp():
    s = make(socket.SOCK_STREAM)
    s.listen(32)
    while True:
        c, peer = s.accept()
        try:
            c.settimeout(3); c.recv(64); c.sendall(("%s peer=%s\n" % (tag, norm(peer[0]))).encode())
        except Exception:
            pass
        finally:
            c.close()
def udp():
    s = make(socket.SOCK_DGRAM)
    while True:
        data, peer = s.recvfrom(256)
        try:
            s.sendto(("%s peer=%s\n" % (tag, norm(peer[0]))).encode(), peer)
        except Exception:
            pass
threading.Thread(target=udp, daemon=True).start()
tcp()
'''

PROBE = r'''
import socket, sys
host, port, proto = sys.argv[1], int(sys.argv[2]), sys.argv[3]
source = sys.argv[4] if len(sys.argv) > 4 else None
family = socket.AF_INET6 if ":" in host else socket.AF_INET
try:
    s = socket.socket(family, socket.SOCK_STREAM if proto == "tcp" else socket.SOCK_DGRAM)
    s.settimeout(4)
    if source:
        s.bind((source, 0))
    if proto == "tcp":
        s.connect((host, port)); s.sendall(b"probe\n")
    else:
        s.sendto(b"probe\n", (host, port))
    print(s.recv(128).decode().strip() or "EMPTY")
except Exception as e:
    print("ERROR " + type(e).__name__)
'''

BIND = r'''
import errno, socket, sys
host, port, proto = sys.argv[1], int(sys.argv[2]), sys.argv[3]
family = socket.AF_INET6 if ":" in host else socket.AF_INET
s = socket.socket(family, socket.SOCK_STREAM if proto == "tcp" else socket.SOCK_DGRAM)
try:
    s.bind((host, port))
    if proto == "tcp":
        s.listen(1)
    print("BOUND")
except OSError as e:
    print("ERROR " + errno.errorcode.get(e.errno, str(e.errno)))
'''


def require_vm():
    if (os.geteuid() != 0 or not IDENTITY.startswith('anas-incus-lifecycle-')
            or Path('/var/lib/cloud/data/instance-id').read_text().strip() != IDENTITY
            or Path('/sys/class/dmi/id/sys_vendor').read_text().strip() != 'QEMU'):
        raise SystemExit('exact disposable QEMU lab VM required')


def sh(*args, check=True, timeout=120, env=None, data=None):
    result = subprocess.run([str(a) for a in args], capture_output=True, text=True, timeout=timeout, env=env, input=data,
                            stdin=None if data is not None else subprocess.DEVNULL)
    if check and result.returncode != 0:
        raise RuntimeError('%s exited %d: %s' % (' '.join(str(a) for a in args[:7]), result.returncode,
                                                  (result.stderr or result.stdout).strip()[-600:]))
    return result


def out(*args, **options):
    return sh(*args, **options).stdout


def short(result):
    text = (result.stderr or result.stdout).strip()
    return 'ok' if result.returncode == 0 else 'rc=%d %s' % (result.returncode, text[-300:])


def cli(*args, **options):
    return sh('incus', '--force-local', *args, **options)


def query(path):
    return json.loads(cli('query', path).stdout)


def lease_cli(project, *args, **options):
    return sh('incus', *args, env=dict(os.environ, INCUS_CONF=str(WORK/('conf-' + project))), **options)


def docker(*args, **options):
    return sh('docker', *args, **options)


def load():
    return json.loads(STATE_PATH.read_text()) if STATE_PATH.exists() else {}


def save(state):
    STATE_PATH.write_text(json.dumps(state, indent=1, sort_keys=True))


def instance_state(project, name):
    return query('/1.0/instances/%s/state?project=%s' % (name, project))


def wait_address(project, name, want4=None, want6=None, v6=False, timeout=120):
    deadline, last = time.monotonic() + timeout, {}
    while time.monotonic() < deadline:
        current = instance_state(project, name)
        nic = ((current.get('network') or {}).get('eth0') or {})
        v4s = [a['address'] for a in nic.get('addresses', []) if a['family'] == 'inet']
        v6s = [a['address'] for a in nic.get('addresses', []) if a['family'] == 'inet6' and a['scope'] == 'global']
        last = {'v4': v4s, 'v6': v6s, 'pid': current.get('pid'), 'status': current.get('status')}
        ok4 = want4 in v4s if want4 else bool(v4s)
        ok6 = (want6 in v6s if want6 else bool(v6s)) if (v6 or want6) else True
        if ok4 and ok6 and last['pid']:
            return last
        time.sleep(2)
    last['timeout'] = True
    return last


def lease_launch(state, project, name, device=None, start=True):
    """Create through the restricted lease certificate, like the shared client."""
    if cli('query', '/1.0/instances/%s?project=%s' % (name, project), check=False).returncode == 0:
        return 'exists'
    args = ['init', 'lease:' + state['pin'], 'lease:' + name, '--project', project, '--profile', PROFILE,
            '-c', 'limits.cpu=1', '-c', 'limits.memory=256MiB', '-c', 'security.privileged=false', '--device', 'root,size=2GiB']
    # One key per --device flag: the value after the first '=' is taken whole.
    for item in ([device] if isinstance(device, str) else device or []):
        args += ['--device', item]
    created = lease_cli(project, *args, check=False, timeout=300)
    if created.returncode:
        return 'init ' + short(created)
    if start:
        started = lease_cli(project, 'start', 'lease:' + name, '--project', project, check=False, timeout=180)
        if started.returncode:
            return 'start ' + short(started)
    return 'created'


def kill_listeners():
    subprocess.run(['pkill', '-f', 'lease-probe-listener'], capture_output=True)
    time.sleep(1)


def where_argv(where, argv):
    if where.startswith('ns:'):
        return ['ip', 'netns', 'exec', where[3:]] + argv
    if where.startswith('pid:'):
        return ['nsenter', '-t', where[4:], '-n'] + argv
    return argv


def listen(where, tag, port):
    subprocess.Popen(where_argv(where, ['python3', '-c', LISTENER, tag, str(port)]), stdin=subprocess.DEVNULL,
                     stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)


def probe(where, host, port, proto='tcp', source=None):
    argv = ['python3', '-c', PROBE, host, str(port), proto] + ([source] if source else [])
    result = subprocess.run(where_argv(where, argv), capture_output=True, text=True, timeout=20, stdin=subprocess.DEVNULL)
    return result.stdout.strip() or 'NO_OUTPUT rc=%d %s' % (result.returncode, result.stderr.strip()[-160:])


def try_bind(host, port, proto):
    return out('python3', '-c', BIND, host, str(port), proto).strip()


def dpid(name):
    return 'pid:' + out('docker', 'inspect', '-f', '{{.State.Pid}}', name).strip()


def dip(name, network):
    template = '{{(index .NetworkSettings.Networks "%s").IPAddress}}' % network
    return out('docker', 'inspect', '-f', template, name).strip()


def rule(action, **fields):
    item = {'action': action, 'state': 'enabled'}
    item.update({key: str(value) for key, value in fields.items()})
    return item


def put_acl(name, egress, ingress):
    current = query('/1.0/network-acls/' + name)
    body = {'description': current.get('description', ''), 'config': current.get('config') or {}, 'egress': egress, 'ingress': ingress}
    return short(cli('query', '-X', 'PUT', '/1.0/network-acls/' + name, '--data', json.dumps(body), check=False))


def address_set(name, addresses):
    """Create or replace; returns 'ok' or the daemon's reason."""
    if cli('network', 'address-set', 'show', name, check=False).returncode:
        created = cli('network', 'address-set', 'create', name, check=False)
        if created.returncode:
            return 'create ' + short(created)
    body = {'description': '', 'addresses': addresses, 'config': {}}
    replaced = cli('query', '-X', 'PUT', '/1.0/network-address-sets/' + name, '--data', json.dumps(body), check=False)
    if replaced.returncode == 0:
        return 'ok'
    added = cli('network', 'address-set', 'add', name, *addresses, check=False)
    return 'put %s; add %s' % (short(replaced), short(added))


STATIC_WIDE = [['-i', 'anas+', '-j', 'ACCEPT'], ['-o', 'anas+', '-j', 'ACCEPT']]
STATIC_NARROW = [['-i', 'anas+', '-o', 'docker0', '-m', 'conntrack', '--ctstate', 'DNAT', '-j', 'ACCEPT'],
                 ['-i', 'anas+', '-o', 'br-+', '-m', 'conntrack', '--ctstate', 'DNAT', '-j', 'ACCEPT'],
                 ['-i', 'anas+', '-o', 'docker0', '-j', 'DROP'], ['-i', 'anas+', '-o', 'br-+', '-j', 'DROP'],
                 ['-i', 'anas+', '-j', 'ACCEPT'], ['-o', 'anas+', '-j', 'ACCEPT']]


def set_static(variant):
    """The INCUS-R-126 host rule, appended after Docker's own FORWARD rules."""
    for tool in ('iptables', 'ip6tables'):
        for item in STATIC_WIDE + STATIC_NARROW:
            while sh(tool, '-C', 'FORWARD', *item, check=False).returncode == 0:
                sh(tool, '-D', 'FORWARD', *item)
        for item in (STATIC_WIDE if variant == 'wide' else STATIC_NARROW):
            sh(tool, '-A', 'FORWARD', *item)


def getent(project, name, target):
    result = cli('exec', name, '--project', project, '--', 'getent', 'ahostsv4', target, check=False, timeout=30)
    return result.stdout.split()[0] if result.returncode == 0 and result.stdout.split() else short(result)


def lease(state, project):
    return state['leases'][project]


def fence(entry):
    rules = [rule('allow', source=entry['net4'])]
    if 'net6' in entry:
        rules.append(rule('allow', source=entry['net6']))
    return rules


def nic_name(entry):
    for name, device in entry['profile']['devices'].items():
        if device.get('type') == 'nic':
            return name
    raise RuntimeError('lease profile has no NIC')


def docker_fixtures(state):
    if docker('image', 'inspect', 'probe/base:1', check=False).returncode:
        docker('import', INPUTS/'ct-root.tar.xz', 'probe/base:1', timeout=900)
    if docker('network', 'inspect', DOCKER_NET, check=False).returncode:
        docker('network', 'create', '--subnet', DOCKER_SUBNET, DOCKER_NET)
    for name, publish in (('traefik-sim', '%d:%d' % (TRAEFIK_HOST_PORT, TRAEFIK_PORT)),
                          ('other-sim', '%d:%d' % (OTHER_HOST_PORT, OTHER_PORT))):
        if docker('inspect', name, check=False).returncode:
            docker('run', '-d', '--name', name, '--restart', 'unless-stopped', '--network', DOCKER_NET, '-p', publish,
                   'probe/base:1', 'sleep', 'infinity', timeout=300)
    state['docker'] = {'version': out('docker', 'version', '--format', '{{.Server.Version}}').strip(),
                       'traefik_ip': dip('traefik-sim', DOCKER_NET), 'other_ip': dip('other-sim', DOCKER_NET)}


def stage_setup(state, report):
    env = dict(os.environ, DEBIAN_FRONTEND='noninteractive')
    if not shutil.which('docker'):
        sh('apt-get', '-o', 'Acquire::Retries=3', 'update', env=env, timeout=900)
        sh('apt-get', '-o', 'Acquire::Retries=3', 'install', '-y', '--no-install-recommends', 'docker.io', 'docker-cli', 'iptables',
           env=env, timeout=1800)
    sh('systemctl', 'start', 'docker')
    report['versions'] = {'incus': out('incus', '--version').strip(), 'kernel': os.uname().release,
                          'nft': out('nft', '--version').strip(), 'iptables': out('iptables', '--version').strip(),
                          'docker_package': out('dpkg-query', '-W', '-f=${Package} ${Version}', 'docker.io', check=False).strip()}
    cli('admin', 'waitready', timeout=90)
    if cli('storage', 'show', POOL, check=False).returncode:
        cli('storage', 'create', POOL, 'btrfs', 'size=12GiB', timeout=180)
    images = json.loads(cli('image', 'list', 'probe-ct', '--format=json').stdout)
    if not images:
        cli('image', 'import', INPUTS/'ct-incus.tar.xz', INPUTS/'ct-root.tar.xz', '--alias', 'probe-ct', timeout=900)
        images = json.loads(cli('image', 'list', 'probe-ct', '--format=json').stdout)
    state['pin'] = images[0]['fingerprint']
    WORK.mkdir(mode=0o700, exist_ok=True)
    if not (WORK/'admin.crt').exists():
        sh('openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', WORK/'admin.key', '-out', WORK/'admin.crt',
           '-days', '2', '-subj', '/CN=lease-probe-admin')
        cli('config', 'trust', 'add-certificate', WORK/'admin.crt', '--name=lease-probe-admin')
    cli('config', 'set', 'core.https_address', '127.0.0.1:8443')
    server = Path('/var/lib/incus/server.crt').read_bytes()

    def b64(path):
        return base64.b64encode(Path(path).read_bytes()).decode()
    state.setdefault('leases', {})
    for project, ipv6 in LEASES.items():
        crt, key = WORK/(project + '.crt'), WORK/(project + '.key')
        if not crt.exists():
            sh('openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', key, '-out', crt, '-days', '2',
               '-subj', '/CN=' + project)
        if cli('project', 'show', project, check=False).returncode:
            cli('project', 'create', project, '-c', 'features.networks=false', '-c', 'features.images=true',
                '-c', 'features.profiles=true')
            cli('image', 'copy', state['pin'], 'local:', '--target-project', project, timeout=600)
        provider_env = {'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'INCUS_ENDPOINT': 'https://127.0.0.1:8443',
                        'INCUS_SERVER_CERT_B64': base64.b64encode(server).decode(),
                        'INCUS_ADMIN_CERT_B64': b64(WORK/'admin.crt'), 'INCUS_ADMIN_KEY_B64': b64(WORK/'admin.key'),
                        'ANAS_RESOURCE_CLIENT_CERT': b64(crt), 'INCUS_STORAGE_POOL': POOL,
                        'INCUS_NETWORK_IPV6': 'true' if ipv6 else 'false', 'ANAS_RESOURCE_CONSUMER': project.replace('-', '_'),
                        'ANAS_RESOURCE_SANDBOX': project, 'ANAS_RESOURCE_INSTANCE_PREFIX': PREFIX,
                        'ANAS_RESOURCE_IMAGE_ARCHITECTURE': 'amd64', 'ANAS_RESOURCE_MAX_INSTANCES': '8',
                        'ANAS_RESOURCE_CPU': '1', 'ANAS_RESOURCE_MEMORY_MIB': '512', 'ANAS_RESOURCE_DISK_GIB': '4',
                        'ANAS_RESOURCE_IMAGE_ALLOWLIST': state['pin']}
        ensured = sh(INPUTS/'provider', 'ensure', '--isolation', 'container', env=provider_env, check=False, timeout=300)
        if ensured.returncode:
            raise RuntimeError('Provider ensure failed for %s: %s' % (project, ensured.stderr.strip()[-400:]))
        bridge = 'anas' + hashlib.sha256(project.encode()).hexdigest()[:10]
        config = query('/1.0/networks/' + bridge)['config']
        entry = {'bridge': bridge, 'ensure': json.loads(ensured.stdout), 'gw4': config['ipv4.address'].split('/')[0],
                 'net4': str(ipaddress.ip_interface(config['ipv4.address']).network), 'network_config': config}
        if ipv6:
            entry['gw6'] = config['ipv6.address'].split('/')[0]
            entry['net6'] = str(ipaddress.ip_interface(config['ipv6.address']).network)
        conf = WORK/('conf-' + project)
        (conf/'servercerts').mkdir(parents=True, exist_ok=True)
        shutil.copy(crt, conf/'client.crt')
        shutil.copy(key, conf/'client.key')
        (conf/'servercerts/lease.crt').write_bytes(server)
        (conf/'config.yml').write_text('default-remote: lease\nremotes:\n  lease:\n    addr: https://127.0.0.1:8443\n'
                                       '    protocol: incus\n    auth_type: tls\n    public: false\n    project: %s\n' % project)
        entry['acl_original'] = query('/1.0/network-acls/' + bridge)
        entry['profile'] = query('/1.0/profiles/%s?project=%s' % (PROFILE, project))
        entry['project_config'] = query('/1.0/projects/' + project)['config']
        state['leases'][project] = entry
    report['lease_cert_can_list'] = short(lease_cli('probe-a', 'list', 'lease:', '--project', 'probe-a', '--format=csv', check=False))
    docker_fixtures(state)
    if LAN_NS not in out('ip', 'netns', 'list'):
        sh('ip', 'netns', 'add', LAN_NS)
        sh('ip', 'link', 'add', 'vl-host', 'type', 'veth', 'peer', 'name', 'vl-ns')
        sh('ip', 'link', 'set', 'vl-ns', 'netns', LAN_NS)
        sh('ip', 'addr', 'add', LAN_HOST4 + '/24', 'dev', 'vl-host')
        sh('ip', '-6', 'addr', 'add', LAN_HOST6 + '/64', 'dev', 'vl-host', 'nodad')
        sh('ip', 'link', 'set', 'vl-host', 'up')
        for argv in (['ip', 'link', 'set', 'lo', 'up'], ['ip', 'addr', 'add', LAN_CLIENT4 + '/24', 'dev', 'vl-ns'],
                     ['ip', '-6', 'addr', 'add', LAN_CLIENT6 + '/64', 'dev', 'vl-ns', 'nodad'], ['ip', 'link', 'set', 'vl-ns', 'up']):
            sh('ip', 'netns', 'exec', LAN_NS, *argv)
    for entry in state['leases'].values():
        sh('ip', 'netns', 'exec', LAN_NS, 'ip', 'route', 'replace', entry['net4'], 'via', LAN_HOST4)
        if 'net6' in entry:
            sh('ip', 'netns', 'exec', LAN_NS, 'ip', '-6', 'route', 'replace', entry['net6'], 'via', LAN_HOST6)
    set_static('wide')
    for project, name in (('probe-a', 'a1'), ('probe-a', 'a2'), ('probe-b', 'b1')):
        report['launch_' + name] = lease_launch(state, project, PREFIX + name)
    for project, name in (('probe-a', 'a1'), ('probe-a', 'a2'), ('probe-b', 'b1')):
        report['address_' + name] = wait_address(project, PREFIX + name, v6=project == 'probe-a')
    report['docker'] = state['docker']
    report['leases'] = {p: {k: v for k, v in e.items() if k in ('bridge', 'net4', 'net6', 'gw4', 'gw6', 'ensure')}
                        for p, e in state['leases'].items()}
    report['acl_original'] = {p: {k: e['acl_original'].get(k) for k in ('egress', 'ingress')} for p, e in state['leases'].items()}
    report['profile_nic'] = {p: e['profile']['devices'] for p, e in state['leases'].items()}
    report['project_config'] = state['leases']['probe-a']['project_config']
    report['forward_chain'] = out('iptables', '-S', 'FORWARD').splitlines()


def addresses(state):
    found = {}
    for project, name in (('probe-a', 'a1'), ('probe-a', 'a2'), ('probe-b', 'b1')):
        found[name] = wait_address(project, PREFIX + name, v6=project == 'probe-a', timeout=60)
    return found


def stage_acl(state, report):
    a, b = lease(state, 'probe-a'), lease(state, 'probe-b')
    acl = a['bridge']
    set_static('wide')
    found = addresses(state)
    a1, a1ip, b1 = 'pid:%s' % found['a1']['pid'], found['a1']['v4'][0], 'pid:%s' % found['b1']['pid']
    traefik, other = dpid('traefik-sim'), dpid('other-sim')
    t_ip = dip('traefik-sim', DOCKER_NET)
    kill_listeners()
    listen(a1, 'A1', HTTP_PORT)
    listen(a1, 'A1', CLOSED_PORT)
    listen('ns:' + LAN_NS, 'LAN', LAN_PORT)
    listen(traefik, 'TRAEFIK', TRAEFIK_PORT)
    listen(other, 'OTHER', OTHER_PORT)
    time.sleep(2)
    report['addresses'] = found
    report['traefik_ip'] = t_ip
    cli('network', 'set', acl, 'security.acls.default.ingress.action=allow')

    # Rule order: does a drop win over a more specific allow added first?
    report['put_order'] = put_acl(acl, [rule('allow', source=a['net4'], destination=LAN_CLIENT4 + '/32'),
                                        rule('drop', destination='198.51.100.0/24')], [])
    report['order_allow_and_broader_drop'] = probe(a1, LAN_CLIENT4, LAN_PORT)
    put_acl(acl, [rule('allow', source=a['net4'], destination=LAN_CLIENT4 + '/32')], [])
    report['order_allow_only'] = probe(a1, LAN_CLIENT4, LAN_PORT)

    # Address set as an egress destination: Traefik after Docker's DNAT.
    report['address_set_traefik'] = address_set('probetraefik', [t_ip + '/32'])
    report['put_egress_set'] = put_acl(acl, [rule('allow', source=a['net4'], destination='$probetraefik', protocol='tcp',
                                                  destination_port=TRAEFIK_PORT)], [])
    report['egress_set_traefik_via_gateway'] = probe(a1, a['gw4'], TRAEFIK_HOST_PORT)
    report['egress_set_traefik_via_lan_address'] = probe(a1, LAN_HOST4, TRAEFIK_HOST_PORT)
    report['egress_set_other_published'] = probe(a1, a['gw4'], OTHER_HOST_PORT)
    report['nft_set_lines'] = [line.strip() for line in out('nft', 'list', 'table', 'inet', 'incus').splitlines()
                               if 'probetraefik' in line][:12]
    put_acl(acl, [], [])
    report['egress_no_rule_traefik'] = probe(a1, a['gw4'], TRAEFIK_HOST_PORT)

    # Default-deny ingress: replies, DHCP and DNS must keep working.
    cli('network', 'set', acl, 'security.acls.default.ingress.action=drop')
    put_acl(acl, fence(a), [])
    report['deny_reply_tcp4'] = probe(a1, LAN_CLIENT4, LAN_PORT)
    report['deny_reply_udp4'] = probe(a1, LAN_CLIENT4, LAN_PORT, 'udp')
    report['deny_reply_tcp6'] = probe(a1, LAN_CLIENT6, LAN_PORT)
    report['deny_new_instance'] = lease_launch(state, 'probe-a', PREFIX + 'a3')
    report['deny_new_instance_address'] = wait_address('probe-a', PREFIX + 'a3', v6=True, timeout=90)
    report['deny_dns_local'] = getent('probe-a', PREFIX + 'a1', PREFIX + 'a3.incus')
    report['deny_dns_forwarded'] = getent('probe-a', PREFIX + 'a1', 'mirrors.aliyun.com')
    report['deny_lan_to_guest'] = probe('ns:' + LAN_NS, a1ip, HTTP_PORT)
    report['deny_container_to_guest'] = probe(other, a1ip, HTTP_PORT)
    report['deny_other_lease_to_guest'] = probe(b1, a1ip, HTTP_PORT)
    cli('network', 'set', acl, 'security.acls.default.ingress.action=allow')
    report['control_allow_lan_to_guest'] = probe('ns:' + LAN_NS, a1ip, HTTP_PORT)
    report['control_allow_container_to_guest'] = probe(other, a1ip, HTTP_PORT)
    cli('network', 'set', acl, 'security.acls.default.ingress.action=drop')

    # Traefik -> guest: the rule names Traefik's address before Docker's masquerade.
    report['put_ingress_set'] = put_acl(acl, fence(a), [rule('allow', source='$probetraefik', destination=a1ip + '/32',
                                                             protocol='tcp', destination_port=HTTP_PORT)])
    report['ingress_traefik_to_http'] = probe(traefik, a1ip, HTTP_PORT)
    report['ingress_other_container_to_http'] = probe(other, a1ip, HTTP_PORT)
    report['ingress_traefik_to_other_port'] = probe(traefik, a1ip, CLOSED_PORT)
    report['ingress_lan_to_http'] = probe('ns:' + LAN_NS, a1ip, HTTP_PORT)
    report['ingress_other_lease_to_http'] = probe(b1, a1ip, HTTP_PORT)
    report['acl_chain_head'] = [line.strip() for line in out('nft', 'list', 'table', 'inet', 'incus').splitlines()
                                if 'acl' in line and a['bridge'] in line][:20]
    cli('network', 'set', b['bridge'], 'security.acls.default.ingress.action=allow')


def stage_docker(state, report):
    a = lease(state, 'probe-a')
    found = addresses(state)
    a1 = 'pid:%s' % found['a1']['pid']
    other = dpid('other-sim')
    o_ip = dip('other-sim', DOCKER_NET)
    kill_listeners()
    listen(other, 'OTHER', OTHER_PORT)
    listen(other, 'OTHER', OTHER_UNPUBLISHED)
    time.sleep(2)
    # internet_lan_host: the ACL lets Docker networks through; what stops unpublished ports?
    report['put'] = put_acl(a['bridge'], fence(a)[1:] + [rule('allow', source=a['net4'], destination=DOCKER_SUBNET)], [])
    report['docker_version'] = out('docker', 'version', '--format', '{{.Server.Version}}').strip()
    for variant in ('wide', 'narrow'):
        set_static(variant)
        report[variant] = {'unpublished_port_direct': probe(a1, o_ip, OTHER_UNPUBLISHED),
                           'published_container_port_direct': probe(a1, o_ip, OTHER_PORT),
                           'published_port_via_host': probe(a1, a['gw4'], OTHER_HOST_PORT)}
    set_static('wide')
    report['iptables_forward'] = out('iptables', '-S', 'FORWARD').splitlines()
    report['iptables_docker'] = out('iptables', '-S', 'DOCKER', check=False).splitlines()[:30]
    report['iptables_chains'] = [line for line in out('iptables', '-S').splitlines() if line.startswith('-N')]


def stage_isolation(state, report):
    a = lease(state, 'probe-a')
    nic = nic_name(a)
    put_acl(a['bridge'], fence(a), [])
    found = addresses(state)
    kill_listeners()
    listen('pid:%s' % found['a2']['pid'], 'A2', HTTP_PORT)
    listen('ns:' + LAN_NS, 'LAN', LAN_PORT)
    time.sleep(2)
    a1, a2ip = 'pid:%s' % found['a1']['pid'], found['a2']['v4'][0]
    report['before_a1_to_a2'] = probe(a1, a2ip, HTTP_PORT)
    report['set'] = short(cli('profile', 'device', 'set', PROFILE, nic, 'security.port_isolation=true', '--project', 'probe-a',
                              check=False))
    report['bridge_links_live'] = [line for line in out('bridge', '-d', 'link', 'show').splitlines() if 'isolated' in line][:8]
    report['live_a1_to_a2'] = probe(a1, a2ip, HTTP_PORT)
    for name in ('a1', 'a2'):
        cli('restart', PREFIX + name, '--project', 'probe-a', timeout=180)
    found = addresses(state)
    kill_listeners()
    listen('pid:%s' % found['a2']['pid'], 'A2', HTTP_PORT)
    listen('ns:' + LAN_NS, 'LAN', LAN_PORT)
    time.sleep(2)
    a1, a2ip = 'pid:%s' % found['a1']['pid'], found['a2']['v4'][0]
    report['isolated_a1_to_a2'] = probe(a1, a2ip, HTTP_PORT)
    report['isolated_a1_to_a2_v6'] = probe(a1, found['a2']['v6'][0], HTTP_PORT) if found['a2']['v6'] else 'no v6'
    report['isolated_a1_to_lan'] = probe(a1, LAN_CLIENT4, LAN_PORT)
    report['isolated_a1_dns'] = getent('probe-a', PREFIX + 'a1', PREFIX + 'a2.incus')
    report['bridge_links'] = [line for line in out('bridge', '-d', 'link', 'show').splitlines() if 'isolated' in line][:8]
    cli('profile', 'device', 'unset', PROFILE, nic, 'security.port_isolation', '--project', 'probe-a')
    for name in ('a1', 'a2'):
        cli('restart', PREFIX + name, '--project', 'probe-a', timeout=180)


def stage_slots(state, report):
    a = lease(state, 'probe-a')
    nic = nic_name(a)
    net4, net6 = ipaddress.ip_network(a['net4']), ipaddress.ip_network(a['net6'])
    slot4, spoof4, slot6 = str(net4.network_address + 20), str(net4.network_address + 21), str(net6.network_address + 0x20)
    state['slot'] = {'v4': slot4, 'v6': slot6, 'name': PREFIX + 'slot1'}
    report['dhcp_ranges'] = short(cli('network', 'set', a['bridge'], 'ipv4.dhcp.ranges=%s-%s' % (
        net4.network_address + 100, net4.network_address + 199), check=False))
    report['dhcpv6_stateful'] = short(cli('network', 'set', a['bridge'], 'ipv6.dhcp.stateful=true', check=False))
    put_acl(a['bridge'], fence(a), [])
    report['nic_profile'] = a['profile']['devices'][nic]
    name = PREFIX + 'slot1'
    fixed = ['%s,ipv4.address=%s' % (nic, slot4), '%s,ipv6.address=%s' % (nic, slot6)]
    report['restricted_fixed_create'] = lease_launch(state, 'probe-a', name, fixed)
    if cli('query', '/1.0/instances/%s?project=probe-a' % name, check=False).returncode == 0:
        instance = query('/1.0/instances/%s?project=probe-a' % name)
        report['instance_nic'] = instance['devices'].get(nic)
        report['expanded_nic'] = instance['expanded_devices'].get(nic)
        got = wait_address('probe-a', name, want4=slot4, want6=slot6, timeout=120)
        report['slot_addresses'] = got
        if got.get('timeout'):
            files = cli('exec', name, '--project', 'probe-a', '--', 'sh', '-c',
                        'ls /etc/systemd/network /etc/network/interfaces.d 2>&1; cat /etc/systemd/network/*.network 2>&1 | head -40',
                        check=False, timeout=30)
            report['guest_network_config'] = files.stdout[-1500:]
        pid = 'pid:%s' % got['pid']
        kill_listeners()
        listen('ns:' + LAN_NS, 'LAN', LAN_PORT)
        time.sleep(2)
        report['legit_source_v4'] = probe(pid, LAN_CLIENT4, LAN_PORT, source=slot4)
        sh('nsenter', '-t', str(got['pid']), '-n', 'ip', 'addr', 'add', spoof4 + '/32', 'dev', 'eth0')
        report['spoofed_in_subnet_source_v4'] = probe(pid, LAN_CLIENT4, LAN_PORT, source=spoof4)
        sh('nsenter', '-t', str(got['pid']), '-n', 'ip', 'addr', 'del', spoof4 + '/32', 'dev', 'eth0')
        if got['v6']:
            report['legit_source_v6'] = probe(pid, LAN_CLIENT6, LAN_PORT, source=slot6)
        # Same fixed address on a second instance in the lease.
        report['duplicate_create_stopped'] = lease_launch(state, 'probe-a', PREFIX + 'slot2', '%s,ipv4.address=%s' % (nic, slot4), start=False)
        if cli('query', '/1.0/instances/%sslot2?project=probe-a' % PREFIX, check=False).returncode == 0:
            report['duplicate_start'] = short(lease_cli('probe-a', 'start', 'lease:%sslot2' % PREFIX, '--project', 'probe-a',
                                                        check=False, timeout=120))
            cli('delete', PREFIX + 'slot2', '--project', 'probe-a', '--force', timeout=120)
        # Can the lease certificate turn the spoofing filters off?
        report['filters_off_create'] = lease_launch(state, 'probe-a', PREFIX + 'slot3', '%s,security.ipv4_filtering=false' % nic,
                                                    start=False)
        if cli('query', '/1.0/instances/%sslot3?project=probe-a' % PREFIX, check=False).returncode == 0:
            report['filters_off_expanded'] = query('/1.0/instances/%sslot3?project=probe-a' % PREFIX)['expanded_devices'].get(nic)
            cli('delete', PREFIX + 'slot3', '--project', 'probe-a', '--force', timeout=120)
        # Rebuilding the same name with the same slot address gets it back.
        cli('delete', name, '--project', 'probe-a', '--force', timeout=120)
        report['recreate'] = lease_launch(state, 'probe-a', name, fixed)
        report['recreate_addresses'] = wait_address('probe-a', name, want4=slot4, want6=slot6, timeout=120)


def ports_ruleset(slot4, slot6):
    return ('table inet anas_probe_ports {\n'
            '  map tcp4 { type inet_service : ipv4_addr . inet_service; elements = { %d : %s . %d } }\n'
            '  map udp4 { type inet_service : ipv4_addr . inet_service; elements = { %d : %s . %d } }\n'
            '  map tcp6 { type inet_service : ipv6_addr . inet_service; elements = { %d : %s . %d } }\n'
            '  map udp6 { type inet_service : ipv6_addr . inet_service; elements = { %d : %s . %d } }\n'
            '  chain bind {\n'
            '    meta nfproto ipv4 meta l4proto tcp dnat ip addr . port to tcp dport map @tcp4\n'
            '    meta nfproto ipv4 meta l4proto udp dnat ip addr . port to udp dport map @udp4\n'
            '    meta nfproto ipv6 meta l4proto tcp dnat ip6 addr . port to tcp dport map @tcp6\n'
            '    meta nfproto ipv6 meta l4proto udp dnat ip6 addr . port to udp dport map @udp6\n'
            '  }\n'
            '  chain pre {\n'
            '    type nat hook prerouting priority dstnat; policy accept;\n'
            '    fib daddr type local jump bind\n'
            '  }\n'
            '  chain out {\n'
            '    type nat hook output priority -100; policy accept;\n'
            '    ip daddr 127.0.0.0/8 return\n'
            '    ip6 daddr ::1 return\n'
            '    fib daddr type local jump bind\n'
            '  }\n'
            '}\n') % (BIND_TCP, slot4, SLOT_TCP, BIND_UDP, slot4, SLOT_UDP, BIND_TCP, slot6, SLOT_TCP, BIND_UDP, slot6, SLOT_UDP)


def reservation_units():
    UNITS.mkdir(parents=True, exist_ok=True)
    (UNITS/'anas-probe-port-tcp.socket').write_text('[Socket]\nListenStream=%d\nAccept=yes\n' % BIND_TCP)
    (UNITS/'anas-probe-port-tcp@.service').write_text('[Service]\nExecStart=/bin/true\nStandardInput=socket\n')
    (UNITS/'anas-probe-port-udp.socket').write_text('[Socket]\nListenDatagram=%d\nService=anas-probe-port-null.service\n' % BIND_UDP)
    (UNITS/'anas-probe-port-null.service').write_text('[Service]\nExecStart=/bin/true\n')
    sh('systemctl', 'daemon-reload')
    return {unit: short(sh('systemctl', 'start', unit, check=False)) for unit in ('anas-probe-port-tcp.socket', 'anas-probe-port-udp.socket')}


def binding_probes(state, found):
    a = lease(state, 'probe-a')
    slot = state['slot']
    slot_pid = 'pid:%s' % found['slot']['pid']
    kill_listeners()
    listen(slot_pid, 'SLOT', SLOT_TCP)
    listen(slot_pid, 'SLOT', SLOT_UDP)
    listen('ns:' + LAN_NS, 'LAN', BIND_TCP)
    time.sleep(2)
    uplink = [a4['local'] for link in json.loads(out('ip', '-j', '-4', 'addr', 'show', 'scope', 'global'))
              for a4 in link.get('addr_info', []) if link['ifname'].startswith('en')]
    results = {
        'lan_tcp4': probe('ns:' + LAN_NS, LAN_HOST4, BIND_TCP),
        'lan_udp4': probe('ns:' + LAN_NS, LAN_HOST4, BIND_UDP, 'udp'),
        'lan_tcp6': probe('ns:' + LAN_NS, LAN_HOST6, BIND_TCP),
        'lan_udp6': probe('ns:' + LAN_NS, LAN_HOST6, BIND_UDP, 'udp'),
        'host_to_own_lan_address': probe('vm', LAN_HOST4, BIND_TCP),
        'host_to_uplink_address': probe('vm', uplink[0], BIND_TCP) if uplink else 'no uplink',
        'host_to_other_machine': probe('vm', LAN_CLIENT4, BIND_TCP),
        'container_to_its_gateway': probe(dpid('other-sim'), '172.30.0.1', BIND_TCP),
        'container_to_lan_address': probe(dpid('other-sim'), LAN_HOST4, BIND_TCP),
        'other_lease_to_host_port': probe('pid:%s' % found['b1']['pid'], LAN_HOST4, BIND_TCP),
        'other_lease_to_slot_direct': probe('pid:%s' % found['b1']['pid'], slot['v4'], SLOT_TCP),
        'same_lease_to_host_port': probe('pid:%s' % found['a1']['pid'], LAN_HOST4, BIND_TCP),
    }
    return results


def stage_ports(state, report):
    a, b = lease(state, 'probe-a'), lease(state, 'probe-b')
    slot = state['slot']
    set_static('wide')
    found = addresses(state)
    found['slot'] = wait_address('probe-a', slot['name'], want4=slot['v4'], timeout=60)
    report['address_set_leases'] = address_set('probeleases', [a['net4'], a['net6'], b['net4']])
    slot_rules = [rule('drop', source='$probeleases')]
    for address in (slot['v4'], slot['v6']):
        slot_rules += [rule('allow', destination=address, protocol='tcp', destination_port=SLOT_TCP),
                       rule('allow', destination=address, protocol='udp', destination_port=SLOT_UDP)]
    report['put_a'] = put_acl(a['bridge'], fence(a), slot_rules)
    cli('network', 'set', a['bridge'], 'security.acls.default.ingress.action=drop')
    report['put_b_fence_only'] = put_acl(b['bridge'], fence(b), [])
    ruleset = ports_ruleset(slot['v4'], slot['v6'])
    sh('nft', 'delete', 'table', 'inet', 'anas_probe_ports', check=False)
    report['nft_load'] = short(sh('nft', '-f', '-', data=ruleset, check=False))
    report['bindings'] = binding_probes(state, found)
    # Without the lease drop rule, only lease B's egress would stand in the way.
    put_acl(a['bridge'], fence(a), slot_rules[1:])
    report['without_lease_drop'] = {'other_lease_to_host_port': probe('pid:%s' % found['b1']['pid'], LAN_HOST4, BIND_TCP)}
    report['put_b_lan_only'] = put_acl(b['bridge'], [rule('allow', source=b['net4'], destination='198.51.100.0/24')], [])
    report['without_lease_drop']['other_lease_lan_only_to_host_port'] = probe('pid:%s' % found['b1']['pid'], LAN_HOST4, BIND_TCP)
    put_acl(a['bridge'], fence(a), slot_rules)
    put_acl(b['bridge'], fence(b), [])

    # Reservation: systemd holds the host ports, the DNAT still wins for real traffic.
    report['reservation_start'] = reservation_units()
    report['reservation_listen'] = out('ss', '-H', '-lntup', 'sport = :%d or sport = :%d' % (BIND_TCP, BIND_UDP)).splitlines()
    report['with_reservation'] = binding_probes(state, found)
    report['loopback_tcp'] = probe('vm', '127.0.0.1', BIND_TCP)
    report['loopback_udp'] = probe('vm', '127.0.0.1', BIND_UDP, 'udp')
    time.sleep(3)
    report['reservation_after_loopback'] = {u: out('systemctl', 'show', u, '-p', 'ActiveState', '-p', 'Result', '-p', 'NAccepted').split()
                                            for u in ('anas-probe-port-tcp.socket', 'anas-probe-port-udp.socket')}
    report['host_bind'] = {'tcp_any4': try_bind('0.0.0.0', BIND_TCP, 'tcp'), 'tcp_lan4': try_bind(LAN_HOST4, BIND_TCP, 'tcp'),
                           'tcp_any6': try_bind('::', BIND_TCP, 'tcp'), 'udp_any4': try_bind('0.0.0.0', BIND_UDP, 'udp')}
    report['docker_publish_conflict'] = docker_conflict(state, userland_proxy=True)
    Path('/etc/docker').mkdir(exist_ok=True)
    Path('/etc/docker/daemon.json').write_text('{"userland-proxy": false}\n')
    report['traefik_ip_before_docker_restart'] = dip('traefik-sim', DOCKER_NET)
    sh('systemctl', 'restart', 'docker', timeout=180)
    time.sleep(5)
    report['traefik_ip_after_docker_restart'] = dip('traefik-sim', DOCKER_NET)
    report['forward_after_docker_restart'] = out('iptables', '-S', 'FORWARD').splitlines()
    report['docker_publish_conflict_no_proxy'] = docker_conflict(state, userland_proxy=False)
    Path('/etc/docker/daemon.json').unlink()
    sh('systemctl', 'restart', 'docker', timeout=180)
    time.sleep(5)
    for unit in ('anas-probe-port-tcp.socket', 'anas-probe-port-udp.socket'):
        sh('systemctl', 'stop', unit, check=False)


def docker_conflict(state, userland_proxy):
    docker('rm', '-f', 'conflict-sim', check=False)
    created = docker('run', '-d', '--name', 'conflict-sim', '--network', DOCKER_NET, '-p', '%d:9999' % BIND_TCP,
                     'probe/base:1', 'sleep', 'infinity', check=False, timeout=120)
    result = {'run': short(created)}
    running = out('docker', 'inspect', '-f', '{{.State.Running}}', 'conflict-sim', check=False).strip()
    result['running'] = running
    if running == 'true':
        found = addresses(state)
        slot = state['slot']
        found['slot'] = wait_address('probe-a', slot['name'], want4=slot['v4'], timeout=60)
        listen(dpid('conflict-sim'), 'CONFLICT', 9999)
        time.sleep(2)
        result['after'] = binding_probes(state, found)
        listen(dpid('conflict-sim'), 'CONFLICT', 9999)
        time.sleep(1)
        result['docker_listen'] = out('ss', '-H', '-lntp', 'sport = :%d' % BIND_TCP).splitlines()
    docker('rm', '-f', 'conflict-sim', check=False)
    return result


def control_connect(container):
    return probe(dpid(container), CONTROL_GW, 8443)


def listening(address):
    return [line for line in out('ss', '-H', '-lnt').splitlines() if address in line]


def boot_like(start_units, drop_bridge=True):
    for unit in ('incus.service', 'incus.socket', 'docker.service', 'docker.socket'):
        sh('systemctl', 'stop', unit, check=False, timeout=180)
    if drop_bridge and Path('/sys/class/net/' + CONTROL_BRIDGE).exists():
        sh('ip', 'link', 'del', CONTROL_BRIDGE)
    started = short(sh('systemctl', 'start', *start_units, check=False, timeout=300))
    cli('admin', 'waitready', '--timeout=90', check=False, timeout=120)
    time.sleep(5)
    return started


def stage_control(state, report):
    if docker('network', 'inspect', CONTROL_NET, check=False).returncode:
        docker('network', 'create', '--subnet', CONTROL_SUBNET, '--gateway', CONTROL_GW,
               '--opt', 'com.docker.network.bridge.name=' + CONTROL_BRIDGE, CONTROL_NET)
    if docker('inspect', 'control-sim', check=False).returncode:
        docker('run', '-d', '--name', 'control-sim', '--restart', 'unless-stopped', '--network', CONTROL_NET, 'probe/base:1',
               'sleep', 'infinity', timeout=120)
    report['unit_files'] = out('systemctl', 'list-unit-files', 'incus*', 'docker*', '--no-legend').splitlines()
    report['incus_unit_order'] = out('systemctl', 'show', 'incus.service', '-p', 'After', '-p', 'Wants', '-p', 'WantedBy').strip()
    address = CONTROL_GW + ':8443'
    report['set'] = short(cli('config', 'set', 'core.https_address', address, check=False))
    time.sleep(2)
    report['listening_now'] = listening(address)
    report['container_connect_now'] = control_connect('control-sim')

    # 1. Incus starts while the bridge address does not exist.
    since = out('date', '+%Y-%m-%d %H:%M:%S').strip()
    report['absent_start'] = boot_like(['incus.service'])
    report['absent_listening'] = listening(address)
    sh('systemctl', 'start', 'docker.service', timeout=180)
    time.sleep(20)
    report['absent_then_docker_listening'] = listening(address)
    report['absent_then_docker_connect'] = control_connect('control-sim')
    report['absent_journal'] = [line for line in out('journalctl', '-u', 'incus.service', '--since', since, '--no-pager', '-o', 'cat',
                                                   check=False).splitlines() if 'listen' in line.lower() or '8443' in line][-8:]
    report['reset_same_value'] = short(cli('config', 'set', 'core.https_address', address, check=False))
    time.sleep(2)
    report['after_reset_same_value_listening'] = listening(address)

    # 2. Order Incus after Docker, then a boot-like transaction of both.
    dropin = Path('/etc/systemd/system/incus.service.d')
    dropin.mkdir(parents=True, exist_ok=True)
    (dropin/'anas-probe-after-docker.conf').write_text('[Unit]\nAfter=docker.service\n')
    sh('systemctl', 'daemon-reload')
    report['ordered_start'] = boot_like(['docker.service', 'incus.service'])
    report['ordered_listening'] = listening(address)
    report['ordered_connect'] = control_connect('control-sim')
    report['multi_user_start'] = boot_like(['multi-user.target'])
    report['multi_user_listening'] = listening(address)
    report['multi_user_connect'] = control_connect('control-sim')

    # 3. Docker restarts and network re-creation while Incus keeps its socket.
    sh('systemctl', 'restart', 'docker', timeout=180)
    time.sleep(8)
    report['docker_restart_listening'] = listening(address)
    report['docker_restart_connect'] = control_connect('control-sim')
    docker('rm', '-f', 'control-sim')
    docker('network', 'rm', CONTROL_NET)
    report['network_removed_listening'] = listening(address)
    docker('network', 'create', '--subnet', CONTROL_SUBNET, '--gateway', CONTROL_GW,
           '--opt', 'com.docker.network.bridge.name=' + CONTROL_BRIDGE, CONTROL_NET)
    docker('run', '-d', '--name', 'control-sim', '--restart', 'unless-stopped', '--network', CONTROL_NET, 'probe/base:1',
           'sleep', 'infinity', timeout=120)
    time.sleep(3)
    report['network_recreated_connect'] = control_connect('control-sim')
    (dropin/'anas-probe-after-docker.conf').unlink()
    sh('systemctl', 'daemon-reload')
    cli('config', 'set', 'core.https_address', '127.0.0.1:8443')


def stage_dockerce(state, report):
    env = dict(os.environ, DEBIAN_FRONTEND='noninteractive')
    key = WORK/'docker.asc'
    sh('curl', '-fsSL', '--max-time', '60', 'https://mirrors.aliyun.com/docker-ce/linux/debian/gpg', '-o', key, timeout=90)
    fingerprint = [line.split(':')[9] for line in out('gpg', '--show-keys', '--with-colons', key).splitlines() if line.startswith('fpr:')]
    if not fingerprint or fingerprint[0] != DOCKER_KEY_FPR:
        raise RuntimeError('unexpected Docker repository key')
    Path('/etc/apt/keyrings').mkdir(exist_ok=True)
    shutil.copy(key, '/etc/apt/keyrings/docker.asc')
    Path('/etc/apt/sources.list.d/docker-ce.sources').write_text(
        'Types: deb\nURIs: https://mirrors.aliyun.com/docker-ce/linux/debian\nSuites: trixie\nComponents: stable\n'
        'Signed-By: /etc/apt/keyrings/docker.asc\n')
    for name in ('traefik-sim', 'other-sim', 'control-sim', 'conflict-sim'):
        docker('rm', '-f', name, check=False)
    sh('apt-get', '-o', 'Acquire::Retries=3', 'update', env=env, timeout=900)
    sh('apt-get', 'purge', '-y', 'docker.io', env=env, timeout=600)
    sh('apt-get', '-o', 'Acquire::Retries=3', 'install', '-y', '--no-install-recommends', 'docker-ce', 'docker-ce-cli', 'containerd.io',
       env=env, timeout=1800)
    sh('systemctl', 'start', 'docker')
    report['docker_ce'] = out('dpkg-query', '-W', '-f=${Package} ${Version}\n', 'docker-ce', 'containerd.io').splitlines()
    docker_fixtures(state)
    set_static('wide')


STAGES = {'setup': stage_setup, 'acl': stage_acl, 'docker': stage_docker, 'isolation': stage_isolation, 'slots': stage_slots,
          'ports': stage_ports, 'control': stage_control, 'dockerce': stage_dockerce}


def main():
    require_vm()
    WORK.mkdir(mode=0o700, exist_ok=True)
    state = load()
    report = {'stage': STAGE, 'started': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())}
    try:
        STAGES[STAGE](state, report)
    except Exception as error:
        report['error'] = '%s: %s' % (type(error).__name__, str(error)[:800])
    finally:
        save(state)
        report['finished'] = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())
        print(json.dumps(report, indent=1, sort_keys=True, default=str))


if __name__ == '__main__':
    main()
