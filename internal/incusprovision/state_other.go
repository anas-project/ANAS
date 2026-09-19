//go:build !linux && !darwin

package incusprovision

import (
	"context"
	"os"
)

func (s *fileStateStore) Lock(context.Context) (stateLock, error) { return nil, ErrUnsupported }

type unsupportedStateLock struct{}

func (unsupportedStateLock) Unlock() error { return nil }

func rootOwnedPrivateDir(os.FileInfo) bool               { return false }
func rootOwnedPrivateFile(os.FileInfo, os.FileMode) bool { return false }
func rootOwnedExecutable(os.FileInfo) bool               { return false }
func sameFileIdentity(os.FileInfo, os.FileInfo) bool     { return false }

func ensureTrustedRootDirectory(string, os.FileMode) error { return ErrUnsupported }
func openRootFileNoFollow(string) (*os.File, error)        { return nil, ErrUnsupported }
