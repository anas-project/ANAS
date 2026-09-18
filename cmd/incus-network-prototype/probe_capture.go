package main

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"time"

	"github.com/anas-project/ANAS/internal/computeingressruntime"
)

// This is the existing administrator lab boundary, not a daemon credential
// grant or a third privileged launcher. The process must be pre-positioned in
// the selected container's netns while retaining a procfs view of its host PID.
type probeCaptureConfig struct {
	DockerSocket    string `json:"docker_socket"`
	DockerContainer string `json:"docker_container"`
	DockerNetwork   string `json:"docker_network"`
}

type probeContainerSample struct {
	PID      int
	Endpoint dockerEndpoint
}

func readProbeContainer(ctx context.Context, reader *socketReader, config probeCaptureConfig) (probeContainerSample, error) {
	empty := probeContainerSample{}
	var container dockerContainer
	if reader.get(ctx, "/containers/"+config.DockerContainer+"/json", &container) != nil || container.ID != config.DockerContainer || !container.State.Running || container.State.Paused || container.State.Restarting || container.State.Pid <= 1 {
		return empty, fmt.Errorf("selected lab Traefik container is unavailable or not running")
	}
	var endpoint dockerEndpoint
	count := 0
	for _, candidate := range container.NetworkSettings.Networks {
		if candidate.NetworkID == config.DockerNetwork {
			endpoint = candidate
			count++
		}
	}
	ip, err := netip.ParseAddr(endpoint.IPAddress)
	if count != 1 || endpoint.EndpointID == "" || err != nil || !ip.Is4() || !ip.IsPrivate() || ip.String() != endpoint.IPAddress || endpoint.IPPrefixLen < 8 || endpoint.IPPrefixLen > 30 {
		return empty, fmt.Errorf("selected lab ingress endpoint is missing or ambiguous")
	}
	var network dockerNetwork
	if reader.get(ctx, "/networks/"+config.DockerNetwork, &network) != nil || network.ID != config.DockerNetwork || network.Driver != "bridge" || network.Scope != "local" {
		return empty, fmt.Errorf("lab probe requires the selected local Docker bridge")
	}
	allocated, ok := network.Containers[config.DockerContainer]
	prefix, err := netip.ParsePrefix(allocated.IPv4Address)
	if !ok || err != nil || prefix.Addr() != ip || prefix.Bits() != endpoint.IPPrefixLen || allocated.EndpointID != endpoint.EndpointID || allocated.MacAddress != endpoint.MacAddress {
		return empty, fmt.Errorf("Docker bridge allocation differs from the selected lab endpoint")
	}
	return probeContainerSample{PID: container.State.Pid, Endpoint: endpoint}, nil
}

func captureLabProbe(configPath, destination string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("lab probe identity capture requires Linux")
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return fmt.Errorf("probe capture requires a new output directory")
	}
	var config probeCaptureConfig
	if err := readInput(configPath, &config); err != nil {
		return err
	}
	identifier := regexp.MustCompile(`^[a-f0-9]{64}$`)
	if !identifier.MatchString(config.DockerContainer) || !identifier.MatchString(config.DockerNetwork) {
		return fmt.Errorf("probe capture requires full selected lab container/network IDs")
	}
	reader, err := newSocketReader(config.DockerSocket, false)
	if err != nil {
		return err
	}
	defer reader.client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	first, err := readProbeContainer(ctx, reader, config)
	if err != nil {
		return err
	}
	identity, err := computeingressruntime.CaptureTraefikProbeIdentity(ctx, first.PID, first.Endpoint.IPAddress)
	if err != nil {
		return err
	}
	second, err := readProbeContainer(ctx, reader, config)
	if err != nil || !reflect.DeepEqual(first, second) {
		return fmt.Errorf("lab Traefik mapping changed during probe identity capture")
	}
	again, err := computeingressruntime.CaptureTraefikProbeIdentity(ctx, second.PID, second.Endpoint.IPAddress)
	if err != nil || again != identity {
		return fmt.Errorf("lab Traefik process or namespace changed during capture")
	}
	record := struct {
		Schema     string                                     `json:"schema"`
		CapturedAt time.Time                                  `json:"captured_at"`
		Container  string                                     `json:"docker_container"`
		Network    string                                     `json:"docker_network"`
		Endpoint   string                                     `json:"docker_endpoint"`
		Identity   computeingressruntime.TraefikProbeIdentity `json:"identity"`
	}{"anas.compute-http-lab-probe-identity/v1", time.Now().UTC(), config.DockerContainer, config.DockerNetwork, first.Endpoint.EndpointID, identity}
	body, err := jsonArtifact(record)
	if err != nil {
		return fmt.Errorf("cannot encode lab probe identity")
	}
	return writeArtifacts(destination, map[string][]byte{"probe-identity.json": body})
}
