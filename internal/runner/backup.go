package runner

// A backup is a snapshot sent somewhere else.
//
// That sentence is the whole design. A snapshot is already self-sufficient —
// config, lock, secret store, the one piece of unrebuildable state, a full copy
// of the deployment artifact, and the data — so backup has nothing left to
// decide about *what* to carry, only about *how* to move it. There is
// deliberately no second include/exclude vocabulary to drift out of step with
// the first one.
//
// Every backup therefore has the same shape at the destination, whatever mode
// produced it:
//
//	<dest>/<backup-id>/
//	  backup.yml     manifest; complete: true written last
//	  snapshot.yml   the source snapshot's own metadata
//	  meta/          config.yml, config.lock.yml, secrets.yml, deployment-state.yml
//	  deployment/    a full copy of the deployment artifact
//	  data/          the data, as a subvolume or an ordinary directory
//	  data.stream    ... or, for send-file, a `btrfs send` stream instead of data/
//	  meta.tar       ... and for the send modes, meta/ + deployment/ + snapshot.yml in one file
//
// One shape means one restore path. It also means the mode is an implementation
// detail of the transfer rather than a fork that reaches all the way into
// recovery, which is when a fork is least welcome.
//
// The host that cannot snapshot at all still produces that shape: `copy` mode
// assembles it out of the live workspace, naming each piece explicitly. Naming
// them is what excludes the caches and the historical deployments — there is no
// filter to get wrong, and in particular no --one-file-system, which would stop
// at the data subvolume boundary and silently omit the only part that matters.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// The four transfer modes, in the order `capabilities` reports them.
const (
	backupModeSnapshot = "snapshot"
	backupModeSend     = "send"
	backupModeSendFile = "send-file"
	backupModeCopy     = "copy"
)

func validBackupMode(mode string) bool {
	switch mode {
	case backupModeSnapshot, backupModeSend, backupModeSendFile, backupModeCopy:
		return true
	}
	return false
}

// Reasons a mode is unavailable. Enumerated machine values: a caller branches
// on these, and only ever shows the accompanying message to a human.
const (
	reasonDestNotSpecified      = "dest_not_specified"
	reasonDestNotExist          = "dest_not_exist"
	reasonDestNotWritable       = "dest_not_writable"
	reasonDestNotBtrfs          = "dest_not_btrfs"
	reasonDestNotSameFilesystem = "dest_not_same_filesystem"
	reasonSourceNotBtrfs        = "source_not_btrfs"
	reasonDataNotSubvolume      = "data_not_subvolume"
	reasonDataIsMountpoint      = "data_is_mountpoint"
	reasonBtrfsToolMissing      = "btrfs_tool_missing"
	reasonInsufficientPrivilege = "insufficient_privilege"
	reasonInsufficientSpace     = "insufficient_space"
)

// Notes: the mode works, but something about it has to be said out loud.
const (
	noteRestoreRequiresBtrfs = "restore_requires_btrfs_target"
	noteSnapshotsExcluded    = "snapshots_excluded_by_default"
	noteNoIncremental        = "no_incremental_support"
	noteCrashConsistentOnly  = "crash_consistent_only"
	notePlaintextSecrets     = "plaintext_secrets_leaving_host"
)

// backupSourceInfo describes the workspace a backup would be taken from.
type backupSourceInfo struct {
	FSType           string `json:"fstype"`
	FSID             string `json:"fsid"`
	DataIsSubvolume  bool   `json:"data_is_subvolume"`
	DataIsMountpoint bool   `json:"data_is_mountpoint"`
	// DataFullyReadable is false when part of data/ cannot be read by this
	// process. Containers write their data as root, so on a real deployment
	// this is the normal state for an ordinary user — and it is exactly what
	// decides whether `copy` can produce a complete backup.
	DataFullyReadable bool `json:"data_fully_readable"`
}

