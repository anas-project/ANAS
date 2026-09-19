package incusingresshost

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func nativeTestConfig(t *testing.T) backendConfig {
	t.Helper()
	backend, _, _ := testBackend(t)
	config := backend.config
	config.Namespace.PID = 1234
	config.Namespace.StartTimeTicks = 123456
	config.Namespace.BootID = "33333333-3333-4333-8333-333333333333"
	config.Namespace.HostVethIfIndex = 99
	config.Namespace.HostVethPeerIfIndex = config.Namespace.TraefikIfIndex
	return config
}

func nativeNetworkDocuments() (string, string, string) {
	inside := `[{"ifindex":12,"link_index":99,"ifname":"eth1","flags":["BROADCAST","MULTICAST","UP","LOWER_UP"],"mtu":1500,"operstate":"UP","link_type":"ether","address":"02:00:00:00:00:20","linkinfo":{"info_kind":"veth"},"addr_info":[{"family":"inet","local":"10.231.2.2","prefixlen":24,"scope":"global"}]}]`
	peer := `[{"ifindex":99,"link_index":12,"ifname":"vethabc","flags":["BROADCAST","MULTICAST","UP","LOWER_UP"],"master":"br-ingress","link_type":"ether","address":"02:00:00:00:00:21","linkinfo":{"info_kind":"veth"}}]`
	bridge := `[{"ifindex":5,"ifname":"br-ingress","flags":["BROADCAST","MULTICAST","UP","LOWER_UP"],"link_type":"ether","linkinfo":{"info_kind":"bridge"},"addr_info":[{"family":"inet","local":"10.231.2.1","prefixlen":24,"scope":"global"}]}]`
	return inside, peer, bridge
}

func TestNativeNetworkIdentityUsesRealIPRouteFields(t *testing.T) {
	config := nativeTestConfig(t)
	inside, peer, bridge := nativeNetworkDocuments()
	if err := verifyNativeNetworkIdentity(config, []byte(inside), []byte(peer), []byte(bridge)); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, inside, peer, bridge string }{
		{"source-drift", strings.ReplaceAll(inside, "10.231.2.2", "10.231.2.3"), peer, bridge},
		{"gateway-on-container", strings.ReplaceAll(inside, "10.231.2.2", "10.231.2.1"), peer, bridge},
		{"peer-reuse", inside, strings.ReplaceAll(peer, `"ifindex":99`, `"ifindex":100`), bridge},
		{"wrong-peer", inside, strings.ReplaceAll(peer, `"link_index":12`, `"link_index":13`), bridge},
		{"wrong-bridge", inside, strings.ReplaceAll(peer, "br-ingress", "br-foreign"), bridge},
		{"gateway-prefix", inside, peer, strings.ReplaceAll(bridge, `"prefixlen":24`, `"prefixlen":25`)},
		{"down", strings.ReplaceAll(inside, `,"UP"`, ""), peer, bridge},
		{"fake-extended-link", linkJSON(), peer, bridge},
		{"missing-addresses", strings.ReplaceAll(inside, "addr_info", "invented_addr_info"), peer, bridge},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := verifyNativeNetworkIdentity(config, []byte(test.inside), []byte(test.peer), []byte(test.bridge)); err == nil {
				t.Fatal("invalid native network identity accepted")
			}
		})
	}
}

func dockerNamespaceDocument(config backendConfig) []byte {
	return []byte(fmt.Sprintf(`{"Id":%q,"State":{"Status":"running","Running":true,"Paused":false,"Restarting":false,"Dead":false,"Pid":%d,"StartedAt":%q},"NetworkSettings":{"Networks":{"ingress":{"IPAddress":%q,"Gateway":%q,"MacAddress":%q,"NetworkID":%q,"EndpointID":%q}}},"Config":{"Labels":{"unrelated":"ignored"}}}`,
		config.Namespace.DockerContainerID, config.Namespace.PID, config.Namespace.DockerStartedAt, config.TraefikSourceIP, config.IngressGateway,
		config.Namespace.TraefikMAC, strings.Repeat("a", 64), strings.Repeat("b", 64)))
}

