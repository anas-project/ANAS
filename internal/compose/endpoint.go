package compose

import (
	"sort"
	"strings"
)

// Endpoint freezes Docker's process-owned selection inputs when Compose is
// detected. Module values never select the daemon checked or modified.
// Unset selectors stay unset, preserving Docker's configured default context.
type Endpoint struct{ environment map[string]string }

func endpointVariable(key string) bool {
	switch key {
	case "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "HOME":
		return true
	}
	return false
}

func freezeEndpoint(environment []string) *Endpoint {
	values := map[string]string{}
	for _, item := range environment {
		key, value, ok := strings.Cut(item, "=")
		if ok && endpointVariable(key) {
			values[key] = value
		}
	}
	return &Endpoint{environment: values}
}

// Environment returns a copy with one assignment per key. Selection variables
// can only come from the process context, never the deployment overlay.
func (c CLI) Environment(base []string, deployment map[string]string) []string {
	values := map[string]string{}
	for _, item := range base {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	for key, value := range deployment {
		if !endpointVariable(key) {
			values[key] = value
		}
	}
	if c.endpoint != nil {
		for key := range values {
			if endpointVariable(key) {
				delete(values, key)
			}
		}
		for key, value := range c.endpoint.environment {
			values[key] = value
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func (c CLI) HasEndpoint() bool { return c.endpoint != nil }