// backupDestInfo describes where it would be written.
type backupDestInfo struct {
	Path      string `json:"path"`
	Exists    bool   `json:"exists"`
	Writable  bool   `json:"writable"`
	FSType    string `json:"fstype"`
	FSID      string `json:"fsid"`
	FreeBytes *int64 `json:"free_bytes"`
}

type backupEstimate struct {
	DataBytes             int64 `json:"data_bytes"`
	UserDataBytes         int64 `json:"userdata_bytes"`
	StateBytes            int64 `json:"state_bytes"`
	ActiveDeploymentBytes int64 `json:"active_deployment_bytes"`
	TotalBytes            int64 `json:"total_bytes"`
}

type backupModeReport struct {
	ID          string   `json:"id"`
	Available   bool     `json:"available"`
	Reason      string   `json:"reason,omitempty"`
	Incremental bool     `json:"incremental,omitempty"`
	Parents     []string `json:"parents,omitempty"`
	Notes       []string `json:"notes,omitempty"`
}

type backupCapabilities struct {
	Workspace   string             `json:"workspace"`
	Source      backupSourceInfo   `json:"source"`
	Dest        *backupDestInfo    `json:"dest"`
	Tools       map[string]bool    `json:"tools"`
	Privileged  bool               `json:"privileged"`
	Estimate    backupEstimate     `json:"estimate"`
	Modes       []backupModeReport `json:"modes"`
	Recommended string             `json:"recommended,omitempty"`
}

// probeBackupCapabilities answers, for one workspace and one optional
// destination, which modes could run right now and why the others could not.
//
// Reporting insufficient_privilege here rather than failing later is most of
// what this command is for. `btrfs send` needs CAP_SYS_ADMIN — measured, not
// assumed: creating and snapshotting subvolumes are unprivileged, which makes
// it entirely reasonable to expect sending to be too. Offering a mode that
// cannot run and discovering it after the containers have been stopped is the
// failure this exists to prevent.
func probeBackupCapabilities(workspace, dest string) (*backupCapabilities, error) {
	data := dataDir(workspace)
	sourceBtrfs, _ := filesystemIsBtrfs(data)
	dataSubvolume := sourceBtrfs && btrfsSubvolumeShow(data) == nil
	estimate, dataReadable := estimateBackupSize(workspace)

	caps := &backupCapabilities{
		Workspace: workspace,
		Source: backupSourceInfo{
			FSType:            filesystemName(data),
			FSID:              btrfsFilesystemID(data),
			DataIsSubvolume:   dataSubvolume,
			DataIsMountpoint:  pathIsMountPoint(data),
			DataFullyReadable: dataReadable,
		},
		Tools:      map[string]bool{"btrfs": haveBtrfsTool(), "rsync": haveTool("rsync")},
		Privileged: hasSysAdmin(),
		Estimate:   estimate,
	}
	if strings.TrimSpace(dest) != "" {
		absolute, err := filepath.Abs(dest)
		if err != nil {
			return nil, usageErrorf("resolve destination %s: %v", dest, err)
		}
		info := backupDestInfo{Path: absolute}
		if stat, err := os.Stat(absolute); err == nil && stat.IsDir() {
			info.Exists = true
			info.Writable = directoryIsWritable(absolute)
			info.FSType = filesystemName(absolute)
			info.FSID = btrfsFilesystemID(absolute)
			if free, ok := filesystemFree(absolute); ok {
				info.FreeBytes = &free
			}
		}
		caps.Dest = &info
	}
	facts := backupFacts{}
	if caps.Dest != nil && caps.Dest.Exists {
		facts.destBtrfs, _ = filesystemIsBtrfs(caps.Dest.Path)
		if facts.destBtrfs && caps.Source.DataIsSubvolume {
			facts.sameFilesystem = sameBtrfsFilesystem(workspace, caps.Dest.Path)
		}
		// A destination inside the workspace is the one place the secrets are
		// not going anywhere new. Everywhere else the snapshot carries
		// config.yml and the generated secret store in the clear, and the user
		// is entitled to know that before it leaves.
		facts.destLeavesHost = !pathWithin(caps.Dest.Path, workspace)
		facts.parents = incrementalParents(workspace, caps.Dest)
	}
	caps.Modes = evaluateBackupModes(caps, facts)
	caps.Recommended = recommendBackupMode(caps.Modes)
	return caps, nil
}

