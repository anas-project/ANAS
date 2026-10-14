//go:build !linux

package main

import (
	"errors"
	"os"
)

func prepareStorage(string) error {
	if os.Geteuid() == 0 {
		return errors.New("privileged portal bootstrap requires Linux")
	}
	return nil
}
