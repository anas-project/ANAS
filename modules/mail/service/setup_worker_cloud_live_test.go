// TEST_CASES: MAILMOD-T-081
package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/store"
	"golang.org/x/sys/unix"
)

func readCloudTestCredential(t *testing.T, path string, out any) {
	t.Helper()
	if !filepath.IsAbs(path) {
		t.Fatal("private test credential path required")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal("private test credential unavailable")
	}
	f := os.NewFile(uintptr(fd), "private-test-credential")
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&0077 != 0 || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		t.Fatal("unsafe test credential")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 16385))
	defer clear(raw)
	if err != nil || len(raw) > 16384 || json.Unmarshal(raw, out) != nil {
		t.Fatal("invalid test credential")
	}
}

// This runs only under the local host test. The directory and session are
// synthetic; the restricted host socket and Cloudflare operations are real.
func TestWorkerAdminThroughRealHostCloud(t *testing.T) {
	if os.Getenv("MAIL_WORKER_ADMIN_CLOUD_TEST") != "1" {
		t.Skip("explicit local isolated host fixture required")
	}
	var deployment struct {
		Account string `json:"account_id"`
		Token   string `json:"token"`
	}
	var routing struct {
		Zone   string `json:"zone_id"`
		Domain string `json:"allowed_domain"`
		Token  string `json:"token"`
	}
	readCloudTestCredential(t, os.Getenv("MAIL_CLOUDFLARE_TEST_CREDENTIAL_FILE"), &deployment)
	readCloudTestCredential(t, os.Getenv("MAIL_CLOUDFLARE_ROUTING_TEST_CREDENTIAL_FILE"), &routing)
	if routing.Domain != "mail-poc.bengbu.cc.cd" || routing.Zone != "0d84033826bc3cd0c283efdb57a90f7a" || deployment.Token == routing.Token {
		t.Fatal("authorized isolated domain and independent tokens required")
	}
	worker, operation := os.Getenv("MAIL_WORKER_TEST_NAME"), os.Getenv("MAIL_WORKER_TEST_OPERATION")
	if !strings.HasPrefix(worker, "anas-reserve-poc-") || !installOperation.MatchString(operation) {
		t.Fatal("invalid isolated resource identity")
	}
	cert, err := os.ReadFile(os.Getenv("MAIL_WORKER_TEST_CA"))
	if err != nil {
		t.Fatal("test CA unavailable")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		t.Fatal("invalid test CA")
	}
	t.Setenv("MAIL_SERVICE_API_URL", "https://127.0.0.1")
	t.Setenv("MAIL_SERVICE_API_WORKSPACE", "main")
	t.Setenv("MAIL_SERVICE_API_TOKEN_FILE", os.Getenv("MAIL_WORKER_TEST_TOKEN_FILE"))
	t.Setenv("MAIL_SERVICE_API_SOCKET_FILE", os.Getenv("MAIL_WORKER_TEST_SOCKET"))
	client, err := configuredMailInstaller([]string{routing.Domain}, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal("restricted test client unavailable")
	}
	h, login, _, dir := loginFixture(t)
	// Static setup assets are served only when the mail protocol is configured.
	// This fixture never invokes protocol operations; it only enables those assets.
	h.protocol = &protocolBinding{}
	h.store = &setupHTTPStore{httpStore: login, state: store.SetupState{Revision: 3, Draft: store.SetupDraft{Inbound: "worker-r2", Domain: routing.Domain, LimitsConfirmed: true, Limits: store.SetupLimits{CloudBytes: 16 << 20, DeliveredDays: 7}}}}
	h.directory, h.install = &setupHTTPDirectory{httpDirectory: dir, admin: true}, client
	if os.Getenv("MAIL_WORKER_ADMIN_CLOUD_BROWSER") == "1" {
		driver, logPath := os.Getenv("MAIL_WORKER_BROWSER_DRIVER"), os.Getenv("MAIL_WORKER_BROWSER_LOG")
		if !filepath.IsAbs(driver) || !filepath.IsAbs(logPath) {
			t.Fatal("isolated browser driver and log required")
		}
		server := httptest.NewTLSServer(h)
		defer server.Close()
		h.origin, err = url.Parse(server.URL)
		if err != nil {
			t.Fatal("isolated browser origin unavailable")
		}
		cmd := exec.CommandContext(t.Context(), "node", driver)
		cmd.Env = append(os.Environ(), "MAIL_WORKER_BROWSER_ORIGIN="+server.URL)
		out, e := cmd.CombinedOutput()
		if err = os.WriteFile(logPath, out, 0600); err != nil {
			t.Fatal("private browser log unavailable")
		}
		if e != nil || !strings.Contains(string(out), "isolated browser prepare, commit and repeat completed") {
			t.Fatal("isolated browser installation failed; inspect private browser log")
		}
		t.Log("browser drove actual administrator API and restricted host")
		return
	}
	var source string
	for _, action := range []string{"prepare", "commit", "commit"} {
		body := map[string]any{"revision": 3, "action": action, "operation_id": operation, "account_id": deployment.Account, "zone_id": routing.Zone, "worker": worker, "bucket": worker, "drain_ids": []string{}, "capacity_bytes": 16 << 20, "routing_token": routing.Token}
		if action == "prepare" {
			body["deployment_token"] = deployment.Token
		}
		raw, e := json.Marshal(body)
		if e != nil {
			t.Fatal("cannot construct isolated request")
		}
		r := httptest.NewRequest("POST", "https://mail.example.test/api/setup/worker-install", bytes.NewReader(raw))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
		r.Header.Set("Origin", "https://mail.example.test")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		clear(raw)
		if strings.Contains(w.Body.String(), deployment.Token) || strings.Contains(w.Body.String(), routing.Token) {
			t.Fatal("private credential appeared in response")
		}
		var result struct {
			State   string `json:"state"`
			Source  string `json:"source_id"`
			Enabled bool   `json:"enabled"`
		}
		want := "configured"
		if action == "prepare" {
			want = "prepared"
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.State != want || result.Enabled || !installSourceID.MatchString(result.Source) {
			t.Fatalf("%s response unavailable: status=%d", action, w.Code)
		}
		if source == "" {
			source = result.Source
		} else if result.Source != source {
			t.Fatal("source changed between installation steps")
		}
		t.Log("administrator API completed", action)
	}
}
