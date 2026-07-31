package runner

// Whether a version change can be undone.
//
// A cask upgrade may rewrite the format of the data already on disk. Nothing in
// the settings diff shows that, and nothing in the artifact records it, so the
// only way the runner can know is for the cask to say so. `upgrade.data_breaking`
// is that statement: the versions at which the on-disk format changed, so that
// data written at or above one of them cannot be read by anything below it.
//
// The list exists because breaking is a property of a *transition*, not of a
// version. 30.0.1 -> 30.0.2 is fine, 30.0.1 -> 31.0.0 is not, and 30.0.1 ->
// 33.0.0 may cross two separate breaks at once. Only the individual break points
// make an arbitrary jump decidable.
//
// The whole file turns on one distinction: a cask that has not declared
// data_breaking is not the same as a cask that has declared it empty. Silence is
// unknown and stays blocked; `[]` is a checkable claim that no release ever
// rewrote the format, and it is what permits a rollback. Collapsing the two
// would make the crossing predicate false for every cask that has not been
// annotated and turn the conservative default inside out.

import (
	"fmt"
	"sort"
	"strings"
)

// dataVerdict is what can be said about the data after moving a cask between
// two versions.
type dataVerdict int

const (
	// dataUnchanged: the versions are the same, so no format can have moved.
	// This is the config-only case, and it is the common one.
	dataUnchanged dataVerdict = iota
	// dataUnknown: the versions differ and the cask says nothing about its data
	// format. Everything the runner could conclude would be a guess.
	dataUnknown
	// dataCompatible: declared, and no break point lies in the interval.
	dataCompatible
	// dataBreaking: a declared break point lies in the interval, so the lower
	// version provably cannot read what the higher one wrote.
	dataBreaking
)

// caskDataVerdict decides what moving a cask between two versions does to the
// data. Direction does not matter — the interval is the same either way, and
// what changes is only what the caller does with the answer.
//
// declared must be the declaration attached to the *higher* of the two
// versions; see governingDataBreaking for why.
//
// Versions are cask versions, not app_version. That is the granularity
// validateUpgrade already constrains, and several casks (samba_dc, lego, ddns,
// core) carry no app_version at all. The two cannot disagree in practice: both
// are read out of the same cask.yml, so equal versions always imply equal
// app_versions.
//
// Anything unparseable degrades to dataUnknown rather than to dataCompatible.
// A malformed version is a bug somewhere, and the safe reading of a bug in a
// safety declaration is that the guard might have applied.
func caskDataVerdict(fromVersion, toVersion string, declared *[]string) (dataVerdict, string) {
	from, fromErr := parseSemver(fromVersion)
	to, toErr := parseSemver(toVersion)
	if fromErr != nil || toErr != nil {
		if strings.TrimSpace(fromVersion) == strings.TrimSpace(toVersion) {
			return dataUnchanged, ""
		}
		return dataUnknown, ""
	}
	if from.Compare(to) == 0 {
		return dataUnchanged, ""
	}
	if declared == nil {
		return dataUnknown, ""
	}
	low, high := from, to
	if low.GreaterThan(high) {
		low, high = high, low
	}
	crossed := []string{}
	for _, raw := range *declared {
		v, err := parseSemver(raw)
		if err != nil {
			return dataUnknown, ""
		}
		// low < v <= high. The upper bound is inclusive because arriving at the
		// breaking version is the act that rewrites the format; excluding it
		// would clear exactly the upgrade that does the damage.
		if v.GreaterThan(low) && !v.GreaterThan(high) {
			crossed = append(crossed, raw)
		}
	}
	if len(crossed) == 0 {
		return dataCompatible, ""
	}
	// The lowest crossed point is reported: going forward it is the first break
	// the data hits, and going backward it is the last one that has to be undone.
	sort.Slice(crossed, func(i, j int) bool {
		a, _ := parseSemver(crossed[i])
		b, _ := parseSemver(crossed[j])
		return a.LessThan(b)
	})
	return dataBreaking, crossed[0]
}

