//go:build !linux

package incusprovision

import "context"

func openInstalledForwardingLease(context.Context, ForwardingPermissionRequest, State, *ForwardingLeaseGrant) (*forwardingLeaseSession, error) {
	return nil, ErrBlocked
}
