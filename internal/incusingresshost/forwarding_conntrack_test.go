package incusingresshost

import (
	"strings"
	"testing"
)

func TestForwardingConntrackPinsBothNATDirectionsWithoutWideningHTTP(t *testing.T) {
	s, instance := forwardingKernelFixture(t)
	route := s.Routes[0]
	line := "ipv4 2 tcp 6 120 ESTABLISHED src=10.83.0.8 dst=192.0.2.12 sport=45001 dport=8080 src=192.0.2.12 dst=192.0.2.2 sport=8080 dport=51001 [ASSURED] mark=0 zone=0 use=1"
	if _, err := parseConntrackLine(line); err == nil {
		t.Fatal("new forwarding support widened routed HTTP ownership")
	}
	entries, err := parseForwardingConntrack([]byte(line), s, instance, route)
	if err != nil || len(entries) != 1 || entries[0].ReplyDstPort != 51001 {
		t.Fatal(entries, err)
	}
	argv := strings.Join(conntrackDeleteEntryArgv(entries[0]), " ")
	for _, required := range []string{"--orig-src 10.83.0.8", "--orig-dst 192.0.2.12", "--sport 45001", "--reply-dst 192.0.2.2", "--reply-port-dst 51001", "--zone 0"} {
		if !strings.Contains(argv, required) {
			t.Fatal("NAT deletion lost observed identity", argv)
		}
	}
	for _, bad := range []string{
		strings.Replace(line, "src=10.83.0.8", "src=10.83.0.9", 1), strings.Replace(line, "dst=192.0.2.2 ", "dst=192.0.2.3 ", 1),
		strings.Replace(line, "sport=8080", "sport=9090", 1), strings.Replace(line, "zone=0", "zone=1", 1), line + "\n" + line,
	} {
		if _, err := parseForwardingConntrack([]byte(bad), s, instance, route); err == nil {
			t.Fatal("foreign NAT tuple accepted")
		}
	}
}
