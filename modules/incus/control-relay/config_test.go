package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func validRelayConfiguration() relayConfiguration {
	return relayConfiguration{
		SchemaVersion: 1, ListenAddress: "10.77.0.1:18443", ControlSubnet: "10.77.0.0/24",
		InterfaceName: "br-anas-ctrl", InterfaceIndex: 42, RunUID: 991, RunGID: 991,
		MaxConnections: 64, IdleTimeoutSeconds: 300,
	}
}

func TestRelayConfigurationStrictShape(t *testing.T) {
	body, err := json.Marshal(validRelayConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeRelayConfiguration(body); err != nil {
		t.Fatal(err)
	}
	base := string(body)
	cases := map[string]string{
		"duplicate":      strings.Replace(base, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1),
		"escaped alias":  strings.Replace(base, `"schema_version":1`, `"schema_version":1,"schema_\u0076ersion":1`, 1),
		"case alias":     strings.Replace(base, `"schema_version"`, `"Schema_version"`, 1),
		"upstream":       strings.TrimSuffix(base, "}") + `,"upstream":"https://untrusted.invalid"}`,
		"TLS key":        strings.TrimSuffix(base, "}") + `,"client_key":"not-accepted"}`,
		"null":           strings.Replace(base, `"schema_version":1`, `"schema_version":null`, 1),
		"missing":        strings.Replace(base, `"schema_version":1,`, "", 1),
		"fraction":       strings.Replace(base, `"schema_version":1`, `"schema_version":1.0`, 1),
		"numeric string": strings.Replace(base, `"schema_version":1`, `"schema_version":"1"`, 1),
		"array":          "[" + base + "]",
		"trailing":       base + "{}",
		"too large":      base + strings.Repeat(" ", maximumConfigurationBytes),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeRelayConfiguration([]byte(body)); !errors.Is(err, errConfiguration) {
				t.Fatal("invalid configuration was accepted")
			}
		})
	}
}

func TestRelayConfigurationBindings(t *testing.T) {
	cases := map[string]func(*relayConfiguration){
		"wildcard":          func(c *relayConfiguration) { c.ListenAddress = "0.0.0.0:18443" },
		"loopback":          func(c *relayConfiguration) { c.ListenAddress = "127.0.0.1:18443" },
		"public":            func(c *relayConfiguration) { c.ListenAddress = "8.8.8.8:18443" },
		"hostname":          func(c *relayConfiguration) { c.ListenAddress = "control.internal:18443" },
		"IPv6":              func(c *relayConfiguration) { c.ListenAddress = "[fd00::1]:18443" },
		"mapped IPv6":       func(c *relayConfiguration) { c.ListenAddress = "[::ffff:10.77.0.1]:18443" },
		"privileged port":   func(c *relayConfiguration) { c.ListenAddress = "10.77.0.1:443" },
		"network address":   func(c *relayConfiguration) { c.ListenAddress = "10.77.0.0:18443" },
		"broadcast address": func(c *relayConfiguration) { c.ListenAddress = "10.77.0.255:18443" },
		"outside subnet":    func(c *relayConfiguration) { c.ListenAddress = "10.78.0.1:18443" },
		"unmasked subnet":   func(c *relayConfiguration) { c.ControlSubnet = "10.77.0.1/24" },
		"broad subnet":      func(c *relayConfiguration) { c.ControlSubnet = "0.0.0.0/0" },
		"no interface":      func(c *relayConfiguration) { c.InterfaceIndex = 0 },
		"interface path":    func(c *relayConfiguration) { c.InterfaceName = "../../net" },
		"root UID":          func(c *relayConfiguration) { c.RunUID = 0 },
		"root GID":          func(c *relayConfiguration) { c.RunGID = 0 },
		"no capacity":       func(c *relayConfiguration) { c.MaxConnections = 0 },
		"excess capacity":   func(c *relayConfiguration) { c.MaxConnections = 257 },
		"no timeout":        func(c *relayConfiguration) { c.IdleTimeoutSeconds = 0 },
		"excess timeout":    func(c *relayConfiguration) { c.IdleTimeoutSeconds = 3601 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			config := validRelayConfiguration()
			change(&config)
			if _, err := validateRelayConfiguration(config); !errors.Is(err, errConfiguration) {
				t.Fatal("invalid binding was accepted")
			}
		})
	}
}

func TestRelaySourceScopeAndDestination(t *testing.T) {
	settings, err := validateRelayConfiguration(validRelayConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	if !settings.acceptsSource(netip.MustParseAddr("10.77.0.2")) {
		t.Fatal("control subnet source rejected")
	}
	for _, source := range []string{"10.77.0.0", "10.77.0.1", "10.77.0.255", "10.78.0.2", "127.0.0.1", "fd00::2", "::ffff:10.77.0.2"} {
		if settings.acceptsSource(netip.MustParseAddr(source)) {
			t.Fatal("out-of-scope or reserved source accepted")
		}
	}
	if incusLoopbackDestination != "127.0.0.1:8443" {
		t.Fatal("fixed daemon destination changed")
	}
}

func TestRelayCommandRejectsOverridesWithoutEcho(t *testing.T) {
	var output bytes.Buffer
	err := runRelayCommand(context.Background(), []string{"--upstream=private-value.invalid"}, &output)
	if !errors.Is(err, errConfiguration) || output.Len() != 0 || strings.Contains(err.Error(), "private-value") {
		t.Fatal("override was accepted or echoed")
	}
	if err := runRelayCommand(context.Background(), []string{"--help"}, &output); err != nil || !strings.Contains(output.String(), "not installed or enabled automatically") {
		t.Fatal("help did not describe installation boundary")
	}
}
