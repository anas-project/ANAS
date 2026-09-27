package incusprovision

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

const uninstallInstancesURL = "/1.0/instances?recursion=2&all-projects=true"
const uninstallPoolURL = "/1.0/storage-pools/anas-btrfs"
const uninstallVolumesURL = uninstallPoolURL + "/volumes?recursion=1"
const unusedPoolFixture = `{"name":"anas-btrfs","driver":"btrfs","config":{"user.anas.owner":"incus-host-provision","user.anas.owner_id":"owner"},"used_by":[]}`

type uninstallProtocolFixture struct {
	runtime  *localRuntime
	owner    Ownership
	metadata map[string]string
	network  string
	calls    []string
	hook     func(*http.Request)
}

func newUninstallProtocolFixture(t *testing.T) *uninstallProtocolFixture {
	t.Helper()
	f := &uninstallProtocolFixture{owner: Ownership{
		ID: "owner", StoragePool: StoragePoolName, StoragePoolDriver: "btrfs",
		DockerNetwork: ControlNetworkName, DockerNetworkID: strings.Repeat("a", 64), ControlBridge: "br-anas-ctrl",
	}, metadata: map[string]string{
		uninstallInstancesURL: "[]", uninstallPoolURL: unusedPoolFixture, uninstallVolumesURL: "[]",
		"/1.0/projects?recursion=1":      `[{"name":"default"}]`,
		"/1.0/storage-pools?recursion=1": "[" + unusedPoolFixture + "]",
		"/1.0/networks?recursion=1":      `[{"name":"eth0","managed":false}]`,
		"/1.0/profiles?recursion=1":      `[{"name":"default","config":{},"devices":{}}]`,
		"/1.0/images?recursion=1":        `[]`,
		"/1.0/certificates?recursion=1":  `[]`,
		"/1.0/network-acls?recursion=1":  `[]`,
	}}
	f.network = `{"Id":"` + f.owner.DockerNetworkID + `","Name":"anas-incus-control","Driver":"bridge","Internal":true,"EnableIPv6":false,"ConfigOnly":false,"Labels":{"dev.anas.owner":"incus-host-provision","dev.anas.owner_id":"owner"},"Options":{"com.docker.network.bridge.enable_icc":"false","com.docker.network.bridge.enable_ip_masquerade":"false","com.docker.network.bridge.name":"br-anas-ctrl"},"IPAM":{"Config":[{"Subnet":"10.77.0.0/24","Gateway":"10.77.0.1"}]},"Containers":{}}`
	f.runtime = &localRuntime{
		incus: &incusUnixClient{transport: incusRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			f.calls = append(f.calls, req.Method+" "+req.URL.RequestURI())
			if req.Method != http.MethodGet {
				t.Fatal("uninstall preflight wrote to Incus")
			}
			if f.hook != nil {
				f.hook(req)
			}
			raw, ok := f.metadata[req.URL.RequestURI()]
			if !ok {
				return incusTestResponse(404, `{"type":"error","error_code":404}`), nil
			}
			return incusTestResponse(200, `{"type":"sync","status_code":200,"metadata":`+raw+`}`), nil
		})},
		docker: &dockerClient{transport: incusRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			f.calls = append(f.calls, req.Method+" "+req.URL.RequestURI())
			if req.Method != http.MethodGet || req.URL.Path != "/v1.44/networks/"+ControlNetworkName {
				t.Fatal("uninstall preflight touched a container or an unrelated Docker resource")
			}
			if f.hook != nil {
				f.hook(req)
			}
			if f.network == "" {
				return incusTestResponse(404, `{"message":"network not found"}`), nil
			}
			return incusTestResponse(200, f.network), nil
		})},
	}
	return f
}

