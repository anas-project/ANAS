//go:build !linux

package incusingresshost

import "fmt"

func newLocalForwardingPlatform() (forwardingKernelPlatform, error) {
	return nil, fmt.Errorf("forwarding writes require the installed Linux host executor")
}
