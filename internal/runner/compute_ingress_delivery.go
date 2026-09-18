package runner

import (
	"context"
	"fmt"
	"reflect"

	"github.com/anas-project/ANAS/internal/computeingress"
	"github.com/anas-project/ANAS/internal/computeingressruntime"
	"github.com/anas-project/ANAS/internal/deployment"
)

// DeliverComputeHTTPReaders runs only in a trusted Core/installer process. It
// exports the active random naming keys and explicitly supplied reader-only
// installation credentials to a new private artifact, without mounting the
// Store or exposing a generic secret lookup API to the mediator. The caller
// must prepare a private parent owned by the eventual runtime UID. No chown,
// mount, credential issuance or production activation is performed here.
func DeliverComputeHTTPReaders(ctx context.Context, workspace, destination, expectedEpoch string, installation computeingressruntime.ReaderInstallation) error {
	reader := deployment.NewReader(workspace)
	snapshot, err := reader.HTTPAuthorizations(ctx)
	if err != nil {
		return err
	}
	if expectedEpoch == "" || snapshot.Epoch != expectedEpoch {
		return fmt.Errorf("HTTP reader delivery requires the expected active epoch")
	}
	keys := make(map[computeingress.Lease]string)
	for _, grant := range snapshot.Authorizations {
		if grant.Policy.Domain.Mode != "random" {
			continue
		}
		lease := computeingress.Lease{Consumer: grant.Consumer, Resource: grant.Resource}
		key, err := ReadComputeHTTPNamingKey(ctx, workspace, snapshot, lease)
		if err != nil {
			return err
		}
		keys[lease] = key
	}
	checkCurrent := func(ctx context.Context) error {
		current, err := reader.HTTPAuthorizations(ctx)
		if err != nil || !reflect.DeepEqual(current, snapshot) {
			return fmt.Errorf("Core authority changed during HTTP reader delivery")
		}
		// Naming-key changes also invalidate an in-progress export even when
		// an out-of-band Store edit has not changed the deployment manifest.
		for lease, expected := range keys {
			value, err := ReadComputeHTTPNamingKey(ctx, workspace, snapshot, lease)
			if err != nil || value != expected {
				return fmt.Errorf("HTTP naming-key source changed during delivery")
			}
		}
		return nil
	}
	return computeingressruntime.WriteReaderCredentials(ctx, destination, snapshot, installation, keys, checkCurrent)
}
