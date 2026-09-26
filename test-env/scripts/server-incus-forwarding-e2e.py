#!/usr/bin/env python3
"""Causal forwarding test in a fresh QEMU VM with real Docker/Incus bridges.

Endpoints are local network namespaces, not booted guests or public registries.
Two temporary /32 TCP permits are a controlled experiment, NOT a production
Docker override. Default policy, unrelated traffic and original rule inventory
must remain unchanged. The outer VM owner separately verifies the physical host.
"""
import argparse
from collections import Counter
import hashlib
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import time

ROOT = Path('/opt/anas-forwarding-native')
INPUTS = Path('/opt/anas-forwarding-inputs')
REQUIRED = ('isolated_identity_and_default_drop', 'controlled_endpoint_responds',
            'early_nft_accept_does_not_override_drop', 'exact_permit_connects',
            'other_source_and_port_stay_denied', 'earlier_foreign_drop_still_wins',
            'withdrawal_restores_denial', 'production_diagnostic_matches',
            'owned_cleanup_and_original_policy_preserved')
BRIDGE, UPLINK = 'an-fw-br', 'an-fw-up'
CLIENTS = ('anas-fw-client', 'anas-fw-other')
ENDPOINT = 'anas-fw-endpoint'
CHAIN_COMMENT = 'anas-forwarding-exp-v1'
TOKEN = b'ANAS_FIXED_FORWARDING_ENDPOINT'
COMMAND_INDEX = 0


class Failure(RuntimeError):
    pass


def require(value, code):
    if not value:
        raise Failure(code)


def valid_environment(identity, facts):
    return (bool(re.fullmatch('anas-incus-host-[a-f0-9]{6}', identity)) and facts.get('uid') == 0 and
            facts.get('vm_id') == identity and facts.get('vendor') == 'QEMU' and
            facts.get('docker_root') == '/var/lib/anas-forwarding-docker' and facts.get('containers') == [])


def complete(events):
    return (len(events) == len(REQUIRED) and Counter(e.get('stage') for e in events) == Counter(REQUIRED) and
            all(e.get('status') == 'passed' for e in events))


def native_events_passed(events, exit_code):
    if type(exit_code) is not int or exit_code != 0 or not events:
        return False
    started = done = package_done = package_started = False
    for row in events:
        if not isinstance(row, dict) or row.get('Package') != 'github.com/anas-project/ANAS/internal/incusprovision' or package_done:
            return False
        action, test = row.get('Action'), row.get('Test', '')
        if test not in ('', 'TestNativeForwardingDiagnostics'):
            return False
        if test:
            if action == 'run' and not started and not done:
                started = True
            elif action == 'pass' and started and not done:
                done = True
            elif action == 'output' and started and not done:
                pass
            else:
                return False
        elif action == 'pass' and done:
            package_done = True
        elif action == 'start' and not started and not package_started:
            package_started = True
        elif action == 'output':
            pass
        else:
            return False
    return started and done and package_done


def endpoint_program():
    # HTTPServer.server_bind does a reverse-DNS lookup even for a numeric
    # address. A namespace intentionally has no resolver service: avoid that
    # unrelated dependency while keeping a real TCP/HTTP endpoint.
    return '''import http.server,os,socketserver,threading
class Handler(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  if self.path!='/probe':self.send_error(404);return
  body=b'ANAS_FIXED_FORWARDING_ENDPOINT';self.send_response(200);self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
 def log_message(self,*args):pass
class LocalEndpoint(http.server.ThreadingHTTPServer):
 def server_bind(self):
  socketserver.TCPServer.server_bind(self)
  self.server_name='fixed-local-endpoint';self.server_port=self.server_address[1]
if __name__=='__main__':
 os.setgroups([]);os.setgid(65534);os.setuid(65534)
 servers=[LocalEndpoint(('198.18.77.2',p),Handler) for p in (18080,18081)]
 threading.Thread(target=servers[1].serve_forever,daemon=True).start();servers[0].serve_forever()
'''


