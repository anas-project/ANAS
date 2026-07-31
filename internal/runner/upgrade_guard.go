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

// cloneStringListPointer copies a *[]string, preserving the nil/empty
// distinction the whole guard rests on.
func cloneStringListPointer(in *[]string) *[]string {
	if in == nil {
		return nil
	}
	out := append([]string{}, *in...)
	return &out
}
