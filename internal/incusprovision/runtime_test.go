package incusprovision

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/incushost"
)

func TestDecodeIncusEnvelopeRequiresIncus404Envelope(t *testing.T) {
	if err := decodeIncusEnvelope(404, []byte(`not an incus envelope`), nil); errors.Is(err, errIncusNotFound) {
		t.Fatal("plain transport 404 was treated as verified object absence")
	}
	body, err := json.Marshal(incusEnvelope{Type: "error", ErrorCode: 404, ErrorText: "not found"})
	if err != nil {
		t.Fatal(err)
	}
	if err := decodeIncusEnvelope(404, body, nil); !errors.Is(err, errIncusNotFound) {
		t.Fatalf("Incus 404 envelope was not treated as absence: %v", err)
	}
}

func TestDockerNetworkOwnershipRequiresPersistentOwnerID(t *testing.T) {
	network := dockerNetwork{
		Name: ControlNetworkName, Driver: "bridge", Internal: true,
		Labels:  map[string]string{"dev.anas.owner": "incus-host-provision", "dev.anas.owner_id": "owner-1"},
		Options: map[string]string{"com.docker.network.bridge.name": "br-anas-ctrl", "com.docker.network.bridge.enable_icc": "false", "com.docker.network.bridge.enable_ip_masquerade": "false"},
	}
	if !network.ownedBy("owner-1") {
		t.Fatal("owned Docker network was not recognized")
	}
	for _, owner := range []string{"", "owner-2"} {
		if network.ownedBy(owner) {
			t.Fatalf("foreign owner %q was accepted", owner)
		}
	}
	for _, mode := range []string{"external", "ipv6", "config-only", "icc", "masquerade"} {
		copy := network
		copy.Options = map[string]string{}
		for key, value := range network.Options {
			copy.Options[key] = value
		}
		switch mode {
		case "external":
			copy.Internal = false
		case "ipv6":
			copy.EnableIPv6 = true
		case "config-only":
			copy.ConfigOnly = true
		case "icc":
			copy.Options["com.docker.network.bridge.enable_icc"] = "true"
		case "masquerade":
			copy.Options["com.docker.network.bridge.enable_ip_masquerade"] = "true"
		}
		if copy.ownedBy("owner-1") {
			t.Fatalf("unsafe %s network accepted solely on its ownership label", mode)
		}
	}
}

func TestManagedGuestDetectionUsesOwnedResourcesNotNamePrefix(t *testing.T) {
	ownership := Ownership{StoragePool: StoragePoolName, DockerNetwork: ControlNetworkName, ControlBridge: "br-anas-ctrl"}
	foreignPrefixOwnedDisk := incusInstance{Name: "operator-vm", Status: "Running", ExpandedDevices: map[string]map[string]string{"root": {"pool": StoragePoolName}}}
	prefixForeignDisk := incusInstance{Name: "anas-foreign", Status: "Running", ExpandedDevices: map[string]map[string]string{"root": {"pool": "other"}}}
	ownedNetwork := incusInstance{Name: "manual", Status: "Running", Devices: map[string]map[string]string{"eth0": {"parent": "br-anas-ctrl"}}}
	if !instanceUsesOwnedResource(foreignPrefixOwnedDisk, ownership) || !instanceUsesOwnedResource(ownedNetwork, ownership) {
		t.Fatal("running guest using owned resources was missed")
	}
	if instanceUsesOwnedResource(prefixForeignDisk, ownership) {
		t.Fatal("name prefix alone was treated as ownership")
	}
}

