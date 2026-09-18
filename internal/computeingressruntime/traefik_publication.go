package computeingressruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"time"
)

func (r *TraefikReader) matchPublication(snapshot *traefikSnapshot, target PublicationTarget) error {
	id := routeID(target) + "@file"
	var router traefikRouter
	if strictTraefikRecord(snapshot.Routers[id], &router) != nil {
		return fmt.Errorf("Traefik publication router is missing or has unsupported fields")
	}
	wantRule := "Host(`" + target.Publication.Host + "`)"
	var tls map[string]json.RawMessage
	if router.Status != "enabled" || len(router.Errors) != 0 || router.Rule != wantRule || (router.RuleSyntax != "" && router.RuleSyntax != "v3") || qualifiedReference(router.Service, id) != id || len(router.ParentRefs) != 0 || !slices.Equal(router.EntryPoints, []string{"https"}) || !slices.Equal(router.Using, []string{"https"}) || (router.Priority != 0 && router.Priority != len(wantRule)) || json.Unmarshal(router.TLS, &tls) != nil || tls == nil || len(tls) != 0 {
		return fmt.Errorf("Traefik router differs from the frozen HTTPS publication")
	}
	if target.Publication.Auth == "none" {
		if len(router.Middlewares) != 0 {
			return fmt.Errorf("Traefik router has an unexpected middleware")
		}
	} else if !slices.Equal(router.Middlewares, []string{target.Publication.Middleware}) {
		return fmt.Errorf("Traefik router does not use the frozen ForwardAuth middleware")
	}
	if err := r.matchAuth(snapshot, target); err != nil {
		return err
	}
	var service struct {
		LoadBalancer *struct {
			Servers []struct {
				URL          string `json:"url"`
				Weight       *int   `json:"weight"`
				PreservePath bool   `json:"preservePath"`
				Fenced       bool   `json:"fenced"`
			} `json:"servers"`
			Strategy           string `json:"strategy"`
			PassHostHeader     *bool  `json:"passHostHeader"`
			ServersTransport   string `json:"serversTransport"`
			ResponseForwarding *struct {
				FlushInterval json.RawMessage `json:"flushInterval"`
			} `json:"responseForwarding"`
		} `json:"loadBalancer"`
		Middlewares  []string          `json:"middlewares"`
		Status       string            `json:"status"`
		Errors       []string          `json:"error"`
		UsedBy       []string          `json:"usedBy"`
		ServerStatus map[string]string `json:"serverStatus"`
	}
	if strictTraefikRecord(snapshot.Services[id], &service) != nil || service.Status != "enabled" || len(service.Errors) != 0 || len(service.Middlewares) != 0 || !slices.Equal(service.UsedBy, []string{id}) || service.LoadBalancer == nil {
		return fmt.Errorf("Traefik publication service is missing, shared or unsupported")
	}
	lb := service.LoadBalancer
	if len(lb.Servers) != 1 || lb.PassHostHeader == nil || !*lb.PassHostHeader || lb.ServersTransport != "" || (lb.Strategy != "" && lb.Strategy != "wrr") || lb.ResponseForwarding == nil || !defaultFlushInterval(lb.ResponseForwarding.FlushInterval) {
		return fmt.Errorf("Traefik backend does not match the constrained file renderer")
	}
	server := lb.Servers[0]
	backend := httpBackend(target)
	// Traefik initially labels an accepted backend UP without probing it.
	// This confirms configuration construction, never HTTP reachability or
	// identity. The executor's independent BackendProbe remains mandatory.
	if server.URL != backend || (server.Weight != nil && *server.Weight != 1) || server.PreservePath || server.Fenced || len(service.ServerStatus) != 1 || service.ServerStatus[backend] != "UP" {
		return fmt.Errorf("Traefik has not enabled the exact observed HTTP backend")
	}
	return nil
}

func (r *TraefikReader) matchAuth(snapshot *traefikSnapshot, target PublicationTarget) error {
	if target.Publication.Auth == "none" {
		return nil
	}
	name := target.Publication.Middleware
	pin, exists := r.authPins[name]
	if !exists {
		return fmt.Errorf("ForwardAuth requires a trusted installation configuration pin")
	}
	var middleware struct {
		ForwardAuth json.RawMessage `json:"forwardAuth"`
		Status      string          `json:"status"`
		Errors      []string        `json:"error"`
		UsedBy      []string        `json:"usedBy"`
	}
	if strictTraefikRecord(snapshot.Middlewares[name], &middleware) != nil || middleware.Status != "enabled" || len(middleware.Errors) != 0 {
		return fmt.Errorf("frozen ForwardAuth middleware is unavailable, disabled or not direct ForwardAuth")
	}
	definition, err := json.Marshal(map[string]json.RawMessage{"forwardAuth": middleware.ForwardAuth})
	if err != nil {
		return fmt.Errorf("cannot normalize the loaded ForwardAuth definition")
	}
	digest, err := ForwardAuthDigest(definition)
	if err != nil || digest != pin {
		return fmt.Errorf("loaded ForwardAuth definition differs from the trusted installation pin")
	}
	return nil
}

// ForwardAuthDigest normalizes an already trusted provider definition for the
// installer. Input must contain only the dynamic forwardAuth object, including
// the defaults of the pinned Traefik version, not runtime status metadata.
// Computing a digest from the live API and trusting it is NOT authorization.
func ForwardAuthDigest(definition []byte) (string, error) {
	var config struct {
		ForwardAuth json.RawMessage `json:"forwardAuth"`
	}
	if len(definition) == 0 || len(definition) > 64<<10 || strictTraefikRecord(definition, &config) != nil || !presentJSONObject(config.ForwardAuth) {
		return "", fmt.Errorf("expected one bounded ForwardAuth definition")
	}
	var selected struct {
		Address string `json:"address"`
	}
	if decodeObservedJSON(config.ForwardAuth, &selected) != nil {
		return "", fmt.Errorf("ForwardAuth definition has no usable address")
	}
	u, err := url.Parse(selected.Address)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return "", fmt.Errorf("ForwardAuth definition has no usable HTTP address")
	}
	canonical, err := canonicalObservedJSON(definition)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

func strictTraefikRecord(raw []byte, out any) error {
	if !presentJSONObject(raw) || decodeObservedJSON(raw, out) != nil {
		return fmt.Errorf("invalid Traefik runtime object")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return fmt.Errorf("unsupported Traefik runtime object fields")
	}
	return nil
}

func canonicalObservedJSON(raw []byte) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if d.Decode(&value) != nil {
		return nil, fmt.Errorf("cannot normalize observer JSON")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("cannot normalize observer JSON")
	}
	return canonical, nil
}

func defaultFlushInterval(raw json.RawMessage) bool {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		duration, err := time.ParseDuration(text)
		return err == nil && duration == 100*time.Millisecond
	}
	var nanos int64
	return json.Unmarshal(raw, &nanos) == nil && nanos == int64(100*time.Millisecond)
}
