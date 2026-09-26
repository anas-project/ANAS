package computeingressruntime

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/computeclient"
	"github.com/anas-project/ANAS/internal/computeingress"
)

// This is a real, pinned mutual-TLS transport with synthetic Incus metadata.
// It does not prove that Incus has granted a server-enforced read-only identity.
type incusReaderFixture struct {
	mu                sync.Mutex
	config            IncusObserverConfig
	reader            *IncusFactReader
	grant             *computeingress.Authorization
	request           computeingress.Request
	server            *httptest.Server
	values            map[string]any
	paths             []string
	mutate            func(string, int, map[string]any)
	response          func(http.ResponseWriter, *http.Request) bool
	clientFingerprint string
	serverFingerprint string
}

func readerCertificate(t *testing.T) ([]byte, []byte, *x509.Certificate) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), cert
}

func newIncusReaderFixture(t *testing.T, iface string) *incusReaderFixture {
	t.Helper()
	f := &incusReaderFixture{values: map[string]any{}}
	f.grant = &computeingress.Authorization{Schema: computeingress.Schema, Deployment: "deployment-one", Consumer: "forgejo", Resource: "runners", Provider: "incus", Interface: iface, Project: "anas-runners", InstancePrefix: "anas-fj-", LeaseSecretRef: "ANAS_COMPUTE_RESOURCE__FORGEJO__RUNNERS__LEASE_SECRET", BaseDomain: "example.test", Policy: computeingress.Policy{AllowedPorts: []uint16{7000}, Auth: "none", Domain: computeingress.Domain{Mode: "fixed", Prefix: "ci"}}}
	f.request = computeingress.Request{Action: "publish", InstanceID: "anas-fj-job1", WorkloadID: "job:123", GuestPort: 7000}
	clientPEM, keyPEM, clientCert := readerCertificate(t)
	clientDigest := sha256.Sum256(clientCert.Raw)
	f.clientFingerprint = hex.EncodeToString(clientDigest[:])
	f.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.paths = append(f.paths, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet || r.TLS == nil || len(r.TLS.PeerCertificates) != 1 || !reflect.DeepEqual(r.TLS.PeerCertificates[0].Raw, clientCert.Raw) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if f.response != nil && f.response(w, r) {
			return
		}
		if f.mutate != nil {
			f.mutate(r.URL.RequestURI(), len(f.paths), f.values)
		}
		value, ok := f.values[r.URL.RequestURI()]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "sync", "status_code": 200, "error_code": 0, "metadata": value})
	}))
	f.server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert}
	f.server.StartTLS()
	t.Cleanup(f.server.Close)
	serverCert := f.server.Certificate()
	serverPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert.Raw})
	serverDigest := sha256.Sum256(serverCert.Raw)
	f.serverFingerprint = hex.EncodeToString(serverDigest[:])
	bridge := computeclient.NetworkName(f.grant.Project)
	f.values["/1.0?project=anas-runners"] = map[string]any{"auth": "trusted", "api_version": "1.0", "auth_user_name": f.clientFingerprint, "auth_user_method": "tls", "environment": map[string]any{"server": "incus", "server_version": "7.3.0", "server_clustered": false, "server_name": "fixture", "server_pid": 123, "certificate_fingerprint": f.serverFingerprint}}
	f.values["/1.0/projects/anas-runners"] = map[string]any{"name": f.grant.Project, "config": map[string]string{"restricted": "true", "features.networks": "false", "restricted.networks.access": bridge, "restricted.devices.nic": "managed", "restricted.containers.privilege": "unprivileged"}}
	f.values["/1.0/networks/"+bridge+"?project=default"] = map[string]any{"name": bridge, "project": "default", "type": "bridge", "managed": true, "status": "Created", "config": map[string]string{"user.anas.consumer": "forgejo", "user.anas.sandbox": f.grant.Project, "ipv4.address": "10.42.0.1/24", "ipv4.nat": "true"}}
	kind := "container"
	if iface == computeclient.InterfaceVM {
		kind = "virtual-machine"
	}
	f.values["/1.0/instances/anas-fj-job1?project=anas-runners"] = map[string]any{"name": f.request.InstanceID, "project": f.grant.Project, "type": kind, "status": "Running", "status_code": 103, "last_used_at": time.Now().Add(-time.Minute).UTC(), "config": map[string]string{"volatile.uuid": "11111111-1111-4111-8111-111111111111", "volatile.uuid.generation": "22222222-2222-4222-8222-222222222222", "volatile.eth0.hwaddr": "00:16:3e:01:02:03", "user.anas.managed": "true", "user.anas.workload": f.request.WorkloadID}, "expanded_config": map[string]string{"security.privileged": "false"}, "expanded_devices": map[string]map[string]string{"root": {"type": "disk", "path": "/", "pool": "default"}, "eth0": {"type": "nic", "network": bridge}}}
	instance := f.values["/1.0/instances/anas-fj-job1?project=anas-runners"].(map[string]any)
	for _, key := range []string{"security.mac_filtering", "security.ipv4_filtering", "security.ipv6_filtering"} {
		instance["expanded_devices"].(map[string]map[string]string)["eth0"][key] = "true"
	}
	f.values["/1.0/instances/anas-fj-job1/state?project=anas-runners"] = map[string]any{"status": "Running", "status_code": 103, "network": map[string]any{"eth0": map[string]any{"state": "up", "type": "broadcast", "hwaddr": "00:16:3e:01:02:03", "host_name": "vethguest0", "addresses": []map[string]string{{"family": "inet", "scope": "global", "address": "10.42.0.2", "netmask": "24"}}}}}
	f.values["/1.0/networks/"+bridge+"/leases?project=default"] = []map[string]string{{"address": "10.42.0.2", "hwaddr": "00:16:3e:01:02:03", "type": "dynamic"}}
	f.config = IncusObserverConfig{Endpoint: f.server.URL, ServerCertPEM: serverPEM, ClientCertPEM: clientPEM, ClientKeyPEM: keyPEM, ServerVersion: "7.3.0", Authorizations: []*computeingress.Authorization{f.grant}}
	reader, err := NewIncusFactReader(f.config)
	if err != nil {
		t.Fatal(err)
	}
	f.reader = reader
	t.Cleanup(reader.CloseIdleConnections)
	return f
}

