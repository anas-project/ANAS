//go:build !linux

package main

import (
	"errors"
	"os"
)

func dropPrivileges() error {
	if os.Geteuid() == 0 {
		return errors.New("mail root bootstrap requires Linux")
	}
	return nil
}
func prepareIngressStorage() error { return errors.New("mail source storage bootstrap requires Linux") }