func TestNFTControlRulesScopeManagedListener(t *testing.T) {
	rules := nftControlRules(ControlNetworkPlan{OwnershipID: "owner-1", Bridge: "br-anas-ctrl", Subnet: "10.77.0.0/24", Gateway: "10.77.0.1"})
	for _, forbidden := range []string{`iifname !=`, `0.0.0.0`, `[::]`} {
		if strings.Contains(rules, forbidden) {
			t.Fatalf("overbroad nft rule fragment present: %s\n%s", forbidden, rules)
		}
	}
	for _, required := range []string{`comment "anas-owner=owner-1"`, `ip daddr 10.77.0.1 tcp dport 18443 accept`, `ip daddr 10.77.0.1 tcp dport 18443 drop`, `iifname "br-anas-ctrl" drop comment "anas-control-host-default-deny"`, `anas-control-forward-iif-deny`, `anas-control-forward-oif-deny`} {
		if !strings.Contains(rules, required) {
			t.Fatalf("required nft rule fragment missing: %s\n%s", required, rules)
		}
	}
}

func TestValidateNFTControlRulesJSONRequiresExpressions(t *testing.T) {
	plan := ControlNetworkPlan{OwnershipID: "owner-1", Bridge: "br-anas-ctrl", Subnet: "10.77.0.0/24", Gateway: "10.77.0.1"}
	valid := controlRulesJSONFixture
	ok, err := validateNFTControlRulesJSON([]byte(valid), plan)
	if err != nil || !ok {
		t.Fatalf("valid nft JSON rejected: ok=%v err=%v", ok, err)
	}
	for name, mutated := range map[string]string{
		"not-equal":    strings.Replace(valid, `"op":"=="`, `"op":"!="`, 1),
		"wrong-port":   strings.Replace(valid, `"right":18443`, `"right":18444`, 1),
		"extra-accept": strings.Replace(valid, `{"drop":null}`, `{"drop":null},{"accept":null}`, 1),
		"missing-prio": strings.Replace(valid, `"prio":-5,`, "", 1),
		"duplicate":    strings.Replace(valid, `]}`, `,{"rule":{"family":"inet","table":"anas_incus_control","chain":"input","comment":"anas-loopback-probe","expr":[{"accept":null}]}}]}`, 1),
	} {
		if ok, err := validateNFTControlRulesJSON([]byte(mutated), plan); err == nil || ok {
			t.Fatalf("%s nft mutation accepted: ok=%v err=%v", name, ok, err)
		}
	}
}

func TestCompiledAPTConfigFilesMatchPackagedFixtures(t *testing.T) {
	recipes, err := incushost.Recipes()
	if err != nil {
		t.Fatal(err)
	}
	for _, recipe := range recipes {
		files, err := CompiledAPTConfigFiles(recipe, false)
		if err != nil {
			t.Fatalf("%s: %v", recipe.ID, err)
		}
		for _, file := range files {
			rel, ok := strings.CutPrefix(file.Path, "/etc/anas/incus-apt/")
			if !ok {
				t.Fatalf("%s: unexpected apt target path %s", recipe.ID, file.Path)
			}
			fixture := filepath.Join("..", "..", "packaging", "incus", "apt", filepath.FromSlash(rel))
			body, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatalf("%s: fixture %s: %v", recipe.ID, fixture, err)
			}
			if string(body) != string(file.Body) {
				t.Fatalf("%s: fixture drift for %s\nfixture:\n%s\nexpected:\n%s", recipe.ID, rel, body, file.Body)
			}
		}
	}
}

func TestOfficialAPTConfigRejectsUnregisteredPolicyInputs(t *testing.T) {
	rows, err := incushost.Recipes()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"path", "package", "suite", "repository"} {
		r := rows[0]
		switch mode {
		case "path":
			r.ID = "../../outside"
		case "package":
			r.Packages = []string{"unapproved-package"}
		case "suite":
			r.Codename = "different-suite"
		case "repository":
			r.Repository = "third-party"
		}
		for _, speedup := range []bool{false, true} {
			if _, err := CompiledAPTConfigFiles(r, speedup); err == nil {
				t.Fatalf("accepted noncompiled %s policy (speedup=%v)", mode, speedup)
			}
		}
	}
}