// backupFacts are the things about the destination that need the filesystem to
// answer. Separating them from the decision keeps evaluateBackupModes a pure
// function of stated facts, which is the only way the mode table can be tested
// on a machine that has no Btrfs to offer.
type backupFacts struct {
	destBtrfs      bool
	sameFilesystem bool
	destLeavesHost bool
	parents        []string
}

// evaluateBackupModes fills in the availability of all four modes. Each mode
// checks its preconditions in the order a human would ask about them, so the
// single reason reported is the most fundamental thing that is wrong rather
// than whichever check happened to run first.
func evaluateBackupModes(caps *backupCapabilities, facts backupFacts) []backupModeReport {
	destBtrfs := facts.destBtrfs
	sameFS := facts.sameFilesystem
	leaving := facts.destLeavesHost

	// destReason returns the first thing wrong with the destination itself,
	// shared by every mode because none of them can write to a place that is
	// not there.
	destReason := func() string {
		switch {
		case caps.Dest == nil:
			return reasonDestNotSpecified
		case !caps.Dest.Exists:
			return reasonDestNotExist
		case !caps.Dest.Writable:
			return reasonDestNotWritable
		}
		return ""
	}
	sourceReason := func() string {
		switch {
		case !caps.Source.DataIsSubvolume && caps.Source.FSType != "btrfs":
			return reasonSourceNotBtrfs
		case !caps.Source.DataIsSubvolume:
			return reasonDataNotSubvolume
		case caps.Source.DataIsMountpoint:
			// A mount point cannot be renamed aside, and the restore path does
			// exactly that. A backup nobody can restore is not worth taking.
			return reasonDataIsMountpoint
		}
		return ""
	}
	withLeaving := func(notes []string) []string {
		if leaving {
			return append(notes, notePlaintextSecrets)
		}
		return notes
	}
	report := func(id, reason string, notes []string) backupModeReport {
		if reason != "" {
			return backupModeReport{ID: id, Available: false, Reason: reason}
		}
		return backupModeReport{ID: id, Available: true, Notes: withLeaving(notes)}
	}

	modes := []backupModeReport{}

	// snapshot: a second reference on the very same filesystem. Instant and
	// free, and useless against the disk dying, which is why it is never the
	// recommendation.
	snapshotReason := firstNonEmpty(sourceReason(), destReason())
	if snapshotReason == "" {
		switch {
		case !caps.Tools["btrfs"]:
			snapshotReason = reasonBtrfsToolMissing
		case !destBtrfs:
			snapshotReason = reasonDestNotBtrfs
		case !sameFS:
			snapshotReason = reasonDestNotSameFilesystem
		}
	}
	modes = append(modes, report(backupModeSnapshot, snapshotReason, []string{noteNoIncremental, noteSnapshotsExcluded}))

	// send and send-file both shell out to `btrfs send`, which is the one
	// operation in the whole subsystem that an ordinary user cannot perform.
	sendCommon := firstNonEmpty(sourceReason(), destReason())
	if sendCommon == "" {
		switch {
		case !caps.Tools["btrfs"]:
			sendCommon = reasonBtrfsToolMissing
		case !caps.Privileged:
			sendCommon = reasonInsufficientPrivilege
		}
	}
	sendReason := sendCommon
	if sendReason == "" && !destBtrfs {
		sendReason = reasonDestNotBtrfs
	}
	sendMode := report(backupModeSend, sendReason, []string{noteRestoreRequiresBtrfs, noteSnapshotsExcluded})
	sendFileMode := report(backupModeSendFile, sendCommon, []string{noteRestoreRequiresBtrfs, noteSnapshotsExcluded})
	// Incremental sending needs a parent that exists at *both* ends: the
	// destination has to hold the backup, and this host has to still hold the
	// snapshot it was made from, because `btrfs send -p` reads the parent
	// locally to compute the difference.
	if sendMode.Available || sendFileMode.Available {
		parents := facts.parents
		for _, mode := range []*backupModeReport{&sendMode, &sendFileMode} {
			if !mode.Available {
				continue
			}
			if len(parents) > 0 {
				mode.Incremental = true
				mode.Parents = parents
			} else {
				mode.Notes = append(mode.Notes, noteNoIncremental)
			}
		}
	}
	modes = append(modes, sendMode, sendFileMode)

	// copy works anywhere something can be written, and is the only mode
	// available when the source is not Btrfs at all.
	//
	// It is also the only mode that reads the data file by file, which is what
	// makes it the only one privilege can stop. Containers write their data as
	// root, so on a real deployment an ordinary user cannot read all of data/ —
	// and a copy that skips what it cannot read is a backup with holes in it
	// that reports success. The contract's table has copy requiring nothing but
	// a writable destination; on any host where containers have actually run,
	// it requires privilege too.
	//
	// The Btrfs modes are unaffected because none of them reads a file:
	// `snapshot` is a metadata operation and `send` reads through the
	// filesystem rather than through the directory permissions.
	copyReason := destReason()
	if copyReason == "" && !caps.Source.DataFullyReadable {
		copyReason = reasonInsufficientPrivilege
	}
	modes = append(modes, report(backupModeCopy, copyReason, []string{noteNoIncremental, noteSnapshotsExcluded}))

	// Space is checked last and only against modes that are otherwise ready:
	// "you do not have room" is more useful than "you do not have room" on a
	// mode that could not have run anyway.
	if caps.Dest != nil && caps.Dest.FreeBytes != nil && caps.Estimate.TotalBytes > *caps.Dest.FreeBytes {
		for i := range modes {
			// The snapshot mode shares extents with the original, so it needs
			// no room to speak of; every other mode writes a full second copy.
			if modes[i].Available && modes[i].ID != backupModeSnapshot && !modes[i].Incremental {
				modes[i].Available = false
				modes[i].Reason = reasonInsufficientSpace
				modes[i].Notes = nil
			}
		}
	}
	return modes
}

