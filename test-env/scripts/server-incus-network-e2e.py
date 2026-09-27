#!/usr/bin/env python3
"""Explicit disposable-VM dual-stack egress test (INCUS-R-044). Never use on a business host.

Inside the owner's disposable lab VM, with a fresh Incus daemon and no Docker:

* an "upstream" network namespace stands in for the outside world, reached
  through a veth pair with documentation addresses on both families;
* the real Provider creates two leases, one with IPv6 enabled and one with it
  disabled, each with its own managed bridge;
* a Debian system container in each lease connects to the upstream listener
  over both families.

With IPv6 enabled, both families must leave through the lease bridge and be
masqueraded to the lab VM's own upstream address; the guest's bridge address
must never reach upstream. With IPv6 disabled the bridge carries no IPv6 at
all and only IPv4 leaves, also masqueraded. The guest has exactly one
interface besides loopback, and its default routes point at the bridge.

Source fence: each guest then forges an off-subnet source on both families
(a static neighbour entry sends the packet straight to the bridge). The
masquerade rule only rewrites the bridge's own subnets, so such a packet would
reach upstream unrewritten; nftables counters in the upstream namespace must
stay at zero. In the IPv6 lease the forged IPv6 packet must be seen reaching
the lab VM's routing stack -- so only the lease ACL can have stopped it --
and one deliberate drift (an extra allow-all rule) must let it through,
make the Provider report the lease not ready, and be repaired by ensure.
"""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import time

ROOT = Path('/run/anas-incus-network')
POOL = 'anas-network-btrfs'
NETNS = 'anas-upstream'
HOST_V4, UPSTREAM_V4 = '198.51.100.1', '198.51.100.2'
HOST_V6, UPSTREAM_V6 = '2001:db8:99::1', '2001:db8:99::2'
PORT = 18080
# project -> whether the lease asks for IPv6
LEASES = {'anas-network-v6': True, 'anas-network-v4': False}
INSTANCE = 'anas-native-net'
# project -> forged (IPv4, IPv6) sources, outside every lease subnet
SPOOF = {'anas-network-v6': ('192.0.2.66', '2001:db8:66::66'), 'anas-network-v4': ('192.0.2.44', '2001:db8:44::44')}
PROBE_TABLE = 'anas_spoof_probe'

LISTENER = r'''
import json, socket, sys, threading
out = open(sys.argv[1], 'a', buffering=1)
def serve(family, address):
    s = socket.socket(family, socket.SOCK_STREAM)
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    if family == socket.AF_INET6:
        s.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 1)
    s.bind((address, %d)); s.listen(8)
    while True:
        c, peer = s.accept()
        token = c.recv(64).decode(errors='replace').strip()
        out.write(json.dumps({'family': 6 if family == socket.AF_INET6 else 4, 'peer': peer[0], 'token': token}) + '\n')
        c.sendall(b'ok\n'); c.close()
threading.Thread(target=serve, args=(socket.AF_INET6, '%s'), daemon=True).start()
serve(socket.AF_INET, '%s')
''' % (PORT, UPSTREAM_V6, UPSTREAM_V4)


def require_vm(identity):
    if (os.geteuid() != 0 or not re.fullmatch(r'anas-incus-lifecycle-[a-z0-9]{6}', identity)
            or Path('/var/lib/cloud/data/instance-id').read_text().strip() != identity
            or Path('/sys/class/dmi/id/sys_vendor').read_text().strip() != 'QEMU'
            or Path('/var/run/docker.sock').exists() or Path('/var/lib/docker').exists()):
        raise RuntimeError('exact disposable QEMU cloud identity without Docker is required')