func TestIncusReaderPinnedObservationAndExactGETSurface(t *testing.T) {
	for _, iface := range []string{computeclient.InterfaceContainer, computeclient.InterfaceVM} {
		t.Run(iface, func(t *testing.T) {
			f := newIncusReaderFixture(t, iface)
			facts, err := f.reader.ObserveHTTP(context.Background(), f.grant, f.request)
			if err != nil {
				t.Fatal(err)
			}
			if facts.Project != f.grant.Project || facts.Interface != iface || facts.GuestIP != "10.42.0.2" || facts.GuestMAC != "00:16:3e:01:02:03" || !validEpoch(facts.Incarnation) {
				t.Fatal("incomplete trusted observation")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.paths) != 12 || !reflect.DeepEqual(f.paths[:6], f.paths[6:]) {
				t.Fatalf("not two complete scoped samples: %v", f.paths)
			}
		})
	}
}

func TestIncusReaderRejectsWorkloadAndManagedIdentityMismatch(t *testing.T) {
	for _, scenario := range []string{"unmanaged", "missing-managed", "wrong-workload", "missing-workload"} {
		t.Run(scenario, func(t *testing.T) {
			f := newIncusReaderFixture(t, computeclient.InterfaceContainer)
			instance := f.values["/1.0/instances/anas-fj-job1?project=anas-runners"].(map[string]any)
			config := instance["config"].(map[string]string)
			switch scenario {
			case "unmanaged":
				config["user.anas.managed"] = "false"
			case "missing-managed":
				delete(config, "user.anas.managed")
			case "wrong-workload":
				config["user.anas.workload"] = "another-job"
			case "missing-workload":
				delete(config, "user.anas.workload")
			}
			facts, err := f.reader.ObserveHTTP(context.Background(), f.grant, f.request)
			if err == nil || facts != (computeingress.Facts{}) {
				t.Fatal("unmanaged or different-workload instance accepted")
			}
		})
	}
}

func TestIncusReaderCannotWidenInstalledAuthorization(t *testing.T) {
	for _, scenario := range []string{"port", "deployment", "domain", "auth"} {
		t.Run(scenario, func(t *testing.T) {
			f := newIncusReaderFixture(t, computeclient.InterfaceContainer)
			grant, request := f.grant.Clone(), f.request
			switch scenario {
			case "port":
				grant.Policy.AllowedPorts = append(grant.Policy.AllowedPorts, 7001)
				request.GuestPort = 7001
			case "deployment":
				grant.Deployment = "deployment-two"
			case "domain":
				grant.Policy.Domain.Prefix = "other"
			case "auth":
				grant.Policy.Auth = "forward_auth"
				grant.ForwardAuth = &computeingress.ForwardAuth{Provider: "authentik", Middleware: "auth@file"}
			}
			if _, err := f.reader.ObserveHTTP(context.Background(), grant, request); err == nil {
				t.Fatal("caller changed frozen installed authorization")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.paths) != 0 {
				t.Fatal("out-of-scope request reached daemon")
			}
		})
	}
}