// INCUS-R-048/R-051: explicit, read-only evidence must precede revocation.
func TestUninstallProtocolRejectsRetainedResourcesAndIncompleteEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*uninstallProtocolFixture)
	}{
		{"frozen root disk", func(f *uninstallProtocolFixture) {
			f.metadata[uninstallInstancesURL] = `[{"name":"worker","project":"lease","status":"Frozen","devices":{},"expanded_devices":{"root":{"type":"disk","pool":"anas-btrfs"}}}]`
		}},
		{"stopped guest", func(f *uninstallProtocolFixture) {
			f.metadata[uninstallInstancesURL] = `[{"name":"worker","project":"lease","status":"Stopped","devices":{"eth0":{"parent":"br-anas-ctrl"}},"expanded_devices":{}}]`
		}},
		{"incomplete instance", func(f *uninstallProtocolFixture) { f.metadata[uninstallInstancesURL] = `[{"name":"worker"}]` }},
		{"null instance", func(f *uninstallProtocolFixture) { f.metadata[uninstallInstancesURL] = `[null]` }},
		{"profile references pool", func(f *uninstallProtocolFixture) {
			f.metadata[uninstallPoolURL] = strings.Replace(unusedPoolFixture, `"used_by":[]`, `"used_by":["/1.0/profiles/default?project=lease"]`, 1)
		}},
		{"missing pool references", func(f *uninstallProtocolFixture) {
			f.metadata[uninstallPoolURL] = strings.Replace(unusedPoolFixture, `,"used_by":[]`, "", 1)
		}},
		{"null pool references", func(f *uninstallProtocolFixture) {
			f.metadata[uninstallPoolURL] = strings.Replace(unusedPoolFixture, `"used_by":[]`, `"used_by":null`, 1)
		}},
		{"reference case alias", func(f *uninstallProtocolFixture) {
			f.metadata[uninstallPoolURL] = strings.Replace(unusedPoolFixture, `"used_by":[]`, `"used_by":["keep"],"Used_By":[]`, 1)
		}},
		{"foreign pool", func(f *uninstallProtocolFixture) {
			f.metadata[uninstallPoolURL] = strings.Replace(unusedPoolFixture, `"owner"`, `"foreign-owner"`, 1)
		}},
		{"custom volume", func(f *uninstallProtocolFixture) {
			f.metadata[uninstallVolumesURL] = `[{"name":"persistent-data","type":"custom"}]`
		}},
		{"volume 404 is not empty", func(f *uninstallProtocolFixture) { delete(f.metadata, uninstallVolumesURL) }},
		{"null volume inventory", func(f *uninstallProtocolFixture) { f.metadata[uninstallVolumesURL] = `null` }},
		{"attached Docker endpoint", func(f *uninstallProtocolFixture) {
			f.network = strings.Replace(f.network, `"Containers":{}`, `"Containers":{"existing-container":{"Name":"business"}}`, 1)
		}},
		{"missing Docker endpoints", func(f *uninstallProtocolFixture) { f.network = strings.Replace(f.network, `,"Containers":{}`, "", 1) }},
		{"null Docker endpoints", func(f *uninstallProtocolFixture) {
			f.network = strings.Replace(f.network, `"Containers":{}`, `"Containers":null`, 1)
		}},
		{"Docker endpoint case alias", func(f *uninstallProtocolFixture) {
			f.network = strings.Replace(f.network, `"Containers":{}`, `"Containers":{"keep":{}},"containers":{}`, 1)
		}},
		{"replacement Docker network", func(f *uninstallProtocolFixture) {
			f.network = strings.Replace(f.network, f.owner.DockerNetworkID, strings.Repeat("b", 64), 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newUninstallProtocolFixture(t)
			tc.mutate(f)
			if err := f.runtime.CheckUninstallResources(context.Background(), f.owner, false); err == nil {
				t.Fatal("retained or unverified resources accepted as unused")
			}
		})
	}
}

func TestUninstallProtocolEmptyOwnedResourcesAndScopedForeignGuests(t *testing.T) {
	for _, mode := range []string{"empty", "already absent", "foreign guest", "upstream extension", "shared packages"} {
		t.Run(mode, func(t *testing.T) {
			f := newUninstallProtocolFixture(t)
			switch mode {
			case "already absent":
				delete(f.metadata, uninstallPoolURL)
				f.network = ""
			case "foreign guest":
				f.metadata[uninstallInstancesURL] = `[{"name":"anas-foreign","project":"external","devices":{},"expanded_devices":{"root":{"pool":"foreign-pool"}}}]`
			case "upstream extension":
				f.metadata[uninstallPoolURL] = strings.Replace(unusedPoolFixture, `"used_by":[]`, `"used_by":[],"future_flag":true`, 1)
			}
			if err := f.runtime.CheckUninstallResources(context.Background(), f.owner, mode == "shared packages"); err != nil {
				t.Fatal("complete unused resource inventory rejected", err)
			}
		})
	}
}

func TestUninstallSharedPackagesPreservesObjectsAddedAfterInstallation(t *testing.T) {
	for name, input := range map[string]struct{ path, body string }{
		"foreign guest":   {uninstallInstancesURL, `[{"name":"business","project":"default","devices":{},"expanded_devices":{}}]`},
		"second project":  {"/1.0/projects?recursion=1", `[{"name":"default"},{"name":"external"}]`},
		"empty projects":  {"/1.0/projects?recursion=1", `[]`},
		"foreign pool":    {"/1.0/storage-pools?recursion=1", `[{"name":"external"}]`},
		"managed network": {"/1.0/networks?recursion=1", `[{"name":"external","managed":true}]`},
		"missing managed": {"/1.0/networks?recursion=1", `[{"name":"external"}]`},
		"default profile": {"/1.0/profiles?recursion=1", `[{"name":"default","config":{"user.keep":"true"},"devices":{}}]`},
		"null profile":    {"/1.0/profiles?recursion=1", `[{"name":"default","config":{},"devices":null}]`},
		"retained image":  {"/1.0/images?recursion=1", `[{"fingerprint":"keep"}]`},
		"foreign trust":   {"/1.0/certificates?recursion=1", `[{"name":"operator"}]`},
		"network ACL":     {"/1.0/network-acls?recursion=1", `[{"name":"anas-lease-acl"}]`},
	} {
		t.Run(name, func(t *testing.T) {
			f := newUninstallProtocolFixture(t)
			f.metadata[input.path] = input.body
			if err := f.runtime.CheckUninstallResources(context.Background(), f.owner, true); err == nil {
				t.Fatal("package ownership was mistaken for ownership of later daemon objects")
			}
		})
	}
}

