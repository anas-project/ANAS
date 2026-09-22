//go:build linux

package incusingresshost

import (
	"context"
	"fmt"
	"os"
	"time"
)

// ObserveLocalGuestVeth runs one fixed read-only ip query, using the existing
// root-owned executable FD checks. Neither binary, flags nor namespace can be
// supplied by a caller. Names must come from a separately pinned Incus sample.
func ObserveLocalGuestVeth(ctx context.Context, name, bridge string) (GuestVethObservation, error) {
	if ctx == nil || os.Geteuid() != 0 || !ifaceName.MatchString(name) || !ifaceName.MatchString(bridge) || name == bridge {
		return GuestVethObservation{}, fmt.Errorf("invalid local guest observation")
	}
	runner := commandRunner{config: backendConfig{CommandTimeout: 5 * time.Second, Binaries: trustedBinaries{IP: trustedIPBinary}}}
	body, err := runner.output(ctx, trustedIPBinary, []string{"-j", "-d", "link", "show", "dev", name})
	if err != nil {
		return GuestVethObservation{}, err
	}
	return decodeGuestVeth(body, name, bridge)
}
