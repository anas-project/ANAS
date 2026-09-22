//go:build !linux && !darwin

package computeingressruntime

import "context"

func (s WorkspaceStateStore) WithExclusive(context.Context, func(Journal) error) error {
	return ErrWorkspaceIngressState
}
