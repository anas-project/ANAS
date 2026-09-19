//go:build !unix

package hostconfirmation

func requireProductionRoot() error {
	return ErrUnavailable
}
