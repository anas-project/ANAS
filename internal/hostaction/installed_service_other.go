//go:build !linux

package hostaction

import "context"

func ServeInstalledActivation(context.Context) error { return ErrUnavailable }
