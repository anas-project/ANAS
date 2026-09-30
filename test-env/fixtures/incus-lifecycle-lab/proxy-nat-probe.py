#!/usr/bin/env python3
"""Disposable lab VM only: which traffic does an Incus proxy device in NAT mode capture?

Compares three ways of exposing guest port PORT on the lab VM (which plays the
Incus host):

* wildcard  -- proxy device, listen=tcp:0.0.0.0:PORT, nat=true
* specific  -- proxy device, listen=tcp:OUT_HOST:PORT, nat=true
* dockerlike -- our own nft DNAT that, like Docker, only matches packets whose
                destination is a local address (fib daddr type local)

Three listeners answer with a tag: INSTANCE (inside the container's network
namespace), HOST (the lab VM itself) and OUTSIDE (a separate namespace that
stands in for another machine). Probes come from the lab VM itself, from a
client namespace whose traffic is only routed through the lab VM, and from the
outside namespace. A reply tagged INSTANCE to a connection that was addressed
to OUTSIDE means the rule captured traffic that was not addressed to the host.
"""
import json
import os
from pathlib import Path
import subprocess
import sys
import time

IDENTITY = sys.argv[1]
INPUTS = '/opt/anas-lifecycle-inputs'
PORT = 18443
NET, GW, INST_IP = 'probebr0', '10.123.0.1', '10.123.0.10'
OUT_HOST, OUT_NS = '198.51.100.1', '198.51.100.2'
CLI_HOST, CLI_NS = '203.0.113.1', '203.0.113.2'
NFT_TABLE = 'anas_probe_dockerlike'

LISTENER = r'''
import socket, sys
tag, port = sys.argv[1], int(sys.argv[2])
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("0.0.0.0", port)); s.listen(16)
while True:
    c, peer = s.accept()
    try:
        c.settimeout(3); c.recv(64); c.sendall(("%s peer=%s\n" % (tag, peer[0])).encode())
    except Exception:
        pass
    finally:
        c.close()
'''

PROBE = r'''
import socket, sys
try:
    s = socket.create_connection((sys.argv[1], int(sys.argv[2])), timeout=3)
    s.sendall(b"probe\n"); s.settimeout(3)
    print(s.recv(128).decode().strip() or "EMPTY")
except Exception as e:
    print("ERROR " + type(e).__name__)
'''

CASES = [
    ('A_vm_to_other_machine', 'vm', OUT_NS, 'the host itself connects to another machine'),
    ('B_routed_client_to_other_machine', 'probe-cli', OUT_NS, 'traffic only routed through the host'),
    ('C_external_to_host_addr', 'probe-out', OUT_HOST, 'external client to a host address (the wanted case)'),
    ('D_vm_to_own_addr', 'vm', OUT_HOST, 'the host to its own address'),
    ('E_client_to_second_host_addr', 'probe-cli', CLI_HOST, 'client to another host address'),
]


def require_vm():
    if (os.geteuid() != 0 or Path('/var/lib/cloud/data/instance-id').read_text().strip() != IDENTITY
            or Path('/sys/class/dmi/id/sys_vendor').read_text().strip() != 'QEMU'
            or Path('/var/run/docker.sock').exists() or Path('/var/lib/docker').exists()):
        raise SystemExit('exact disposable QEMU lab VM without Docker is required')


def sh(*args, check=True, timeout=120):
    result = subprocess.run(args, capture_output=True, text=True, timeout=timeout, stdin=subprocess.DEVNULL)
    if check and result.returncode != 0:
        raise RuntimeError('%s exited %d: %s' % (' '.join(args[:6]), result.returncode, result.stderr.strip()[:500]))
    return result.stdout