def call(args, env=None, timeout=90, data=None, check=True):
    result = subprocess.run(args, input=data, stdin=None if data is not None else subprocess.DEVNULL,
                            env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
    if len(result.stdout) > 4 << 20 or len(result.stderr) > 64 << 10:
        raise RuntimeError('test subprocess output exceeded its limit')
    if check and result.returncode:
        # Name the subcommand and keep the daemon's short reason; this harness
        # handles no credentials, only lab object names.
        words = [a for a in args[1:5] if not a.startswith('--')]
        raise RuntimeError('test subprocess failed: ' + Path(args[0]).name + ' ' + ' '.join(words) + ': ' +
                           result.stderr.decode(errors='replace').strip()[-300:])
    return result


def emit(name, **fields):
    print(json.dumps({'check': name, 'passed': True, **fields}), flush=True)


def retain_diagnostics(path, cli, project, bridge):
    """Keep the guest's view and the bridge's firewall state in the private report."""
    sections = []
    guest = [['ip', '-6', 'addr'], ['ip', '-6', 'route'], ['ip', '-6', 'neigh'], ['ip', 'route'], ['ip', 'neigh']]
    for argv in guest:
        result = cli('exec', INSTANCE, '--project', project, '--', *argv, check=False, timeout=30)
        sections.append(('guest ' + ' '.join(argv), result.stdout + result.stderr))
    for argv in (['/usr/bin/incus', '--force-local', 'network', 'show', bridge],
                 ['/usr/bin/incus', '--force-local', 'network', 'acl', 'show', bridge]):
        result = cli(*argv[2:], check=False, timeout=30)
        sections.append((' '.join(argv[2:]), result.stdout + result.stderr))
    for argv in (['/usr/sbin/nft', 'list', 'table', 'inet', 'incus'], ['/usr/sbin/ip', '-6', 'route'],
                 ['/usr/sbin/sysctl', 'net.ipv6.conf.all.forwarding', 'net.ipv6.conf.%s.forwarding' % bridge]):
        result = call(argv, check=False, timeout=30)
        sections.append((' '.join(argv), result.stdout + result.stderr))
    path.write_bytes(b''.join(b'== ' + title.encode() + b'\n' + body + b'\n' for title, body in sections))


def masquerade_verdict(records, token, family, expected_peer, guest_addresses):
    """The upstream listener must see the lab VM's own address, never the guest's."""
    seen = [r for r in records if r['token'] == token and r['family'] == family]
    if len(seen) != 1:
        return False
    return seen[0]['peer'] == expected_peer and seen[0]['peer'] not in guest_addresses


def counter_name(address):
    return 's' + re.sub(r'[^0-9a-f]', '_', address)


def probe_ruleset():
    """Named counters for every forged source, before conntrack and routing."""
    counters, rules = [], []
    for sources in SPOOF.values():
        for family, address in zip(('ip', 'ip6'), sources):
            counters.append('\tcounter %s {}' % counter_name(address))
            rules.append('\t\t%s saddr %s counter name "%s"' % (family, address, counter_name(address)))
    return ('table inet %s {\n%s\n\tchain pre {\n\t\ttype filter hook prerouting priority -400; policy accept;\n%s\n\t}\n}\n'
            % (PROBE_TABLE, '\n'.join(counters), '\n'.join(rules)))


def read_counters(listing):
    counters = {}
    for item in json.loads(listing).get('nftables', []):
        if 'counter' in item and item['counter'].get('table') == PROBE_TABLE:
            counters[item['counter']['name']] = item['counter']['packets']
    return {address: counters[counter_name(address)] for sources in SPOOF.values() for address in sources}


def spoof_verdict(host, upstream, sources, ipv6):
    """No forged source may reach upstream; in the IPv6 lease the forged IPv6
    packet must have reached the lab VM, so the fence is what stopped it."""
    if any(upstream[address] for address in sources):
        return False
    return host[sources[1]] > 0 if ipv6 else True


def drift_verdict(drift):
    """The drift must leak (the counters can see it), be reported, and be closed by ensure."""
    return (drift['inspect_ready'] is False and drift['leaked_upstream'] >= 1 and drift['ensure_rc'] == 0
            and drift['repaired_ready'] is True and drift['after_repair_upstream'] == 0)


def main(args):
    require_vm(args.vm_id)
    for name in ('provider', 'ct_metadata', 'ct_rootfs'):
        p = Path(getattr(args, name))
        if not p.is_absolute() or not p.is_file() or p.is_symlink():
            raise RuntimeError('explicit regular inputs required')
    report = Path(args.report_root)
    if not report.is_absolute() or report.exists() or ROOT.exists():
        raise RuntimeError('fresh report and runtime directories required')
    os.umask(0o077)
    report.mkdir(mode=0o700)
    ROOT.mkdir(mode=0o700)
    (ROOT/'admin').mkdir(mode=0o700)
    env = {'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'HOME': str(ROOT/'admin'), 'INCUS_CONF': str(ROOT/'admin'),
           'INCUS_DIR': '/var/lib/incus', 'INCUS_SOCKET': '/var/lib/incus/unix.socket'}

    def cli(*arguments, **options):
        return call(['/usr/bin/incus', '--force-local', *arguments], env, **options)

    def netns(*arguments, **options):
        return call(['/usr/sbin/ip', 'netns', 'exec', NETNS, *arguments], **options)

    call(['/usr/bin/systemctl', 'start', 'incus.service'])
    cli('admin', 'waitready', timeout=60)
    for kind in ('storage', 'image', 'list'):
        arguments = ('list', '--format=json') if kind != 'list' else ('--format=json',)
        if json.loads(cli(kind, *arguments).stdout):
            raise RuntimeError('lab daemon must have no existing pools, images or instances')
    if call(['/usr/sbin/ip', 'netns', 'list']).stdout.strip():
        raise RuntimeError('lab VM must have no named network namespaces')
    listener = None
    try:
        # Upstream stand-in: its own namespace, reached only through the lab VM.
        call(['/usr/sbin/ip', 'netns', 'add', NETNS])
        call(['/usr/sbin/ip', 'link', 'add', 'anasup0', 'type', 'veth', 'peer', 'name', 'anasup1'])
        call(['/usr/sbin/ip', 'link', 'set', 'anasup1', 'netns', NETNS])
        call(['/usr/sbin/ip', 'addr', 'add', HOST_V4 + '/24', 'dev', 'anasup0'])
        call(['/usr/sbin/ip', '-6', 'addr', 'add', HOST_V6 + '/64', 'dev', 'anasup0', 'nodad'])
        call(['/usr/sbin/ip', 'link', 'set', 'anasup0', 'up'])
        netns('/usr/sbin/ip', 'link', 'set', 'lo', 'up')
        netns('/usr/sbin/ip', 'addr', 'add', UPSTREAM_V4 + '/24', 'dev', 'anasup1')
        netns('/usr/sbin/ip', '-6', 'addr', 'add', UPSTREAM_V6 + '/64', 'dev', 'anasup1', 'nodad')
        netns('/usr/sbin/ip', 'link', 'set', 'anasup1', 'up')
        # Upstream knows nothing about any lease subnet: an unmasqueraded guest
        # packet would have no route back, and its source would be recorded.
        netns('/usr/sbin/ip', 'route', 'add', 'default', 'via', HOST_V4)
        netns('/usr/sbin/ip', '-6', 'route', 'add', 'default', 'via', HOST_V6)
        call(['/usr/sbin/nft', '-f', '-'], data=probe_ruleset().encode())
        netns('/usr/sbin/nft', '-f', '-', data=probe_ruleset().encode())

        def counters():
            host = read_counters(call(['/usr/sbin/nft', '-j', 'list', 'counters', 'table', 'inet', PROBE_TABLE]).stdout)
            upstream = read_counters(netns('/usr/sbin/nft', '-j', 'list', 'counters', 'table', 'inet', PROBE_TABLE).stdout)
            return host, upstream
        records_path = ROOT/'upstream.jsonl'
        listener = subprocess.Popen(['/usr/sbin/ip', 'netns', 'exec', NETNS, '/usr/bin/python3', '-c', LISTENER, str(records_path)],
                                    stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        time.sleep(1)
        if listener.poll() is not None:
            raise RuntimeError('upstream listener did not start')
        emit('upstream_namespace_ready', host_v4=HOST_V4, host_v6=HOST_V6)

        cli('storage', 'create', POOL, 'btrfs', 'size=8GiB')
        cli('image', 'import', args.ct_metadata, args.ct_rootfs, '--alias', 'anas-network-debian', timeout=600)
        image = json.loads(cli('image', 'list', 'anas-network-debian', '--format=json').stdout)
        if len(image) != 1 or image[0]['type'] != 'container':
            raise RuntimeError('upstream container image import is ambiguous')
        pin = image[0]['fingerprint']
        call(['/usr/bin/openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', str(ROOT/'manager.key'),
              '-out', str(ROOT/'manager.crt'), '-days', '1', '-subj', '/CN=anas-network-manager'], timeout=20)
        cli('config', 'trust', 'add-certificate', str(ROOT/'manager.crt'), '--name=anas-network-manager')
        cli('config', 'set', 'core.https_address', '127.0.0.1:8443')
        server = Path('/var/lib/incus/server.crt').read_bytes()
        encode = lambda p: base64.b64encode(p.read_bytes()).decode()
        results = {}
        for project, ipv6 in LEASES.items():
            consumer = project.replace('-', '_')
            call(['/usr/bin/openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', str(ROOT/(project+'.key')),
                  '-out', str(ROOT/(project+'.crt')), '-days', '1', '-subj', '/CN=' + project], timeout=20)
            cli('project', 'create', project, '-c', 'features.networks=false', '-c', 'features.images=true', '-c', 'features.profiles=true')
            cli('image', 'copy', pin, 'local:', '--target-project', project, timeout=600)
            provider_env = {'PATH': env['PATH'], 'INCUS_ENDPOINT': 'https://127.0.0.1:8443',
                            'INCUS_SERVER_CERT_B64': base64.b64encode(server).decode(),
                            'INCUS_ADMIN_CERT_B64': encode(ROOT/'manager.crt'), 'INCUS_ADMIN_KEY_B64': encode(ROOT/'manager.key'),
                            'ANAS_RESOURCE_CLIENT_CERT': encode(ROOT/(project+'.crt')), 'INCUS_STORAGE_POOL': POOL,
                            'INCUS_NETWORK_IPV6': 'true' if ipv6 else 'false', 'ANAS_RESOURCE_CONSUMER': consumer,
                            'ANAS_RESOURCE_SANDBOX': project, 'ANAS_RESOURCE_INSTANCE_PREFIX': 'anas-native-',
                            'ANAS_RESOURCE_IMAGE_ARCHITECTURE': 'amd64', 'ANAS_RESOURCE_MAX_INSTANCES': '1',
                            'ANAS_RESOURCE_CPU': '1', 'ANAS_RESOURCE_MEMORY_MIB': '512', 'ANAS_RESOURCE_DISK_GIB': '4',
                            'ANAS_RESOURCE_IMAGE_ALLOWLIST': pin}
            result = call([args.provider, 'ensure', '--isolation', 'container'], provider_env, check=False)
            if result.returncode:
                (report/(project+'-ensure-failure.txt')).write_bytes(result.stderr)
                raise RuntimeError('real Provider ensure failed; private diagnostic retained')
            if json.loads(result.stdout) != {'exists': True, 'ready': True, 'restricted': True, 'quota_enforced': True}:
                raise RuntimeError('Provider did not confirm complete lease')
            bridge = 'anas' + hashlib.sha256(project.encode()).hexdigest()[:10]
            network = json.loads(cli('query', '/1.0/networks/' + bridge).stdout)
            config = network['config']
            if config.get('ipv4.nat') != 'true' or config.get('ipv4.address') in (None, '', 'none'):
                raise RuntimeError('lease bridge does not masquerade IPv4')
            if ipv6 and (config.get('ipv6.nat') != 'true' or config.get('ipv6.address') in (None, '', 'none')):
                raise RuntimeError('IPv6-enabled lease bridge does not masquerade IPv6')
            if not ipv6 and config.get('ipv6.address') != 'none':
                raise RuntimeError('IPv6-disabled lease bridge must set ipv6.address=none explicitly')
            if (config.get('security.acls') != bridge or config.get('security.acls.default.egress.action') != 'drop'
                    or config.get('security.acls.default.ingress.action') != 'allow'):
                raise RuntimeError('lease bridge does not carry its source fence ACL')
            # The lease profile supplies the only NIC; the admin creates the
            # guest only because the upstream image has no fixture entrypoint.
            cli('init', pin, INSTANCE, '--project', project, '--profile', 'anas-lease', '--device', 'root,size=4GiB',
                '-c', 'limits.cpu=1', '-c', 'limits.memory=512MiB', '-c', 'security.privileged=false', timeout=300)
            cli('start', INSTANCE, '--project', project, timeout=120)
            addresses, deadline = {}, time.monotonic() + 120
            while time.monotonic() < deadline:
                state = json.loads(cli('query', '/1.0/instances/%s/state?project=%s' % (INSTANCE, project)).stdout)
                interfaces = {name: nic for name, nic in (state.get('network') or {}).items() if name != 'lo'}
                v4 = [a['address'] for nic in interfaces.values() for a in nic['addresses'] if a['family'] == 'inet']
                v6 = [a['address'] for nic in interfaces.values() for a in nic['addresses']
                      if a['family'] == 'inet6' and a['scope'] == 'global']
                if v4 and (v6 or not ipv6):
                    addresses = {'interfaces': sorted(interfaces), 'v4': v4, 'v6': v6}
                    break
                time.sleep(2)
            else:
                raise RuntimeError('guest did not obtain its lease addresses')
            if addresses['interfaces'] != ['eth0']:
                raise RuntimeError('guest has an interface besides the lease NIC')
            routes = cli('exec', INSTANCE, '--project', project, '--', 'ip', '-j', 'route', 'show', 'default').stdout
            routes6 = cli('exec', INSTANCE, '--project', project, '--', 'ip', '-j', '-6', 'route', 'show', 'default').stdout
            if any(r.get('dev') != 'eth0' for r in json.loads(routes) + json.loads(routes6)):
                raise RuntimeError('guest default route leaves through something other than the lease NIC')
            probe, attempts = {}, {}
            for family, target in ((4, UPSTREAM_V4), (6, UPSTREAM_V6)):
                token = '%s-v%d' % (project, family)
                script = 'exec 3<>/dev/tcp/%s/%d && printf "%s\\n" >&3 && read -r -t 10 reply <&3 && [ "$reply" = ok ]' % (target, PORT, token)
                # A fresh SLAAC address is briefly tentative; retry a posture
                # that should work a few times and record how many it took.
                expected = family == 4 or ipv6
                for attempts[family] in range(1, 6 if expected else 2):
                    probe[family] = cli('exec', INSTANCE, '--project', project, '--', 'timeout', '15', 'bash', '-c', script,
                                        check=False, timeout=30).returncode
                    if probe[family] == 0 or not expected:
                        break
                    time.sleep(3)
            time.sleep(1)
            records = [json.loads(line) for line in records_path.read_text().splitlines()] if records_path.exists() else []
            v4_ok = probe[4] == 0 and masquerade_verdict(records, project+'-v4', 4, HOST_V4, addresses['v4'])
            if ipv6:
                v6_ok = probe[6] == 0 and masquerade_verdict(records, project+'-v6', 6, HOST_V6, addresses['v6'])
            else:
                # No IPv6 on the bridge: the guest cannot reach upstream over
                # IPv6 at all, and nothing reached the listener.
                v6_ok = probe[6] != 0 and not addresses['v6'] and not [r for r in records if r['token'] == project+'-v6']
            if not (v4_ok and v6_ok):
                (report/(project+'-records.json')).write_text(json.dumps({'probe': probe, 'attempts': attempts, 'records': records,
                                                                          'addresses': addresses}))
                retain_diagnostics(report/(project+'-diagnostics.txt'), cli, project, bridge)
                raise RuntimeError('dual-stack egress did not match the lease posture')
            results[project] = {'ipv6_enabled': ipv6, 'guest_v4': addresses['v4'], 'guest_v6': addresses['v6'], 'probe_attempts': attempts,
                                'upstream_saw_v4': HOST_V4, 'upstream_saw_v6': HOST_V6 if ipv6 else None}
            emit('lease_egress_posture', project=project, ipv6_enabled=ipv6, masqueraded=True)

            # Forge an off-subnet source on both families. The static
            # neighbour entries hand the packet straight to the bridge, so
            # neither ARP nor NDP can be what stops it.
            mac = Path('/sys/class/net/%s/address' % bridge).read_text().strip()
            spoof4, spoof6 = SPOOF[project]
            cli('exec', INSTANCE, '--project', project, '--', 'sh', '-ec', ' && '.join((
                'ip addr add %s/32 dev eth0' % spoof4, 'ip -6 addr add %s/128 dev eth0 nodad' % spoof6,
                'ip neigh replace %s lladdr %s dev eth0 nud permanent' % (UPSTREAM_V4, mac),
                'ip -6 neigh replace %s lladdr %s dev eth0 nud permanent' % (UPSTREAM_V6, mac),
                'ip route replace %s/32 dev eth0 src %s' % (UPSTREAM_V4, spoof4),
                'ip -6 route replace %s/128 dev eth0 src %s' % (UPSTREAM_V6, spoof6))))

            def forge(families=(4, 6)):
                for family in families:
                    target = UPSTREAM_V4 if family == 4 else UPSTREAM_V6
                    cli('exec', INSTANCE, '--project', project, '--', 'timeout', '4', 'bash', '-c',
                        'exec 3<>/dev/tcp/%s/%d' % (target, PORT), check=False, timeout=30)
                time.sleep(1)
                return counters()

            host, upstream = forge()
            fence = {'host': {a: host[a] for a in (spoof4, spoof6)}, 'upstream': {a: upstream[a] for a in (spoof4, spoof6)}}
            if not spoof_verdict(host, upstream, (spoof4, spoof6), ipv6):
                (report/(project+'-spoof.json')).write_text(json.dumps(fence))
                raise RuntimeError('a forged source was not stopped by the lease source fence')
            results[project]['source_fence'] = fence
            if ipv6:
                # One deliberate drift proves the counters can see a leak and
                # that the Provider notices and repairs it.
                cli('network', 'acl', 'rule', 'add', bridge, 'egress', 'action=allow', 'state=enabled')
                inspected = json.loads(call([args.provider, 'inspect', '--isolation', 'container'], provider_env).stdout)
                _, leaked = forge((6,))
                repaired = call([args.provider, 'ensure', '--isolation', 'container'], provider_env, check=False)
                reinspected = json.loads(call([args.provider, 'inspect', '--isolation', 'container'], provider_env).stdout)
                _, after = forge((6,))
                drift = {'inspect_ready': inspected.get('ready'), 'leaked_upstream': leaked[spoof6] - upstream[spoof6],
                         'ensure_rc': repaired.returncode, 'repaired_ready': reinspected.get('ready'),
                         'after_repair_upstream': after[spoof6] - leaked[spoof6]}
                results[project]['source_fence_drift'] = drift
                if not drift_verdict(drift):
                    (report/(project+'-drift.json')).write_text(json.dumps(drift))
                    raise RuntimeError('source fence drift was not observable, detected and repaired')
            emit('lease_source_fence', project=project, ipv6_enabled=ipv6,
                 upstream_forged=sum(fence['upstream'].values()), host_forged_v6=fence['host'][spoof6])
        (report/'network.json').write_text(json.dumps(results, sort_keys=True))
        emit('dual_stack_egress', leases=len(results))
    finally:
        if listener is not None:
            listener.terminate()
            try:
                listener.wait(timeout=10)
            except subprocess.TimeoutExpired:
                listener.kill()
        for project in LEASES:
            if cli('project', 'show', project, check=False).returncode:
                continue
            for instance in json.loads(cli('list', '--project', project, '--format=json').stdout):
                if instance['name'] != INSTANCE:
                    raise RuntimeError('unexpected lab instance; refusing cleanup')
                cli('delete', INSTANCE, '--project', project, '--force', timeout=120)
            for certificate in json.loads(cli('config', 'trust', 'list', '--format=json').stdout):
                if certificate.get('projects') == [project]:
                    cli('config', 'trust', 'remove', certificate['fingerprint'])
            for item in json.loads(cli('image', 'list', '--project', project, '--format=json').stdout):
                cli('image', 'delete', item['fingerprint'], '--project', project)
            for item in json.loads(cli('profile', 'list', '--project', project, '--format=json').stdout):
                if item['name'] not in ('default', 'anas-lease'):
                    raise RuntimeError('unexpected profile; refusing cleanup')
                if item['name'] == 'anas-lease':
                    cli('profile', 'delete', item['name'], '--project', project)
            cli('project', 'delete', project)
            bridge = 'anas' + hashlib.sha256(project.encode()).hexdigest()[:10]
            if cli('network', 'show', bridge, check=False).returncode == 0:
                cli('network', 'delete', bridge)
            # The lease source fence ACL shares the bridge name; free it after the bridge.
            if cli('network', 'acl', 'show', bridge, check=False).returncode == 0:
                cli('network', 'acl', 'delete', bridge)
        for item in json.loads(cli('image', 'list', '--format=json').stdout):
            if {a.get('name') for a in item.get('aliases') or []} != {'anas-network-debian'}:
                raise RuntimeError('unexpected image; refusing cleanup')
            cli('image', 'delete', item['fingerprint'])
        if cli('storage', 'show', POOL, check=False).returncode == 0:
            cli('storage', 'delete', POOL)
        for certificate in json.loads(cli('config', 'trust', 'list', '--format=json').stdout):
            if certificate.get('name') == 'anas-network-manager':
                cli('config', 'trust', 'remove', certificate['fingerprint'])
        cli('config', 'unset', 'core.https_address')
        call(['/usr/sbin/nft', 'delete', 'table', 'inet', PROBE_TABLE], check=False)
        call(['/usr/sbin/ip', 'link', 'del', 'anasup0'], check=False)
        call(['/usr/sbin/ip', 'netns', 'del', NETNS], check=False)
        emit('owned_lab_resources_removed')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('vm-id', 'provider', 'ct-metadata', 'ct-rootfs', 'report-root'):
        parser.add_argument('--' + name, required=True)
    try:
        main(parser.parse_args())
    except Exception as exc:
        print(json.dumps({'passed': False, 'error_type': type(exc).__name__,
                          'message': str(exc) if type(exc) is RuntimeError else 'private network harness failed'}), file=sys.stderr)
        sys.exit(1)
