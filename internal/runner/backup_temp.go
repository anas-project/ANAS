package runner

// Who is writing a temporary backup, and how cleanup tells a transfer that is
// still running from the debris of one that died.
//
// A destination is often shared. Several workspaces on one host, or several
// hosts on one network directory, back up into it, and nothing serialises
// them: the runtime lock belongs to a workspace, not to a destination. So a
// .tmp- directory seen when a create starts may be another backup's transfer in
// progress. Removing it fails that backup at best. At worst the other writer
// carries on, because the manifest write recreates a missing directory, and
// publishes a backup that holds nothing but a manifest claiming it is complete.
//
// Every temporary directory therefore carries an ownership record, owner.yml,
// written straight after the directory is created and before anything else
// lands in it. Cleanup removes a directory only when that record proves its
// writer is gone:
//
//   - in the same PID space (same boot, same PID namespace) the process is
//     asked directly, and it has exited, is a zombie, or its PID now names a
//     process that started at a different moment;
//   - anywhere else, the heartbeat the writer refreshes every minute has not
//     moved for backupTempStaleAfter.
//
// A directory without a record is never removed while it holds anything:
// nothing proves it abandoned, and releases before this one wrote no record.
// It is reported instead.
//
// No destination lock is taken, deliberately. The destination is exactly where
// flock is least trustworthy — NFS, SMB and FUSE mounts each emulate it
// differently or not at all — and a lock directory would need stale-holder
// detection of its own, which is this same problem again. The two steps that
// can race are settled by operations those filesystems all perform atomically:
// a claim is an exclusive mkdir, and taking a directory away is a rename to an
// .abandoned- name before anything is deleted, so a writer publishing and a
// cleanup reaping the same directory cannot both succeed.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	backupTempOwnerAPIVersion = "anas.dev/backup-owner/v1"
	backupTempOwnerFile       = "owner.yml"
	// backupAbandonedPrefix marks a tree on its way out. Nothing that has to
	// survive is ever stored under it, so anyone may delete it at any moment.
	backupAbandonedPrefix = ".abandoned-"
)

var (
	backupTempHeartbeatInterval = time.Minute
	// backupTempStaleAfter is how long a writer in another PID space may stay
	// silent before its directory counts as abandoned. It is generous on
	// purpose: waiting longer only postpones reclaiming a dead writer's space,
	// while judging too early fails a live backup. It also bounds how far apart
	// the clocks of hosts sharing a destination may drift.
	backupTempStaleAfter = 30 * time.Minute
	backupTempNow        = time.Now
)

func backupTempOwnerPath(root string) string { return filepath.Join(root, backupTempOwnerFile) }

// backupTempOwner is <dest>/.tmp-<id>/owner.yml.
type backupTempOwner struct {
	APIVersion string `yaml:"api_version"`
	BackupID   string `yaml:"backup_id"`
	// Token is random per claim. A record carrying it is what proves, just
	// before publishing, that the directory about to be renamed into place is
	// the one this writer filled and not an empty stand-in recreated after a
	// cleanup took the original away.
	Token string `yaml:"token"`
	// Host is for the person reading a warning and decides nothing: host names
	// repeat, and so do the machine IDs of cloned machines.
	Host string `yaml:"host,omitempty"`
	// BootID and PIDNamespace name the PID space PID belongs to. Only a reader
	// in that same space can ask about the process directly.
	BootID       string `yaml:"boot_id,omitempty"`
	PIDNamespace string `yaml:"pid_namespace,omitempty"`
	PID          int    `yaml:"pid"`
	// ProcessStart tells the writer apart from an unrelated process that was
	// later handed the same PID.
	ProcessStart string `yaml:"process_start,omitempty"`
	StartedAt    string `yaml:"started_at"`
	HeartbeatAt  string `yaml:"heartbeat_at"`
}

