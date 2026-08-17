//go:build !linux

package main

import "errors"

// The operations this binary exists for are Linux ones: macvlan interfaces and
// file capabilities have no counterpart elsewhere. It still builds everywhere,
// because `go build ./...` and `go test ./...` on a developer's macOS machine
// have to cover the argument validation in bridge.go -- which is where the
// rules about what may be touched actually live.
func applyBridge(bridgeRequest) error {
	return errors.New("host network setup is only supported on Linux")
}
