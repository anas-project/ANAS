package outbound

import (
	"fmt"

	"github.com/anas-project/ANAS/modules/mail/smtpconfig"
)

// DefaultConfig prepares an explicit new default without changing existing
// sender-domain overrides. The input profile must have no ID: it is assigned
// here so a browser cannot select and accidentally change a shared profile.
// It does not save, deploy or send mail. Call Commit with the same snapshot.
func DefaultConfig(previous Previous, profile smtpconfig.Profile) (smtpconfig.Config, error) {
	if profile.ID != "" {
		return smtpconfig.Config{}, ErrInvalid
	}
	current, err := smtpconfig.Parse(previous.Routing, previous.Passwords)
	if err != nil {
		return smtpconfig.Config{}, ErrConflict
	}
	// Parse creates independent data. Preserve every unrelated profile and
	// password, including any profile referenced by a domain override.
	defaultIndex := -1
	ids := make(map[string]bool, len(current.Profiles))
	for i, p := range current.Profiles {
		ids[p.ID] = true
		if p.ID == current.Default {
			defaultIndex = i
		}
	}
	if defaultIndex >= 0 {
		candidate := profile
		candidate.ID = current.Default
		if current.Profiles[defaultIndex] == candidate {
			return current, nil
		}
		referenced := false
		for _, id := range current.Domains {
			referenced = referenced || id == current.Default
		}
		if !referenced {
			current.Profiles[defaultIndex] = candidate
			if current.Validate() != nil {
				return smtpconfig.Config{}, ErrInvalid
			}
			return current, nil
		}
	}
	// Never remove another configured profile to make room automatically.
	if len(current.Profiles) >= 8 {
		return smtpconfig.Config{}, ErrConflict
	}
	for i := 0; i <= 8; i++ {
		id := "primary"
		if i != 0 {
			id = fmt.Sprintf("primary_%d", i)
		}
		if ids[id] {
			continue
		}
		profile.ID = id
		current.Profiles = append(current.Profiles, profile)
		current.Default = id
		if current.Validate() != nil {
			return smtpconfig.Config{}, ErrInvalid
		}
		return current, nil
	}
	return smtpconfig.Config{}, ErrConflict
}
