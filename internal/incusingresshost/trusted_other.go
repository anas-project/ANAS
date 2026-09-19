//go:build !linux

package incusingresshost

import (
	"fmt"
	"os"
)

func validateTrustedExecutable(string, os.FileInfo) error {
	return fmt.Errorf("production Incus ingress host backend is Linux-only")
}

func validateTrustedDirectory(string, os.FileInfo) error {
	return fmt.Errorf("production Incus ingress host backend is Linux-only")
}

func prepareCommandPath(path string, fixture bool) (string, *os.File, error) {
	if fixture {
		return path, nil, nil
	}
	return "", nil, fmt.Errorf("production Incus ingress host backend is Linux-only")
}
