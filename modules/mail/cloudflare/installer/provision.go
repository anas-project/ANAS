package installer

import (
	"context"
	"strings"

	"github.com/anas-project/ANAS/modules/mail/ingress"
)

// provisionResult contains no credentials and does not imply public mail
// routing, NAS configuration, notification delivery or DMS readiness.
type provisionResult struct {
	SourceID, Generation, Origin string
}

// provision performs only the fixed, journaled Worker/R2 preparation steps.
// Private source credentials remain in the locked 0600 source file; integration
// with the existing ANAS Secret store and product activation is separate.
func (j *Journal) provision(ctx context.Context, c endpointClient, inputs sourceInputs, proxy string) (provisionResult, error) {
	if j == nil || j.file == nil || j.failed || c == nil || !validSourceInputs(j.state.Spec, inputs) {
		return provisionResult{}, ErrJournal
	}
	// Check the exact reader transport rules before making any cloud writes.
	reader, err := ingress.NewSourceClient(ingress.SourceConfig{
		ID: "11111111-1111-4111-8111-111111111111", Generation: "11111111-1111-4111-8111-111111111111",
		Origin: "https://preflight.example.test", Token: strings.Repeat("a", 64), ControlToken: strings.Repeat("b", 64),
		Domains: inputs.Domains, Proxy: proxy,
	})
	if err != nil {
		return provisionResult{}, ErrJournal
	}
	reader.Close()
	if err := j.ReserveResources(ctx, c); err != nil {
		return provisionResult{}, err
	}
	cfg, err := j.sourceConfig(inputs)
	if err != nil {
		return provisionResult{}, err
	}
	if err = j.deployVersion(ctx, c, cfg); err != nil {
		return provisionResult{}, err
	}
	if err = j.installSchedule(ctx, c, cfg); err != nil {
		return provisionResult{}, err
	}
	if err = j.enableEndpoint(ctx, c, cfg, proxy); err != nil {
		return provisionResult{}, err
	}
	return provisionResult{SourceID: cfg.SourceID, Generation: cfg.Generation, Origin: j.state.EndpointOrigin}, nil
}
