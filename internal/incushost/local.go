package incushost

import (
	"context"
	"runtime"
)

// LocalPreflight has no path/URL/executable overrides. This is installation
// metadata only: it never probes the administrative Incus socket, because even
// a GET connection could socket-activate a previously stopped daemon.
func LocalPreflight(ctx context.Context, options Options) (Report, error) {
	if ctx == nil {
		return Report{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	facts := Facts{OS: runtime.GOOS, Architecture: runtime.GOARCH}
	// Explicit skip does not need valid OS files, privileges or a working
	// optional installation. Preserve the main deployment's skip path.
	if options.Skip {
		return Preflight(facts, options)
	}
	if err := localFacts(ctx, &facts); err != nil {
		return Report{}, err
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	return Preflight(facts, options)
}
