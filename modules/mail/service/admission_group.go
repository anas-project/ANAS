package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

type admissionGroupBinding struct{ Role, DN string }

type admissionGroupStore interface {
	AdmissionGroup(context.Context, string) (string, error)
	PinAdmissionGroup(context.Context, string, string) error
}

func bindAdmissionGroups(ctx context.Context, cfg *loginConfig, data any, resolve func(context.Context, string) (string, error)) error {
	if len(cfg.AdmissionGroups) == 0 {
		return nil
	}
	if len(cfg.Directory.AllowGroups) != 0 || len(cfg.AdmissionGroups) > 3 || cfg.Directory.GroupsDN == "" {
		return errors.New("mail admission group binding invalid")
	}
	pins, ok := data.(admissionGroupStore)
	if !ok {
		return errors.New("mail admission group storage unavailable")
	}
	seen := map[string]bool{}
	for _, binding := range cfg.AdmissionGroups {
		if (binding.Role != "application" && binding.Role != "all" && binding.Role != "administrator") || seen[binding.Role] || binding.DN == "" {
			return errors.New("mail admission group binding invalid")
		}
		seen[binding.Role] = true
	}
	var groups []identity.Group
	for _, binding := range cfg.AdmissionGroups {
		anchor, err := pins.AdmissionGroup(ctx, binding.Role)
		if errors.Is(err, store.ErrAdmissionGroupUnknown) {
			anchor, err = resolve(ctx, binding.DN)
			if err == nil {
				err = pins.PinAdmissionGroup(ctx, binding.Role, anchor)
			}
		}
		if err != nil {
			return fmt.Errorf("%w: admission groups", errAuthorizationDependency)
		}
		groups = append(groups, identity.Group{DN: binding.DN, Anchor: anchor})
	}
	cfg.Directory.AllowGroups = groups
	return nil
}
