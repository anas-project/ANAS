package incusprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var errIncusNotFound = errors.New("incus object not found")

type incusUnixClient struct {
	socket string
	// Private transport seam for protocol tests; never populated from a
	// request, environment, manifest or production configuration.
	transport http.RoundTripper
}

const maxIncusResponseBytes = 4 << 20

var incusOperationPath = regexp.MustCompile(`^/1\.0/operations/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)

type incusEnvelope struct {
	Type       string          `json:"type"`
	Status     string          `json:"status"`
	StatusCode int             `json:"status_code"`
	Operation  string          `json:"operation"`
	ErrorText  string          `json:"error"`
	ErrorCode  int             `json:"error_code"`
	Metadata   json.RawMessage `json:"metadata"`
}

type incusServer struct {
	Config map[string]string `json:"config"`
}

type incusStoragePool struct {
	Name   string            `json:"name"`
	Driver string            `json:"driver"`
	Config map[string]string `json:"config"`
	Status string            `json:"status"`
}

type incusCertificate struct {
	Name        string   `json:"name"`
	Fingerprint string   `json:"fingerprint"`
	Certificate string   `json:"certificate"`
	Type        string   `json:"type"`
	Restricted  bool     `json:"restricted"`
	Projects    []string `json:"projects"`
}

type incusInstance struct {
	Name            string                       `json:"name"`
	Project         string                       `json:"project"`
	Status          string                       `json:"status"`
	Devices         map[string]map[string]string `json:"devices"`
	ExpandedDevices map[string]map[string]string `json:"expanded_devices"`
}

type incusNetwork struct {
	Name   string            `json:"name"`
	Type   string            `json:"type"`
	Config map[string]string `json:"config"`
}

type incusStorageVolume struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func (c *incusUnixClient) httpClient() *http.Client {
	var transport http.RoundTripper = &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		if err := verifyRootOwnedUnixSocket(c.socket); err != nil {
			return nil, err
		}
		var d net.Dialer
		return d.DialContext(ctx, "unix", c.socket)
	}}
	if c.transport != nil {
		transport = c.transport
	}
	return &http.Client{Transport: transport, Timeout: 40 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (c *incusUnixClient) do(ctx context.Context, method, path string, body any, out any) error {
	status, raw, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	env, err := parseIncusEnvelope(raw)
	if err != nil {
		return err
	}
	if env.Type == "async" {
		// Reads must never be silently upgraded to asynchronous writes; an
		// accepted response is not evidence that metadata has been populated.
		if status != http.StatusAccepted || env.StatusCode != 100 || env.ErrorCode != 0 || env.ErrorText != "" || out != nil ||
			(method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch && method != http.MethodDelete) {
			return ErrExternalEffects
		}
		return c.waitOperation(ctx, env.Operation)
	}
	return decodeIncusMethodEnvelope(method, status, raw, out)
}

func parseIncusEnvelope(raw []byte) (incusEnvelope, error) {
	var env incusEnvelope
	if len(raw) == 0 || len(raw) > maxIncusResponseBytes || !utf8.Valid(raw) || validateNoDuplicateJSONFields(raw) != nil {
		return env, ErrExternalEffects
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return env, ErrExternalEffects
	}
	// The response envelope is fixed, while metadata remains extensible.
	for key := range fields {
		switch key {
		case "type", "status", "status_code", "operation", "error", "error_code", "metadata":
		default:
			return env, ErrExternalEffects
		}
	}
	if json.Unmarshal(raw, &env) != nil {
		return env, ErrExternalEffects
	}
	return env, nil
}

func decodeIncusEnvelope(status int, raw []byte, out any) error {
	return decodeIncusMethodEnvelope(http.MethodGet, status, raw, out)
}

// Incus returns HTTP 201 for synchronous POST creation (including pools and
// certificates), while its envelope status remains 200. Do not retry a write
// that has already completed, or relax reads/async operations to arbitrary 2xx.
func decodeIncusMethodEnvelope(method string, status int, raw []byte, out any) error {
	env, err := parseIncusEnvelope(raw)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound && env.Type == "error" && env.ErrorCode == http.StatusNotFound {
		return errIncusNotFound
	}
	completed := status == http.StatusOK || (method == http.MethodPost && status == http.StatusCreated)
	if !completed || env.Type != "sync" || env.StatusCode != 200 || env.ErrorCode != 0 || env.ErrorText != "" || env.Operation != "" {
		return ErrExternalEffects
	}
	if out != nil {
		if len(env.Metadata) == 0 || bytes.Equal(bytes.TrimSpace(env.Metadata), []byte("null")) || json.Unmarshal(env.Metadata, out) != nil {
			return ErrExternalEffects
		}
	}
	return nil
}

func (c *incusUnixClient) waitOperation(ctx context.Context, operation string) error {
	match := incusOperationPath.FindStringSubmatch(operation)
	if ctx == nil || len(match) != 2 {
		return ErrExternalEffects
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	for {
		var result struct {
			ID         string `json:"id"`
			StatusCode int    `json:"status_code"`
			Err        string `json:"err"`
		}
		if err := c.doSyncOnly(ctx, http.MethodGet, operation+"/wait?timeout=30", nil, &result); err != nil {
			return err
		}
		if result.ID != match[1] || result.Err != "" {
			return ErrExternalEffects
		}
		if result.StatusCode == 200 {
			return nil
		}
		switch result.StatusCode {
		case 100, 103, 105: // Created, running or pending. Keep waiting, not resubmitting.
		default:
			return ErrExternalEffects
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *incusUnixClient) doSyncOnly(ctx context.Context, method, path string, body any, out any) error {
	status, raw, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	return decodeIncusEnvelope(status, raw, out)
}

func (c *incusUnixClient) request(ctx context.Context, method, path string, body any) (status int, raw []byte, result error) {
	if c == nil || ctx == nil || !strings.HasPrefix(path, "/1.0") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\r\n\x00#") {
		return 0, nil, ErrInvalid
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, ErrInvalid
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://unix.socket"+path, reader)
	if err != nil {
		return 0, nil, ErrInvalid
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.httpClient()
	defer client.CloseIdleConnections()
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		return 0, nil, ErrExternalEffects
	}
	defer func() {
		if res.Body.Close() != nil {
			raw = nil
			result = ErrExternalEffects
		}
	}()
	if res.ContentLength > maxIncusResponseBytes {
		return 0, nil, ErrExternalEffects
	}
	raw, err = io.ReadAll(io.LimitReader(res.Body, maxIncusResponseBytes+1))
	if err != nil || len(raw) > maxIncusResponseBytes {
		return 0, nil, ErrExternalEffects
	}
	return res.StatusCode, raw, nil
}

func (c *incusUnixClient) getServer(ctx context.Context) (incusServer, error) {
	var out incusServer
	err := c.do(ctx, "GET", "/1.0", nil, &out)
	return out, err
}

func (c *incusUnixClient) patchServer(ctx context.Context, config map[string]string) error {
	return c.do(ctx, "PATCH", "/1.0", map[string]any{"config": config}, nil)
}

func (c *incusUnixClient) getStoragePool(ctx context.Context, name string) (incusStoragePool, error) {
	var out incusStoragePool
	err := c.do(ctx, "GET", "/1.0/storage-pools/"+url.PathEscape(name), nil, &out)
	return out, err
}

func (c *incusUnixClient) createStoragePool(ctx context.Context, name, driver string, config map[string]string) error {
	return c.do(ctx, "POST", "/1.0/storage-pools", map[string]any{"name": name, "driver": driver, "config": config}, nil)
}

func (c *incusUnixClient) deleteStoragePool(ctx context.Context, name string) error {
	err := c.do(ctx, "DELETE", "/1.0/storage-pools/"+url.PathEscape(name), nil, nil)
	if errors.Is(err, errIncusNotFound) {
		return nil
	}
	return err
}

func (c *incusUnixClient) addCertificate(ctx context.Context, credential Credential) error {
	body := map[string]any{"name": credential.Name, "type": "client", "certificate": credential.Certificate, "restricted": false}
	return c.do(ctx, "POST", "/1.0/certificates", body, nil)
}

func (c *incusUnixClient) getCertificate(ctx context.Context, fingerprint string) (incusCertificate, error) {
	var out incusCertificate
	err := c.do(ctx, "GET", "/1.0/certificates/"+url.PathEscape(fingerprint), nil, &out)
	return out, err
}

func (c *incusUnixClient) deleteCertificate(ctx context.Context, fingerprint string) error {
	if fingerprint == "" {
		return nil
	}
	err := c.do(ctx, "DELETE", "/1.0/certificates/"+url.PathEscape(fingerprint), nil, nil)
	if errors.Is(err, errIncusNotFound) {
		return nil
	}
	return err
}

func (c *incusUnixClient) listInstances(ctx context.Context) ([]incusInstance, error) {
	var out []incusInstance
	err := c.do(ctx, "GET", "/1.0/instances?recursion=2&all-projects=true", nil, &out)
	return out, err
}

func (c *incusUnixClient) listNetworkCIDRs(ctx context.Context) ([]string, error) {
	var out []incusNetwork
	if err := c.do(ctx, "GET", "/1.0/networks?recursion=1", nil, &out); err != nil {
		return nil, err
	}
	var cidrs []string
	for _, network := range out {
		for _, key := range []string{"ipv4.address", "ipv6.address"} {
			value := network.Config[key]
			if value == "" || value == "none" || value == "auto" {
				continue
			}
			prefix, err := netip.ParsePrefix(value)
			if err != nil {
				return nil, ErrIncomplete
			}
			cidrs = append(cidrs, prefix.Masked().String())
		}
	}
	return cidrs, nil
}

func (c *incusUnixClient) listStoragePoolVolumes(ctx context.Context, name string) ([]incusStorageVolume, error) {
	var out []incusStorageVolume
	err := c.do(ctx, "GET", "/1.0/storage-pools/"+url.PathEscape(name)+"/volumes?recursion=1", nil, &out)
	if errors.Is(err, errIncusNotFound) {
		return nil, nil
	}
	return out, err
}