def background(*args):
    return subprocess.Popen(args, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def probe_all():
    out = {}
    for name, where, target, _ in CASES:
        argv = ['python3', '-c', PROBE, target, str(PORT)]
        if where != 'vm':
            argv = ['ip', 'netns', 'exec', where] + argv
        reply = sh(*argv, check=False, timeout=20).strip()
        out[name] = reply.split(' peer=')[0] if reply else 'NO_OUTPUT'
    return out


def rules_for_port():
    text = sh('nft', 'list', 'ruleset')
    return [line.strip() for line in text.splitlines() if str(PORT) in line]


def main():
    require_vm()
    report = {'incus': sh('incus', '--version').strip(), 'kernel': os.uname().release,
              'nft': sh('nft', '--version').strip(), 'cases': {n: d for n, _, _, d in CASES}}

    # Re-entrant: a previous attempt may already have built the environment.
    fresh = subprocess.run(['incus', 'info', 'c1'], capture_output=True).returncode != 0
    if fresh:
        # Incus: one dir pool, one managed bridge, one container with a static address.
        sh('incus', 'storage', 'create', 'probe', 'dir')
        sh('incus', 'network', 'create', NET, 'ipv4.address=%s/24' % GW, 'ipv4.nat=true', 'ipv6.address=none')
        sh('incus', 'profile', 'device', 'add', 'default', 'root', 'disk', 'pool=probe', 'path=/')
        sh('incus', 'profile', 'device', 'add', 'default', 'eth0', 'nic', 'network=' + NET, 'name=eth0')
        sh('incus', 'image', 'import', INPUTS + '/ct-incus.tar.xz', INPUTS + '/ct-root.tar.xz', '--alias', 'probe-ct', timeout=900)
        sh('incus', 'init', 'probe-ct', 'c1', '--device', 'eth0,ipv4.address=' + INST_IP)
        sh('incus', 'start', 'c1')
    subprocess.run(['incus', 'config', 'device', 'remove', 'c1', 'probe'], capture_output=True)
    subprocess.run(['nft', 'delete', 'table', 'ip', NFT_TABLE], capture_output=True)
    subprocess.run(['pkill', '-f', 'tag, port = sys.argv'], capture_output=True)
    time.sleep(1)
    deadline = time.monotonic() + 120
    while True:
        state = json.loads(sh('incus', 'query', '/1.0/instances/c1/state'))
        addrs = [a['address'] for a in ((state.get('network') or {}).get('eth0') or {}).get('addresses', [])]
        if INST_IP in addrs:
            break
        if time.monotonic() > deadline:
            raise RuntimeError('container never got its static address: %s' % addrs)
        time.sleep(2)
    pid = str(state['pid'])

    # Listeners: inside the container's netns (VM's python3 via nsenter), on the lab VM, and "outside".
    background('nsenter', '-t', pid, '-n', 'python3', '-c', LISTENER, 'INSTANCE', str(PORT))
    background('python3', '-c', LISTENER, 'HOST', str(PORT))
    sh('sysctl', '-q', '-w', 'net.ipv4.ip_forward=1')
    for ns, host_if, ns_if, host_ip, ns_ip in (('probe-out', 'vo-host', 'vo-ns', OUT_HOST, OUT_NS),
                                               ('probe-cli', 'vc-host', 'vc-ns', CLI_HOST, CLI_NS)):
        if ns in sh('ip', 'netns', 'list'):
            continue
        sh('ip', 'netns', 'add', ns)
        sh('ip', 'link', 'add', host_if, 'type', 'veth', 'peer', 'name', ns_if)
        sh('ip', 'link', 'set', ns_if, 'netns', ns)
        sh('ip', 'addr', 'add', host_ip + '/24', 'dev', host_if)
        sh('ip', 'link', 'set', host_if, 'up')
        sh('ip', 'netns', 'exec', ns, 'ip', 'addr', 'add', ns_ip + '/24', 'dev', ns_if)
        sh('ip', 'netns', 'exec', ns, 'ip', 'link', 'set', ns_if, 'up')
        sh('ip', 'netns', 'exec', ns, 'ip', 'link', 'set', 'lo', 'up')
    sh('ip', 'netns', 'exec', 'probe-cli', 'ip', 'route', 'replace', 'default', 'via', CLI_HOST)
    sh('ip', 'netns', 'exec', 'probe-out', 'ip', 'route', 'replace', '203.0.113.0/24', 'via', OUT_HOST)
    background('ip', 'netns', 'exec', 'probe-out', 'python3', '-c', LISTENER, 'OUTSIDE', str(PORT))
    time.sleep(2)

    scenarios = {}
    scenarios['no_rule'] = {'probes': probe_all()}

    for name, listen in (('proxy_nat_wildcard', '0.0.0.0'), ('proxy_nat_specific', OUT_HOST)):
        sh('incus', 'config', 'device', 'add', 'c1', 'probe', 'proxy', 'listen=tcp:%s:%d' % (listen, PORT),
           'connect=tcp:%s:%d' % (INST_IP, PORT), 'nat=true')
        time.sleep(3)
        scenarios[name] = {'rules': rules_for_port(), 'probes': probe_all()}
        sh('incus', 'config', 'device', 'remove', 'c1', 'probe')
        time.sleep(2)

    nft = ('table ip %s {\n'
           '  chain pre {\n'
           '    type nat hook prerouting priority dstnat; policy accept;\n'
           '    fib daddr type local tcp dport %d dnat to %s:%d\n'
           '  }\n'
           '  chain out {\n'
           '    type nat hook output priority -100; policy accept;\n'
           '    ip daddr != 127.0.0.0/8 fib daddr type local tcp dport %d dnat to %s:%d\n'
           '  }\n'
           '}\n') % (NFT_TABLE, PORT, INST_IP, PORT, PORT, INST_IP, PORT)
    subprocess.run(['nft', '-f', '-'], input=nft, text=True, check=True, timeout=30)
    time.sleep(1)
    scenarios['dockerlike_fib_local'] = {'rules': rules_for_port(), 'probes': probe_all()}
    sh('nft', 'delete', 'table', 'ip', NFT_TABLE)

    report['scenarios'] = scenarios
    print(json.dumps(report, indent=1, sort_keys=True))


if __name__ == '__main__':
    main()
