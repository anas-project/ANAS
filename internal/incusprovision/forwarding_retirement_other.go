//go:build !linux

package incusprovision

import "context"

func openInstalledForwardingRetirement(context.Context, State, ForwardingPermissionRecord) (*forwardingRetirementSession, error) {
	return nil, ErrUnsupported
}