func TestAbsentProvisionStateRemainsDistinguishableFromUnsafeState(t *testing.T) {
	_, err := readRootOnlyFile(filepath.Join(t.TempDir(), "missing", "state.json"), 1024)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fresh installation cannot distinguish absent state: %v", err)
	}
}

func TestWriteAPTConfigFilesWritesUnderProvidedRoot(t *testing.T) {
	recipes, err := incushost.Recipes()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	written, err := WriteAPTConfigFiles(root, recipes[0], false)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) == 0 {
		t.Fatal("no apt files written")
	}
	for _, file := range written {
		if !strings.HasPrefix(file.Path, root+"/") {
			t.Fatalf("write escaped fake root: %s", file.Path)
		}
		body, err := os.ReadFile(file.Path)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != string(file.Body) {
			t.Fatalf("written file drift for %s", file.Path)
		}
	}
}

func TestSensitiveTypesFormatRedacted(t *testing.T) {
	credential := Credential{Fingerprint: strings.Repeat("a", 64), PrivateKey: "PRIVATE KEY"}
	bundle := ConnectionBundle{ControlNetwork: ControlNetworkName, Endpoint: "https://10.77.0.1:18443", AdminPrivateKeyPEM: "PRIVATE KEY", ServerCertificatePEM: "BEGIN CERTIFICATE"}
	for _, text := range []string{credential.String(), credential.GoString(), bundle.String(), bundle.GoString()} {
		if strings.Contains(text, "PRIVATE KEY") || strings.Contains(text, "BEGIN CERTIFICATE") || strings.Contains(text, "https://") {
			t.Fatalf("sensitive formatter leaked material: %s", text)
		}
	}
}

// Protocol-shape fixture: metainfo, handles and interleaved chain/rules.
// It is not represented as a native firewall execution record.
const controlRulesJSONFixture = `{"nftables":[{"metainfo":{"version":"1.1.3","release_name":"fixture","json_schema_version":1}},{"table":{"family":"inet","name":"anas_incus_control","comment":"anas-owner=owner-1","handle":1}},{"chain":{"family":"inet","table":"anas_incus_control","name":"input","type":"filter","hook":"input","prio":-5,"policy":"accept"}},{"rule":{"family":"inet","table":"anas_incus_control","chain":"input","comment":"anas-loopback-probe","expr":[{"match":{"op":"==","left":{"meta":{"key":"iifname"}},"right":"lo"}},{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"daddr"}},"right":"127.0.0.1"}},{"match":{"op":"==","left":{"payload":{"protocol":"tcp","field":"dport"}},"right":8443}},{"accept":null}],"handle":4}},{"rule":{"family":"inet","table":"anas_incus_control","chain":"input","comment":"anas-local-relay-probe","expr":[{"match":{"op":"==","left":{"meta":{"key":"iifname"}},"right":"lo"}},{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"saddr"}},"right":"10.77.0.1"}},{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"daddr"}},"right":"10.77.0.1"}},{"match":{"op":"==","left":{"payload":{"protocol":"tcp","field":"dport"}},"right":18443}},{"accept":null}],"handle":100}},{"rule":{"family":"inet","table":"anas_incus_control","chain":"input","comment":"anas-control-relay","expr":[{"match":{"op":"==","left":{"meta":{"key":"iifname"}},"right":"br-anas-ctrl"}},{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"saddr"}},"right":{"prefix":{"addr":"10.77.0.0","len":24}}}},{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"daddr"}},"right":"10.77.0.1"}},{"match":{"op":"==","left":{"payload":{"protocol":"tcp","field":"dport"}},"right":18443}},{"accept":null}],"handle":5}},{"rule":{"family":"inet","table":"anas_incus_control","chain":"input","comment":"anas-control-relay-default-deny","expr":[{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"daddr"}},"right":"10.77.0.1"}},{"match":{"op":"==","left":{"payload":{"protocol":"tcp","field":"dport"}},"right":18443}},{"drop":null}],"handle":6}},{"rule":{"family":"inet","table":"anas_incus_control","chain":"input","comment":"anas-control-host-default-deny","expr":[{"match":{"op":"==","left":{"meta":{"key":"iifname"}},"right":"br-anas-ctrl"}},{"drop":null}],"handle":7}},{"chain":{"family":"inet","table":"anas_incus_control","name":"forward","type":"filter","hook":"forward","prio":-5,"policy":"accept"}},{"rule":{"family":"inet","table":"anas_incus_control","chain":"forward","comment":"anas-control-forward-iif-deny","expr":[{"match":{"op":"==","left":{"meta":{"key":"iifname"}},"right":"br-anas-ctrl"}},{"drop":null}],"handle":8}},{"rule":{"family":"inet","table":"anas_incus_control","chain":"forward","comment":"anas-control-forward-oif-deny","expr":[{"match":{"op":"==","left":{"meta":{"key":"oifname"}},"right":"br-anas-ctrl"}},{"drop":null}],"handle":9}}]}`

