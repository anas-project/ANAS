//go:build linux

package incusingresshost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This exercises the real compiled kernel backend, NOT Core/lease authorization
// or a business workload. The externally supervised, fresh QEMU VM and each
// failed generation are retained by the owner. This switch exists only in the
// test binary; there is no equivalent production environment bypass.
func TestForwardingKernelNative(t *testing.T) {
	id := os.Getenv("ANAS_REQUIRE_FORWARDING_PERMISSION_NATIVE")
	if id == "" {
		t.Skip("requires exact disposable VM identity and frozen kernel test binary")
	}
	if !regexp.MustCompile(`^anas-incus-host-[a-f0-9]{6}$`).MatchString(id) || os.Getuid() != 0 || os.Geteuid() != 0 {
		t.Fatal("invalid native VM authorization")
	}
	for path, expected := range map[string]string{"/var/lib/cloud/data/instance-id": id, "/sys/class/dmi/id/sys_vendor": "QEMU"} {
		body, err := os.ReadFile(path)
		if err != nil || strings.TrimSpace(string(body)) != expected {
			t.Fatal("not the independently supervised test VM")
		}
	}
	round, err := strconv.Atoi(os.Getenv("ANAS_FORWARDING_NATIVE_ROUND"))
	if err != nil || round < 1 || round > 12 {
		t.Fatal("a new bounded test generation is required")
	}
	root := fmt.Sprintf("/opt/anas-forwarding-kernel/run-r%d", round)
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal("native generation already exists; retain it and use a new frozen round", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	log, err := os.OpenFile(filepath.Join(root, "commands.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	run := func(binary string, args ...string) ([]byte, error) {
		commandCtx, stop := context.WithTimeout(ctx, 15*time.Second)
		defer stop()
		cmd := exec.CommandContext(commandCtx, binary, args...)
		cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
		body, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			code = -1
			if cmd.ProcessState != nil {
				code = cmd.ProcessState.ExitCode()
			}
		}
		_ = json.NewEncoder(log).Encode(map[string]any{"binary": binary, "args": args, "exit": code, "output": string(body)})
		return body, err
	}
	must := func(binary string, args ...string) []byte {
		t.Helper()
		body, err := run(binary, args...)
		if err != nil {
			t.Fatalf("native setup command failed: %s %v: %s", binary, args, body)
		}
		return body
	}
	if rootdir := strings.TrimSpace(string(must("/usr/bin/docker", "info", "--format", "{{.DockerRootDir}}"))); rootdir != "/var/lib/anas-forwarding-docker" {
		t.Fatal("not the fresh default-Docker test daemon")
	}
	policy := must("/usr/sbin/xtables-nft-multi", "iptables-save", "-t", "filter")
	if !strings.Contains(string(policy), ":FORWARD DROP ") {
		t.Fatal("default Docker DROP was replaced")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(binary)
	_ = os.WriteFile(filepath.Join(root, "identity.json"), mustNativeJSON(t, map[string]any{"vm_id": id, "round": round, "binary_sha256": hex.EncodeToString(sum[:]), "acceptance_scope": "kernel_backend_only_not_guest_or_core"}), 0600)
	bridge := fmt.Sprintf("anasfwk%d", round)
	a := fmt.Sprintf("fwka%d", round)
	b := fmt.Sprintf("fwkb%d", round)
	dest := fmt.Sprintf("fwkd%d", round)
	ha, hb, out := fmt.Sprintf("fwha%d", round), fmt.Sprintf("fwhb%d", round), fmt.Sprintf("fwout%d", round)
	cidr := fmt.Sprintf("10.83.%d.1/24", round)
	gateway := fmt.Sprintf("10.83.%d.1", round)
	ipA := fmt.Sprintf("10.83.%d.8", round)
	ipB := fmt.Sprintf("10.83.%d.9", round)
	target := fmt.Sprintf("198.18.%d.2", round)
	outIP := fmt.Sprintf("198.18.%d.1", round)
	macA, macB := fmt.Sprintf("02:03:00:%02x:00:08", round), fmt.Sprintf("02:03:00:%02x:00:09", round)
	must("/usr/bin/incus", "network", "create", bridge, "ipv4.address="+cidr, "ipv4.nat=true", "ipv6.address=none")
	for _, item := range []struct{ ns, host, ip, mac string }{{a, ha, ipA, macA}, {b, hb, ipB, macB}} {
		must(trustedIPBinary, "netns", "add", item.ns)
		must(trustedIPBinary, "link", "add", item.host, "type", "veth", "peer", "name", "peer0", "netns", item.ns)
		must(trustedIPBinary, "link", "set", item.host, "master", bridge)
		must(trustedIPBinary, "link", "set", item.host, "up")
		must(trustedIPBinary, "-n", item.ns, "link", "set", "peer0", "address", item.mac)
		must(trustedIPBinary, "-n", item.ns, "address", "add", item.ip+"/24", "dev", "peer0")
		must(trustedIPBinary, "-n", item.ns, "link", "set", "peer0", "up")
		must(trustedIPBinary, "-n", item.ns, "link", "set", "lo", "up")
		must(trustedIPBinary, "-n", item.ns, "route", "add", "default", "via", gateway)
	}
	must(trustedIPBinary, "netns", "add", dest)
	must(trustedIPBinary, "link", "add", out, "type", "veth", "peer", "name", "peer0", "netns", dest)
	must(trustedIPBinary, "address", "add", outIP+"/24", "dev", out)
	must(trustedIPBinary, "link", "set", out, "up")
	must(trustedIPBinary, "-n", dest, "address", "add", target+"/24", "dev", "peer0")
	must(trustedIPBinary, "-n", dest, "link", "set", "peer0", "up")
	must(trustedIPBinary, "-n", dest, "link", "set", "lo", "up")
	must(trustedIPBinary, "-n", dest, "route", "add", "default", "via", outIP)
	serverCode := `import http.server,threading,time
class H(http.server.BaseHTTPRequestHandler):
 protocol_version='HTTP/1.1'
 def do_GET(self):
  b=b'forwarding-kernel-endpoint\n'; self.send_response(200); self.send_header('Content-Length',str(len(b))); self.end_headers(); self.wfile.write(b); self.wfile.flush()
 def log_message(self,*args):pass
for port in (8080,8081,8082):threading.Thread(target=http.server.ThreadingHTTPServer(('0.0.0.0',port),H).serve_forever,daemon=True).start()
time.sleep(3600)
`
	serverLog, err := os.OpenFile(filepath.Join(root, "endpoint.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer serverLog.Close()
	server := exec.Command(trustedIPBinary, "netns", "exec", dest, "/usr/bin/python3", "-u", "-c", serverCode)
	server.Stdout, server.Stderr = serverLog, serverLog
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(root, "endpoint.pid"), []byte(strconv.Itoa(server.Process.Pid)), 0600)
	// This independently owned VM process remains until normal VM shutdown;
	// no failure path deletes it, the namespace, or a failed experiment receipt.
	curl := func(ns string, port int) bool {
		body, err := run(trustedIPBinary, "netns", "exec", ns, "/usr/bin/curl", "--noproxy", "*", "--connect-timeout", "2", "--max-time", "3", "--fail", "--silent", fmt.Sprintf("http://%s:%d/", target, port))
		return err == nil && strings.TrimSpace(string(body)) == "forwarding-kernel-endpoint"
	}
	ready := false
	for i := 0; i < 20; i++ {
		if curl(dest, 8080) {
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatal("native endpoint did not start")
	}
	if curl(a, 8080) {
		t.Fatal("default DROP baseline was already routable")
	}
	adminComment := fmt.Sprintf("anas-native-admin-deny-r%d", round)
	must(forwardingXTables, "iptables", "-I", "FORWARD", "1", "-s", ipA, "-d", target, "-p", "tcp", "--dport", "8082", "-m", "comment", "--comment", adminComment, "-j", "DROP")
	adminBefore := must(forwardingXTables, "iptables-save", "-t", "filter")
	s, proof := forwardingKernelFixture(t)
	s.ID = forwardingHash([]string{id, strconv.Itoa(round), hex.EncodeToString(sum[:])})[:32]
	s.Network, err = ObserveForwardingBridge(ctx, bridge, cidr)
	if err != nil {
		t.Fatal(err)
	}
	s.Routes = nil
	for _, port := range []uint16{8080, 8082} {
		route, err := ObserveForwardingRoute(ctx, s.Network, target, port)
		if err != nil {
			t.Fatal(err)
		}
		s.Routes = append(s.Routes, route)
	}
	veth, err := ObserveLocalGuestVeth(ctx, ha, bridge)
	if err != nil {
		t.Fatal(err)
	}
	proof.GuestIPv4, proof.GuestMAC = ipA, macA
	proof.HostVethName, proof.HostVethMAC, proof.HostVethID, proof.PeerVethID = veth.Name, veth.MAC, veth.IfIndex, veth.PeerIfIndex
	_ = os.WriteFile(filepath.Join(root, "scope.json"), mustNativeJSON(t, s), 0600)
	grammar, err := forwardingNFTCreate(s)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(root, "nft-create.json"), grammar, 0600)
	backend, err := NewForwardingKernelBackend()
	if err != nil {
		t.Fatal(err)
	}
	events, err := os.OpenFile(filepath.Join(root, "receipts.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer events.Close()
	var saved ForwardingKernelReceipt
	save := func(saveCtx context.Context, r ForwardingKernelReceipt) error {
		if err := saveCtx.Err(); err != nil {
			return err
		}
		if err := r.Validate(); err != nil {
			return err
		}
		if err := json.NewEncoder(events).Encode(r); err != nil {
			return err
		}
		if err := events.Sync(); err != nil {
			return err
		}
		body, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return err
		}
		file, err := os.OpenFile(filepath.Join(root, "current-receipt.json"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(body)
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
		saved = r
		return nil
	}
	r, err := backend.Install(ctx, s, nil, save)
	if err != nil {
		t.Fatalf("native empty-guard installation failed (retained phase %s): %v", saved.Phase, err)
	}
	if curl(a, 8080) {
		t.Fatal("empty guard unexpectedly authorized traffic")
	}
	r, err = backend.Refresh(ctx, r, []ForwardingInstanceProof{proof}, []ForwardingKernelScope{s}, save)
	if err != nil {
		// Capture only this experiment's owned tables while TTL members still
		// exist. A later reboot/expiry cannot reconstruct this failed readback.
		for _, family := range []string{"inet", "bridge"} {
			body, captureErr := run(trustedNFTBinary, "-j", "list", "table", family, forwardingNFTName(s))
			if captureErr == nil {
				_ = os.WriteFile(filepath.Join(root, "failed-"+family+".json"), body, 0600)
			}
		}
		t.Fatal("native permission refresh failed", err)
	}
	if !curl(a, 8080) {
		t.Fatal("authorized exact native flow failed")
	}
	if curl(b, 8080) || curl(a, 8081) || curl(a, 8082) {
		t.Fatal("source/port isolation or earlier administrator deny was bypassed")
	}
	// A different physical veth may not inherit a grant by replacing its IP
	// and MAC with those of the authorized namespace.
	must(trustedIPBinary, "-n", b, "address", "del", ipB+"/24", "dev", "peer0")
	must(trustedIPBinary, "-n", b, "link", "set", "peer0", "address", macA)
	must(trustedIPBinary, "-n", b, "address", "add", ipA+"/24", "dev", "peer0")
	if curl(b, 8080) {
		t.Fatal("a substituted physical source inherited the permission")
	}
	if !curl(a, 8080) {
		t.Fatal("spoofed source disrupted the authorized return identity")
	}
	// Establish one connection before withdrawal, then reuse that exact socket
	// afterward. A failed NEW curl alone cannot prove existing-flow revocation.
	streamCode := `import socket,pathlib,sys,time
s=socket.create_connection((sys.argv[1],8080),timeout=3)
s.settimeout(3)
def request():
 s.sendall(b'GET / HTTP/1.1\r\nHost: native\r\nConnection: keep-alive\r\n\r\n')
 b=b''
 while b'forwarding-kernel-endpoint\n' not in b:
  d=s.recv(4096)
  if not d:raise EOFError()
  b+=d
  if len(b)>65536:raise ValueError()
request()
root=pathlib.Path(sys.argv[2]);(root/'stream-ready').write_text('established')
until=time.monotonic()+30
while not (root/'stream-withdrawn').exists():
 if time.monotonic()>until:sys.exit(41)
 time.sleep(.02)
try:request()
except (TimeoutError,ConnectionError,EOFError):s.close();sys.exit(0)
s.close();sys.exit(42)
`
	stream := exec.CommandContext(ctx, trustedIPBinary, "netns", "exec", a, "/usr/bin/python3", "-c", streamCode, target, root)
	stream.Stdout, stream.Stderr = serverLog, serverLog
	if err := stream.Start(); err != nil {
		t.Fatal(err)
	}
	streamWaited := false
	defer func() {
		if !streamWaited {
			_ = stream.Process.Kill()
			_ = stream.Wait()
		}
	}()
	streamReady := false
	for attempt := 0; attempt < 100; attempt++ {
		if body, e := os.ReadFile(filepath.Join(root, "stream-ready")); e == nil && string(body) == "established" {
			streamReady = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !streamReady {
		t.Fatal("persistent native connection did not become established")
	}
	r, err = backend.Close(ctx, r, save)
	if err != nil {
		t.Fatal("native connection revocation was not verified", err)
	}
	if !r.NewConnectionsClosed || !r.ConnectionsRevoked || curl(a, 8080) {
		t.Fatal("closed permission still admitted a new flow")
	}
	if err := os.WriteFile(filepath.Join(root, "stream-withdrawn"), []byte("closed"), 0600); err != nil {
		t.Fatal(err)
	}
	err = stream.Wait()
	streamWaited = true
	if err != nil {
		t.Fatal("the preexisting socket retained access, or stream proof was incomplete", err)
	}
	r, err = backend.Release(ctx, r, save)
	if err != nil {
		t.Fatal("owned forwarding retirement failed", err)
	}
	after := must(forwardingXTables, "iptables-save", "-t", "filter")
	beforeInventory, err := parseForwardingFilter(adminBefore)
	if err != nil {
		t.Fatal(err)
	}
	afterInventory, err := parseForwardingFilter(after)
	if err != nil {
		t.Fatal(err)
	}
	if forwardingForeignFilterDigest(beforeInventory, s) != forwardingForeignFilterDigest(afterInventory, s) {
		t.Fatal("foreign Docker or administrator filter changed")
	}
	if curl(a, 8080) {
		t.Fatal("default DROP was not restored after retirement")
	}
	_ = os.WriteFile(filepath.Join(root, "kernel-result.json"), mustNativeJSON(t, map[string]any{"passed": true, "vm_id": id, "round": round, "scope": "kernel_only", "new_connections_closed": r.NewConnectionsClosed, "connections_revoked": r.ConnectionsRevoked, "existing_socket_access_blocked": true, "phase": r.Phase}), 0600)
	t.Log("native compiled kernel transaction passed; no Core, guest or Forgejo acceptance is implied")
}

func mustNativeJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(body, '\n')
}
