//go:build !linux && !darwin

package computeclient

import "context"

func publishClientCredentials(context.Context, string, []credentialItem) error {
	return errClientCredentialState
}
