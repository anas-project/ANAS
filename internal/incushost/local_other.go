//go:build !linux

package incushost

import "context"

func localFacts(_ context.Context, _ *Facts) error { return nil }