// processIdentity names one process: its PID within one PID space, and when it
// started, which is what survives PID reuse.
type processIdentity struct {
	bootID       string
	pidNamespace string
	pid          int
	start        string
}

func currentProcessIdentity() processIdentity {
	bootID, namespace := currentPIDSpace()
	pid := os.Getpid()
	return processIdentity{bootID: bootID, pidNamespace: namespace, pid: pid, start: inspectProcess(pid).start}
}

// sharesPIDSpace reports whether owner's PID can be looked up from here. An
// unknown boot never matches, not even another unknown one.
func (self processIdentity) sharesPIDSpace(owner backupTempOwner) bool {
	return owner.BootID != "" && owner.BootID == self.bootID && owner.PIDNamespace == self.pidNamespace
}

type processStatus int

const (
	// processUnknown is the zero value on purpose: when nothing can be learned
	// about a writer, it is treated as still running.
	processUnknown processStatus = iota
	processRunning
	processGone
)

type processProbe struct {
	status processStatus
	start  string // "" when it cannot be read
}

// probeProcess is replaced in tests, like btrfsCommand.
var probeProcess = inspectProcess

// ---------------------------------------------------------------- claiming

// backupTempClaims holds the IDs this process is writing right now. Within one
// process a PID and start time identify the process but not which of its
// backups is still alive, so a long-running caller that starts several needs
// this to judge its own directories.
var backupTempClaims = struct {
	sync.Mutex
	ids map[string]bool
}{ids: map[string]bool{}}

func setBackupTempClaimed(id string, claimed bool) {
	backupTempClaims.Lock()
	defer backupTempClaims.Unlock()
	if claimed {
		backupTempClaims.ids[id] = true
	} else {
		delete(backupTempClaims.ids, id)
	}
}

func backupTempClaimed(id string) bool {
	backupTempClaims.Lock()
	defer backupTempClaims.Unlock()
	return backupTempClaims.ids[id]
}

// backupTempClaim is one create's hold on its temporary directory. It ends in
// exactly one of publish or discard.
type backupTempClaim struct {
	dest    string
	id      string
	root    string
	owner   backupTempOwner
	stop    chan struct{}
	done    chan struct{}
	stopped bool
}

// claimBackupTemp creates <dest>/.tmp-<id> with its ownership record and starts
// the heartbeat.
func claimBackupTemp(dest string) (*backupTempClaim, error) {
	self := currentProcessIdentity()
	host, _ := os.Hostname()
	for attempt := 1; ; attempt++ {
		id, err := newBackupID()
		if err != nil {
			return nil, err
		}
		token, err := randomBackupSuffix(16)
		if err != nil {
			return nil, err
		}
		// Registered before the record exists, so a cleanup elsewhere in this
		// process can never see the record without the registration.
		setBackupTempClaimed(id, true)
		root := backupTempRoot(dest, id)
		// Exclusive rather than MkdirAll: two writers that drew the same ID
		// must not end up sharing one tree.
		if err := os.Mkdir(root, 0700); err != nil {
			setBackupTempClaimed(id, false)
			if os.IsExist(err) && attempt < 3 {
				continue
			}
			return nil, failuref("dest_unwritable", "create %s: %v", root, err)
		}
		now := backupTempNow().UTC().Format(time.RFC3339)
		owner := backupTempOwner{
			APIVersion: backupTempOwnerAPIVersion, BackupID: id, Token: token, Host: host,
			BootID: self.bootID, PIDNamespace: self.pidNamespace, PID: self.pid, ProcessStart: self.start,
			StartedAt: now, HeartbeatAt: now,
		}
		if err := createBackupTempOwner(root, &owner); err != nil {
			_ = os.RemoveAll(root)
			setBackupTempClaimed(id, false)
			return nil, failuref("dest_unwritable", "record the owner of %s: %v", root, err)
		}
		claim := &backupTempClaim{
			dest: dest, id: id, root: root, owner: owner,
			stop: make(chan struct{}), done: make(chan struct{}),
		}
		go claim.heartbeat()
		return claim, nil
	}
}