// governingDataBreaking picks which of the two declarations decides a
// transition: always the one belonging to the higher version.
//
// Only the release that broke the format can know that it did. The older
// cask.yml was written before the break existed and cannot mention it, so
// consulting it would report "compatible" for precisely the transitions that
// are not. The runner only ever holds the cask.yml of the versions involved,
// which is why this has to be resolved by rule rather than by looking at the
// history in between.
func governingDataBreaking(fromVersion string, fromDeclared *[]string, toVersion string, toDeclared *[]string) *[]string {
	from, fromErr := parseSemver(fromVersion)
	to, toErr := parseSemver(toVersion)
	if fromErr != nil || toErr != nil {
		return toDeclared
	}
	if from.GreaterThan(to) {
		return fromDeclared
	}
	return toDeclared
}

// caskTransitionVerdict answers the question for one cask across a deployment
// transition, resolving the governing declaration on the caller's behalf.
func caskTransitionVerdict(from, to deploymentCask) (dataVerdict, string) {
	declared := governingDataBreaking(from.Version, from.DataBreaking, to.Version, to.DataBreaking)
	return caskDataVerdict(from.Version, to.Version, declared)
}

// ---------------------------------------------------------------- rollback

// dataBreakingCrossing is one cask whose rollback would step back over a
// declared break point.
type dataBreakingCrossing struct {
	Cask string
	From string // the deployed version, the one that wrote the data
	To   string // the rollback target, the one that would have to read it
	At   string // the declared version at which the format changed
}

// rollbackVersionGuard separates the two kinds of "no" a rollback can be given,
// because they deserve different escapes.
type rollbackVersionGuard struct {
	// Blocked lists changes whose data compatibility is merely unknown: an
	// undeclared version change, a cask appearing, a cask disappearing.
	// --allow-risky is a legitimate answer to these, since an operator may know
	// something the cask author never wrote down.
	Blocked []string
	// Crossings lists changes that provably cannot work. There is no escape for
	// these — see breakingError.
	Crossings []dataBreakingCrossing
}

// deploymentRollbackVersionGuard classifies every cask difference between the
// running deployment and the rollback target.
//
// This replaces a placeholder that treated any version difference at all as
// unknown and blocked it, down to a patch bump. That was the only honest answer
// while no cask said anything about its data format; now that they can, the
// block narrows to the cases that are actually unsafe.
//
// The narrowing is a usability change, not a safety one, and it rests entirely
// on cask authors declaring correctly — which is why an absent declaration
// still lands in Blocked exactly as before.
func deploymentRollbackVersionGuard(current, target *deploymentManifest) rollbackVersionGuard {
	guard := rollbackVersionGuard{Blocked: []string{}}
	if current == nil || target == nil {
		return guard
	}
	names := map[string]bool{}
	for name := range current.Casks {
		names[name] = true
	}
	for name := range target.Casks {
		names[name] = true
	}
	for name := range names {
		from, fromOK := current.Casks[name]
		to, toOK := target.Casks[name]
		switch {
		case !fromOK:
			// Rolling forward into a cask the running deployment does not have.
			guard.Blocked = append(guard.Blocked,
				fmt.Sprintf("cask %s removal (data compatibility unknown)", name))
			continue
		case !toOK:
			// The target predates this cask; its data would be left on disk with
			// nothing running against it.
			guard.Blocked = append(guard.Blocked,
				fmt.Sprintf("cask %s addition (data compatibility unknown)", name))
			continue
		}
		verdict, at := caskTransitionVerdict(from, to)
		switch verdict {
		case dataUnchanged, dataCompatible:
			// Nothing to say. Rollback never touches data, so a cask whose format
			// did not move across this interval simply carries on reading it.
		case dataUnknown:
			guard.Blocked = append(guard.Blocked, fmt.Sprintf(
				"cask %s %s/%s -> %s/%s (data compatibility unknown; the cask does not declare upgrade.data_breaking)",
				name, from.Version, from.AppVersion, to.Version, to.AppVersion))
		case dataBreaking:
			guard.Crossings = append(guard.Crossings, dataBreakingCrossing{
				Cask: name, From: from.Version, To: to.Version, At: at,
			})
		}
	}
	sort.Strings(guard.Blocked)
	sort.Slice(guard.Crossings, func(i, j int) bool {
		return guard.Crossings[i].Cask < guard.Crossings[j].Cask
	})
	return guard
}

