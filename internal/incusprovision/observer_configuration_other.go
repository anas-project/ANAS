//go:build !linux

package incusprovision

import "context"

func openInstalledObserverConfiguration(context.Context, ObserverConfigurationRequest, bool) (*observerConfigurationSession, error) {
	return nil, ErrBlocked
}
