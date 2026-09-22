//go:build !linux

package incusingresshost

import (
	"context"
	"fmt"
)

func ObserveLocalGuestVeth(context.Context, string, string) (GuestVethObservation, error) {
	return GuestVethObservation{}, fmt.Errorf("local guest observation requires Linux")
}
