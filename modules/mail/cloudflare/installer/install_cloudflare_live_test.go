// TEST_CASES: MAILMOD-T-043 MAILMOD-T-081
package installer

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/unix"
)

type cloudInstallTrace struct {
	t      *testing.T
	base   http.RoundTripper
	writes *atomic.Int64
}

func (c cloudInstallTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		c.writes.Add(1)
	}
	response, err := c.base.RoundTrip(req)
	if err != nil {
		c.t.Log("provider request transport unavailable", req.Method)
	} else if response.StatusCode >= 400 {
		c.t.Log("provider request refused", req.Method, response.StatusCode)
	}
	return response, err
}

// This combines actual Cloudflare preparation and the existing private
// workspace transaction. It creates no routing rule, sends no mail and does
// not touch the running workspace. Cleanup is restricted to owned fixtures.
func TestCloudflareInstallAndWorkspaceCommitReal(t *testing.T) {
	if os.Getenv("MAIL_CLOUDFLARE_INSTALL_TEST") != "1" {
		t.Skip("explicit isolated Cloudflare installation required")
	}
	routingPath := os.Getenv("MAIL_CLOUDFLARE_ROUTING_TEST_CREDENTIAL_FILE")
	if !filepath.IsAbs(routingPath) {
		t.Fatal("private routing credential path required")
	}
	fd, err := unix.Open(routingPath, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil || !safeFile(fd, false) {
		if err == nil {
			_ = unix.Close(fd)
		}
		t.Fatal("private routing credential unavailable")
	}
	f := os.NewFile(uintptr(fd), "routing-credential")
	raw, err := io.ReadAll(io.LimitReader(f, 16385))
	_ = f.Close()
	defer clear(raw)
	var routing struct {
		Zone   string `json:"zone_id"`
		Domain string `json:"allowed_domain"`
		Token  string `json:"token"`
	}
	if err != nil || len(raw) > 16384 || json.Unmarshal(raw, &routing) != nil || routing.Zone != "0d84033826bc3cd0c283efdb57a90f7a" || routing.Domain != "mail-poc.bengbu.cc.cd" {
		t.Fatal("authorized routing fixture required")
	}
	cloud, spec, dir := liveCloudFixture(t)
	var writes atomic.Int64
	cloud.http.Transport = cloudInstallTrace{t: t, base: http.DefaultTransport, writes: &writes}
	journalPath := filepath.Join(dir, "reservation.jsonl")
	j, err := OpenJournal(journalPath, spec)
	if err != nil {
		t.Fatal("isolated journal unavailable")
	}
	defer func() {
		cleanupVersionTest(t, cloud, j.state, dir)
		removeTestSourceAfterCleanup(t, dir)
		if err := j.Close(); err != nil {
			t.Error("journal close failed")
		}
	}()
	service, workspace := installWorkspace(t)
	request := InstallRequest{Runtime: RuntimeRequest{Domains: []string{routing.Domain}, AllowedDomains: []string{routing.Domain}, CapacityBytes: 16 << 20, DeliveredDays: 7, ZoneID: routing.Zone, RoutingToken: routing.Token, Proxy: os.Getenv("MAIL_CLOUDFLARE_TEST_PROXY")}, TotalCapacity: 16 << 20, OperationID: "cfg-0123456789abcdef0123456789abcdef"}
	audit := &provisionAudit{}
	prepared, err := j.PrepareInstallRuntime(t.Context(), cloud, audit, request)
	if err != nil || prepared.ID == "" {
		t.Fatal("real preparation failed at", j.state.Stage, err)
	}
	t.Log("real cloud preparation completed")
	configBefore, err := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if err != nil || bytes.Contains(configBefore, []byte(prepared.ID)) {
		t.Fatal("preparation changed workspace")
	}
	configured, err := j.CommitPreparedRuntime(t.Context(), service, audit, noopObserver{}, request)
	if err != nil || configured.ID != prepared.ID || configured.Origin != prepared.Origin {
		t.Fatal("workspace commit failed", err)
	}
	t.Log("isolated workspace transaction committed")
	config, err := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if err != nil || !bytes.Contains(config, []byte(prepared.ID)) || bytes.Contains(config, []byte(routing.Token)) {
		t.Fatal("public source configuration invalid")
	}
	secrets, err := os.ReadFile(filepath.Join(workspace, ".anas", "secrets.yml"))
	if err != nil || !bytes.Contains(secrets, []byte(routing.Token)) {
		t.Fatal("private source transaction missing")
	}
	defer clear(secrets)
	beforeRetry := writes.Load()
	if beforeRetry == 0 {
		t.Fatal("provider fixture did not observe initial writes")
	}
	if err = j.Close(); err != nil {
		t.Fatal("journal close failed")
	}
	reopened, err := OpenJournal(journalPath, spec)
	if err != nil {
		t.Fatal("journal reopen failed")
	}
	j = reopened
	again, err := j.PrepareInstallRuntime(t.Context(), cloud, audit, request)
	if err != nil || again.ID != prepared.ID || again.Origin != prepared.Origin {
		t.Fatal("real preparation recovery failed at", j.state.Stage, err)
	}
	committed, err := j.CommitPreparedRuntime(t.Context(), service, audit, noopObserver{}, request)
	if err != nil || committed.ID != prepared.ID {
		t.Fatal("commit recovery failed", err)
	}
	if writes.Load() != beforeRetry {
		t.Fatal("recovery repeated a provider write")
	}
	configAfter, e1 := os.ReadFile(filepath.Join(workspace, "config.yml"))
	secretsAfter, e2 := os.ReadFile(filepath.Join(workspace, ".anas", "secrets.yml"))
	defer clear(secretsAfter)
	if e1 != nil || e2 != nil || !bytes.Equal(config, configAfter) || !bytes.Equal(secrets, secretsAfter) {
		t.Fatal("recovery changed workspace or secrets")
	}
	t.Log("real Cloudflare preparation, isolated workspace/Secret transaction and reopen/readback passed; no routing or mail enabled")
}

// Recover cleanup only from the original locked journal; an unknown or
// changed provider object is retained, never adopted by name.
func TestCloudflareInstallCleanupRetryReal(t *testing.T) {
	if os.Getenv("MAIL_CLOUDFLARE_INSTALL_CLEANUP") != "1" {
		t.Skip("explicit isolated cleanup required")
	}
	cloud, account, dir := liveCloudFixture(t)
	name := os.Getenv("MAIL_CLOUDFLARE_JOURNAL_NAME")
	if name == "" {
		name = "reservation.jsonl"
	}
	if name != "reservation.jsonl" && name != "journal.jsonl" {
		t.Fatal("unsupported journal name")
	}
	path := filepath.Join(dir, name)
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 || len(raw) > 1<<20 {
		t.Fatal("original journal unavailable")
	}
	first, _, _ := bytes.Cut(raw, []byte("\n"))
	var initial record
	if json.Unmarshal(first, &initial) != nil || initial.Spec.AccountID != account.AccountID || !strings.HasPrefix(initial.Spec.Worker, "anas-reserve-poc-") || initial.Spec.Worker != initial.Spec.Bucket {
		t.Fatal("original test identity unavailable")
	}
	j, err := OpenJournal(path, initial.Spec)
	if err != nil {
		t.Fatal("original journal cannot be locked")
	}
	defer j.Close()
	if j.state.Stage == "prepared" && j.state.WorkerID == "" {
		t.Log("journal confirms no cloud creation was issued")
		return
	}
	match, err := cloud.WorkerMatches(t.Context(), j.state.Spec.AccountID, j.state.WorkerID, j.state.Spec.Worker)
	if err != nil || !match {
		t.Fatal("provider did not confirm recorded Worker identity; retained", err)
	}
	cleanupVersionTest(t, cloud, j.state, dir)
	removeTestSourceAfterCleanup(t, dir)
	if !t.Failed() {
		t.Log("recorded Worker and empty bucket independently verified and removed")
	}
}
