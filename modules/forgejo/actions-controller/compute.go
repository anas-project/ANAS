package main

import (
	"context"
	"io"
	"time"

	"github.com/anas-project/ANAS/internal/computeclient"
)

// guestEntrypoint is the only program this controller may start inside a
// guest. It belongs here rather than in the shared client because it is
// Forgejo's guest image that provides it; another consumer of the same lease
// mechanism runs something else entirely.
const guestEntrypoint = "/usr/local/libexec/anas-forgejo-runner-start"

type (
	InstanceSpec = computeclient.InstanceSpec
	Instance     = computeclient.Instance
)

// ComputeProvider is the execution boundary used by the Actions controller. It
// deliberately has no Incus devices, raw config, mount, or socket fields for a
// caller to smuggle into an instance request; the lease owns all of those.
type ComputeProvider interface {
	Create(context.Context, InstanceSpec) error
	Inspect(context.Context, string) (Instance, error)
	Start(context.Context, string) error
	ExecStdin(context.Context, string, []string, io.Reader) error
	Stop(context.Context, string) error
	Delete(context.Context, string) error
	ListManaged(context.Context) ([]Instance, error)
}

// leasedCompute adapts the shared compute client to this controller.
//
// The only behaviour it adds is that Start does not return until the guest can
// accept the entrypoint: the very next thing the controller does is stream a
// one-time registration token into it, and a token streamed into an instance
// whose agent is not up yet is a token spent for nothing.
type leasedCompute struct {
	*computeclient.Client
	guestPoll time.Duration
}

func (l leasedCompute) Start(ctx context.Context, id string) error {
	if err := l.Client.Start(ctx, id); err != nil {
		return err
	}
	return l.Client.WaitForGuest(ctx, id, l.guestPoll)
}

var _ ComputeProvider = leasedCompute{}
