package runner

import (
	"archive/tar"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The mode table is the part of backup that decides whether an operation is
// even attempted, so it is exercised as a pure function of stated facts rather
// than against whatever filesystem the test machine happens to have. That also
// means the Btrfs cases run on macOS, which is where most of the development
// happens and none of the Btrfs exists.
func TestBackupModeSelection(t *testing.T) {
	btrfsSource := backupSourceInfo{FSType: "btrfs", FSID: "aaaa", DataIsSubvolume: true, DataFullyReadable: true}
	plainSource := backupSourceInfo{FSType: "ext4", DataFullyReadable: true}

	cases := []struct {
		name  string
		caps  backupCapabilities
		facts backupFacts
		want  map[string]string // mode -> "" for available, else the reason
	}{
		{
			name: "no destination leaves every dependent mode unavailable",
			caps: backupCapabilities{
				Source: btrfsSource, Tools: map[string]bool{"btrfs": true}, Privileged: true,
			},
			want: map[string]string{
				backupModeSnapshot: reasonDestNotSpecified,
				backupModeSend:     reasonDestNotSpecified,
				backupModeSendFile: reasonDestNotSpecified,
				backupModeCopy:     reasonDestNotSpecified,
			},
		},
		{
			name: "same Btrfs filesystem offers every mode",
			caps: backupCapabilities{
				Source: btrfsSource, Tools: map[string]bool{"btrfs": true}, Privileged: true,
				Dest: &backupDestInfo{Path: "/data/backup", Exists: true, Writable: true, FSType: "btrfs"},
			},
			facts: backupFacts{destBtrfs: true, sameFilesystem: true},
			want: map[string]string{
				backupModeSnapshot: "", backupModeSend: "", backupModeSendFile: "", backupModeCopy: "",
			},
		},
		{
			name: "a different Btrfs rules out snapshot but not send",
			caps: backupCapabilities{
				Source: btrfsSource, Tools: map[string]bool{"btrfs": true}, Privileged: true,
				Dest: &backupDestInfo{Path: "/mnt/other", Exists: true, Writable: true, FSType: "btrfs"},
			},
			facts: backupFacts{destBtrfs: true, sameFilesystem: false},
			want: map[string]string{
				backupModeSnapshot: reasonDestNotSameFilesystem,
				backupModeSend:     "", backupModeSendFile: "", backupModeCopy: "",
			},
		},
		{
			name: "an ext4 destination leaves send-file and copy",
			caps: backupCapabilities{
				Source: btrfsSource, Tools: map[string]bool{"btrfs": true}, Privileged: true,
				Dest: &backupDestInfo{Path: "/mnt/usb", Exists: true, Writable: true, FSType: "ext4"},
			},
			want: map[string]string{
				backupModeSnapshot: reasonDestNotBtrfs,
				backupModeSend:     reasonDestNotBtrfs,
				backupModeSendFile: "", backupModeCopy: "",
			},
		},
		{
			// The measured fact that shapes the whole design: creating and
			// snapshotting subvolumes are unprivileged, sending is not. Both
			// send modes have to say so here rather than fail after the
			// containers have already been stopped.
			name: "without CAP_SYS_ADMIN both send modes report insufficient_privilege",
			caps: backupCapabilities{
				Source: btrfsSource, Tools: map[string]bool{"btrfs": true}, Privileged: false,
				Dest: &backupDestInfo{Path: "/mnt/other", Exists: true, Writable: true, FSType: "btrfs"},
			},
			facts: backupFacts{destBtrfs: true},
			want: map[string]string{
				backupModeSnapshot: reasonDestNotSameFilesystem,
				backupModeSend:     reasonInsufficientPrivilege,
				backupModeSendFile: reasonInsufficientPrivilege,
				backupModeCopy:     "",
			},
		},
		{
			name: "a non-Btrfs source leaves copy as the only mode",
			caps: backupCapabilities{
				Source: plainSource, Tools: map[string]bool{"btrfs": true}, Privileged: true,
				Dest: &backupDestInfo{Path: "/mnt/usb", Exists: true, Writable: true, FSType: "ext4"},
			},
			want: map[string]string{
				backupModeSnapshot: reasonSourceNotBtrfs,
				backupModeSend:     reasonSourceNotBtrfs,
				backupModeSendFile: reasonSourceNotBtrfs,
				backupModeCopy:     "",
			},
		},
		{
			name: "Btrfs without a subvolume data directory is not a snapshot source",
			caps: backupCapabilities{
				Source: backupSourceInfo{FSType: "btrfs", DataFullyReadable: true},
				Tools:  map[string]bool{"btrfs": true}, Privileged: true,
				Dest: &backupDestInfo{Path: "/mnt/usb", Exists: true, Writable: true},
			},
			want: map[string]string{
				backupModeSnapshot: reasonDataNotSubvolume,
				backupModeSend:     reasonDataNotSubvolume,
				backupModeSendFile: reasonDataNotSubvolume,
				backupModeCopy:     "",
			},
		},
		{
			// A mount point cannot be renamed aside, and the restore path does
			// exactly that, so a backup taken here could never be put back.
			name: "a data directory that is a mount point is refused",
			caps: backupCapabilities{
				Source: backupSourceInfo{FSType: "btrfs", DataIsSubvolume: true, DataIsMountpoint: true, DataFullyReadable: true},
				Tools:  map[string]bool{"btrfs": true}, Privileged: true,
				Dest: &backupDestInfo{Path: "/mnt/usb", Exists: true, Writable: true},
			},
			want: map[string]string{
				backupModeSnapshot: reasonDataIsMountpoint,
				backupModeSend:     reasonDataIsMountpoint,
				backupModeSendFile: reasonDataIsMountpoint,
				backupModeCopy:     "",
			},
		},
		{
			name: "an unwritable destination stops everything",
			caps: backupCapabilities{
				Source: btrfsSource, Tools: map[string]bool{"btrfs": true}, Privileged: true,
				Dest: &backupDestInfo{Path: "/mnt/ro", Exists: true, Writable: false},
			},
			want: map[string]string{
				backupModeSnapshot: reasonDestNotWritable,
				backupModeSend:     reasonDestNotWritable,
				backupModeSendFile: reasonDestNotWritable,
				backupModeCopy:     reasonDestNotWritable,
			},
		},
		{
			// The contract's table has copy needing nothing but a writable
			// destination. On any host where containers have run, data/ holds
			// root-owned files an ordinary user cannot read, and a copy that
			// skipped them would publish a backup with holes in it. The Btrfs
			// modes are unaffected: none of them reads a file.
			name: "data written by root rules out copy but not the Btrfs modes",
			caps: backupCapabilities{
				Source: backupSourceInfo{FSType: "btrfs", DataIsSubvolume: true, DataFullyReadable: false},
				Tools:  map[string]bool{"btrfs": true}, Privileged: true,
				Dest: &backupDestInfo{Path: "/mnt/other", Exists: true, Writable: true, FSType: "btrfs"},
			},
			facts: backupFacts{destBtrfs: true, sameFilesystem: true},
			want: map[string]string{
				backupModeSnapshot: "", backupModeSend: "", backupModeSendFile: "",
				backupModeCopy: reasonInsufficientPrivilege,
			},
		},
		{
			name: "a missing btrfs binary is reported as such, not as insufficient privilege",
			caps: backupCapabilities{
				Source: btrfsSource, Tools: map[string]bool{"btrfs": false}, Privileged: true,
				Dest: &backupDestInfo{Path: "/mnt/usb", Exists: true, Writable: true},
			},
			want: map[string]string{
				backupModeSnapshot: reasonBtrfsToolMissing,
				backupModeSend:     reasonBtrfsToolMissing,
				backupModeSendFile: reasonBtrfsToolMissing,
				backupModeCopy:     "",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			modes := evaluateBackupModes(&tc.caps, tc.facts)
			if len(modes) != 4 {
				t.Fatalf("expected 4 modes, got %d", len(modes))
			}
			for id, wantReason := range tc.want {
				mode, ok := backupModeReportFor(modes, id)
				if !ok {
					t.Fatalf("mode %s missing from the report", id)
				}
				if wantReason == "" {
					if !mode.Available {
						t.Errorf("mode %s: want available, got unavailable (%s)", id, mode.Reason)
					}
					continue
				}
				if mode.Available {
					t.Errorf("mode %s: want unavailable (%s), got available", id, wantReason)
					continue
				}
				if mode.Reason != wantReason {
					t.Errorf("mode %s: want reason %s, got %s", id, wantReason, mode.Reason)
				}
			}
		})
	}
}

// A backup exists to survive the loss of the source disk, so the mode that
// writes to that same disk must never be what gets recommended when anything
// else is possible.
func TestBackupRecommendationPrefersOffDisk(t *testing.T) {
	cases := []struct {
		name  string
		modes []backupModeReport
		want  string
	}{
		{
			name: "send wins over everything",
			modes: []backupModeReport{
				{ID: backupModeSnapshot, Available: true}, {ID: backupModeSend, Available: true},
				{ID: backupModeSendFile, Available: true}, {ID: backupModeCopy, Available: true},
			},
			want: backupModeSend,
		},
		{
			name: "send-file beats copy",
			modes: []backupModeReport{
				{ID: backupModeSnapshot, Available: true}, {ID: backupModeSend, Available: false},
				{ID: backupModeSendFile, Available: true}, {ID: backupModeCopy, Available: true},
			},
			want: backupModeSendFile,
		},
		{
			name: "snapshot is only recommended when it is the last option",
			modes: []backupModeReport{
				{ID: backupModeSnapshot, Available: true}, {ID: backupModeSend, Available: false},
				{ID: backupModeSendFile, Available: false}, {ID: backupModeCopy, Available: false},
			},
			want: backupModeSnapshot,
		},
		{
			name:  "nothing available recommends nothing",
			modes: []backupModeReport{{ID: backupModeCopy, Available: false}},
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := recommendBackupMode(tc.modes); got != tc.want {
				t.Errorf("recommended %q, want %q", got, tc.want)
			}
		})
	}
}

