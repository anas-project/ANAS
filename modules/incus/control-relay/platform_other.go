//go:build !linux

package main

func readRelayConfiguration(string) ([]byte, error) { return nil, errUnsupported }
func checkRelayIdentity(relaySettings) error        { return errUnsupported }
