//go:build !linux && !darwin

package computeingressruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
)

var ErrExecutorBusy = errors.New("HTTP executor state is already locked")

func (s FileStateStore) WithExclusive(context.Context, func(Journal) error) error {
	return fmt.Errorf("HTTP executor file locking requires Linux or macOS")
}

func trustedRouteOwner(os.FileInfo) bool { return false }

func openRouteArtifact(*os.Root, string) (*os.File, error) {
	return nil, fmt.Errorf("HTTP route artifact reading requires Linux or macOS")
}
