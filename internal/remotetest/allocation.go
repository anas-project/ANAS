package remotetest

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

var runIDPattern = regexp.MustCompile(`^rt-[0-9]{8}t[0-9]{6}z-[a-f0-9]{12}$`)

type Allocation struct {
	RunID               string `json:"run_id"`
	Slot                int    `json:"slot"`
	Workspace           string `json:"workspace"`
	ReportDirectory     string `json:"report_directory"`
	ContainerPrefix     string `json:"container_prefix"`
	NetworkPrefix       string `json:"network_prefix"`
	NetworkNamespace    string `json:"network_namespace"`
	DockerSocket        string `json:"docker_socket"`
	DockerDataRoot      string `json:"docker_data_root"`
	DockerExecRoot      string `json:"docker_exec_root"`
	ContainerdSocket    string `json:"containerd_socket"`
	ContainerdRoot      string `json:"containerd_root"`
	ContainerdState     string `json:"containerd_state"`
	ContainerdNamespace string `json:"containerd_namespace"`
	PortStart           int    `json:"port_start"`
	PortEnd             int    `json:"port_end"`
}

func NewRunID(now time.Time) (string, error) {
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return fmt.Sprintf("rt-%s-%s", now.UTC().Format("20060102t150405z"), hex.EncodeToString(random)), nil
}

func ValidateRunID(runID string) error {
	if !runIDPattern.MatchString(runID) {
		return fmt.Errorf("run-id %q must match %s", runID, runIDPattern.String())
	}
	return nil
}

func AllocateRun(runID, remoteWorkRoot string, slot, maxConcurrency, portBase, portBlockSize int) (Allocation, error) {
	if err := ValidateRunID(runID); err != nil {
		return Allocation{}, err
	}
	if err := validateRemoteWorkRoot(remoteWorkRoot); err != nil {
		return Allocation{}, err
	}
	if maxConcurrency < 1 || slot < 0 || slot >= maxConcurrency {
		return Allocation{}, fmt.Errorf("slot %d is outside target concurrency 0..%d", slot, maxConcurrency-1)
	}
	if portBase < 1024 || portBase > 65535 || portBlockSize < 16 {
		return Allocation{}, errors.New("port allocation needs a non-privileged base and a block of at least 16 ports")
	}
	if portBlockSize > 65536-portBase || maxConcurrency > (65536-portBase)/portBlockSize {
		return Allocation{}, errors.New("port allocation cannot provide one complete block per concurrency slot")
	}
	portStart := portBase + slot*portBlockSize
	portEnd := portStart + portBlockSize - 1
	if portEnd > 65535 {
		return Allocation{}, errors.New("port allocation exceeds 65535")
	}
	scope := strings.ReplaceAll(runID, "-", "_")
	workspace := path.Join(remoteWorkRoot, "runs", runID)
	runtimeRoot := path.Join(remoteWorkRoot, "runtime", runID)
	return Allocation{
		RunID: runID, Slot: slot,
		Workspace: workspace, ReportDirectory: path.Join(workspace, "reports"),
		ContainerPrefix: "anas_" + scope + "_", NetworkPrefix: "anas_" + scope + "_",
		NetworkNamespace: runID,
		DockerSocket:     path.Join("/run/anas-test", runID, "docker.sock"),
		DockerDataRoot:   path.Join(runtimeRoot, "docker"), DockerExecRoot: path.Join("/run/anas-test", runID, "docker-exec"),
		ContainerdSocket: path.Join("/run/anas-test", runID, "containerd.sock"),
		ContainerdRoot:   path.Join(runtimeRoot, "containerd"), ContainerdState: path.Join("/run/anas-test", runID, "containerd-state"),
		ContainerdNamespace: runID,
		PortStart:           portStart, PortEnd: portEnd,
	}, nil
}