// Writing to anywhere outside the workspace carries config.yml and the
// generated secret store off this host in the clear. Failing to say so is a
// security regression that produces no visible symptom.
func TestBackupWarnsWhenSecretsLeaveTheHost(t *testing.T) {
	caps := backupCapabilities{
		Source: backupSourceInfo{FSType: "btrfs", DataIsSubvolume: true, DataFullyReadable: true},
		Tools:  map[string]bool{"btrfs": true}, Privileged: true,
		Dest: &backupDestInfo{Path: "/mnt/usb", Exists: true, Writable: true},
	}
	modes := evaluateBackupModes(&caps, backupFacts{destLeavesHost: true})
	copyMode, _ := backupModeReportFor(modes, backupModeCopy)
	if !containsString(copyMode.Notes, notePlaintextSecrets) {
		t.Errorf("a destination off this host must carry %s; got %v", notePlaintextSecrets, copyMode.Notes)
	}

	modes = evaluateBackupModes(&caps, backupFacts{destLeavesHost: false})
	copyMode, _ = backupModeReportFor(modes, backupModeCopy)
	if containsString(copyMode.Notes, notePlaintextSecrets) {
		t.Errorf("a destination inside the workspace must not claim secrets are leaving; got %v", copyMode.Notes)
	}
}

