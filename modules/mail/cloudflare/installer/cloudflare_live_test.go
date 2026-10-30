// TEST_CASES: MAILMOD-T-029 MAILMOD-T-030 MAILMOD-T-032 MAILMOD-T-033 MAILMOD-T-036 MAILMOD-T-041 MAILMOD-T-043 MAILMOD-T-044
package installer

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/ingress"
	"golang.org/x/sys/unix"
)

// Uses explicit authorization and creates only randomly named test resources.
// An unknown create result is retained for review and is never adopted.
func liveCloudFixture(t *testing.T) (*Cloudflare, Spec, string) {
	t.Helper()
	credentials, dir := os.Getenv("MAIL_CLOUDFLARE_TEST_CREDENTIAL_FILE"), os.Getenv("MAIL_CLOUDFLARE_RESERVATION_TEST_DIR")
	if credentials == "" || dir == "" {
		t.Skip("explicit private credential and test directory required; no real-cloud pass")
	}
	if !filepath.IsAbs(credentials) || !filepath.IsAbs(dir) {
		t.Fatal("private input paths must be absolute")
	}
	fd, err := unix.Open(credentials, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil || !safeFile(fd, false) {
		if err == nil {
			unix.Close(fd)
		}
		t.Fatal("private credentials unavailable")
	}
	f := os.NewFile(uintptr(fd), "private-credential")
	raw, err := io.ReadAll(io.LimitReader(f, 16385))
	_ = f.Close()
	var cfg struct {
		Account string `json:"account_id"`
		Token   string `json:"token"`
	}
	if err != nil || len(raw) > 16384 || json.Unmarshal(raw, &cfg) != nil {
		t.Fatal("private credentials invalid")
	}
	client, err := NewCloudflare(cfg.Token)
	if err != nil {
		t.Fatal("private credentials invalid")
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	name := "anas-reserve-poc-" + hex.EncodeToString(nonce[:])
	spec := Spec{AccountID: cfg.Account, Worker: name, Bucket: name}
	return client, spec, dir
}

func TestCloudflareReservationReal(t *testing.T) {
	client, spec, dir := liveCloudFixture(t)
	j, err := OpenJournal(filepath.Join(dir, "reservation.jsonl"), spec)
	if err != nil {
		t.Fatal("private reservation journal unavailable")
	}
	defer func() {
		if err := j.Close(); err != nil {
			t.Error("reservation journal close failed")
		}
	}()
	defer cleanupTestResources(t, client, j, dir)
	if err := j.ReserveResources(t.Context(), client); err != nil {
		t.Fatal("real reservation did not complete; inspect private journal")
	}
	if err := j.ReserveResources(t.Context(), client); err != nil {
		t.Fatal("real reservation readback failed")
	}
	t.Log("real Worker and R2 reservation and repeat readback passed; no route or mail enabled")
}

type lostReplyClient struct {
	cloud                        *Cloudflare
	drop                         string
	workerID, bucketFingerprint  string
	workerCreates, bucketCreates int
}

func (c *lostReplyClient) CreateWorker(ctx context.Context, account, name string) (string, error) {
	c.workerCreates++
	id, err := c.cloud.CreateWorker(ctx, account, name)
	if err == nil {
		c.workerID = id
	}
	if err == nil && c.drop == "worker" {
		return "", errors.New("injected lost acknowledgement")
	}
	return id, err
}
func (c *lostReplyClient) WorkerMatches(ctx context.Context, account, id, name string) (bool, error) {
	return c.cloud.WorkerMatches(ctx, account, id, name)
}
func (c *lostReplyClient) CreateBucket(ctx context.Context, account, name string) (string, error) {
	c.bucketCreates++
	fingerprint, err := c.cloud.CreateBucket(ctx, account, name)
	if err == nil {
		c.bucketFingerprint = fingerprint
	}
	if err == nil && c.drop == "bucket" {
		return "", errors.New("injected lost acknowledgement")
	}
	return fingerprint, err
}
func (c *lostReplyClient) BucketMatches(ctx context.Context, account, name, fingerprint string) (bool, error) {
	return c.cloud.BucketMatches(ctx, account, name, fingerprint)
}

// A test-only witness retains the provider's successful response while the
// installer sees it as lost. That witness is used only to safely clean up the
// random fixtures; production recovery must never infer ownership this way.
func TestCloudflareReservationLostReplyReal(t *testing.T) {
	caseName := os.Getenv("MAIL_CLOUDFLARE_DROP_CASE")
	if caseName == "" {
		t.Skip("explicit lost-response case required; no real-cloud pass")
	}
	if caseName != "worker" && caseName != "bucket" {
		t.Fatal("invalid lost-response case")
	}
	cloud, spec, dir := liveCloudFixture(t)
	witness := &lostReplyClient{cloud: cloud, drop: caseName}
	j, err := OpenJournal(filepath.Join(dir, "reservation.jsonl"), spec)
	if err != nil {
		t.Fatal("private reservation journal unavailable")
	}
	defer func() {
		r := j.state
		if witness.workerID != "" {
			r.WorkerID = witness.workerID
		}
		if witness.bucketFingerprint != "" {
			r.Stage, r.BucketFingerprint = "reserved", witness.bucketFingerprint
		}
		f, err := os.OpenFile(filepath.Join(dir, "test-witness.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Error("test cleanup witness unavailable; resources retained")
			_ = j.Close()
			return
		}
		payload, _ := json.Marshal(map[string]string{"worker_id": witness.workerID, "bucket_fingerprint": witness.bucketFingerprint, "case": caseName})
		_, e1 := f.Write(payload)
		e2 := f.Sync()
		e3 := f.Close()
		if e1 != nil || e2 != nil || e3 != nil {
			t.Error("test cleanup witness not durable; resources retained")
			_ = j.Close()
			return
		}
		cleanupKnownResources(t, cloud, r, dir)
		if err := j.Close(); err != nil {
			t.Error("reservation journal close failed")
		}
	}()
	if !errors.Is(j.ReserveResources(t.Context(), witness), ErrUnknown) {
		t.Fatal("lost acknowledgement was not kept unknown")
	}
	if witness.workerID == "" || caseName == "bucket" && witness.bucketFingerprint == "" {
		t.Fatal("provider success was not observed by test witness")
	}
	beforeWorker, beforeBucket := witness.workerCreates, witness.bucketCreates
	if err := j.Close(); err != nil {
		t.Fatal("cannot close journal for restart")
	}
	reopened, err := OpenJournal(filepath.Join(dir, "reservation.jsonl"), spec)
	if err != nil {
		t.Fatal("cannot reopen journal for restart")
	}
	j = reopened
	if !errors.Is(j.ReserveResources(t.Context(), witness), ErrUnknown) || witness.workerCreates != beforeWorker || witness.bucketCreates != beforeBucket {
		t.Fatal("restart retried an unknown cloud create")
	}
	t.Log("provider create succeeded, acknowledgement was dropped, restart remained unknown without retry")
}

func randomTestUUID(t *testing.T) string {
	t.Helper()
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	hexID := hex.EncodeToString(raw[:])
	return hexID[:8] + "-" + hexID[8:12] + "-" + hexID[12:16] + "-" + hexID[16:20] + "-" + hexID[20:]
}

func randomTestSecret(t *testing.T) string {
	t.Helper()
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(raw[:])
}

// This test deploys only the isolated Worker itself, with no Email Routing,
// DNS change, notification target or real mailbox data.
func TestCloudflareVersionReal(t *testing.T) {
	if os.Getenv("MAIL_CLOUDFLARE_VERSION_TEST") != "1" {
		t.Skip("explicit isolated Worker deployment required; no real-cloud pass")
	}
	client, spec, dir := liveCloudFixture(t)
	j, err := OpenJournal(filepath.Join(dir, "reservation.jsonl"), spec)
	if err != nil {
		t.Fatal("private reservation journal unavailable")
	}
	defer func() {
		cleanupVersionTest(t, client, j.state, dir)
		removeTestSourceAfterCleanup(t, dir)
		if err := j.Close(); err != nil {
			t.Error("reservation journal close failed")
		}
	}()
	if err := j.ReserveResources(t.Context(), client); err != nil {
		t.Fatal("isolated resource reservation failed")
	}
	inputs := sourceInputs{Domains: []string{"isolation.example.test"}, CapacityBytes: 16 << 20, DeliveredDays: 7, NotifyOrigin: "https://notify.example.test"}
	cfg, err := j.sourceConfig(inputs)
	if err != nil {
		t.Fatal("private source credentials unavailable")
	}
	if err := j.deployVersion(t.Context(), client, cfg); err != nil {
		t.Fatal("isolated version deployment unavailable, conflicting or unknown")
	}
	if err := j.Close(); err != nil {
		t.Fatal("version journal close failed")
	}
	reopened, err := OpenJournal(filepath.Join(dir, "reservation.jsonl"), spec)
	if err != nil {
		t.Fatal("version journal reopen failed")
	}
	j = reopened
	recovered, err := j.sourceConfig(inputs)
	if err != nil || !reflect.DeepEqual(cfg, recovered) {
		t.Fatal("private source recovery failed")
	}
	if err := j.deployVersion(t.Context(), client, cfg); err != nil {
		t.Fatal("isolated repeat version readback failed")
	}
	t.Log("durable Worker version and optional notification bindings read back; no email routing or notification sent")
}

type lostVersionClient struct {
	*Cloudflare
	creates   int
	versionID string
}

type observedScheduleClient struct {
	*Cloudflare
	writes    int
	loseReply bool
}

// The endpoint test deliberately substitutes only the cron API, preventing
// background writes to its empty fixture bucket. It does not test scheduling.
type endpointTestClient struct {
	*Cloudflare
	scheduled bool
	writes    int
	loseReply bool
	t         *testing.T
}

// The endpoint test intentionally uses a synthetic routing credential. It
// does not prove routing access; a separate test exercises real read preflight.
func (c *endpointTestClient) preflightRouting(context.Context, string, string, string) error {
	return nil
}

func (c *endpointTestClient) readSchedules(context.Context, string, string, string) ([]string, error) {
	if c.scheduled {
		return []string{maintenanceCron}, nil
	}
	return []string{}, nil
}
func (c *endpointTestClient) writeMaintenanceSchedule(context.Context, string, string, string) error {
	c.scheduled = true
	return nil
}
func (c *endpointTestClient) writeEndpoint(ctx context.Context, account, id, name string) error {
	c.writes++
	err := c.Cloudflare.writeEndpoint(ctx, account, id, name)
	if err == nil && c.loseReply {
		return errors.New("injected endpoint acknowledgement loss")
	}
	return err
}
func (c *endpointTestClient) readEndpoint(ctx context.Context, account, id, name string) (bool, bool, error) {
	enabled, previews, err := c.Cloudflare.readEndpoint(ctx, account, id, name)
	c.t.Log("endpoint readback", "enabled", enabled, "previews", previews, "error", err)
	return enabled, previews, err
}
func (c *endpointTestClient) probeEndpoint(ctx context.Context, origin string, cfg versionConfig, proxy string) error {
	err := c.Cloudflare.probeEndpoint(ctx, origin, cfg, proxy)
	c.t.Log("endpoint probe", err)
	return err
}

func TestCloudflareEndpointReal(t *testing.T) {
	if os.Getenv("MAIL_CLOUDFLARE_ENDPOINT_TEST") != "1" {
		t.Skip("explicit isolated endpoint test required")
	}
	cloud, spec, dir := liveCloudFixture(t)
	client := &endpointTestClient{Cloudflare: cloud, loseReply: os.Getenv("MAIL_CLOUDFLARE_ENDPOINT_DROP") == "1", t: t}
	j, err := OpenJournal(filepath.Join(dir, "reservation.jsonl"), spec)
	if err != nil {
		t.Fatal("private journal unavailable")
	}
	defer func() {
		cleanupVersionTest(t, cloud, j.state, dir)
		removeTestSourceAfterCleanup(t, dir)
		if err := j.Close(); err != nil {
			t.Error("journal close failed")
		}
	}()
	inputs := sourceInputs{Domains: []string{"isolation.example.test"}, CapacityBytes: 16 << 20, DeliveredDays: 7}
	proxy := os.Getenv("MAIL_CLOUDFLARE_TEST_PROXY")
	req := RuntimeRequest{Domains: inputs.Domains, AllowedDomains: inputs.Domains, CapacityBytes: inputs.CapacityBytes, DeliveredDays: inputs.DeliveredDays, Proxy: proxy, ZoneID: strings.Repeat("1", 32), RoutingToken: strings.Repeat("R", 32)}
	prepared, runErr := j.prepareRuntime(t.Context(), client, req)
	if runErr != nil && j.state.Stage != "endpoint-issued" && j.state.Stage != "endpoint-configured" && j.state.Stage != "endpoint-verified" {
		t.Fatal("preparation stopped at", j.state.Stage, "with", runErr)
	}
	cfg, err := j.sourceConfig(inputs)
	if err != nil {
		t.Fatal("private source unavailable")
	}
	err = runErr
	if client.loseReply {
		if !errors.Is(err, ErrUnknown) {
			t.Fatal("lost endpoint reply accepted")
		}
		enabled, previews, e := cloud.readEndpoint(t.Context(), spec.AccountID, j.state.WorkerID, spec.Worker)
		label, e2 := cloud.accountSubdomain(t.Context(), spec.AccountID)
		origin := "https://" + spec.Worker + "." + label + ".workers.dev"
		if e != nil || e2 != nil || !enabled || previews || cloud.probeEndpoint(t.Context(), origin, cfg, proxy) != nil {
			t.Fatal("independent endpoint witness failed")
		}
	} else if spec := prepared.Specification(); err != nil || spec.ID != cfg.SourceID || spec.Generation != cfg.Generation || spec.Origin != j.state.EndpointOrigin {
		t.Fatal("endpoint check failed", err)
	}
	if !client.loseReply {
		var credential bytes.Buffer
		description, e := json.Marshal([]ingress.SourceSpec{prepared.Specification()})
		if e != nil || prepared.WriteCredentials(&credential) != nil {
			t.Fatal("private handoff encoding failed")
		}
		if _, e = ingress.ParseSources(string(description), credential.String(), inputs.Domains); e != nil {
			t.Fatal("private handoff not accepted by runtime")
		}
		merged, e := prepared.Merge(nil, inputs.Domains, inputs.CapacityBytes)
		if e != nil {
			t.Fatal("handoff merge failed")
		}
		description, e = json.Marshal(merged.Specifications())
		credential.Reset()
		if e != nil || merged.WriteCredentials(&credential) != nil {
			t.Fatal("merged handoff encoding failed")
		}
		if _, e = ingress.ParseSources(string(description), credential.String(), inputs.Domains); e != nil {
			t.Fatal("merged handoff not accepted by runtime")
		}
	}
	if err = j.Close(); err != nil {
		t.Fatal("journal close failed")
	}
	reopened, err := OpenJournal(filepath.Join(dir, "reservation.jsonl"), spec)
	if err != nil {
		t.Fatal("journal reopen failed")
	}
	j = reopened
	recovered, err := j.sourceConfig(inputs)
	if err != nil || !reflect.DeepEqual(cfg, recovered) {
		t.Fatal("source recovery failed")
	}
	_, err = j.prepareRuntime(t.Context(), client, req)
	if client.loseReply && !errors.Is(err, ErrUnknown) || !client.loseReply && err != nil || client.writes != 1 {
		t.Fatal("endpoint recovery failed", err)
	}
	t.Log("real endpoint rejects missing auth and accepts source credentials; cron simulated; no email routing; drop mode:", client.loseReply)
}

// Read-only check with an explicitly authorized, existing restricted routing
// token. It does not create resources or assert that write access is granted.
func TestCloudflareRoutingPreflightReal(t *testing.T) {
	path := os.Getenv("MAIL_CLOUDFLARE_ROUTING_TEST_CREDENTIAL_FILE")
	if path == "" {
		t.Skip("explicit private routing credential required")
	}
	if !filepath.IsAbs(path) {
		t.Fatal("absolute private credential path required")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil || !safeFile(fd, false) {
		if err == nil {
			unix.Close(fd)
		}
		t.Fatal("private credential unavailable")
	}
	f := os.NewFile(uintptr(fd), "private-routing-credential")
	raw, err := io.ReadAll(io.LimitReader(f, 16385))
	_ = f.Close()
	var cfg struct {
		Zone   string `json:"zone_id"`
		Domain string `json:"allowed_domain"`
		Token  string `json:"token"`
	}
	if err != nil || len(raw) > 16384 || json.Unmarshal(raw, &cfg) != nil || cfg.Zone != "0d84033826bc3cd0c283efdb57a90f7a" || cfg.Domain != "mail-poc.bengbu.cc.cd" {
		t.Fatal("authorized routing fixture required")
	}
	c, err := NewCloudflare("synthetic-deployment-token-for-read-only-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.preflightRouting(t.Context(), cfg.Zone, cfg.Token, os.Getenv("MAIL_CLOUDFLARE_TEST_PROXY")); err != nil {
		t.Fatal("routing read preflight failed", err)
	}
	t.Log("restricted token read one rules page; no mutation or write-permission verdict")
}

func (c *observedScheduleClient) writeMaintenanceSchedule(ctx context.Context, account, id, name string) error {
	c.writes++
	err := c.Cloudflare.writeMaintenanceSchedule(ctx, account, id, name)
	if err == nil && c.loseReply {
		return errors.New("injected lost schedule acknowledgement")
	}
	return err
}

// This test confirms API readback only, not cron execution or retention.
func TestCloudflareScheduleReal(t *testing.T) {
	if os.Getenv("MAIL_CLOUDFLARE_SCHEDULE_TEST") != "1" {
		t.Skip("explicit isolated schedule test required")
	}
	cloud, spec, dir := liveCloudFixture(t)
	client := &observedScheduleClient{Cloudflare: cloud, loseReply: os.Getenv("MAIL_CLOUDFLARE_SCHEDULE_DROP") == "1"}
	j, err := OpenJournal(filepath.Join(dir, "reservation.jsonl"), spec)
	if err != nil {
		t.Fatal("private reservation journal unavailable")
	}
	defer func() {
		cleanupVersionTest(t, cloud, j.state, dir)
		removeTestSourceAfterCleanup(t, dir)
		if err := j.Close(); err != nil {
			t.Error("reservation journal close failed")
		}
	}()
	if err := j.ReserveResources(t.Context(), client); err != nil {
		t.Fatal("isolated reservation failed")
	}
	inputs := sourceInputs{Domains: []string{"isolation.example.test"}, CapacityBytes: 16 << 20, DeliveredDays: 7}
	cfg, err := j.sourceConfig(inputs)
	if err != nil {
		t.Fatal("source unavailable")
	}
	if err := j.deployVersion(t.Context(), client, cfg); err != nil {
		t.Fatal("version deployment failed")
	}
	err = j.installSchedule(t.Context(), client, cfg)
	if client.loseReply && !errors.Is(err, ErrUnknown) || !client.loseReply && err != nil {
		t.Fatal("schedule result unexpected")
	}
	if client.loseReply {
		items, err := cloud.readSchedules(t.Context(), spec.AccountID, j.state.WorkerID, spec.Worker)
		if err != nil || len(items) != 1 || items[0] != maintenanceCron {
			t.Fatal("test witness cannot confirm real schedule")
		}
	}
	if err := j.Close(); err != nil {
		t.Fatal("journal close failed")
	}
	reopened, err := OpenJournal(filepath.Join(dir, "reservation.jsonl"), spec)
	if err != nil {
		t.Fatal("journal reopen failed")
	}
	j = reopened
	recovered, err := j.sourceConfig(inputs)
	if err != nil || !reflect.DeepEqual(cfg, recovered) {
		t.Fatal("source recovery failed")
	}
	err = j.installSchedule(t.Context(), client, cfg)
	if client.loseReply && !errors.Is(err, ErrUnknown) || !client.loseReply && err != nil || client.writes != 1 {
		t.Fatal("unsafe schedule retry")
	}
	t.Log("isolated schedule API and restart readback passed; cron execution not established")
}

func (c *lostVersionClient) createVersion(ctx context.Context, account, worker string, cfg versionConfig) (string, error) {
	c.creates++
	id, err := c.Cloudflare.createVersion(ctx, account, worker, cfg)
	if err == nil {
		c.versionID = id
		return "", errors.New("injected lost version acknowledgement")
	}
	return id, err
}

func TestCloudflareVersionLostReplyReal(t *testing.T) {
	if os.Getenv("MAIL_CLOUDFLARE_VERSION_DROP_TEST") != "1" {
		t.Skip("explicit version lost-response case required; no real-cloud pass")
	}
	cloud, spec, dir := liveCloudFixture(t)
	client := &lostVersionClient{Cloudflare: cloud}
	j, err := OpenJournal(filepath.Join(dir, "reservation.jsonl"), spec)
	if err != nil {
		t.Fatal("private reservation journal unavailable")
	}
	defer func() {
		cleanupVersionTest(t, cloud, j.state, dir)
		removeTestSourceAfterCleanup(t, dir)
		if err := j.Close(); err != nil {
			t.Error("reservation journal close failed")
		}
	}()
	if err := j.ReserveResources(t.Context(), client); err != nil {
		t.Fatal("isolated resource reservation failed")
	}
	inputs := sourceInputs{Domains: []string{"isolation.example.test"}, CapacityBytes: 16 << 20, DeliveredDays: 7}
	cfg, err := j.sourceConfig(inputs)
	if err != nil {
		t.Fatal("private source credentials unavailable")
	}
	if !errors.Is(j.deployVersion(t.Context(), client, cfg), ErrUnknown) || client.creates != 1 || !uuid.MatchString(client.versionID) {
		t.Fatal("real version success was not withheld from installer")
	}
	// This independent witness is for this test only; the installer cannot use
	// it to claim an unknown result. It also supports safe fixture cleanup.
	witness, err := os.OpenFile(filepath.Join(dir, "version-test-witness.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("private version witness unavailable")
	}
	b, _ := json.Marshal(map[string]string{"version_id": client.versionID, "operation_id": cfg.OperationID})
	_, e1 := witness.Write(b)
	e2, e3 := witness.Sync(), witness.Close()
	if e1 != nil || e2 != nil || e3 != nil {
		t.Fatal("private version witness not durable")
	}
	match, err := cloud.versionMatches(t.Context(), spec.AccountID, j.state.WorkerID, client.versionID, cfg)
	if err != nil || !match {
		t.Fatal("test witness could not independently read version")
	}
	active, err := cloud.activeVersion(t.Context(), spec.AccountID, j.state.WorkerID, spec.Worker, client.versionID)
	if err != nil || !active {
		t.Fatal("test witness could not confirm active deployment")
	}
	if err := j.Close(); err != nil {
		t.Fatal("version journal close failed")
	}
	reopened, err := OpenJournal(filepath.Join(dir, "reservation.jsonl"), spec)
	if err != nil {
		t.Fatal("version journal reopen failed")
	}
	j = reopened
	recovered, err := j.sourceConfig(inputs)
	if err != nil || !reflect.DeepEqual(cfg, recovered) {
		t.Fatal("private source recovery failed")
	}
	if !errors.Is(j.deployVersion(t.Context(), client, cfg), ErrUnknown) || client.creates != 1 {
		t.Fatal("restart retried an unknown version deployment")
	}
	t.Log("real version deployed, acknowledgement was dropped, restart remained unknown without retry")
}

// Test credentials are removed only after both isolated resources are proven
// deleted. Otherwise retain the private file for manual recovery.
func removeTestSourceAfterCleanup(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, "source-secrets.json")
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return
	}
	worker, e1 := os.ReadFile(filepath.Join(dir, "version-cleanup.jsonl"))
	bucket, e2 := os.ReadFile(filepath.Join(dir, "cleanup.jsonl"))
	if e1 != nil || e2 != nil || !strings.Contains(string(worker), `"stage":"worker-deleted-confirmed"`) || !strings.Contains(string(bucket), `"stage":"bucket-deleted-confirmed"`) {
		t.Error("test cleanup unconfirmed; private source retained")
		return
	}
	if err := os.Remove(path); err != nil {
		t.Error("test source credential removal failed")
	}
}

// Remove the deployed test Worker first, then apply the normal empty-bucket
// checks. No production or unknown resource is adopted or deleted.
func cleanupVersionTest(t *testing.T, client *Cloudflare, r record, dir string) {
	t.Helper()
	if r.WorkerID == "" {
		cleanupKnownResources(t, client, r, dir)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if !strings.HasPrefix(r.Spec.Worker, "anas-reserve-poc-") {
		t.Error("test Worker identity invalid; retained")
		return
	}
	match, err := client.WorkerMatches(ctx, r.Spec.AccountID, r.WorkerID, r.Spec.Worker)
	if err != nil || !match {
		t.Error("test Worker identity unconfirmed; resources retained")
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "version-cleanup.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Error("test Worker cleanup intent unavailable; retained")
		return
	}
	write := func(stage string) bool {
		b, _ := json.Marshal(map[string]string{"stage": stage, "worker_id": r.WorkerID})
		if _, err := f.Write(append(b, '\n')); err != nil || f.Sync() != nil {
			return false
		}
		return true
	}
	if !write("worker-delete-issued") {
		_ = f.Close()
		t.Error("test Worker cleanup intent failed; retained")
		return
	}
	status, result, err := client.request(ctx, "DELETE", r.Spec.AccountID+"/workers/workers/"+r.WorkerID, nil)
	after, _, e2 := client.request(ctx, "GET", r.Spec.AccountID+"/workers/workers/"+r.WorkerID, nil)
	if err != nil || status < 200 || status >= 300 || !result.Success || e2 != nil || after != 404 || !write("worker-deleted-confirmed") {
		_ = f.Close()
		t.Error("test Worker cleanup unconfirmed; bucket retained")
		return
	}
	_ = f.Close()
	r.WorkerID = ""
	if r.Stage == "version-issued" || r.Stage == "version-created" || r.Stage == "version-verified" || r.Stage == "schedule-issued" || r.Stage == "schedule-verified" || r.Stage == "endpoint-issued" || r.Stage == "endpoint-configured" || r.Stage == "endpoint-verified" {
		r.Stage = "reserved"
	}
	cleanupKnownResources(t, client, r, dir)
}

func cleanupTestResources(t *testing.T, c *Cloudflare, j *Journal, dir string) {
	cleanupKnownResources(t, c, j.state, dir)
}

func cleanupKnownResources(t *testing.T, c *Cloudflare, r record, dir string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if !strings.HasPrefix(r.Spec.Worker, "anas-reserve-poc-") || r.Spec.Bucket != r.Spec.Worker {
		t.Error("test resource name invalid; cleanup skipped")
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "cleanup.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Error("private cleanup journal unavailable")
		return
	}
	defer f.Close()
	record := func(stage string) bool {
		data, _ := json.Marshal(map[string]string{"stage": stage, "worker_id": r.WorkerID, "bucket": r.Spec.Bucket})
		if _, err := f.Write(append(data, '\n')); err != nil || f.Sync() != nil {
			return false
		}
		return true
	}
	if r.Stage == "bucket-issued" {
		t.Error("bucket creation outcome unknown; no bucket deletion attempted")
	} else if r.Stage == "reserved" {
		match, e := c.BucketMatches(ctx, r.Spec.AccountID, r.Spec.Bucket, r.BucketFingerprint)
		status, objects, e2 := c.request(ctx, "GET", r.Spec.AccountID+"/r2/buckets/"+r.Spec.Bucket+"/objects?per_page=1", nil)
		var items []json.RawMessage
		var info struct {
			Cursor    string `json:"cursor"`
			Truncated bool   `json:"is_truncated"`
		}
		validInfo := len(objects.Info) == 0 || string(objects.Info) == "null" || json.Unmarshal(objects.Info, &info) == nil && !info.Truncated && info.Cursor == ""
		if e != nil || !match || e2 != nil || status != 200 || !objects.Success || json.Unmarshal(objects.Result, &items) != nil || items == nil || len(items) != 0 || !validInfo {
			t.Error("test bucket ownership or emptiness unknown; retained")
		} else if !record("bucket-delete-issued") {
			t.Error("bucket cleanup intent unavailable; retained")
		} else {
			status, result, e := c.request(ctx, "DELETE", r.Spec.AccountID+"/r2/buckets/"+r.Spec.Bucket, nil)
			after, _, e2 := c.request(ctx, "GET", r.Spec.AccountID+"/r2/buckets/"+r.Spec.Bucket, nil)
			if e != nil || status < 200 || status >= 300 || !result.Success || e2 != nil || after != 404 || !record("bucket-deleted-confirmed") {
				t.Error("test bucket cleanup unconfirmed")
			}
		}
	}
	if r.WorkerID != "" {
		match, e := c.WorkerMatches(ctx, r.Spec.AccountID, r.WorkerID, r.Spec.Worker)
		if e != nil || !match || !record("worker-delete-issued") {
			t.Error("test Worker ownership or cleanup intent unknown; retained")
		} else {
			status, result, e := c.request(ctx, "DELETE", r.Spec.AccountID+"/workers/workers/"+r.WorkerID, nil)
			after, _, e2 := c.request(ctx, "GET", r.Spec.AccountID+"/workers/workers/"+r.WorkerID, nil)
			if e != nil || status < 200 || status >= 300 || !result.Success || e2 != nil || after != 404 || !record("worker-deleted-confirmed") {
				t.Error("test Worker cleanup unconfirmed")
			}
		}
	} else if r.Stage == "worker-issued" {
		t.Error("Worker creation outcome unknown; no Worker deletion attempted")
	}
}