func TestIncusReaderRejectsPausedAndChangedIncarnations(t *testing.T) {
	for _, scenario := range []string{"paused", "stopped", "uuid", "generation", "restarted", "address", "allocation", "server"} {
		t.Run(scenario, func(t *testing.T) {
			f := newIncusReaderFixture(t, computeclient.InterfaceContainer)
			f.mutate = func(_ string, n int, values map[string]any) {
				if n != 7 {
					return
				}
				instance := values["/1.0/instances/anas-fj-job1?project=anas-runners"].(map[string]any)
				config := instance["config"].(map[string]string)
				switch scenario {
				case "paused":
					instance["status"] = "Frozen"
					instance["status_code"] = 110
				case "stopped":
					instance["status"] = "Stopped"
					instance["status_code"] = 102
				case "uuid":
					config["volatile.uuid"] = "33333333-3333-4333-8333-333333333333"
				case "generation":
					config["volatile.uuid.generation"] = "33333333-3333-4333-8333-333333333333"
				case "restarted":
					instance["last_used_at"] = time.Now().Add(-time.Second).UTC()
				case "address":
					state := values["/1.0/instances/anas-fj-job1/state?project=anas-runners"].(map[string]any)
					state["network"].(map[string]any)["eth0"].(map[string]any)["addresses"].([]map[string]string)[0]["address"] = "10.42.0.3"
				case "allocation":
					values["/1.0/networks/"+computeclient.NetworkName(f.grant.Project)+"/leases?project=default"] = []map[string]string{}
				case "server":
					values["/1.0?project=anas-runners"].(map[string]any)["environment"].(map[string]any)["server_pid"] = 124
				}
			}
			facts, err := f.reader.ObserveHTTP(context.Background(), f.grant, f.request)
			if err == nil || facts != (computeingress.Facts{}) {
				t.Fatal("inconsistent lifecycle observation accepted")
			}
		})
	}
}

func TestIncusReaderRejectsTransportAndEnvelopeFaults(t *testing.T) {
	for _, scenario := range []string{"redirect", "duplicate", "case-alias", "null", "async", "error", "oversized", "wrong-user", "wrong-version"} {
		t.Run(scenario, func(t *testing.T) {
			f := newIncusReaderFixture(t, computeclient.InterfaceContainer)
			const secret = "DO-NOT-ECHO-DAEMON-PAYLOAD"
			f.response = func(w http.ResponseWriter, r *http.Request) bool {
				switch scenario {
				case "redirect":
					w.Header().Set("Location", "https://example.test/"+secret)
					w.WriteHeader(302)
				case "duplicate":
					_, _ = io.WriteString(w, `{"type":"error","type":"sync","status_code":200,"metadata":{}}`)
				case "case-alias":
					body, _ := json.Marshal(map[string]any{"type": "sync", "status_code": 200, "metadata": f.values[r.URL.RequestURI()]})
					_, _ = w.Write([]byte(strings.Replace(string(body), `"auth":"trusted"`, `"auth":"untrusted","Auth":"trusted"`, 1)))
				case "null":
					_, _ = io.WriteString(w, `{"type":"sync","status_code":200,"metadata":null}`)
				case "async":
					_, _ = io.WriteString(w, `{"type":"async","status_code":200,"metadata":{}}`)
				case "error":
					w.WriteHeader(403)
					_, _ = io.WriteString(w, secret)
				case "oversized":
					_, _ = io.WriteString(w, strings.Repeat(" ", (2<<20)+1))
				case "wrong-user":
					f.values["/1.0?project=anas-runners"].(map[string]any)["auth_user_name"] = strings.Repeat("f", 64)
					return false
				case "wrong-version":
					f.values["/1.0?project=anas-runners"].(map[string]any)["environment"].(map[string]any)["server_version"] = "other"
					return false
				}
				return true
			}
			facts, err := f.reader.ObserveHTTP(context.Background(), f.grant, f.request)
			if err == nil || facts != (computeingress.Facts{}) {
				t.Fatal("invalid transport or envelope accepted")
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), f.server.URL) {
				t.Fatal("observer echoed endpoint or raw error")
			}
		})
	}
}