func TestControlFirewallDropsAreConfinedToManagedBridgeAndEndpoint(t *testing.T) {
	plan := ControlNetworkPlan{OwnershipID: "owner-1", Bridge: "br-anas-ctrl", Subnet: "10.77.0.0/24", Gateway: "10.77.0.1"}
	for _, line := range strings.Split(nftControlRules(plan), "\n") {
		if !strings.Contains(line, " drop") {
			continue
		}
		if !strings.Contains(line, `iifname "br-anas-ctrl"`) && !strings.Contains(line, `oifname "br-anas-ctrl"`) &&
			!strings.Contains(line, "ip daddr 10.77.0.1 tcp dport 18443") {
			t.Fatalf("drop affects unrelated host traffic: %s", line)
		}
	}
}

func TestControlFirewallReadbackRejectsOrderFlagsAliasesAndIncompleteRules(t *testing.T) {
	plan := ControlNetworkPlan{OwnershipID: "owner-1", Bridge: "br-anas-ctrl", Subnet: "10.77.0.0/24", Gateway: "10.77.0.1"}
	for _, kind := range []string{"order", "dormant", "missing rule", "extra rule", "extra chain", "unknown key", "null priority", "duplicate meta"} {
		t.Run(kind, func(t *testing.T) {
			var d map[string][]map[string]any
			if err := json.Unmarshal([]byte(controlRulesJSONFixture), &d); err != nil {
				t.Fatal(err)
			}
			items := d["nftables"]
			switch kind {
			case "order":
				items[3], items[4] = items[4], items[3]
			case "dormant":
				items[1]["table"].(map[string]any)["flags"] = []string{"dormant"}
			case "missing rule":
				items = append(items[:4], items[5:]...)
			case "extra rule":
				items = append(items, items[3])
			case "extra chain":
				items = append(items, items[2])
			case "unknown key":
				items[1]["table"].(map[string]any)["ignored"] = true
			case "null priority":
				items[2]["chain"].(map[string]any)["prio"] = nil
			case "duplicate meta":
				items = append([]map[string]any{items[0]}, items...)
			}
			d["nftables"] = items
			body, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			if ok, err := validateNFTControlRulesJSON(body, plan); ok || err == nil {
				t.Fatal("invalid readback accepted")
			}
		})
	}
	for _, body := range []string{
		strings.Replace(controlRulesJSONFixture, `"op":"=="`, `"op":"!=","op":"=="`, 1),
		strings.Replace(controlRulesJSONFixture, `"name":"input"`, `"Name":"input"`, 1),
		controlRulesJSONFixture + "{}",
		strings.Repeat(" ", 64<<10) + controlRulesJSONFixture,
	} {
		if ok, err := validateNFTControlRulesJSON([]byte(body), plan); ok || err == nil {
			t.Fatal("invalid JSON boundary accepted")
		}
	}
}
