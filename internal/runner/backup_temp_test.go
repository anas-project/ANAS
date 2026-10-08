package runner

import (
	"archive/tar"
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------- the race

// Two workspaces on one host, or two hosts sharing a network directory, can
// back up to the same destination at once: the runtime lock only serialises
// operations inside one workspace. The startup cleanup of the second create
// must leave the first one's in-flight tree alone, and the first must still be
// able to publish once the second has finished.
func TestConcurrentBackupCreateSparesAnInFlightTransfer(t *testing.T) {
	dest := t.TempDir()
	first := startBackupWriter(t, dest)
	partial := backupStreamPath(first.root)

	workspace, _ := newSnapshotWorkspace(t)
	cleanStaleBackupTemp(dest, false)
	second, err := createBackup(workspace, &backupPlan{Mode: backupModeCopy, Dest: dest}, backupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !fileExists(partial) {
		t.Fatalf("the second create removed %s while the first backup was still writing it", first.root)
	}

	firstID := first.publish(t)
	backups, err := listBackups(dest)
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{}
	for _, manifest := range backups {
		present[manifest.BackupID] = manifest.Complete
	}
	if len(backups) != 2 || !present[firstID] || !present[second.BackupID] {
		t.Fatalf("want both backups complete, got %+v", backups)
	}
	for _, manifest := range backups {
		if problems := verifyBackup(dest, manifest, present); len(problems) != 0 {
			t.Errorf("backup %s does not verify: %+v", manifest.BackupID, problems)
		}
		if fileExists(backupTempOwnerPath(backupRoot(dest, manifest.BackupID))) {
			t.Errorf("published backup %s still carries its ownership record", manifest.BackupID)
		}
	}
	assertNoBackupDebris(t, dest)
}

// A tree written by a release that predates ownership records has nothing to
// prove it abandoned, so it has to survive as well.
func TestConcurrentBackupCreateSparesAnUnrecordedTransfer(t *testing.T) {
	dest := t.TempDir()
	root := backupTempRoot(dest, "20260928T000000Z-0badf00d")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	partial := backupStreamPath(root)
	if err := os.WriteFile(partial, []byte("half a send stream"), 0600); err != nil {
		t.Fatal(err)
	}

	workspace, _ := newSnapshotWorkspace(t)
	cleanStaleBackupTemp(dest, false)
	if _, err := createBackup(workspace, &backupPlan{Mode: backupModeCopy, Dest: dest}, backupOptions{}); err != nil {
		t.Fatal(err)
	}
	if !fileExists(partial) {
		t.Fatalf("the second create removed the unrecorded in-flight tree %s", root)
	}
}

// The other half of the contract: once its writer is gone the tree is debris,
// and the next create clears it, including when the writer was killed and
// never ran its own cleanup.
func TestBackupCleanupReapsATreeWhoseWriterDied(t *testing.T) {
	requireLocalProcessLookup(t)
	dest := t.TempDir()
	writer := startBackupWriter(t, dest)
	writer.kill(t)

	outcomes := reapBackupTemps(dest, currentProcessIdentity(), time.Now())
	if len(outcomes) != 1 || outcomes[0].path != writer.root || !outcomes[0].verdict.abandoned || outcomes[0].err != nil {
		t.Fatalf("want the dead writer's tree reaped, got %+v", outcomes)
	}
	if !strings.Contains(outcomes[0].verdict.why, "has exited") {
		t.Errorf("verdict does not say why: %q", outcomes[0].verdict.why)
	}
	assertNoBackupDebris(t, dest)
}

// A killed writer whose parent has not collected it yet is a zombie: it still
// has a PID and answers kill(0), but it will never write again.
func TestBackupCleanupTreatsAZombieWriterAsGone(t *testing.T) {
	requireLocalProcessLookup(t)
	dest := t.TempDir()
	writer := startBackupWriter(t, dest)
	if err := writer.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	pid := writer.cmd.Process.Pid
	deadline := time.Now().Add(10 * time.Second)
	for inspectProcess(pid).status != processGone {
		if time.Now().After(deadline) {
			t.Fatalf("killed writer %d still reported as running: %+v", pid, inspectProcess(pid))
		}
		time.Sleep(10 * time.Millisecond)
	}

	outcomes := reapBackupTemps(dest, currentProcessIdentity(), time.Now())
	if len(outcomes) != 1 || !outcomes[0].verdict.abandoned || outcomes[0].err != nil {
		t.Fatalf("want the zombie writer's tree reaped, got %+v", outcomes)
	}
}

// ---------------------------------------------------------------- verdicts

// Every doubt resolves towards keeping a tree. Only a record that proves the
// writer gone lets cleanup remove anything that is not empty.
func TestJudgeBackupTempRequiresProofOfAbandonment(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	self := processIdentity{bootID: "boot-a", pidNamespace: "pid:[4026531836]", pid: 100, start: "900"}
	probes := map[int]processProbe{
		200: {status: processGone},
		201: {status: processRunning, start: "1000"},
		202: {status: processRunning, start: "5555"},
		204: {status: processRunning},
	}
	originalProbe := probeProcess
	probeProcess = func(pid int) processProbe { return probes[pid] }
	t.Cleanup(func() { probeProcess = originalProbe })
	setBackupTempClaimed("mine-live", true)
	t.Cleanup(func() { setBackupTempClaimed("mine-live", false) })

	stamp := func(at time.Time) string { return at.UTC().Format(time.RFC3339) }
	record := func(id, boot, namespace string, pid int, start string, beat time.Time) *backupTempOwner {
		return &backupTempOwner{
			APIVersion: backupTempOwnerAPIVersion, BackupID: id, Token: "t", Host: "nas2",
			BootID: boot, PIDNamespace: namespace, PID: pid, ProcessStart: start,
			StartedAt: stamp(beat), HeartbeatAt: stamp(beat),
		}
	}
	// A local record's heartbeat is long stale on purpose: in the same PID
	// space the process is asked, and the heartbeat must not decide anything.
	local := func(pid int, start string) *backupTempOwner {
		return record("other", self.bootID, self.pidNamespace, pid, start, now.Add(-24*time.Hour))
	}
	remote := func(boot, namespace string, beat time.Time) *backupTempOwner {
		// PID 200 is gone in this PID space. A remote record naming it proves
		// the lookup is not attempted across spaces.
		return record("other", boot, namespace, 200, "1000", beat)
	}
	unreadableBeat := remote("boot-b", "pid:[1]", now)
	unreadableBeat.HeartbeatAt = "yesterday"
	futureVersion := local(200, "1000")
	futureVersion.APIVersion = "anas.dev/backup-owner/v2"

	type want int
	const (
		keepQuietly want = iota
		keepAndReport
		reap
	)
	cases := []struct {
		name   string
		owner  *backupTempOwner
		raw    string
		empty  bool
		age    time.Duration
		expect want
	}{
		{name: "local writer exited", owner: local(200, "1000"), expect: reap},
		{name: "local writer running", owner: local(201, "1000"), expect: keepQuietly},
		{name: "local PID reused by a later process", owner: local(202, "1000"), expect: reap},
		{name: "local writer cannot be looked up", owner: local(203, "1000"), expect: keepAndReport},
		{name: "local writer start time unreadable", owner: local(204, "1000"), expect: keepQuietly},
		{name: "this process, claim held", owner: record("mine-live", self.bootID, self.pidNamespace, self.pid, self.start, now), expect: keepQuietly},
		{name: "this process, claim released", owner: record("mine-gone", self.bootID, self.pidNamespace, self.pid, self.start, now), expect: reap},
		{name: "other host, fresh heartbeat", owner: remote("boot-b", "pid:[1]", now.Add(-time.Minute)), expect: keepQuietly},
		{name: "other host, heartbeat stale", owner: remote("boot-b", "pid:[1]", now.Add(-backupTempStaleAfter-time.Minute)), expect: reap},
		{name: "other host, clock ahead of ours", owner: remote("boot-b", "pid:[1]", now.Add(10*time.Minute)), expect: keepQuietly},
		{name: "other host, heartbeat unreadable", owner: unreadableBeat, expect: keepAndReport},
		{name: "same boot, other PID namespace", owner: remote(self.bootID, "pid:[4026532999]", now.Add(-time.Minute)), expect: keepQuietly},
		{name: "same boot, other PID namespace, stale", owner: remote(self.bootID, "pid:[4026532999]", now.Add(-backupTempStaleAfter-time.Minute)), expect: reap},
		{name: "writer's boot unknown", owner: remote("", self.pidNamespace, now.Add(-time.Minute)), expect: keepQuietly},
		{name: "record from a newer release", owner: futureVersion, expect: keepAndReport},
		{name: "record unreadable", raw: "{{{ not yaml", expect: keepAndReport},
		{name: "no record, recent", age: time.Minute, expect: keepQuietly},
		{name: "no record, recent and empty", empty: true, age: time.Minute, expect: keepQuietly},
		{name: "no record, old", age: 2 * backupTempStaleAfter, expect: keepAndReport},
		{name: "no record, old and empty", empty: true, age: 2 * backupTempStaleAfter, expect: reap},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := backupTempRoot(t.TempDir(), "20260928T115900Z-00000001")
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			switch {
			case tc.owner != nil:
				if err := createBackupTempOwner(root, tc.owner); err != nil {
					t.Fatal(err)
				}
			case tc.raw != "":
				if err := os.WriteFile(backupTempOwnerPath(root), []byte(tc.raw), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if !tc.empty {
				if err := os.WriteFile(backupStreamPath(root), []byte("partial"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			verdict := judgeBackupTemp(root, now.Add(-tc.age), self, now)
			got := keepAndReport
			switch {
			case verdict.abandoned:
				got = reap
			case verdict.quiet:
				got = keepQuietly
			}
			if got != tc.expect {
				t.Fatalf("verdict %+v, want %v", verdict, []string{"keep quietly", "keep and report", "reap"}[tc.expect])
			}
			if verdict.why == "" {
				t.Error("a verdict has to say why")
			}
		})
	}
}

// The command name in /proc/<pid>/stat is free text in parentheses, so a
// process called "anas (backup) x" must not shift the fields after it.
func TestParseProcStatCountsFieldsFromTheLastParenthesis(t *testing.T) {
	tail := " 1 4242 4242 0 -1 4194560 100 0 0 0 5 3 0 0 20 0 3 0 987654 1234567 89"
	state, start, ok := parseProcStat("4242 (anas (backup) x) S" + tail)
	if !ok || state != 'S' || start != "987654" {
		t.Errorf("got state %q start %q ok %v", state, start, ok)
	}
	if state, _, ok := parseProcStat("4243 (anas) Z" + tail); !ok || state != 'Z' {
		t.Errorf("zombie state read as %q, ok %v", state, ok)
	}
	for _, malformed := range []string{"", "4244 anas S 1 2", "4245 (anas) S 1 2 3"} {
		if _, _, ok := parseProcStat(malformed); ok {
			t.Errorf("parsed malformed stat %q", malformed)
		}
	}
}

// ---------------------------------------------------------------- claims

// If a cleanup elsewhere takes the tree away mid-transfer, the writer must not
// publish whatever now sits at the old path. The manifest write recreates a
// missing directory, so without the check the result would be a backup that
// holds nothing but a manifest claiming it is complete.
func TestBackupClaimRefusesToPublishATreeItNoLongerHolds(t *testing.T) {
	for _, recreate := range []bool{false, true} {
		t.Run(fmt.Sprintf("transfer recreates the directory=%v", recreate), func(t *testing.T) {
			dest := t.TempDir()
			claim, err := claimBackupTemp(dest)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(backupStreamPath(claim.root), []byte("partial"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := discardBackupTemp(dest, claim.root); err != nil {
				t.Fatal(err)
			}
			if recreate {
				if err := os.MkdirAll(filepath.Join(claim.root, "meta"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			err = claim.publish(&backupManifest{
				BackupID: claim.id, Mode: backupModeSendFile, CreatedAt: nowUTC(),
				Channels: []string{backupChannelData, backupChannelMetadata}, Complete: true,
			})
			var cliErr *CLIError
			if !errors.As(err, &cliErr) || cliErr.Code != "backup_temp_lost" {
				t.Fatalf("want backup_temp_lost, got %v", err)
			}
			claim.discard()
			if backups, err := listBackups(dest); err != nil || len(backups) != 0 {
				t.Fatalf("a lost tree was published: %+v, %v", backups, err)
			}
			assertNoBackupDebris(t, dest)
		})
	}
}

func TestBackupClaimDiscardLeavesNothingBehind(t *testing.T) {
	dest := t.TempDir()
	claim, err := claimBackupTemp(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !backupTempClaimed(claim.id) {
		t.Fatal("a live claim is not registered")
	}
	if err := os.MkdirAll(filepath.Join(claim.root, "meta"), 0700); err != nil {
		t.Fatal(err)
	}
	claim.discard()
	if backupTempClaimed(claim.id) {
		t.Error("a discarded claim is still registered")
	}
	assertNoBackupDebris(t, dest)
}

// The heartbeat rewrites the record in place and never creates it: once the
// tree has been taken away, planting a fresh record in a stand-in directory
// would forge the proof the publish check relies on.
func TestBackupClaimHeartbeatNeverRecreatesItsRecord(t *testing.T) {
	dest := t.TempDir()
	claim, err := claimBackupTemp(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.discard()
	claim.stopHeartbeat()

	later := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := claim.beat(later); err != nil {
		t.Fatal(err)
	}
	var owner backupTempOwner
	if err := readYAML(backupTempOwnerPath(claim.root), &owner); err != nil {
		t.Fatal(err)
	}
	if owner.HeartbeatAt != "2030-01-02T03:04:05Z" || owner.Token != claim.owner.Token || owner.StartedAt != claim.owner.StartedAt {
		t.Fatalf("heartbeat rewrote the record wrongly: %+v", owner)
	}

	if err := discardBackupTemp(dest, claim.root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(claim.root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := claim.beat(later.Add(time.Minute)); !os.IsNotExist(err) {
		t.Fatalf("want a missing record reported, got %v", err)
	}
	if fileExists(backupTempOwnerPath(claim.root)) {
		t.Fatal("the heartbeat planted a record in a stand-in directory")
	}
}

func TestBackupClaimHeartbeatRuns(t *testing.T) {
	var clock atomic.Pointer[time.Time]
	originalInterval, originalNow := backupTempHeartbeatInterval, backupTempNow
	backupTempHeartbeatInterval = 5 * time.Millisecond
	backupTempNow = func() time.Time {
		if at := clock.Load(); at != nil {
			return *at
		}
		return time.Now()
	}
	t.Cleanup(func() { backupTempHeartbeatInterval, backupTempNow = originalInterval, originalNow })

	dest := t.TempDir()
	claim, err := claimBackupTemp(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.discard()
	// Only a beat can put this time into the record.
	later := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	clock.Store(&later)

	deadline := time.Now().Add(10 * time.Second)
	for {
		var owner backupTempOwner
		if err := readYAML(backupTempOwnerPath(claim.root), &owner); err == nil && owner.HeartbeatAt == "2030-01-02T03:04:05Z" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the heartbeat never refreshed the record")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ---------------------------------------------------------------- cleanup

func TestBackupCleanupFinishesAbandonedTreesAndLeavesBackupsAlone(t *testing.T) {
	dest := t.TempDir()
	writeManifest(t, backupRoot(dest, "published"), &backupManifest{
		BackupID: "published", Mode: backupModeCopy, CreatedAt: "2026-09-28T00:00:00Z", Complete: true,
	})
	leftover := filepath.Join(dest, backupAbandonedPrefix+"20260927T000000Z-00000002-1a2b3c4d")
	if err := os.MkdirAll(filepath.Join(leftover, "data"), 0700); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(dest, backupTempPrefix+"not-a-directory")
	if err := os.WriteFile(stray, nil, 0600); err != nil {
		t.Fatal(err)
	}

	outcomes := reapBackupTemps(dest, currentProcessIdentity(), time.Now())
	if len(outcomes) != 1 || outcomes[0].path != leftover || outcomes[0].err != nil {
		t.Fatalf("want only the abandoned leftover handled, got %+v", outcomes)
	}
	if exists(leftover) {
		t.Error("an abandoned leftover survived cleanup")
	}
	if !fileExists(backupManifestPath(backupRoot(dest, "published"))) || !fileExists(stray) {
		t.Error("cleanup touched something that is not temporary")
	}
}

// Under --json the warnings are JSON Lines on stderr with enumerated codes.
func TestBackupCleanupWarningsCarryCodes(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can remove a directory it cannot write, so the removal failure cannot be staged")
	}
	originalNow := backupTempNow
	backupTempNow = func() time.Time { return time.Now().Add(2 * backupTempStaleAfter) }
	t.Cleanup(func() { backupTempNow = originalNow })

	dest := t.TempDir()
	unrecorded := backupTempRoot(dest, "20260928T000000Z-00000003")
	if err := os.MkdirAll(unrecorded, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupStreamPath(unrecorded), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(dest, backupAbandonedPrefix+"20260927T000000Z-00000004-1a2b3c4d", "meta")
	if err := os.MkdirAll(locked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "config.yml"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0700) })

	stderr := captureBackupStderr(t, func() { cleanStaleBackupTemp(dest, true) })
	codes := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
		var record struct{ Type, Code, Message string }
		if err := json.Unmarshal([]byte(line), &record); err != nil || record.Type != "warning" {
			t.Fatalf("stderr line is not a JSON warning: %q", line)
		}
		codes[record.Code] = record.Message
	}
	if !strings.Contains(codes["backup_temp_kept"], unrecorded) {
		t.Errorf("want backup_temp_kept naming %s, got %v", unrecorded, codes)
	}
	if _, ok := codes["backup_temp_removal_failed"]; !ok {
		t.Errorf("want backup_temp_removal_failed, got %v", codes)
	}
	if !exists(unrecorded) {
		t.Error("an unrecorded tree that is not empty was removed")
	}
}

// ---------------------------------------------------------------- helpers

func assertNoBackupDebris(t *testing.T, dest string) {
	t.Helper()
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			t.Errorf("left behind at the destination: %s", entry.Name())
		}
	}
}

// requireLocalProcessLookup skips where a writer's PID cannot be looked up:
// there cleanup relies on the heartbeat alone, which these tests do not wait
// out.
func requireLocalProcessLookup(t *testing.T) {
	t.Helper()
	if self := currentProcessIdentity(); self.bootID == "" || self.start == "" {
		t.Skip("this platform cannot name its PID space or read process start times")
	}
}

func captureBackupStderr(t *testing.T, run func()) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	realErr := os.Stderr
	os.Stderr = file
	run()
	os.Stderr = realErr
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	return string(b)
}

// ---------------------------------------------------------------- the other process

const backupWriterHelperMarker = "ANAS_BACKUP_TEMP_WRITER_HELPER"

// backupWriter is a create in another process, stopped mid-transfer: it has
// claimed a temporary tree and written part of a stream into it, exactly as
// createBackup does, and holds there until told to publish.
type backupWriter struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *bytes.Buffer
	root   string
	exited bool
}

func startBackupWriter(t *testing.T, dest string) *backupWriter {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestBackupTempWriterHelper$")
	cmd.Env = append(os.Environ(), backupWriterHelperMarker+"=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	writer := &backupWriter{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), stderr: &bytes.Buffer{}}
	cmd.Stderr = writer.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		if !writer.exited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	if _, err := fmt.Fprintln(stdin, dest); err != nil {
		t.Fatal(err)
	}
	writer.root = writer.expect(t, "ready")
	return writer
}

// expect reads protocol lines until the one named, skipping whatever the test
// framework prints around them.
func (w *backupWriter) expect(t *testing.T, verb string) string {
	t.Helper()
	for {
		line, err := w.stdout.ReadString('\n')
		if err != nil {
			_ = w.cmd.Wait()
			w.exited = true
			t.Fatalf("backup writer exited before %q: %v\nstderr:\n%s", verb, err, w.stderr.String())
		}
		verbAndArg := strings.SplitN(strings.TrimSpace(line), " ", 2)
		switch {
		case len(verbAndArg) == 2 && verbAndArg[0] == verb:
			return verbAndArg[1]
		case verbAndArg[0] == "error":
			t.Fatalf("backup writer failed: %s", strings.TrimSpace(line))
		}
	}
}

func (w *backupWriter) publish(t *testing.T) string {
	t.Helper()
	if _, err := fmt.Fprintln(w.stdin, "publish"); err != nil {
		t.Fatal(err)
	}
	id := w.expect(t, "published")
	_ = w.stdin.Close()
	err := w.cmd.Wait()
	w.exited = true
	if err != nil {
		t.Fatalf("backup writer: %v\nstderr:\n%s", err, w.stderr.String())
	}
	return id
}

func (w *backupWriter) kill(t *testing.T) {
	t.Helper()
	if err := w.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = w.cmd.Wait()
	w.exited = true
}

// TestBackupTempWriterHelper is the other process in the tests above. It does
// nothing unless startBackupWriter started it.
func TestBackupTempWriterHelper(t *testing.T) {
	if os.Getenv(backupWriterHelperMarker) == "" {
		t.Skip("helper process for the backup temp tests")
	}
	fail := func(format string, args ...any) {
		fmt.Printf("error "+format+"\n", args...)
		os.Exit(1)
	}
	in := bufio.NewReader(os.Stdin)
	dest, err := in.ReadString('\n')
	if err != nil {
		fail("read the destination: %v", err)
	}
	claim, err := claimBackupTemp(strings.TrimSpace(dest))
	if err != nil {
		fail("claim: %v", err)
	}
	stream := []byte("the first half of a send stream, ")
	if err := os.WriteFile(backupStreamPath(claim.root), stream, 0600); err != nil {
		fail("write: %v", err)
	}
	fmt.Printf("ready %s\n", claim.root)

	if command, _ := in.ReadString('\n'); strings.TrimSpace(command) != "publish" {
		os.Exit(0)
	}
	stream = append(stream, "and the second"...)
	if err := os.WriteFile(backupStreamPath(claim.root), stream, 0600); err != nil {
		fail("write: %v", err)
	}
	var metadata bytes.Buffer
	archive := tar.NewWriter(&metadata)
	deployment := []byte("api_version: " + deploymentAPIVersion + "\nmodules: {}\n")
	if err := archive.WriteHeader(&tar.Header{Name: "deployment/deployment.yml", Mode: 0600, Size: int64(len(deployment))}); err != nil {
		fail("metadata header: %v", err)
	}
	if _, err := archive.Write(deployment); err != nil {
		fail("metadata body: %v", err)
	}
	if err := archive.Close(); err != nil {
		fail("metadata close: %v", err)
	}
	if err := os.WriteFile(backupMetaTarPath(claim.root), metadata.Bytes(), 0600); err != nil {
		fail("write: %v", err)
	}
	manifest := &backupManifest{
		BackupID: claim.id, Mode: backupModeSendFile, CreatedAt: nowUTC(), SizeBytes: int64(len(stream)),
		Channels: []string{backupChannelData, backupChannelMetadata}, Complete: true,
	}
	if err := claim.publish(manifest); err != nil {
		claim.discard()
		fail("publish: %v", err)
	}
	fmt.Printf("published %s\n", claim.id)
}

// The record is YAML a person may read while chasing a warning, so its shape
// is pinned: every field that decides a verdict is present by name.
func TestBackupTempOwnerRecordNamesItsWriter(t *testing.T) {
	dest := t.TempDir()
	claim, err := claimBackupTemp(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.discard()
	b, err := os.ReadFile(backupTempOwnerPath(claim.root))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := yaml.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"api_version", "backup_id", "token", "pid", "started_at", "heartbeat_at"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("owner.yml lacks %s:\n%s", key, b)
		}
	}
	if fields["pid"] != os.Getpid() || fields["backup_id"] != claim.id {
		t.Errorf("owner.yml names the wrong writer:\n%s", b)
	}
}
