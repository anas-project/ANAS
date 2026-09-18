#!/usr/bin/env python3
"""Real Linux packet test for generated lab rules; not Incus/Docker acceptance.

Run with sudo on an explicitly authorized test target. The outer process only
reads host network state and starts a fresh network namespace. All links,
addresses, rules, servers and probes live in that namespace and its children.
No Docker or Incus API, host firewall mutation, or named /run/netns entry is used.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import select
import signal
import subprocess
import sys
import time


def command(*args, **kwargs):
    return subprocess.run(args, check=True, text=True, capture_output=True,
                          timeout=kwargs.pop("timeout", 10), **kwargs).stdout.strip()


def namespace():
    return os.stat("/proc/self/ns/net").st_ino


def host_digest():
    # Stateless output omits live counters that other services may update.
    return hashlib.sha256(command("nft", "--stateless", "list", "ruleset").encode()).hexdigest()


def outer(artifacts):
    before = host_digest()
    original = namespace()
    result = subprocess.run([
        "unshare", "--net", "--fork", "--kill-child=SIGKILL", sys.executable,
        str(Path(__file__).resolve()), "--artifacts", str(artifacts),
        "--inside", str(original),
    ], timeout=100, check=False)
    after = host_digest()
    intact = namespace() == original and before == after
    print(json.dumps({"check": "host_rules_unchanged", "passed": intact}), flush=True)
    if not intact:
        raise RuntimeError("host rules changed during the run; inspect concurrent activity")
    if result.returncode:
        raise RuntimeError("isolated namespace checks failed")


SERVER = r'''
import http.server, socket, threading, time, signal
signal.alarm(90)
class Handler(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  self.send_response(200); self.end_headers()
  try:
   if self.path == '/stream':
    while True:
     self.wfile.write(b'x'*1024); self.wfile.flush(); time.sleep(.05)
   else: self.wfile.write(b'anas-netns-http')
  except (BrokenPipeError, ConnectionResetError): pass
 def log_message(self, *args): pass
class V6(http.server.ThreadingHTTPServer):
 address_family = socket.AF_INET6
 def server_bind(self):
  self.socket.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 1)
  super().server_bind()
servers = [http.server.ThreadingHTTPServer(('0.0.0.0', p), Handler) for p in (7000,7001)]
servers.append(V6(('::',7000),Handler))
for server in servers: threading.Thread(target=server.serve_forever,daemon=True).start()
time.sleep(85)
'''
PROBE = r'''
import socket,sys
try:
 with socket.create_connection((sys.argv[1],int(sys.argv[2])),timeout=1) as s:
  s.sendall(b'GET / HTTP/1.0\r\nHost: incus-lab.example.test\r\n\r\n')
  data=b''
  while True:
   chunk=s.recv(4096)
   if not chunk: break
   data+=chunk
  sys.exit(0 if b'anas-netns-http' in data else 2)
except OSError: sys.exit(1)
'''
STREAM = r'''
import socket,time,sys
with socket.create_connection(('10.231.1.2',7000),timeout=3) as s:
 s.sendall(b'GET /stream HTTP/1.0\r\nHost: incus-lab.example.test\r\n\r\n')
 first=s.recv(4096)
 if not first: sys.exit(2)
 print('ready',flush=True)
 total=0
 try:
  while True:
   data=s.recv(4096)
   if not data: sys.exit(3)
   total+=len(data)
 except socket.timeout:
  sys.exit(0 if total>=4096 else 4)
'''


def inside(artifacts, parent_namespace):
    links = json.loads(command("ip", "-j", "link", "show"))
    if namespace() == parent_namespace or {x["ifname"] for x in links} != {"lo"}:
        raise RuntimeError("requires a newly isolated network namespace containing only lo")
    if command("nft", "list", "tables") or command("ip", "-4", "route", "show"):
        raise RuntimeError("refusing a namespace with pre-existing rules or routes")
    ops = json.loads((artifacts / "operations.json").read_text())
    expected_route = ["ip", "-4", "route", "replace", "10.231.1.2/32", "via", "10.231.2.1", "dev", "eth1", "src", "10.231.2.2"]
    expected_revoke = ["nft", "delete", "element", "inet", "anas_incus_lab", "http_backend", "{", "10.231.1.2 . 7000", "}"]
    if ops["AddRoute"] != expected_route or ops["Revoke"] != expected_revoke:
        raise RuntimeError("use artifacts generated from the repository's synthetic observation fixture")
    processes = []
    checks = []

    def passed(name, condition):
        print(json.dumps({"check": name, "passed": condition}), flush=True)
        if not condition:
            raise RuntimeError(name + " failed")
        checks.append(name)

    def spawn(*args):
        process = subprocess.Popen(args, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
        processes.append(process)
        return process

    def peer(host_if, peer_if, bridge, address, v6, mac):
        holder = spawn("unshare", "--net", "sleep", "90")
        for _ in range(100):
            if os.stat(f"/proc/{holder.pid}/ns/net").st_ino != namespace():
                break
            time.sleep(.01)
        else:
            raise RuntimeError("child namespace not ready")
        command("ip", "link", "add", host_if, "type", "veth", "peer", "name", "p"+host_if)
        command("ip", "link", "set", "p"+host_if, "netns", str(holder.pid))
        command("ip", "link", "set", host_if, "master", bridge)
        command("ip", "link", "set", host_if, "up")
        prefix = ("nsenter", "-t", str(holder.pid), "-n")
        command(*prefix, "ip", "link", "set", "lo", "up")
        command(*prefix, "ip", "link", "set", "p"+host_if, "name", peer_if)
        command(*prefix, "ip", "link", "set", peer_if, "address", mac)
        command(*prefix, "ip", "addr", "add", address, "dev", peer_if)
        command(*prefix, "ip", "-6", "addr", "add", v6, "dev", peer_if, "nodad")
        command(*prefix, "ip", "link", "set", peer_if, "up")
        gateway = address.rsplit(".",1)[0]+".1"
        command(*prefix, "ip", "route", "add", "default", "via", gateway)
        v6gateway = v6.split("::")[0]+"::1"
        command(*prefix, "ip", "-6", "route", "add", "default", "via", v6gateway)
        return prefix

    def probe(prefix, ip="10.231.1.2", port=7000):
        result = subprocess.run([*prefix, sys.executable, "-c", PROBE, ip, str(port)], timeout=4)
        if result.returncode not in (0,1):
            raise RuntimeError("HTTP probe failed independently of connectivity")
        return result.returncode == 0

    def terminate(_signum, _frame):
        raise RuntimeError("namespace test interrupted")

    for sig in (signal.SIGTERM, signal.SIGHUP, signal.SIGINT):
        signal.signal(sig, terminate)
    try:
        command("ip", "link", "set", "lo", "up")
        command("sysctl", "-qw", "net.ipv4.ip_forward=1")
        command("sysctl", "-qw", "net.ipv6.conf.all.forwarding=1")
        for bridge, subnet in (("br-lab",2),("incuslab0",1),("otherlab",3)):
            command("ip", "link", "add", bridge, "type", "bridge")
            command("ip", "addr", "add", f"10.231.{subnet}.1/24", "dev", bridge)
            command("ip", "-6", "addr", "add", f"fd00:231:{subnet}::1/64", "dev", bridge, "nodad")
            command("ip", "link", "set", bridge, "up")
        source = peer("vethlab","eth1","br-lab","10.231.2.2/24","fd00:231:2::2/64","00:16:3e:02:02:02")
        guest = peer("vethguest","eth0","incuslab0","10.231.1.2/24","fd00:231:1::2/64","00:16:3e:01:02:03")
        attacker = peer("vethbad","eth1","br-lab","10.231.2.3/24","fd00:231:2::3/64","00:16:3e:02:02:03")
        outsider = peer("vethother","eth0","otherlab","10.231.3.2/24","fd00:231:3::2/64","00:16:3e:03:02:02")
        spawn(*guest, sys.executable, "-c", SERVER)
        spawn(*source, sys.executable, "-c", SERVER)
        time.sleep(.3)
        # Positive controls establish that each later denial is meaningful.
        for label,prefix,ip,port in (("source",source,"10.231.1.2",7000),("port",source,"10.231.1.2",7001),("peer",attacker,"10.231.1.2",7000),("outside",outsider,"10.231.1.2",7000),("ipv6",source,"fd00:231:1::2",7000),("reverse",guest,"10.231.2.2",7000)):
            passed("baseline_"+label, probe(prefix,ip,port))
        command("nft","--check","--file",str(artifacts/"firewall.nft"))
        command("nft","--file",str(artifacts/"firewall.nft"))
        command(*source,*expected_route)
        route = json.loads(command(*source,"ip","-j","route","get","10.231.1.2"))[0]
        passed("route_source_interface",route.get("dev")=="eth1" and route.get("prefsrc")=="10.231.2.2")
        passed("approved_http",probe(source))
        passed("unapproved_port_denied",not probe(source,port=7001))
        passed("same_bridge_peer_denied",not probe(attacker))
        passed("other_bridge_source_denied",not probe(outsider))
        passed("ipv6_backend_denied",not probe(source,"fd00:231:1::2"))
        passed("guest_reverse_denied",not probe(guest,"10.231.2.2"))
        # Pin neighbor discovery so the spoof probe tests IP packets as well
        # as the source guard's denial of the attacker's ARP packets.
        gateway_mac = json.loads(command("ip","-j","link","show","br-lab"))[0]["address"]
        command(*source,"ip","link","set","eth1","down")
        command(*attacker,"ip","addr","replace","10.231.2.2/24","dev","eth1")
        command(*attacker,"ip","addr","del","10.231.2.3/24","dev","eth1")
        command(*attacker,"ip","link","set","eth1","address","00:16:3e:02:02:02")
        command(*attacker,"ip","neigh","replace","10.231.2.1","lladdr",gateway_mac,"nud","permanent","dev","eth1")
        passed("source_ip_mac_spoof_denied",not probe(attacker))
        command(*attacker,"ip","link","set","eth1","down")
        command(*source,"ip","link","set","eth1","up")
        # Link-down removes per-interface routes on some kernels. Restore the
        # generated route and clear neighbor entries affected by the spoof.
        command(*source,*expected_route)
        command(*source,"ip","neigh","flush","dev","eth1")
        command("ip","neigh","flush","dev","br-lab")
        recovered = probe(source)
        if not recovered:
            print(json.dumps({"diagnostic": "after_spoof", "source_routes":command(*source,"ip","-j","route","show"), "source_neighbors":command(*source,"ip","-j","neigh","show"), "host_neighbors":command("ip","-j","neigh","show"), "permit":command("nft","list","set","inet","anas_incus_lab","http_backend")}),flush=True)
        passed("approved_source_recovers",recovered)
        stream = spawn(*source,sys.executable,"-u","-c",STREAM)
        ready,_,_ = select.select([stream.stdout],[],[],4)
        passed("established_stream_started",bool(ready) and stream.stdout.readline().strip()=="ready")
        time.sleep(.4)
        command(*expected_revoke)
        passed("established_stream_stops_after_revoke",stream.wait(timeout=6)==0)
        passed("new_http_denied_after_revoke",not probe(source))
        command("nft","add","element","inet","anas_incus_lab","http_backend","{","10.231.1.2 . 7000 timeout 30s","}")
        passed("fresh_fixture_permit",probe(source))
        print(json.dumps({"phase":"wait_for_30_second_expiry"}),flush=True)
        time.sleep(31)
        passed("expired_permit_denied",not probe(source))
        print(json.dumps({"passed":True,"checks":checks,"scope":"Linux namespaces with synthetic HTTP peers only","not_run":["Incus container/VM identity and lifecycle","Traefik renderer/public ingress","Docker/Incus firewall ordering","conntrack deletion","managed IP reassignment"]}),flush=True)
    finally:
        for process in reversed(processes):
            if process.poll() is None:
                process.kill()
            process.wait(timeout=5)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--artifacts",required=True,type=Path)
    parser.add_argument("--inside",type=int)
    args = parser.parse_args()
    if os.geteuid()!=0:
        parser.error("run with sudo on the explicitly authorized test target")
    if args.inside is None:
        outer(args.artifacts.resolve())
    else:
        inside(args.artifacts.resolve(),args.inside)


if __name__ == "__main__":
    main()