func TestInstalledDockerNamespaceObservation(t *testing.T) {
	config := nativeTestConfig(t)
	if err := validateInstalledNamespacePin(config.Namespace); err != nil {
		t.Fatal(err)
	}
	body := dockerNamespaceDocument(config)
	if err := verifyDockerNamespaceDocument(body, config); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ from, to string }{
		{`"Pid":1234`, `"Pid":1235`}, {`"Running":true`, `"Running":false`},
		{`"Paused":false`, `"Paused":true`}, {`"Dead":false`, `"Dead":true`},
		{`"Status":"running"`, `"Status":"restarting"`},
		{config.Namespace.DockerStartedAt, "2026-09-20T00:00:00Z"},
		{config.TraefikSourceIP, "10.231.2.9"}, {config.Namespace.TraefikMAC, "02:00:00:00:00:99"},
		{`"Running":true`, `"Running":false,"Running":true`},
	} {
		changed := strings.ReplaceAll(string(body), test.from, test.to)
		if err := verifyDockerNamespaceDocument([]byte(changed), config); err == nil {
			t.Errorf("accepted stale or ambiguous Docker observation: %s", test.to)
		}
	}
}

func TestInstalledNamespaceArgumentsNeverExposeNamespacePath(t *testing.T) {
	input := []string{"-j", "-n", "anas-traefik", "-4", "route", "show", "table", "172"}
	got, found, err := installedNamespaceArguments(input, "anas-traefik")
	want := []string{"-j", "-4", "route", "show", "table", "172"}
	if err != nil || !found || !reflect.DeepEqual(got, want) || input[1] != "-n" {
		t.Fatalf("invalid namespace argument projection: %v %v %v", got, found, err)
	}
	for _, argv := range [][]string{{"-n"}, {"-n", "/proc/1/ns/net"}, {"-n", "anas-traefik", "-n", "anas-traefik"}} {
		if _, _, err := installedNamespaceArguments(argv, "anas-traefik"); err == nil {
			t.Fatalf("accepted malformed namespace selector: %v", argv)
		}
	}
}

func TestProcessStartTicksHandlesParenthesesAndRejectsReusedPID(t *testing.T) {
	stat := "1234 (traefik (worker) ) S " + strings.Repeat("0 ", 18) + "123456 0"
	if ticks, err := processStartTicks([]byte(stat), 1234); err != nil || ticks != 123456 {
		t.Fatalf("valid process identity: ticks=%d error=%v", ticks, err)
	}
	for _, value := range []string{strings.ReplaceAll(stat, "1234 (", "1235 ("), strings.ReplaceAll(stat, ") S ", ") Z "), "1234 (broken"} {
		if _, err := processStartTicks([]byte(value), 1234); err == nil {
			t.Fatal("invalid process incarnation accepted")
		}
	}
}

func TestReceiptRejectsChangedInstalledNamespace(t *testing.T) {
	backend, target, _ := testBackend(t)
	if err := backend.HoldAddress(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	backend.config.Namespace.NetNSCookie++
	if _, err := backend.loadReceipt(target); err == nil {
		t.Fatal("old receipt authorized a replacement namespace")
	}
}

func TestLegacyReceiptIsNotSilentlyMigrated(t *testing.T) {
	backend, target, _ := testBackend(t)
	if err := backend.HoldAddress(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	r, err := backend.loadReceipt(target)
	if err != nil {
		t.Fatal(err)
	}
	r.Schema = "anas.incus-http-host-receipt/v1"
	body, _ := json.Marshal(r)
	var decoded receipt
	if err := decodeObservedJSON(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := backend.validateReceipt(decoded, target); err == nil {
		t.Fatal("legacy incomplete digest accepted as v2 ownership evidence")
	}
}