def forward_policy(body):
    rows = re.findall(r'^:FORWARD (ACCEPT|DROP) \[([0-9]+):[0-9]+\]$', body, re.MULTILINE)
    require(len(rows) == 1 and body.count('*filter\n') == 1 and body.count('\nCOMMIT\n') == 1, 'incomplete_forward_policy')
    return rows[0][0], int(rows[0][1])


def permit_rules():
    return [
        ['-i', BRIDGE, '-o', UPLINK, '-s', '10.231.77.2/32', '-d', '198.18.77.2/32', '-p', 'tcp', '--dport', '18080',
         '-m', 'conntrack', '--ctstate', 'NEW,ESTABLISHED', '-m', 'comment', '--comment', CHAIN_COMMENT, '-j', 'ACCEPT'],
        ['-i', UPLINK, '-o', BRIDGE, '-s', '198.18.77.2/32', '-d', '10.231.77.2/32', '-p', 'tcp', '--sport', '18080',
         '-m', 'conntrack', '--ctstate', 'ESTABLISHED', '-m', 'comment', '--comment', CHAIN_COMMENT, '-j', 'ACCEPT'],
    ]


def early_accept_rules():
    # Separate nested blocks and statements exactly as in an nft configuration
    # file. The test rule is narrow and counted; it grants no production permit.
    return b'''table inet anas_forwarding_probe {
  chain early {
    type filter hook forward priority -110; policy accept;
    iifname "an-fw-br" oifname "an-fw-up" ip saddr 10.231.77.2 ip daddr 198.18.77.2 tcp dport 18080 counter accept
  }
}
'''


def command_diagnostic(executable, error):
    if executable == '/usr/sbin/nft' and b'syntax error' in error:
        return 'nft_syntax_error'
    if b'Operation not permitted' in error or b'Permission denied' in error:
        return 'permission_denied'
    return 'command_error'


