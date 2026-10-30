// TEST_CASES: MAILMOD-T-068 MAILMOD-T-069 MAILMOD-T-070
package installer

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/cloudrelay"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"golang.org/x/crypto/ssh"
)

func provisionSSH(t *testing.T) RelaySSHConfig {
	t.Helper()
	_, host, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hs, err := ssh.NewSignerFromKey(host)
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(key, "")
	if err != nil {
		t.Fatal(err)
	}
	return RelaySSHConfig{Host: "127.0.0.1", Port: 22, User: "relaydeploy", HostKey: ssh.MarshalAuthorizedKey(hs.PublicKey()), PrivateKey: pem.EncodeToMemory(block)}
}

func TestRelayProvisionForwardBindsModeAndPrivateCredentials(t *testing.T) {
	input := provisionFixture(t)
	input.Template.ForwardDomain = "delivery.example.test"
	input.CloudflareForward = &ingress.CloudflareForwardSpec{AccountID: strings.Repeat("1", 32), ZoneID: strings.Repeat("2", 32), Domain: input.Template.ForwardDomain}
	input.Routing, input.Destination = strings.Repeat("r", 40), strings.Repeat("d", 40)
	dir := provisionDir(t)
	j, err := OpenRelayProvisionJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	prepared, err := j.Prepare(input)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.SourceSpec().CloudflareForward == nil || prepared.SourceSpec().CloudflareForward.Domain != input.Template.ForwardDomain || prepared.Source.Routing != input.Routing || prepared.Source.Destination != input.Destination {
		t.Fatal("forward handoff incomplete")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "provision.json"))
	if err != nil || bytes.Contains(raw, []byte(input.Routing)) || bytes.Contains(raw, []byte(input.Destination)) {
		t.Fatal("provider tokens persisted in provision journal", err)
	}
	if strings.Contains(fmt.Sprintf("%+v", prepared), input.Routing) || strings.Contains(fmt.Sprintf("%+v", input), input.Destination) {
		t.Fatal("formatted output exposed provider tokens")
	}
	relayDir := provisionDir(t)
	relay, err := OpenRelayJournal(relayDir)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	request := RelayInstallRequest{Runtime: prepared.Source, TotalCapacity: input.Template.CapacityBytes, OperationID: input.OperationID}
	if err = relay.BindRemotePlan(t.Context(), &relayAudit{}, request, nil); err != nil {
		t.Fatal(err)
	}
	changed := request
	changed.Runtime.Routing = strings.Repeat("s", 40)
	if err = relay.BindRemotePlan(t.Context(), &relayAudit{}, changed, nil); !errors.Is(err, ErrConflict) {
		t.Fatal("changed provider credential reused bound installation", err)
	}
	for _, name := range []string{"relay-binding.json", "relay.lock"} {
		data, e := os.ReadFile(filepath.Join(relayDir, name))
		if e != nil || bytes.Contains(data, []byte(input.Routing)) || bytes.Contains(data, []byte(input.Destination)) {
			t.Fatal("provider token exposed in relay journal", name, e)
		}
	}
	changedInput := input
	changedBinding := *input.CloudflareForward
	changedBinding.ZoneID = strings.Repeat("3", 32)
	changedInput.CloudflareForward = &changedBinding
	if _, err = j.Prepare(changedInput); !errors.Is(err, ErrConflict) {
		t.Fatal("changed Cloudflare identity accepted", err)
	}
	missing := input
	missing.Destination = ""
	if _, err = j.Prepare(missing); !errors.Is(err, ErrConflict) {
		t.Fatal("missing token accepted", err)
	}
	wrongMode := input
	wrongMode.Template.ForwardDomain = "other.example.test"
	if _, err = j.Prepare(wrongMode); !errors.Is(err, ErrConflict) {
		t.Fatal("mismatched delivery domain accepted", err)
	}
}
func provisionFixture(t *testing.T) RelayProvisionInput {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "relay.example.test"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), DNSNames: []string{"relay.example.test"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return RelayProvisionInput{OperationID: "cfg-0123456789abcdef0123456789abcdef", Origin: "https://relay.example.test:18448", AllowedDomains: []string{"example.test"}, SSH: provisionSSH(t), Template: cloudrelay.RuntimeConfig{Schema: "anas.mail-vps-runtime/v1", Directory: "/var/lib/anas-mail-relay/data/source", Domains: []string{"example.test"}, CapacityBytes: 8 << 20, DeliveredDays: 7, Listen: "0.0.0.0:18448", CertificateHost: "relay.example.test", TLSCertificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), TLSPrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})), SMTPListen: "0.0.0.0:25", SMTPHostname: "relay.example.test", NotificationOrigin: "https://nas.example.test"}}
}
func provisionDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
func TestRelayProvisionGeneratesStableIndependentCredentials(t *testing.T) {
	input := provisionFixture(t)
	dir := provisionDir(t)
	j, err := OpenRelayProvisionJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	first, err := j.Prepare(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Runtime.Reader == first.Runtime.Control || first.Runtime.Reader == first.Runtime.NotificationSecret || first.Runtime.Control == first.Runtime.NotificationSecret || first.Runtime.SourceID == first.Runtime.Generation || first.Source.Control != first.Runtime.Control {
		t.Fatal("credentials or identity not independent")
	}
	if _, err = OpenRelayProvisionJournal(dir); !errors.Is(err, ErrBusy) {
		t.Fatal("concurrent operation", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "provision.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, input.SSH.PrivateKey) {
		t.Fatal("SSH private key persisted")
	}
	if st, err := os.Stat(filepath.Join(dir, "provision.json")); err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("unsafe permissions", err)
	}
	if strings.Contains(fmt.Sprintf("%+v", first), first.Runtime.Reader) || strings.Contains(fmt.Sprintf("%+v", input), input.Template.TLSPrivateKey) {
		t.Fatal("format leaks secrets")
	}
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = OpenRelayProvisionJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	second, err := j.Prepare(input)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatal("recovery changed credentials", err)
	}
	changed := input
	changed.SSH = provisionSSH(t)
	if _, err = j.Prepare(changed); !errors.Is(err, ErrConflict) {
		t.Fatal("changed SSH identity accepted", err)
	}
	changed = input
	changed.Template.CapacityBytes++
	if _, err = j.Prepare(changed); !errors.Is(err, ErrConflict) {
		t.Fatal("changed configuration accepted", err)
	}
}
func TestRelayProvisionRefusesLostOrUnsafeState(t *testing.T) {
	for _, scenario := range []string{"missing", "partial", "duplicate", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			input := provisionFixture(t)
			dir := provisionDir(t)
			j, err := OpenRelayProvisionJournal(dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = j.Prepare(input); err != nil {
				t.Fatal(err)
			}
			j.Close()
			path := filepath.Join(dir, "provision.json")
			switch scenario {
			case "missing":
				err = os.Remove(path)
			case "partial":
				err = os.WriteFile(path, []byte("{"), 0600)
			case "duplicate":
				data, e := os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
				err = os.WriteFile(path, bytes.Replace(data, []byte(`"schema":`), []byte(`"schema":"bad","schema":`), 1), 0600)
			case "symlink":
				if err = os.Remove(path); err == nil {
					err = os.Symlink("/dev/null", path)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			j, err = OpenRelayProvisionJournal(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			if _, err = j.Prepare(input); err == nil {
				t.Fatal("unsafe state accepted")
			}
			if scenario == "missing" {
				if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("missing credentials regenerated")
				}
			}
		})
	}
	t.Run("foreign directory", func(t *testing.T) {
		dir := provisionDir(t)
		if err := os.WriteFile(filepath.Join(dir, "foreign"), []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenRelayProvisionJournal(dir); !errors.Is(err, ErrUnknown) {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(dir, "provision.lock")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("modified foreign directory")
		}
	})
	t.Run("wrong origin", func(t *testing.T) {
		input := provisionFixture(t)
		input.Origin = "https://other.example.test:18448"
		dir := provisionDir(t)
		j, err := OpenRelayProvisionJournal(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer j.Close()
		if _, err = j.Prepare(input); !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
		if _, err = os.Lstat(filepath.Join(dir, "provision.json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("created credentials for bad origin")
		}
	})
}
