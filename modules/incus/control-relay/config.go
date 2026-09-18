package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"regexp"
)

const (
	maximumConfigurationBytes = 8192
	incusLoopbackDestination  = "127.0.0.1:8443"
)

var (
	errConfiguration = errors.New("invalid Incus control relay installation configuration")
	errIdentity      = errors.New("Incus control relay requires its installed unprivileged identity")
	errTopology      = errors.New("Incus control relay installed interface binding is unavailable")
	errListener      = errors.New("Incus control relay listener failed")
	errUnsupported   = errors.New("Incus control relay is only supported on Linux")
	interfaceName    = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,14}$`)
)

// This is trusted installation input, not a consumer request or Module config.
// There is intentionally no upstream URL, target port, TLS identity, command,
// environment, or proxy protocol. Changes require replacing the root-owned
// configuration and restarting the service through the host action channel.
type relayConfiguration struct {
	SchemaVersion      int    `json:"schema_version"`
	ListenAddress      string `json:"listen_address"`
	ControlSubnet      string `json:"control_subnet"`
	InterfaceName      string `json:"interface_name"`
	InterfaceIndex     int    `json:"interface_index"`
	RunUID             uint32 `json:"run_uid"`
	RunGID             uint32 `json:"run_gid"`
	MaxConnections     int    `json:"max_connections"`
	IdleTimeoutSeconds int    `json:"idle_timeout_seconds"`
}

type relaySettings struct {
	relayConfiguration
	listen netip.AddrPort
	subnet netip.Prefix
}

func decodeRelayConfiguration(body []byte) (relaySettings, error) {
	if len(body) == 0 || len(body) > maximumConfigurationBytes {
		return relaySettings{}, errConfiguration
	}
	allowed := map[string]bool{
		"schema_version": false, "listen_address": false, "control_subnet": false,
		"interface_name": false, "interface_index": false, "run_uid": false,
		"run_gid": false, "max_connections": false, "idle_timeout_seconds": false,
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return relaySettings{}, errConfiguration
	}
	for decoder.More() {
		token, err = decoder.Token()
		key, ok := token.(string)
		seen, exists := allowed[key]
		if err != nil || !ok || !exists || seen {
			return relaySettings{}, errConfiguration
		}
		allowed[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return relaySettings{}, errConfiguration
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') || decoder.Decode(&struct{}{}) != io.EOF {
		return relaySettings{}, errConfiguration
	}
	for _, seen := range allowed {
		if !seen {
			return relaySettings{}, errConfiguration
		}
	}
	var config relayConfiguration
	if json.Unmarshal(body, &config) != nil {
		return relaySettings{}, errConfiguration
	}
	return validateRelayConfiguration(config)
}

func validateRelayConfiguration(config relayConfiguration) (relaySettings, error) {
	listen, listenErr := netip.ParseAddrPort(config.ListenAddress)
	subnet, subnetErr := netip.ParsePrefix(config.ControlSubnet)
	if listenErr != nil || subnetErr != nil || config.SchemaVersion != 1 ||
		!listen.Addr().Is4() || !listen.Addr().IsPrivate() || listen.String() != config.ListenAddress || listen.Port() < 1024 ||
		!subnet.Addr().Is4() || subnet.Bits() < 1 || subnet.Bits() > 30 || subnet != subnet.Masked() ||
		subnet.String() != config.ControlSubnet || !subnet.Contains(listen.Addr()) ||
		!subnet.Addr().IsPrivate() || !lastIPv4(subnet).IsPrivate() ||
		listen.Addr() == subnet.Addr() || listen.Addr() == lastIPv4(subnet) ||
		!interfaceName.MatchString(config.InterfaceName) || config.InterfaceIndex <= 0 ||
		config.RunUID == 0 || config.RunGID == 0 ||
		config.MaxConnections < 1 || config.MaxConnections > 256 ||
		config.IdleTimeoutSeconds < 10 || config.IdleTimeoutSeconds > 3600 {
		return relaySettings{}, errConfiguration
	}
	return relaySettings{relayConfiguration: config, listen: listen, subnet: subnet}, nil
}

func lastIPv4(prefix netip.Prefix) netip.Addr {
	value := prefix.Masked().Addr().As4()
	last := binary.BigEndian.Uint32(value[:]) | (^uint32(0) >> prefix.Bits())
	binary.BigEndian.PutUint32(value[:], last)
	return netip.AddrFrom4(value)
}

// Source filtering narrows the reachable set but is NOT authentication and does
// not prove the ingress interface. Host INPUT rules and the daemon's mTLS and
// project authorization remain mandatory. TLS bytes are never inspected here.
func (settings relaySettings) acceptsSource(address netip.Addr) bool {
	return address.Is4() && settings.subnet.Contains(address) &&
		address != settings.listen.Addr() && address != settings.subnet.Addr() && address != lastIPv4(settings.subnet)
}
