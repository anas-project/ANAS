package computeimage

import (
	"slices"
)

type PrunePlan struct {
	Retain []string `json:"retain"`
	Delete []string `json:"delete"`
}

func PlanPrune(imported, current, previous, running []string) (PrunePlan, error) {
	retain := map[string]bool{}
	for _, list := range [][]string{current, previous, running} {
		for _, fingerprint := range list {
			if !fingerprintPattern.MatchString(fingerprint) {
				return PrunePlan{}, ErrArtifactInvalid
			}
			retain[fingerprint] = true
		}
	}
	seenDelete := map[string]bool{}
	plan := PrunePlan{}
	for fingerprint := range retain {
		plan.Retain = append(plan.Retain, fingerprint)
	}
	for _, fingerprint := range imported {
		if !fingerprintPattern.MatchString(fingerprint) {
			return PrunePlan{}, ErrArtifactInvalid
		}
		if retain[fingerprint] || seenDelete[fingerprint] {
			continue
		}
		seenDelete[fingerprint] = true
		plan.Delete = append(plan.Delete, fingerprint)
	}
	slices.Sort(plan.Retain)
	slices.Sort(plan.Delete)
	return plan, nil
}