// breakingError reports a rollback that steps back over a declared break point.
//
// There is deliberately no --allow-risky for this. The other blockers describe
// something the runner does not know; this one describes something it does: the
// old code cannot read the new format, so letting the rollback through buys
// nothing but a deployment that will not start. The message therefore has to
// name the operation that *would* work rather than a flag that would not.
func (g rollbackVersionGuard) breakingError() error {
	if len(g.Crossings) == 0 {
		return nil
	}
	var b strings.Builder
	for _, c := range g.Crossings {
		fmt.Fprintf(&b, "cannot roll back %s %s -> %s: crosses data-breaking version %s\n", c.Cask, c.From, c.To, c.At)
		fmt.Fprintf(&b, "data written by %s cannot be read by %s\n", c.From, c.To)
	}
	b.WriteString("\nto return to that state, restore a snapshot instead:\n")
	b.WriteString("  anas snapshot list\n")
	b.WriteString("  anas snapshot restore <id>")
	return preconditionErrorf("data_breaking_crossed", "%s", b.String())
}

// ---------------------------------------------------------------- apply

// applySnapshotTrigger is why an apply has to snapshot the data before it runs.
type applySnapshotTrigger struct {
	reason string
	detail string
}

// deploymentSnapshotTrigger decides whether an apply is one the operator cannot
// take back by editing config.yml and applying again.
//
// Two things qualify. A cask upgrade that crosses a declared break point, since
// after it the old artifact can no longer read the data. And a changed setting
// whose effect is not automatically reversible — data_migrate rewrites what is
// there, credential_rotate changes state inside the service that putting the old
// value back in config.yml will not change back.
//
// Nothing else does, and that restraint is the point. keep_auto is a handful of
// slots; snapshotting every apply would fill them with routine config edits and
// evict the pre-breaking snapshot that is the entire reason the mechanism
// exists. reconcile is idempotent by contract and the restart-family effects do
// not touch data, so neither is worth a slot.
func deploymentSnapshotTrigger(current, target *deploymentManifest) *applySnapshotTrigger {
	if current == nil || target == nil {
		return nil
	}
	// The upgrade case is reported first: it is the more severe of the two, and
	// only one snapshot is taken either way.
	breaking := []string{}
	for name, to := range target.Casks {
		from, ok := current.Casks[name]
		if !ok {
			// A cask being added has no prior data, so there is nothing to break.
			continue
		}
		if verdict, at := caskTransitionVerdict(from, to); verdict == dataBreaking {
			breaking = append(breaking, fmt.Sprintf("%s %s -> %s crosses data-breaking version %s", name, from.Version, to.Version, at))
		}
	}
	if len(breaking) > 0 {
		sort.Strings(breaking)
		return &applySnapshotTrigger{
			reason: snapshotReasonCaskUpgradeBreaking,
			detail: "cask upgrade rewrites data on disk: " + strings.Join(breaking, "; "),
		}
	}
	changed := []string{}
	for _, change := range guardedSettingChanges(current, target) {
		// immutable cannot change by definition — activateDeployment refuses the
		// apply outright — so in practice this is the other two.
		if change.Effect == "data_migrate" || change.Effect == "credential_rotate" {
			changed = append(changed, fmt.Sprintf("%s (%s)", change.Key, change.Effect))
		}
	}
	if len(changed) > 0 {
		sort.Strings(changed)
		// One reason covers both effects. The name reads as if it were only about
		// data_migrate, but a credential rotation is equally irreversible from
		// config.yml and equally in need of a way back.
		return &applySnapshotTrigger{
			reason: snapshotReasonSettingDataMigrate,
			detail: "setting change is not automatically reversible: " + strings.Join(changed, ", "),
		}
	}
	return nil
}

// cloneStringListPointer copies a *[]string, preserving the nil/empty
// distinction the whole guard rests on.
func cloneStringListPointer(in *[]string) *[]string {
	if in == nil {
		return nil
	}
	out := append([]string{}, *in...)
	return &out
}