def capture(args, *, data=None, check=True, timeout=15, env=None):
    global COMMAND_INDEX
    COMMAND_INDEX += 1
    index, started = COMMAND_INDEX, time.monotonic()
    argv = [str(a) for a in args]
    def record(code, out=b'', err=b''):
        directory = ROOT/'reports'
        if not directory.is_dir(): return
        row = {'command': [part if len(part)<96 and '\n' not in part else '<fixed-program>' for part in argv[:6]],
               'index': index, 'exit': code, 'elapsed_ms': int((time.monotonic()-started)*1000)}
        if code not in (0,):
            row['diagnostic'] = command_diagnostic(argv[0], err or b'')
        fd = os.open(directory/'commands.jsonl', os.O_WRONLY|os.O_APPEND|os.O_CREAT|os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'ab') as stream: stream.write(json.dumps(row, sort_keys=True).encode()+b'\n')
        if code not in (0,) and (ROOT/'private').is_dir():
            write_new(ROOT/'private'/('command-'+str(index)+'.log'), (out or b'')[:16384]+b'\n'+(err or b'')[:16384])
    try:
        # Incus create commands may read optional YAML from stdin. Inherit an
        # open SSH supervisor pipe and they can wait forever for EOF even with
        # complete argv. Only explicit nft input receives a pipe; all other
        # commands are strictly noninteractive and receive immediate EOF.
        input_options = {'stdin': subprocess.DEVNULL} if data is None else {'input': data}
        result = subprocess.run(argv, **input_options, capture_output=True, timeout=timeout,
                                env=env or {'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'LC_ALL': 'C', 'HOME': '/root'})
    except subprocess.TimeoutExpired as error:
        record('timeout', error.stdout, error.stderr)
        raise Failure('command_timeout_'+Path(argv[0]).name) from None
    record(result.returncode, result.stdout, result.stderr)
    require(len(result.stdout)+len(result.stderr) < 2 << 20, 'command_output_bound')
    if check:
        require(result.returncode == 0, 'command_failed')
    return result


def write_new(path, body):
    if not isinstance(body, bytes):
        body = json.dumps(body, sort_keys=True, indent=2).encode()+b'\n'
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'wb') as output:
        output.write(body); output.flush(); os.fsync(output.fileno())


def fingerprint(path):
    with path.open('rb') as source:
        h = hashlib.sha256()
        for block in iter(lambda: source.read(1 << 20), b''): h.update(block)
        return h.hexdigest()


def run(identity):
    facts = {'uid': os.geteuid(), 'vendor': Path('/sys/class/dmi/id/sys_vendor').read_text().strip(),
             'vm_id': Path('/var/lib/cloud/data/instance-id').read_text().strip(),
             'docker_root': '/var/lib/anas-forwarding-docker', 'containers': []}
    require(valid_environment(identity, facts), 'vm_identity')
    info = json.loads(capture(['/usr/bin/docker', 'info', '--format', '{{json .}}']).stdout)
    facts['docker_root'] = info['DockerRootDir']
    facts['containers'] = capture(['/usr/bin/docker', 'ps', '-aq', '--no-trunc']).stdout.split()
    require(valid_environment(identity, facts), 'experimental_docker_identity')
    require(not ROOT.exists(), 'fresh_report_required')
    ROOT.mkdir(mode=0o700); reports = ROOT/'reports'; reports.mkdir(mode=0o700); (ROOT/'private').mkdir(mode=0o700)
    manifest = json.loads((INPUTS/'source-manifest.json').read_bytes())
    for name, digest in manifest['files'].items():
        path = INPUTS/name
        require(path.name == name and path.is_file() and not path.is_symlink() and
                path.stat().st_uid == 0 and path.stat().st_nlink == 1 and not path.stat().st_mode & 0o022 and
                fingerprint(path) == digest, 'source_identity')
    ip = lambda *a: capture(['/usr/sbin/ip', *a])
    nft = lambda *a, **kw: capture(['/usr/sbin/nft', *a], **kw)
    xt = lambda *a, **kw: capture(['/usr/sbin/iptables', '-w', '5', *a], **kw)
    incus = lambda *a: capture(['/usr/bin/incus', '--force-local', *a], timeout=60)
    def policy():
        return forward_policy(capture(['/usr/sbin/iptables-save', '-c', '-t', 'filter']).stdout.decode())
    def rules():
        return xt('-S').stdout
    def docker_inventory():
        return {key: sorted(capture(['/usr/bin/docker', *args]).stdout.decode().splitlines()) for key, args in
                {'containers': ['ps', '-aq', '--no-trunc'], 'networks': ['network', 'ls', '--no-trunc', '--format', '{{.ID}}'],
                 'volumes': ['volume', 'ls', '--format', '{{.Name}}'], 'images': ['images', '-aq', '--no-trunc']}.items()}
    before_docker = docker_inventory(); before_rules = rules(); before_ns = ip('netns', 'list').stdout
    require(policy()[0] == 'DROP' and Path('/proc/sys/net/ipv4/ip_forward').read_text().strip() == '1', 'default_docker_drop_required')
    require(xt('-S', 'DOCKER-USER').returncode == 0, 'docker_user_hook_missing')
    require(json.loads(incus('network', 'list', '--format=json').stdout) is not None, 'incus_inventory')
    require(not any(row.get('name') == BRIDGE for row in json.loads(incus('network', 'list', '--format=json').stdout)), 'bridge_preexists')
    for name in (*CLIENTS, ENDPOINT): require(name.encode() not in before_ns, 'namespace_preexists')
    require(nft('list', 'table', 'inet', 'anas_forwarding_probe', check=False).returncode != 0, 'probe_table_preexists')
    stages = []; phase = REQUIRED[0]; server = None; networks = []; installed = []; table_created = bridge_created = False
    def passed(stage, **evidence):
        item = {'stage': stage, 'status': 'passed', **evidence}; stages.append(item); print(json.dumps(item, sort_keys=True), flush=True)
    probe_code = '''import socket,sys,time
try:
 deadline=time.monotonic()+2
 s=socket.create_connection(('198.18.77.2',int(sys.argv[1])),2)
 s.sendall(b'GET /probe HTTP/1.0\\r\\nHost: local\\r\\n\\r\\n')
 body=b''
 while len(body)<4096:
  remaining=deadline-time.monotonic()
  if remaining<=0:sys.exit(3)
  s.settimeout(remaining)
  chunk=s.recv(4096-len(body))
  if not chunk:break
  body+=chunk
 s.close()
 sys.exit(0 if b'ANAS_FIXED_FORWARDING_ENDPOINT' in body else 2)
except OSError:sys.exit(3)
'''
    def probe(namespace=None, port=18080):
        cmd = ['/usr/bin/python3', '-c', probe_code, str(port)]
        if namespace: cmd = ['/usr/sbin/ip', 'netns', 'exec', namespace, *cmd]
        return capture(cmd, check=False, timeout=4).returncode == 0
    def snapshot(label):
        write_new(reports/(label+'.json'), json.loads(nft('-j', 'list', 'ruleset').stdout))
    try:
        snapshot('before'); passed(phase, forward_policy='DROP', ip_forward=1, docker_version=info['ServerVersion'])
        phase = REQUIRED[1]
        ip('netns', 'add', ENDPOINT); networks.append(ENDPOINT)
        ip('link', 'add', UPLINK, 'type', 'veth', 'peer', 'name', 'an-fw-peer')
        ip('link', 'set', 'an-fw-peer', 'netns', ENDPOINT)
        ip('addr', 'add', '198.18.77.1/30', 'dev', UPLINK); ip('link', 'set', UPLINK, 'up')
        ip('-n', ENDPOINT, 'link', 'set', 'lo', 'up'); ip('-n', ENDPOINT, 'addr', 'add', '198.18.77.2/30', 'dev', 'an-fw-peer')
        ip('-n', ENDPOINT, 'link', 'set', 'an-fw-peer', 'up'); ip('-n', ENDPOINT, 'route', 'add', 'default', 'via', '198.18.77.1')
        server_code = endpoint_program()
        server = subprocess.Popen(['/usr/sbin/ip', 'netns', 'exec', ENDPOINT, '/usr/bin/python3', '-c', server_code],
                                  stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for _ in range(20):
            require(server.poll() is None, 'endpoint_exited')
            if probe() and probe(port=18081): break
            time.sleep(.1)
        else: raise Failure('controlled_endpoint_failed')
        incus('network', 'create', BRIDGE, 'ipv4.address=10.231.77.1/24', 'ipv4.nat=true', 'ipv6.address=none', 'user.anas.forwarding-experiment='+identity)
        bridge_created = True
        for index, namespace in enumerate(CLIENTS):
            ip('netns', 'add', namespace); networks.append(namespace)
            host, peer = 'an-fw-v'+str(index), 'an-fw-c'+str(index)
            ip('link', 'add', host, 'type', 'veth', 'peer', 'name', peer); ip('link', 'set', peer, 'netns', namespace)
            ip('link', 'set', host, 'master', BRIDGE); ip('link', 'set', host, 'up')
            ip('-n', namespace, 'link', 'set', 'lo', 'up'); ip('-n', namespace, 'addr', 'add', '10.231.77.'+str(index+2)+'/24', 'dev', peer)
            ip('-n', namespace, 'link', 'set', peer, 'up'); ip('-n', namespace, 'route', 'add', 'default', 'via', '10.231.77.1')
        require(policy()[0] == 'DROP', 'incus_changed_default_policy')
        passed(phase, local_only=True, namespace_clients=True, incus_bridge=True)
        phase = REQUIRED[2]
        nft('--check', '-f', '-', data=early_accept_rules())
        nft('-f', '-', data=early_accept_rules())
        table_created = True
        count_before = policy()[1]
        require(not probe(CLIENTS[0]) and policy()[1] > count_before, 'expected_drop_not_observed')
        table = json.loads(nft('-j', 'list', 'table', 'inet', 'anas_forwarding_probe').stdout)
        packets = [expr['counter']['packets'] for row in table['nftables'] if 'rule' in row for expr in row['rule']['expr'] if 'counter' in expr]
        require(len(packets) == 1 and packets[0] > 0, 'earlier_accept_not_hit')
        passed(phase, early_accept_packets=packets[0], later_default_drop_increased=True)
        phase = REQUIRED[3]
        for row in permit_rules(): xt('-I', 'DOCKER-USER', '1', *row); installed.append(row)
        require(probe(CLIENTS[0]) and policy()[0] == 'DROP', 'exact_permit_failed')
        passed(phase, global_policy_unchanged=True, experimental_tuple_only=True)
        phase = REQUIRED[4]
        require(not probe(CLIENTS[1]) and not probe(CLIENTS[0], 18081) and probe(port=18081), 'unrelated_flow_admitted')
        passed(phase)
        phase = REQUIRED[5]
        foreign = [*permit_rules()[0][:-2], '-j', 'DROP']
        xt('-I', 'DOCKER-USER', '1', *foreign); installed.append(foreign)
        require(not probe(CLIENTS[0]), 'earlier_explicit_drop_bypassed')
        xt('-D', 'DOCKER-USER', *foreign); installed.pop()
        require(probe(CLIENTS[0]), 'permit_not_restored_after_experiment_drop')
        passed(phase)
        phase = REQUIRED[6]
        for row in reversed(installed): xt('-D', 'DOCKER-USER', *row)
        installed.clear(); count_before = policy()[1]
        require(not probe(CLIENTS[0]) and policy()[1] > count_before, 'withdrawal_failed')
        passed(phase)
        phase = REQUIRED[7]; snapshot('observed')
        test = capture([INPUTS/'test2json', '-t', '-p', 'github.com/anas-project/ANAS/internal/incusprovision', INPUTS/'incusprovision.test',
                        '-test.v=test2json', '-test.run=^TestNativeForwardingDiagnostics$', '-test.count=1', '-test.timeout=20s'], check=False, timeout=25,
                       env={'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'LC_ALL': 'C', 'ANAS_REQUIRE_FORWARDING_NATIVE': identity})
        write_new(reports/'diagnostic-native.jsonl', test.stdout)
        events = [json.loads(row) for row in test.stdout.splitlines()]
        require(native_events_passed(events, test.returncode), 'production_diagnostic_gate')
        passed(phase)
    except Exception as error:
        code = str(error) if isinstance(error, Failure) else type(error).__name__
        stages.append({'stage': phase, 'status': 'failed', 'code': code})
    finally:
        cleanup_error = None
        try:
            for row in reversed(installed): xt('-D', 'DOCKER-USER', *row)
            if table_created: nft('delete', 'table', 'inet', 'anas_forwarding_probe')
            if server is not None:
                server.terminate(); server.wait(timeout=5)
            for namespace in reversed(networks): ip('netns', 'delete', namespace)
            if bridge_created:
                config = json.loads(incus('query', '/1.0/networks/'+BRIDGE).stdout)
                require(config['config'].get('user.anas.forwarding-experiment') == identity, 'bridge_owner_changed')
                incus('network', 'delete', BRIDGE)
            require(policy()[0] == 'DROP' and rules() == before_rules and ip('netns', 'list').stdout == before_ns and
                    docker_inventory() == before_docker, 'baseline_not_restored')
            passed(REQUIRED[8], exact_rules_restored=True, docker_inventory_unchanged=True)
        except Exception as error:
            cleanup_error = str(error) if isinstance(error, Failure) else type(error).__name__
            stages.append({'stage': REQUIRED[8], 'status': 'failed', 'code': cleanup_error})
    result = {'schema': 'anas.forwarding-causality-native/v1', 'vm_id': identity, 'events': stages,
              'passed': complete(stages), 'cleanup_error': cleanup_error,
              'scope': 'local namespace endpoints over actual Incus bridge and default Docker filter; not production grants or booted guest workload'}
    write_new(reports/'summary.json', result); print(json.dumps(result, sort_keys=True), flush=True)
    return 0 if result['passed'] else 1


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__); parser.add_argument('--vm-id', required=True)
    args = parser.parse_args(); os.umask(0o077)
    raise SystemExit(run(args.vm_id))