func TestUninstallProtocolCancellationCannotReturnUnusedEvidence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newUninstallProtocolFixture(t)
	f.hook = func(req *http.Request) {
		if req.URL.Path == "/v1.44/networks/"+ControlNetworkName {
			cancel()
		}
	}
	if err := f.runtime.CheckUninstallResources(ctx, f.owner, false); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled inventory returned success", err)
	}
}

func TestDockerNetworkClientCannotConnectDisconnectOrPrune(t *testing.T) {
	for _, path := range []string{
		"/v1.44/networks/" + ControlNetworkName + "/connect", "/v1.44/networks/" + ControlNetworkName + "/disconnect",
		"/v1.44/networks/prune", "/v1.44/networks/create?force=1", "/v1.44/networks/../containers/json",
		"/v1.44/networks/%2e%2e/containers/json", "/v1.44/networks/foreign", "/v1.44/networks?filters={}",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodPatch} {
			client := &dockerClient{transport: incusRoundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatalf("forbidden Docker request reached transport: %s %s", method, path)
				return nil, ErrInvalid
			})}
			if err := client.do(context.Background(), method, path, nil, nil); !errors.Is(err, ErrInvalid) {
				t.Fatal("root Docker surface accepted a non-network-lifecycle operation", err)
			}
		}
	}
}

func TestOwnedNetworkDeletionRechecksEndpointsBeforeAnyDelete(t *testing.T) {
	f := newUninstallProtocolFixture(t)
	f.network = strings.Replace(f.network, `"Containers":{}`, `"Containers":{"existing-container":{}}`, 1)
	if err := f.runtime.RemoveDockerControlNetwork(context.Background(), ControlNetworkName, f.owner.ID, f.owner.DockerNetworkID); !errors.Is(err, ErrBlocked) {
		t.Fatal("occupied network reached deletion", err)
	}
}

func TestUnusedPoolDeletionRejectsMissingVolumeInventoryAndRemainsIdempotent(t *testing.T) {
	for _, mode := range []string{"references", "volumes missing", "already absent"} {
		t.Run(mode, func(t *testing.T) {
			f := newUninstallProtocolFixture(t)
			switch mode {
			case "references":
				f.metadata[uninstallPoolURL] = strings.Replace(unusedPoolFixture, `"used_by":[]`, `"used_by":["keep-profile"]`, 1)
			case "volumes missing":
				delete(f.metadata, uninstallVolumesURL)
			case "already absent":
				delete(f.metadata, uninstallPoolURL)
			}
			err := f.runtime.RemoveStoragePool(context.Background(), StoragePoolName, f.owner.ID)
			if (err == nil) != (mode == "already absent") {
				t.Fatal("pool deletion treated incomplete evidence as safe absence", err)
			}
		})
	}
}

func TestSharedPackageInventoryAcceptsOnlyExactOwnedManagementTrust(t *testing.T) {
	f := newUninstallProtocolFixture(t)
	credential, err := generateCredential()
	if err != nil {
		t.Fatal(err)
	}
	f.owner.ManagementTrust = credential.Fingerprint
	cert := incusCertificate{Name: ManagementCertName, Fingerprint: credential.Fingerprint, Certificate: credential.Certificate, Type: "client", Projects: []string{}}
	body, err := json.Marshal([]incusCertificate{cert})
	if err != nil {
		t.Fatal(err)
	}
	f.metadata["/1.0/certificates?recursion=1"] = string(body)
	if err := f.runtime.CheckUninstallResources(context.Background(), f.owner, true); err != nil {
		t.Fatal("exact existing management credential blocked its own removal", err)
	}
}

