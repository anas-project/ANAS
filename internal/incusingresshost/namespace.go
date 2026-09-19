package incusingresshost

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func validateInstalledNamespacePin(pin RouteNamespacePin) error {
	if err := validateNamespacePin(pin); err != nil {
		return err
	}
	if pin.PID <= 1 || pin.StartTimeTicks == 0 || !uuidString.MatchString(pin.BootID) || pin.HostVethIfIndex == 0 || pin.HostVethPeerIfIndex != pin.TraefikIfIndex {
		return fmt.Errorf("installed namespace requires a kernel process incarnation")
	}
	if _, err := time.Parse(time.RFC3339Nano, pin.DockerStartedAt); err != nil {
		return fmt.Errorf("installed namespace requires a Docker start timestamp")
	}
	return nil
}

func processStartTicks(body []byte, pid int) (uint64, error) {
	value := string(body)
	end := strings.LastIndex(value, ") ")
	if end < 0 || !strings.HasPrefix(value, strconv.Itoa(pid)+" (") {
		return 0, fmt.Errorf("invalid installed process identity")
	}
	fields := strings.Fields(value[end+2:])
	if len(fields) < 20 || (fields[0] != "R" && fields[0] != "S" && fields[0] != "D") {
		return 0, fmt.Errorf("installed container process is not running")
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || ticks == 0 {
		return 0, fmt.Errorf("installed process has no valid start identity")
	}
	return ticks, nil
}

// Docker responses have additional version-dependent fields. Observe only the
// documented fields needed here, after checking the entire response for duplicate
// keys. A container name, namespace pathname or consumer-supplied PID is not proof.
func verifyDockerNamespaceDocument(body []byte, config backendConfig) error {
	var document map[string]any
	if err := decodeObservedJSON(body, &document); err != nil {
		return err
	}
	state, ok := document["State"].(map[string]any)
	if !ok || stringField(document, "Id") != config.Namespace.DockerContainerID ||
		stringField(state, "StartedAt") != config.Namespace.DockerStartedAt ||
		numberField(state, "Pid") != strconv.Itoa(config.Namespace.PID) || stringField(state, "Status") != "running" {
		return fmt.Errorf("installed Docker container incarnation changed")
	}
	for field, expected := range map[string]bool{"Running": true, "Paused": false, "Restarting": false, "Dead": false} {
		value, ok := state[field].(bool)
		if !ok || value != expected {
			return fmt.Errorf("installed Docker container is not running")
		}
	}
	settings, ok := document["NetworkSettings"].(map[string]any)
	if !ok {
		return fmt.Errorf("installed Docker network identity is unavailable")
	}
	networks, ok := settings["Networks"].(map[string]any)
	if !ok || len(networks) == 0 || len(networks) > 64 {
		return fmt.Errorf("installed Docker network inventory is incomplete")
	}
	matches := 0
	for _, value := range networks {
		endpoint, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("installed Docker network inventory is invalid")
		}
		if stringField(endpoint, "IPAddress") != config.TraefikSourceIP {
			continue
		}
		if stringField(endpoint, "MacAddress") != config.Namespace.TraefikMAC || stringField(endpoint, "Gateway") != config.IngressGateway ||
			!hex64(stringField(endpoint, "NetworkID")) || !hex64(stringField(endpoint, "EndpointID")) {
			return fmt.Errorf("installed Docker ingress endpoint changed")
		}
		matches++
	}
	if matches != 1 {
		return fmt.Errorf("installed Docker ingress endpoint is not unique")
	}
	return nil
}
