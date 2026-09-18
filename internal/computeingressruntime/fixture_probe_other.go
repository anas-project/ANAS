//go:build !linux

package computeingressruntime

import (
	"context"
	"fmt"
	"syscall"
)

func checkProbeNamespace(context.Context, TraefikProbeIdentity) error {
	return fmt.Errorf("HTTP fixture namespace probe requires Linux")
}

func checkProbeSocket(syscall.RawConn, uint64) error {
	return fmt.Errorf("HTTP fixture socket attestation requires Linux")
}

func CaptureTraefikProbeIdentity(context.Context, int, string) (TraefikProbeIdentity, error) {
	return TraefikProbeIdentity{}, fmt.Errorf("HTTP probe identity capture requires Linux")
}