// Debian 13 / Incus 6.0.4 returns metadata:null from the recursive storage
// collection after the last pool is removed. Null is not empty evidence:
// the independent non-recursive collection and named-pool absence must agree.
func TestSharedPackageRemovalVerifiesNullStorageCollectionIndependently(t *testing.T) {
	for _, names := range []string{"[]", "null", "[ ]"} {
		t.Run(names, func(t *testing.T) {
			f := newUninstallProtocolFixture(t)
			f.owner.StoragePool = ""
			delete(f.metadata, uninstallPoolURL)
			f.metadata["/1.0/storage-pools?recursion=1"] = "null"
			f.metadata["/1.0/storage-pools"] = names
			if err := f.runtime.CheckUninstallResources(context.Background(), f.owner, true); err != nil {
				t.Fatal("independently verified empty pool inventory rejected", err)
			}
			if countCalls(f.calls, "GET /1.0/storage-pools") != 1 || countCalls(f.calls, "GET "+uninstallPoolURL) != 1 {
				t.Fatal("null recursive collection was accepted without both independent queries")
			}
		})
	}
}

func TestSharedPackageRemovalDoesNotTreatNullOrConflictingListsAsEmpty(t *testing.T) {
	for name, raw := range map[string]string{
		"null names but actual pool exists": "null", "missing endpoint": "", "object": "{}",
		"empty names but actual pool exists": "[]",
		"wrong item type":                    "[{}]", "null item": "[null]",
		"retained pool":           `["/1.0/storage-pools/retained"]`,
		"owned pool still exists": `["/1.0/storage-pools/anas-btrfs"]`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newUninstallProtocolFixture(t)
			f.owner.StoragePool = ""
			f.metadata["/1.0/storage-pools?recursion=1"] = "null"
			if raw != "" {
				f.metadata["/1.0/storage-pools"] = raw
			}
			if err := f.runtime.CheckUninstallResources(context.Background(), f.owner, true); err == nil {
				t.Fatal("incomplete or nonempty independent pool inventory authorized package removal")
			}
		})
	}
}

func TestSharedPackageNullCompatibilityIsLimitedToExplicitStorageNull(t *testing.T) {
	for _, collection := range []string{"instances", "projects", "networks", "profiles", "images", "certificates", "storage-pools"} {
		t.Run(collection, func(t *testing.T) {
			f := newUninstallProtocolFixture(t)
			f.owner.StoragePool = ""
			f.metadata["/1.0/storage-pools?recursion=1"] = "[]"
			f.metadata["/1.0/storage-pools"] = "[]"
			path := "/1.0/" + collection + "?recursion=1"
			if collection == "instances" {
				path = uninstallInstancesURL
			}
			if collection == "storage-pools" {
				delete(f.metadata, path) // A failed HTTP request is not explicit null.
			} else {
				f.metadata[path] = "null"
			}
			if err := f.runtime.CheckUninstallResources(context.Background(), f.owner, true); err == nil {
				t.Fatal("missing or null evidence outside the narrowly verified storage case was accepted")
			}
			if countCalls(f.calls, "GET /1.0/storage-pools") != 0 {
				t.Fatal("non-null failure triggered the storage compatibility path")
			}
		})
	}
}

func TestSharedPackageStorageNullReadbackStillObservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newUninstallProtocolFixture(t)
	f.owner.StoragePool = ""
	f.metadata["/1.0/storage-pools?recursion=1"] = "null"
	f.metadata["/1.0/storage-pools"] = "[]"
	delete(f.metadata, uninstallPoolURL)
	f.hook = func(req *http.Request) {
		if req.URL.RequestURI() == "/1.0/storage-pools" {
			cancel()
		}
	}
	if err := f.runtime.CheckUninstallResources(ctx, f.owner, true); err == nil {
		t.Fatal("canceled independent pool inventory returned permission to delete packages")
	}
}

func TestStoragePoolNullCompatibilityRequiresACompleteSuccessfulEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		body string
	}{
		{"metadata missing", 200, `{"type":"sync","status_code":200}`},
		{"metadata alias", 200, `{"type":"sync","status_code":200,"Metadata":null}`},
		{"duplicate metadata", 200, `{"type":"sync","status_code":200,"metadata":[],"metadata":null}`},
		{"error response", 500, `{"type":"error","error_code":500,"metadata":null}`},
		{"nonzero error", 200, `{"type":"sync","status_code":200,"error_code":500,"metadata":null}`},
		{"async read", 202, `{"type":"async","status_code":100,"metadata":null}`},
		{"truncated", 200, `{"type":"sync","status_code":200,"metadata":null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			runtime := &localRuntime{incus: &incusUnixClient{transport: incusRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != http.MethodGet || req.URL.RequestURI() != "/1.0/storage-pools?recursion=1" {
					t.Fatal("incomplete response triggered a substitute query or mutation")
				}
				return incusTestResponse(tc.code, tc.body), nil
			})}}
			if _, err := runtime.storagePoolsForPackageRemoval(context.Background()); err == nil || calls != 1 {
				t.Fatal("an incomplete or unsuccessful envelope acquired empty-inventory authority")
			}
		})
	}
}