// Mount table parsing is what stands between the fsid trap and a wrong
// answer, so the awkward shapes are pinned down: a variable number of optional
// fields before the separator, and octal-escaped paths.
func TestMountEntryParsing(t *testing.T) {
	if len(readMounts()) == 0 && !fileExists("/proc/self/mountinfo") {
		t.Skip("no /proc/self/mountinfo on this host")
	}
	for _, path := range []string{"/", os.TempDir()} {
		if _, ok := mountEntryFor(path); !ok {
			t.Errorf("no mount entry found for %s", path)
		}
	}
}

func TestUnescapeMountField(t *testing.T) {
	cases := map[string]string{
		`/mnt/plain`:         "/mnt/plain",
		`/mnt/with\040space`: "/mnt/with space",
		`/mnt/tab\011here`:   "/mnt/tab\there",
		`/mnt/back\134slash`: `/mnt/back\slash`,
		`/mnt/trailing\`:     `/mnt/trailing\`,
	}
	for in, want := range cases {
		if got := unescapeMountField(in); got != want {
			t.Errorf("unescapeMountField(%q) = %q, want %q", in, got, want)
		}
	}
}

// /datastore must not be treated as living under /data, or the longest-prefix
// mount lookup picks the wrong filesystem for any path that shares a prefix
// with a mount point.
func TestPathWithinComparesWholeComponents(t *testing.T) {
	cases := []struct {
		path, root string
		want       bool
	}{
		{"/data/ws", "/data", true},
		{"/data", "/data", true},
		{"/datastore/ws", "/data", false},
		{"/anything", "/", true},
		{"/data", "/data/ws", false},
	}
	for _, tc := range cases {
		if got := pathWithin(tc.path, tc.root); got != tc.want {
			t.Errorf("pathWithin(%q, %q) = %t, want %t", tc.path, tc.root, got, tc.want)
		}
	}
}

// An increment is meaningless without its ancestors, so the chain has to be
// resolved before anything is written — and produce the parent-first order a
// `btrfs receive` sequence needs.
func TestBackupChainOrdersOldestFirst(t *testing.T) {
	all := []backupManifest{
		{BackupID: "c", Parent: "b", Complete: true},
		{BackupID: "b", Parent: "a", Complete: true},
		{BackupID: "a", Complete: true},
	}
	chain, err := backupChain(all, "c")
	if err != nil {
		t.Fatalf("backupChain: %v", err)
	}
	want := []string{"a", "b", "c"}
	if len(chain) != len(want) {
		t.Fatalf("chain has %d links, want %d", len(chain), len(want))
	}
	for i, id := range want {
		if chain[i].BackupID != id {
			t.Errorf("chain[%d] = %s, want %s", i, chain[i].BackupID, id)
		}
	}

	if _, err := backupChain(all[:2], "c"); err == nil {
		t.Error("a chain with a missing ancestor must fail rather than restore a partial one")
	}
}

func TestBackupChainRejectsCycles(t *testing.T) {
	all := []backupManifest{
		{BackupID: "a", Parent: "b"},
		{BackupID: "b", Parent: "a"},
	}
	if _, err := backupChain(all, "a"); err == nil {
		t.Error("a cyclic parent chain must be refused rather than looped over")
	}
}

// verify exists because the most common way a backup system fails is that
// somebody believes there is a backup and there is not. A truncated stream is
// the case a presence check misses.
func TestVerifyBackupCatchesTruncationAndMissingChannels(t *testing.T) {
	dest := t.TempDir()

	writeStreamBackup := func(id string, size int, withTar bool) backupManifest {
		root := backupRoot(dest, id)
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(backupStreamPath(root), make([]byte, size), 0600); err != nil {
			t.Fatal(err)
		}
		if withTar {
			if err := os.WriteFile(backupMetaTarPath(root), []byte("tar"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return backupManifest{
			BackupID: id, Mode: backupModeSendFile, SizeBytes: 100, Complete: true,
		}
	}

	present := map[string]bool{"whole": true, "truncated": true, "lonely": true}

	whole := writeStreamBackup("whole", 100, true)
	if problems := verifyBackup(dest, whole, present); len(problems) != 0 {
		t.Errorf("an intact backup reported problems: %v", problems)
	}

	withUserData := writeStreamBackup("with-userdata", 60, true)
	withUserData.Channels = []string{backupChannelData, backupChannelMetadata, backupChannelUserData}
	if err := os.WriteFile(backupUserStreamPath(backupRoot(dest, "with-userdata")), make([]byte, 40), 0600); err != nil {
		t.Fatal(err)
	}
	if problems := verifyBackup(dest, withUserData, present); len(problems) != 0 {
		t.Errorf("an intact two-stream backup reported problems: %v", problems)
	}
	if err := os.Truncate(backupUserStreamPath(backupRoot(dest, "with-userdata")), 20); err != nil {
		t.Fatal(err)
	}
	if problems := verifyBackup(dest, withUserData, present); !hasProblemCode(problems, "size_mismatch") {
		t.Errorf("a truncated user-data stream must report size_mismatch; got %v", problems)
	}

	truncated := writeStreamBackup("truncated", 60, true)
	problems := verifyBackup(dest, truncated, present)
	if !hasProblemCode(problems, "size_mismatch") {
		t.Errorf("a truncated stream must report size_mismatch; got %v", problems)
	}

	// One channel missing is the failure the two-channel design makes possible,
	// so it has its own code rather than being reported as a generic fault.
	lonely := writeStreamBackup("lonely", 100, false)
	problems = verifyBackup(dest, lonely, present)
	if !hasProblemCode(problems, "metadata_stream_missing") {
		t.Errorf("a backup with no metadata channel must report metadata_stream_missing; got %v", problems)
	}

	orphan := backupManifest{BackupID: "orphan", Mode: backupModeSendFile, Parent: "gone", Complete: true}
	root := backupRoot(dest, "orphan")
	_ = os.MkdirAll(root, 0700)
	_ = os.WriteFile(backupStreamPath(root), []byte("x"), 0600)
	_ = os.WriteFile(backupMetaTarPath(root), []byte("t"), 0600)
	problems = verifyBackup(dest, orphan, present)
	if !hasProblemCode(problems, "parent_missing") {
		t.Errorf("a broken chain must report parent_missing; got %v", problems)
	}
}

// An interrupted transfer must not be mistaken for a backup. The temporary
// prefix keeps it out of every listing, and the manifest's complete flag keeps
// it out of any restore that names it explicitly.
func TestListBackupsHidesInterruptedTransfers(t *testing.T) {
	dest := t.TempDir()
	writeManifest(t, backupRoot(dest, "good"), &backupManifest{
		BackupID: "good", Mode: backupModeCopy, CreatedAt: "2026-07-30T00:00:00Z", Complete: true,
	})
	writeManifest(t, backupTempRoot(dest, "partial"), &backupManifest{
		BackupID: "partial", Mode: backupModeCopy, CreatedAt: "2026-07-31T00:00:00Z", Complete: false,
	})
	backups, err := listBackups(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 || backups[0].BackupID != "good" {
		t.Fatalf("listing showed %d backup(s): %+v", len(backups), backups)
	}

	if _, _, err := selectBackup(dest, ""); err != nil {
		t.Errorf("selecting the newest complete backup failed: %v", err)
	}
}

func TestListBackupsFlagsBrokenChains(t *testing.T) {
	dest := t.TempDir()
	writeManifest(t, backupRoot(dest, "child"), &backupManifest{
		BackupID: "child", Mode: backupModeSendFile, Parent: "vanished",
		CreatedAt: "2026-07-30T00:00:00Z", Incremental: true, Complete: true,
	})
	backups, err := listBackups(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 || !backups[0].ChainBroken {
		t.Errorf("a backup whose parent is gone must be flagged chain_broken: %+v", backups)
	}
}

// The metadata channel has to survive the round trip intact, including the
// 0600 on the files that hold plaintext secrets.
func TestMetadataArchiveRoundTrip(t *testing.T) {
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "meta"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "meta", "config.yml"), []byte("secret: hunter2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "snapshot.yml"), []byte("id: x\n"), 0600); err != nil {
		t.Fatal(err)
	}

	archive := filepath.Join(t.TempDir(), "meta.tar")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := tar.NewWriter(file)
	if err := appendToTar(writer, filepath.Join(source, "meta"), "meta"); err != nil {
		t.Fatal(err)
	}
	if err := appendToTar(writer, filepath.Join(source, "snapshot.yml"), "snapshot.yml"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	if err := extractTar(archive, out); err != nil {
		t.Fatalf("extractTar: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(out, "meta", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "secret: hunter2\n" {
		t.Errorf("config.yml came back as %q", body)
	}
	info, err := os.Stat(filepath.Join(out, "meta", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("config.yml extracted as %o, want 600 — it holds plaintext secrets", info.Mode().Perm())
	}
	if !exists(filepath.Join(out, "snapshot.yml")) {
		t.Error("snapshot.yml did not survive the archive")
	}
}

// The destination may be shared with other hosts, so its archives are not
// trusted input.
func TestExtractTarRefusesPathsThatEscape(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "evil.tar")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := tar.NewWriter(file)
	body := []byte("owned\n")
	if err := writer.WriteHeader(&tar.Header{
		Name: "../../escaped.yml", Mode: 0600, Size: int64(len(body)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	_ = file.Close()

	out := t.TempDir()
	// The path is cleaned against the destination root, so an entry naming
	// ../ lands inside rather than outside; what matters is that nothing is
	// written above out.
	if err := extractTar(archive, out); err != nil {
		t.Fatalf("extractTar: %v", err)
	}
	if exists(filepath.Join(filepath.Dir(out), "escaped.yml")) {
		t.Error("a tar entry escaped the destination directory")
	}
}

// A backup taken from an existing snapshot stops nothing, because the snapshot
// has already frozen the data. Getting this wrong would impose an outage for no
// benefit at all.
func TestPlanActionsRestartBeforeTheTransfer(t *testing.T) {
	plan := &backupPlan{
		Workspace: "/ws", Mode: backupModeSendFile, Dest: "/mnt/usb",
		StopContainers: true, ContainersToStop: []string{"anas_traefik"},
		useSnapshot: true,
	}
	ops := opSequence(backupActions(plan))
	start := indexOfOp(ops, "start_containers")
	send := indexOfOp(ops, "send_stream")
	if start < 0 || send < 0 {
		t.Fatalf("expected both start_containers and send_stream in %v", ops)
	}
	// This is the whole point of the ordering: services come back while the
	// transfer is still running, so the outage is the snapshot's duration and
	// not the data's.
	if start > send {
		t.Errorf("start_containers must precede send_stream; got %v", ops)
	}
	if stop := indexOfOp(ops, "stop_containers"); stop < 0 || stop > start {
		t.Errorf("stop_containers must precede start_containers; got %v", ops)
	}
	// Both channels are named, and finalize comes after both.
	metadata := indexOfOp(ops, "send_metadata")
	finalize := indexOfOp(ops, "finalize")
	if metadata < 0 {
		t.Errorf("the send modes must carry a separate metadata channel; got %v", ops)
	}
	if finalize < send || finalize < metadata {
		t.Errorf("finalize must come after both channels; got %v", ops)
	}
}

func TestPlanWithoutSnapshotKeepsServicesDownForTheCopy(t *testing.T) {
	withSnapshot := estimatedDowntimeSeconds(backupModeCopy, true, 10<<30)
	withoutSnapshot := estimatedDowntimeSeconds(backupModeCopy, false, 10<<30)
	if withSnapshot >= withoutSnapshot {
		t.Errorf("a snapshot must shorten the estimated downtime: %ds with, %ds without",
			withSnapshot, withoutSnapshot)
	}
	if withSnapshot != backupSnapshotDowntimeSeconds {
		t.Errorf("with a snapshot the downtime is a constant, got %ds", withSnapshot)
	}
}

// --no-stop means two very different things depending on whether a snapshot is
// possible, and reporting the milder one on the host that gets the worse one
// would be actively misleading.
func TestCrashConsistencyMessageDistinguishesTheTwoCases(t *testing.T) {
	atomic := crashConsistencyMessage(backupModeSendFile, true)
	fileByFile := crashConsistencyMessage(backupModeCopy, false)
	if atomic == fileByFile {
		t.Fatal("the --no-stop warning must differ between an atomic snapshot and a live file copy")
	}
	if !containsSubstring(atomic, "crash-consistent") {
		t.Errorf("the snapshot warning should say crash-consistent: %q", atomic)
	}
	if !containsSubstring(fileByFile, "no single point in time") {
		t.Errorf("the copy warning should say it captures no point in time: %q", fileByFile)
	}
}

// A workspace that has never been used is the expected target on a rebuilt
// machine, and demanding a confirmation there would be noise.
func TestWorkspaceLooksUsed(t *testing.T) {
	fresh := t.TempDir()
	if err := os.MkdirAll(filepath.Join(fresh, workspaceDataDir), 0700); err != nil {
		t.Fatal(err)
	}
	if workspaceLooksUsed(fresh) {
		t.Error("an empty workspace must not require a confirmation")
	}
	if err := os.WriteFile(workspaceConfigPath(fresh), []byte("modules: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if !workspaceLooksUsed(fresh) {
		t.Error("a workspace with a config must require a confirmation before being overwritten")
	}
}

// The compensation is the guarantee that a killed backup does not leave
// services down, so what it selects out of the transaction directory is worth
// pinning: only its own kind, and only records still in the stopped state.
func TestContainerTransactionRoundTrip(t *testing.T) {
	base := filepath.Join(t.TempDir(), workspaceStateDir)
	if err := os.MkdirAll(transactionsDir(base), 0700); err != nil {
		t.Fatal(err)
	}
	txn := containerTransaction{
		APIVersion: activeStateVersion, ID: "t1", Kind: containerTransactionKind,
		StartedAt: nowUTC(), DeploymentID: "d1", Modules: []string{"traefik"},
		State: containerTransactionStopped,
	}
	if err := writeYAMLAtomic(transactionPath(base, txn.ID), &txn, 0600); err != nil {
		t.Fatal(err)
	}
	var back containerTransaction
	if err := readYAML(transactionPath(base, "t1"), &back); err != nil {
		t.Fatal(err)
	}
	if back.Kind != containerTransactionKind || back.State != containerTransactionStopped {
		t.Fatalf("transaction did not survive the round trip: %+v", back)
	}
	if len(back.Modules) != 1 || back.Modules[0] != "traefik" {
		t.Errorf("the record must name exactly what was stopped, got %v", back.Modules)
	}
	// A record with nothing to restart is cleared rather than retried forever.
	empty := containerTransaction{
		APIVersion: activeStateVersion, ID: "t2", Kind: containerTransactionKind,
		State: containerTransactionStopped,
	}
	if err := writeYAMLAtomic(transactionPath(base, empty.ID), &empty, 0600); err != nil {
		t.Fatal(err)
	}
	compensateContainerTransactions(base)
	if exists(transactionPath(base, "t2")) {
		t.Error("a transaction that stopped nothing must be cleared")
	}
}

// ---------------------------------------------------------------- helpers

func hasProblemCode(problems []backupProblem, code string) bool {
	for _, problem := range problems {
		if problem.Code == code {
			return true
		}
	}
	return false
}

func writeManifest(t *testing.T, root string, manifest *backupManifest) {
	t.Helper()
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeBackupManifest(root, manifest); err != nil {
		t.Fatal(err)
	}
}

func opSequence(actions []backupAction) []string {
	ops := make([]string, 0, len(actions))
	for _, action := range actions {
		ops = append(ops, action.Op)
	}
	return ops
}

func indexOfOp(ops []string, want string) int {
	for i, op := range ops {
		if op == want {
			return i
		}
	}
	return -1
}

func containsSubstring(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
