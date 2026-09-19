package incushost

import "slices"

const PreflightSchema = "anas.incus-host-preflight/v1"

// Facts are local preflight observations. They are NOT proof of daemon
// ownership, endpoint reachability, storage quotas or installation readiness.
type Facts struct {
	OS           string  `json:"os"`
	Architecture string  `json:"architecture"`
	Release      Release `json:"release"`
	Systemd      bool    `json:"systemd_observed"`
	KVMDevice    bool    `json:"kvm_device_observed"`
}

type Options struct {
	Skip      bool
	Interface string
}

type Report struct {
	Schema              string   `json:"schema"`
	Facts               Facts    `json:"facts"`
	Interface           string   `json:"interface"`
	DistributionMatched bool     `json:"distribution_matched"`
	Recipe              *Recipe  `json:"recipe,omitempty"`
	Disposition         string   `json:"disposition"`
	Blockers            []string `json:"blockers"`
	ComputeReady        bool     `json:"compute_ready"`
	RuntimeVerified     bool     `json:"runtime_verified"`
	ManualGuide         string   `json:"manual_guide"`
}

// Preflight is deterministic and side-effect free. Recognizing an official
// package is not admission to automatic installation. No variant here enables
// compute; host actions, ownership, networking and real-host gates remain due.
// Unsupported systems are a disabled feature, not a failed main deployment.
func Preflight(facts Facts, options Options) (Report, error) {
	if facts.OS == "" || !distroID.MatchString(facts.OS) || !distroID.MatchString(facts.Architecture) {
		return Report{}, ErrInvalid
	}
	if (!options.Skip && facts.OS == "linux" && facts.Release.ID == "") || (facts.Release.ID != "" && !distroID.MatchString(facts.Release.ID)) || (facts.Release.Version != "" && !versionID.MatchString(facts.Release.Version)) || (facts.Release.Codename != "" && !versionID.MatchString(facts.Release.Codename)) {
		return Report{}, ErrInvalid
	}
	if options.Interface == "" {
		options.Interface = "incus_container"
	}
	if options.Interface != "incus_container" && options.Interface != "incus_vm" {
		return Report{}, ErrInvalid
	}
	report := Report{Schema: PreflightSchema, Facts: facts, Interface: options.Interface,
		Disposition: "disabled", Blockers: []string{}, ManualGuide: "https://linuxcontainers.org/incus/docs/main/installing/"}
	if options.Skip {
		report.Disposition = "skipped"
		return report, nil
	}
	if facts.OS != "linux" {
		report.Blockers = append(report.Blockers, "linux_required")
		return report, nil
	}
	rows, err := Recipes()
	if err != nil {
		return Report{}, err
	}
	for _, r := range rows {
		if r.Distribution != facts.Release.ID || r.Version != facts.Release.Version {
			continue
		}
		// Absence is allowed by os-release; a contradictory codename is not.
		if facts.Release.Codename != "" && facts.Release.Codename != r.Codename {
			report.Blockers = append(report.Blockers, "release_identity_conflict")
			return report, nil
		}
		report.DistributionMatched = true
		report.Recipe = &r
		break
	}
	if report.Recipe == nil {
		report.Blockers = append(report.Blockers, "distribution_not_adapted")
		return report, nil
	}
	if !slices.Contains(report.Recipe.Architectures, facts.Architecture) {
		report.Blockers = append(report.Blockers, "architecture_not_adapted")
	}
	if !facts.Systemd {
		report.Blockers = append(report.Blockers, "init_not_adapted")
	}
	if options.Interface == "incus_vm" && !facts.KVMDevice {
		report.Blockers = append(report.Blockers, "kvm_not_observed")
	}
	// These are separate from hardware/distro admission, and cannot be
	// overridden by a caller-supplied flag, runtime version or prior report.
	report.Blockers = append(report.Blockers, "host_actions_not_installed", "package_origin_unverified", "daemon_compatibility_unverified", "storage_network_unverified")
	return report, nil
}
