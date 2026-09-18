package runner

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"

	"github.com/anas-project/ANAS/internal/computeingress"
	"github.com/anas-project/ANAS/internal/deployment"
)

// ReadComputeHTTPNamingKey is for a trusted Core-side/lab adapter, never a
// consumer API. Reuse the Store's canonical parser and ownership/lifecycle
// checks, returning only the one independent naming key. It does not run Hooks,
// regenerate missing keys or project the Store into a mediator container.
func ReadComputeHTTPNamingKey(ctx context.Context, workspace string, expected *deployment.HTTPAuthorizationSnapshot, lease computeingress.Lease) (string, error) {
	if expected == nil {
		return "", fmt.Errorf("HTTP naming key requires an active authorization snapshot")
	}
	reader := deployment.NewReader(workspace)
	before, err := reader.HTTPAuthorizations(ctx)
	if err != nil || !reflect.DeepEqual(before, expected) {
		return "", fmt.Errorf("HTTP authorization changed before naming-key resolution")
	}
	var grant *computeingress.Authorization
	for _, candidate := range before.Authorizations {
		if candidate.Consumer == lease.Consumer && candidate.Resource == lease.Resource {
			grant = candidate
			break
		}
	}
	if grant == nil {
		return "", fmt.Errorf("HTTP lease has no active naming-key authorization")
	}
	store, err := loadSecretStore(filepath.Join(workspace, ".anas"))
	if err != nil {
		return "", fmt.Errorf("HTTP lease Secret Store could not be read")
	}
	a := &app{secrets: store}
	value, err := a.readComputeLeaseSecret(lease.Consumer, lease.Resource, grant.LeaseSecretRef)
	if err != nil {
		return "", fmt.Errorf("HTTP lease naming-key record is missing or invalid; restore the original Store entry")
	}
	after, err := reader.HTTPAuthorizations(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		return "", fmt.Errorf("HTTP authorization changed during naming-key resolution")
	}
	return value, nil
}
