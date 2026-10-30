//go:build linux

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// Formal containers may read private mounted configuration/TLS identity during
// bootstrap. No listener or directory worker may start until root is dropped.
func dropPrivileges() error {
	if os.Geteuid() != 0 {
		return nil
	}
	if err := unix.Setgroups([]int{}); err != nil {
		return errors.New("mail privilege bootstrap failed")
	}
	if err := unix.Setgid(65532); err != nil {
		return errors.New("mail privilege bootstrap failed")
	}
	if err := unix.Setuid(65532); err != nil {
		return errors.New("mail privilege bootstrap failed")
	}
	return nil
}
func prepareIngressStorage() error {
	if e := os.MkdirAll(ingressDirectory, 0700); e != nil {
		return errors.New("mail source storage unavailable")
	}
	i, e := os.Lstat(ingressDirectory)
	if e != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
		return errors.New("mail source storage invalid")
	}
	if os.Geteuid() == 0 {
		if os.Chmod(ingressDirectory, 0700) != nil || os.Chown(ingressDirectory, 65532, 65532) != nil {
			return errors.New("mail source storage ownership unavailable")
		}
	}
	if i, e = os.Lstat(ingressDirectory); e != nil || i.Mode().Perm() != 0700 {
		return errors.New("mail source storage must be private")
	}
	return nil
}
