//go:build !linux

package hostaction

func openInstalledLedger() (*fileLedger, error) { return nil, ErrUnavailable }

type fileLedger struct{}

func (*fileLedger) close() error { return nil }