func createBackupTempOwner(root string, owner *backupTempOwner) error {
	b, err := yaml.Marshal(owner)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(backupTempOwnerPath(root), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := file.Write(b); err != nil {
		_ = file.Close()
		return err
	}
	// Flushed, because a record lost to a power cut would leave a directory
	// that no later cleanup can prove anything about.
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func (c *backupTempClaim) heartbeat() {
	defer close(c.done)
	ticker := time.NewTicker(backupTempHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			// A missing record means the tree was taken away. The publish
			// check reports that; there is nothing left here to keep alive.
			if err := c.beat(backupTempNow()); os.IsNotExist(err) {
				return
			}
		}
	}
}

// beat rewrites heartbeat_at in place. The record is opened without O_CREATE on
// purpose: once a cleanup has taken the tree away, a heartbeat must not plant a
// fresh record in whatever now sits at the old path, because that record is
// the proof the publish check trusts.
func (c *backupTempClaim) beat(now time.Time) error {
	owner := c.owner
	owner.HeartbeatAt = now.UTC().Format(time.RFC3339)
	b, err := yaml.Marshal(&owner)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(backupTempOwnerPath(c.root), os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	// Same length every time, so a reader never catches the file truncated.
	_, err = file.WriteAt(b, 0)
	if err == nil {
		err = file.Truncate(int64(len(b)))
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

func (c *backupTempClaim) stopHeartbeat() {
	if c.stopped {
		return
	}
	c.stopped = true
	close(c.stop)
	<-c.done
}

// ownsTree reports whether the record at the claimed path is still this
// claim's own.
func (c *backupTempClaim) ownsTree() bool {
	var current backupTempOwner
	if err := readYAML(backupTempOwnerPath(c.root), &current); err != nil {
		return false
	}
	return current.Token == c.owner.Token
}

// publish writes the manifest and renames the tree into place, which is what
// makes it a backup.
func (c *backupTempClaim) publish(manifest *backupManifest) error {
	c.stopHeartbeat()
	if err := writeBackupManifest(c.root, manifest); err != nil {
		return failuref("finalize_failed", "write the backup manifest: %v", err)
	}
	// Checked after the manifest is written, not before: that write recreates a
	// missing directory, and only the record says whether the manifest landed
	// in the tree this process filled or in an empty stand-in for it.
	if !c.ownsTree() {
		return failuref("backup_temp_lost",
			"%s is no longer the directory this backup was writing: it was removed during the transfer, "+
				"by an anas release that predates ownership records, by a create on another host that saw no heartbeat for %s, "+
				"or by hand; nothing was published", c.root, backupTempStaleAfter)
	}
	final := backupRoot(c.dest, c.id)
	if err := os.Rename(c.root, final); err != nil {
		return failuref("finalize_failed", "publish the backup: %v", err)
	}
	setBackupTempClaimed(c.id, false)
	// Best effort: a published backup still carrying its record is untidy,
	// not wrong. Nothing that reads a backup looks at it.
	_ = os.Remove(backupTempOwnerPath(final))
	return nil
}

// discard gives the tree up after a failure.
func (c *backupTempClaim) discard() {
	c.stopHeartbeat()
	setBackupTempClaimed(c.id, false)
	_ = discardBackupTemp(c.dest, c.root)
}

// discardBackupTemp takes a temporary tree out of the way and deletes it. The
// rename comes first so that a deletion cut short leaves an .abandoned- name,
// which anyone may finish, rather than a half-deleted .tmp- tree that may have
// lost its record and can no longer be proven abandoned.
func discardBackupTemp(dest, root string) error {
	suffix, err := randomBackupSuffix(4)
	if err != nil {
		return err
	}
	abandoned := filepath.Join(dest,
		backupAbandonedPrefix+strings.TrimPrefix(filepath.Base(root), backupTempPrefix)+"-"+suffix)
	if err := os.Rename(root, abandoned); err != nil {
		if os.IsNotExist(err) {
			// Its writer published it, or another cleanup got there first.
			return nil
		}
		return err
	}
	return removeBackupTree(abandoned)
}

func randomBackupSuffix(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ---------------------------------------------------------------- cleaning up

// backupTempVerdict is what cleanup concluded about one directory.
type backupTempVerdict struct {
	abandoned bool
	// quiet means kept for an ordinary reason, a backup in progress, which is
	// not worth a warning.
	quiet bool
	why   string
}

type backupTempOutcome struct {
	path    string
	verdict backupTempVerdict
	// err is set when a directory judged abandoned could not be removed.
	err error
}

// cleanStaleBackupTemp removes the debris of interrupted creates at a
// destination: temporary trees whose ownership record proves their writer
// gone, and .abandoned- trees an earlier removal did not finish. Everything
// else is left alone, and a tree kept for want of proof is reported.
func cleanStaleBackupTemp(dest string, jsonMode bool) {
	for _, outcome := range reapBackupTemps(dest, currentProcessIdentity(), backupTempNow()) {
		switch {
		case outcome.err != nil:
			emitWarning(jsonMode, "backup_temp_removal_failed",
				"could not remove %s (%s): %v", outcome.path, outcome.verdict.why, outcome.err)
		case !outcome.verdict.abandoned && !outcome.verdict.quiet:
			emitWarning(jsonMode, "backup_temp_kept", "left %s in place: %s", outcome.path, outcome.verdict.why)
		}
	}
}

func reapBackupTemps(dest string, self processIdentity, now time.Time) []backupTempOutcome {
	entries, err := os.ReadDir(dest)
	if err != nil {
		return nil
	}
	outcomes := []backupTempOutcome{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dest, entry.Name())
		switch {
		case strings.HasPrefix(entry.Name(), backupAbandonedPrefix):
			outcome := backupTempOutcome{path: path, verdict: backupTempVerdict{
				abandoned: true, why: "an earlier removal did not finish",
			}}
			outcome.err = removeBackupTree(path)
			outcomes = append(outcomes, outcome)
		case strings.HasPrefix(entry.Name(), backupTempPrefix):
			info, err := entry.Info()
			if err != nil {
				// Gone since the listing: published or reaped elsewhere.
				continue
			}
			outcome := backupTempOutcome{path: path, verdict: judgeBackupTemp(path, info.ModTime(), self, now)}
			if outcome.verdict.abandoned {
				outcome.err = discardBackupTemp(dest, path)
			}
			outcomes = append(outcomes, outcome)
		}
	}
	return outcomes
}

// judgeBackupTemp decides whether a temporary directory is proven abandoned.
// Every doubt resolves towards keeping it: the price of keeping debris is disk
// space until a later run, the price of removing a live transfer is a backup.
func judgeBackupTemp(root string, modTime time.Time, self processIdentity, now time.Time) backupTempVerdict {
	var owner backupTempOwner
	err := readYAML(backupTempOwnerPath(root), &owner)
	if os.IsNotExist(err) {
		return judgeUnrecordedBackupTemp(root, modTime, now)
	}
	if err == nil && (owner.APIVersion != backupTempOwnerAPIVersion || owner.PID <= 0) {
		err = fmt.Errorf("unsupported record (api_version %q, pid %d)", owner.APIVersion, owner.PID)
	}
	if err != nil {
		return backupTempVerdict{why: fmt.Sprintf("its %s cannot be read: %v", backupTempOwnerFile, err)}
	}
	if self.sharesPIDSpace(owner) {
		return judgeLocalBackupTempOwner(owner, self)
	}
	return judgeRemoteBackupTempOwner(owner, now)
}

func judgeUnrecordedBackupTemp(root string, modTime, now time.Time) backupTempVerdict {
	if now.Sub(modTime) < backupTempStaleAfter {
		// A writer between its mkdir and its record, or an older release that
		// writes none. Neither is debris yet, and neither is news.
		return backupTempVerdict{quiet: true, why: "recently created and not yet recorded"}
	}
	if empty, err := dirIsEmpty(root); err == nil && empty {
		// Removing an empty directory destroys nothing, whoever made it.
		return backupTempVerdict{abandoned: true,
			why: fmt.Sprintf("it is empty and has had no %s for over %s", backupTempOwnerFile, backupTempStaleAfter)}
	}
	return backupTempVerdict{why: fmt.Sprintf("it has no %s, so nothing shows whether a backup is still writing it; "+
		"anas releases before ownership records leave such directories behind. Remove it by hand once no backup "+
		"is running against this destination, deleting any Btrfs subvolume inside with `btrfs subvolume delete` first",
		backupTempOwnerFile)}
}

func judgeLocalBackupTempOwner(owner backupTempOwner, self processIdentity) backupTempVerdict {
	if owner.PID == self.pid && owner.ProcessStart != "" && owner.ProcessStart == self.start {
		if backupTempClaimed(owner.BackupID) {
			return backupTempVerdict{quiet: true, why: "this process is writing it"}
		}
		return backupTempVerdict{abandoned: true, why: "this process created it and no longer writes it"}
	}
	probe := probeProcess(owner.PID)
	switch probe.status {
	case processGone:
		return backupTempVerdict{abandoned: true,
			why: fmt.Sprintf("its writer, process %d on this host, has exited", owner.PID)}
	case processRunning:
		if owner.ProcessStart != "" && probe.start != "" && probe.start != owner.ProcessStart {
			return backupTempVerdict{abandoned: true,
				why: fmt.Sprintf("its writer, process %d on this host, has exited and the PID now belongs to another process", owner.PID)}
		}
		return backupTempVerdict{quiet: true, why: fmt.Sprintf("process %d on this host is writing it", owner.PID)}
	}
	return backupTempVerdict{why: fmt.Sprintf("cannot tell whether its writer, process %d on this host, is still running", owner.PID)}
}

func judgeRemoteBackupTempOwner(owner backupTempOwner, now time.Time) backupTempVerdict {
	host := owner.Host
	if host == "" {
		host = "another host"
	}
	writer := fmt.Sprintf("process %d on %s", owner.PID, host)
	beat, err := time.Parse(time.RFC3339, owner.HeartbeatAt)
	if err != nil {
		return backupTempVerdict{why: fmt.Sprintf("the heartbeat of its writer, %s, cannot be read: %q", writer, owner.HeartbeatAt)}
	}
	// A heartbeat from the future is a clock ahead of this one, not a writer
	// that has stopped.
	if silent := now.Sub(beat); silent > backupTempStaleAfter {
		return backupTempVerdict{abandoned: true,
			why: fmt.Sprintf("its writer, %s, has not refreshed its heartbeat for %s", writer, silent.Round(time.Second))}
	}
	return backupTempVerdict{quiet: true, why: fmt.Sprintf("%s is writing it", writer)}
}

func dirIsEmpty(path string) (bool, error) {
	dir, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer dir.Close()
	if _, err := dir.Readdirnames(1); err == io.EOF {
		return true, nil
	} else if err != nil {
		return false, err
	}
	return false, nil
}

// parseProcStat reads the state (field 3) and start time (field 22) out of a
// Linux /proc/<pid>/stat. The command name in field 2 is parenthesised and may
// itself contain spaces and parentheses, so fields are counted from the last
// ')'. It lives here rather than in the Linux file so the parsing is tested on
// every platform.
func parseProcStat(stat string) (state byte, start string, ok bool) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, "", false
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 20 || len(fields[0]) != 1 {
		return 0, "", false
	}
	return fields[0][0], fields[19], true
}
