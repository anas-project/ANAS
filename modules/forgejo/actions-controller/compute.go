package main

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// ComputeProvider is the provider-neutral execution boundary used by the
// Actions controller. It deliberately has no Incus devices, raw config, mount,
// or socket fields for a caller to smuggle into a VM request.
type ComputeProvider interface {
	Create(context.Context, InstanceSpec) error
	Inspect(context.Context, string) (Instance, error)
	Start(context.Context, string) error
	ExecStdin(context.Context, string, []string, io.Reader) error
	Stop(context.Context, string) error
	Delete(context.Context, string) error
	ListManaged(context.Context) ([]Instance, error)
}

type InstanceSpec struct {
	ID         string
	Image      string
	WorkloadID string
	CPU        int
	MemoryMiB  int
	DiskGiB    int
}

type Instance struct {
	ID    string
	State string
}

var (
	instanceIDPattern   = regexp.MustCompile(`^anas-fj-[a-f0-9]{20}$`)
	imageFingerprintPat = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

func (s InstanceSpec) Validate() error {
	if !instanceIDPattern.MatchString(s.ID) {
		return fmt.Errorf("compute instance identity is invalid")
	}
	if !imageFingerprintPat.MatchString(s.Image) {
		return fmt.Errorf("compute image must be a pinned SHA-256 fingerprint")
	}
	if strings.TrimSpace(s.WorkloadID) == "" || len(s.WorkloadID) > 128 || hasControl(s.WorkloadID) {
		return fmt.Errorf("compute workload identity is invalid")
	}
	if s.CPU < 1 || s.CPU > 64 || s.MemoryMiB < 512 || s.MemoryMiB > 262144 || s.DiskGiB < 4 || s.DiskGiB > 2048 {
		return fmt.Errorf("compute resource limits are outside the supported range")
	}
	return nil
}

func validateGuestCommand(command []string) error {
	if len(command) == 0 || len(command) > 32 || command[0] != "/usr/local/libexec/anas-forgejo-runner-start" {
		return fmt.Errorf("compute exec command is not an approved guest entrypoint")
	}
	for _, value := range command {
		if value == "" || len(value) > 1024 || hasControl(value) {
			return fmt.Errorf("compute exec contains an invalid argument")
		}
	}
	return nil
}

func hasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