// recommendBackupMode picks the mode a user should normally take.
//
// The order is not the contract's table order. A backup exists to survive the
// loss of the source disk, so `snapshot` — which puts the second copy on that
// same disk — is ranked last despite being by far the fastest. Recommending it
// would be recommending something that is not a backup.
func recommendBackupMode(modes []backupModeReport) string {
	for _, want := range []string{backupModeSend, backupModeSendFile, backupModeCopy, backupModeSnapshot} {
		for _, mode := range modes {
			if mode.ID == want && mode.Available {
				return want
			}
		}
	}
	return ""
}

func backupModeReportFor(modes []backupModeReport, id string) (backupModeReport, bool) {
	for _, mode := range modes {
		if mode.ID == id {
			return mode, true
		}
	}
	return backupModeReport{}, false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// ---------------------------------------------------------------- probes

func haveTool(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// btrfsToolCheck is overridable so tests can exercise mode selection on a
// machine whose temp directory is not Btrfs, for the same reason btrfsCommand
// and btrfsSubvolumeCheck are.
var btrfsToolCheck = func() bool { return haveTool("btrfs") }

func haveBtrfsTool() bool { return btrfsToolCheck() }

// privilegeCheck is overridable for the same reason.
var privilegeCheck = detectSysAdmin

func hasSysAdmin() bool { return privilegeCheck() }

// capSysAdmin is bit 21 of the capability bitmask; `btrfs send` and `receive`
// are gated on it.
const capSysAdmin = 21

// detectSysAdmin reports whether this process could run `btrfs send`.
//
// uid 0 is not the whole answer. A process can hold CAP_SYS_ADMIN without being
// root — under systemd's AmbientCapabilities, for instance, which is exactly
// how an operator would grant just this one privilege to a backup timer rather
// than running all of anas as root. Reading the effective set is both more
// precise and the more useful of the two answers.
//
// anas never escalates on its own. Shelling out to sudo would put root-owned
// files into .anas/state/ and make the privileged path implicit; keeping it
// explicit means the operator chooses it, and `capabilities` is where they find
// out that they have to.
func detectSysAdmin() bool {
	if os.Geteuid() == 0 {
		return true
	}
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		value, ok := strings.CutPrefix(line, "CapEff:")
		if !ok {
			continue
		}
		mask, err := strconv.ParseUint(strings.TrimSpace(value), 16, 64)
		if err != nil {
			return false
		}
		return mask&(1<<capSysAdmin) != 0
	}
	return false
}

// directoryIsWritable answers by writing, because the permission bits do not
// account for a read-only mount, a full filesystem, or an ACL.
func directoryIsWritable(dir string) bool {
	probe, err := os.CreateTemp(dir, ".anas-write-probe-")
	if err != nil {
		return false
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return true
}

// ---------------------------------------------------------------- estimates

// estimateBackupSize measures what a backup would carry, and reports whether
// all of it could even be seen.
//
// Directories that cannot be read are skipped rather than failing the walk:
// container data is routinely owned by root with 0700 directories, and an
// unprivileged `capabilities` that refused to produce any estimate would be
// less useful than one that produces a low estimate and says so.
func estimateBackupSize(workspace string) (backupEstimate, bool) {
	base := stateDir(workspace)
	dataBytes, dataReadable := measureTree(dataDir(workspace))
	userDataBytes, userDataReadable := measureTree(userDataDir(workspace))
	stateBytes, _ := measureTree(filepath.Join(base, "state"))
	secretBytes, _ := measureTree(filepath.Join(base, "secrets.yml"))
	estimate := backupEstimate{DataBytes: dataBytes, UserDataBytes: userDataBytes, StateBytes: stateBytes + secretBytes}
	if active, err := loadActiveState(base); err == nil && active.ActiveDeployment != "" {
		estimate.ActiveDeploymentBytes, _ = measureTree(deploymentArtifactDir(base, active.ActiveDeployment))
	}
	estimate.TotalBytes = estimate.DataBytes + estimate.UserDataBytes + estimate.StateBytes + estimate.ActiveDeploymentBytes
	return estimate, dataReadable && userDataReadable
}

// measureTree returns the size of a tree and whether every part of it was
// readable.
func measureTree(root string) (int64, bool) {
	var total int64
	readable := true
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsPermission(err) {
				readable = false
			}
			// Skipping the directory is important: returning the error would
			// abandon every sibling too, and the point is to measure as much as
			// can be measured.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if os.IsPermission(err) {
				readable = false
			}
			return nil
		}
		total += info.Size()
		return nil
	})
	return total, readable
}

func treeSize(root string) int64 {
	size, _ := measureTree(root)
	return size
}

// estimatedDowntimeSeconds is how long services are expected to be down.
//
// The two cases differ by orders of magnitude, and that difference is the
// reason the action order in the contract puts start_containers before the
// transfer. With a snapshot, the containers are down only while Btrfs creates
// one, which is a constant few seconds regardless of how much data there is.
// Without one, they are down for the entire copy.
func estimatedDowntimeSeconds(mode string, snapshotCapable bool, totalBytes int64) int {
	if snapshotCapable {
		return backupSnapshotDowntimeSeconds
	}
	seconds := int(totalBytes / backupAssumedThroughputBytes)
	if seconds < 1 {
		seconds = 1
	}
	return seconds
}

const (
	// A Btrfs snapshot is a metadata operation; five seconds is generous and
	// covers stopping and starting the containers around it.
	backupSnapshotDowntimeSeconds = 5
	// A deliberately conservative figure for a spinning disk or a network
	// target. It only feeds an estimate a human reads before deciding.
	backupAssumedThroughputBytes = 100 * 1024 * 1024
)

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for value := n / unit; value >= unit; value /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
