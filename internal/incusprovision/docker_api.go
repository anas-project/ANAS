package incusprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type dockerClient struct {
	socket    string
	transport http.RoundTripper // Private protocol-test seam, not configuration.
}

type dockerNetwork struct {
	ID         string                     `json:"Id"`
	Name       string                     `json:"Name"`
	Driver     string                     `json:"Driver"`
	Internal   bool                       `json:"Internal"`
	EnableIPv6 bool                       `json:"EnableIPv6"`
	ConfigOnly bool                       `json:"ConfigOnly"`
	Labels     map[string]string          `json:"Labels"`
	Options    map[string]string          `json:"Options"`
	Containers map[string]json.RawMessage `json:"Containers"`
	IPAM       struct {
		Config []struct {
			Subnet  string `json:"Subnet"`
			Gateway string `json:"Gateway"`
		} `json:"Config"`
	} `json:"IPAM"`
}

func (n dockerNetwork) ownedBy(ownerID string) bool {
	return ownerID != "" && n.Name == ControlNetworkName && n.Driver == "bridge" &&
		n.Internal && !n.EnableIPv6 && !n.ConfigOnly &&
		n.Labels["dev.anas.owner"] == "incus-host-provision" &&
		n.Labels["dev.anas.owner_id"] == ownerID &&
		n.Options["com.docker.network.bridge.enable_icc"] == "false" &&
		n.Options["com.docker.network.bridge.enable_ip_masquerade"] == "false" &&
		n.Options["com.docker.network.bridge.name"] == "br-anas-ctrl"
}

func (c *dockerClient) httpClient() *http.Client {
	var transport http.RoundTripper = &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		if verifyRootOwnedUnixSocket(c.socket) != nil {
			return nil, ErrUnsafeState
		}
		var d net.Dialer
		return d.DialContext(ctx, "unix", c.socket)
	}}
	if c.transport != nil {
		transport = c.transport
	}
	return &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (c *dockerClient) do(ctx context.Context, method, path string, body any, out any) (result error) {
	if c == nil || ctx == nil || !allowedDockerNetworkRequest(method, path) {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return ErrInvalid
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, reader)
	if err != nil {
		return ErrInvalid
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.httpClient()
	defer client.CloseIdleConnections()
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrExternalEffects
	}
	defer func() {
		if res.Body.Close() != nil {
			result = ErrExternalEffects
		}
	}()
	if res.ContentLength > maxIncusResponseBytes {
		return ErrExternalEffects
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxIncusResponseBytes+1))
	if err != nil || len(raw) > maxIncusResponseBytes {
		return ErrExternalEffects
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if res.StatusCode == http.StatusNotFound {
		var failure struct {
			Message string `json:"message"`
		}
		if (method != http.MethodGet && method != http.MethodDelete) || !strings.HasPrefix(path, "/v1.44/networks/") ||
			validateNoDuplicateJSONFields(raw) != nil || decodeNFTObject(raw, &failure, "message") != nil || failure.Message == "" {
			return ErrExternalEffects
		}
		return errIncusNotFound
	}
	expected := 0
	switch method {
	case http.MethodGet:
		expected = 200
	case http.MethodPost:
		expected = 201
	case http.MethodDelete:
		expected = 204
	}
	if expected == 0 || res.StatusCode != expected {
		return ErrExternalEffects
	}
	if out != nil {
		if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || validateNoDuplicateJSONFields(raw) != nil || json.Unmarshal(raw, out) != nil {
			return ErrExternalEffects
		}
	} else if method == http.MethodDelete && len(bytes.TrimSpace(raw)) != 0 {
		return ErrExternalEffects
	}
	return nil
}

func (c *dockerClient) inspectNetwork(ctx context.Context, name string) (dockerNetwork, error) {
	var out dockerNetwork
	var raw json.RawMessage
	if err := c.do(ctx, "GET", "/v1.44/networks/"+url.PathEscape(name), nil, &raw); err != nil {
		return out, err
	}
	err := decodeInventoryObject(raw, &out, "Id", "Name", "Driver", "Internal", "EnableIPv6", "ConfigOnly", "Labels", "Options", "IPAM", "Containers")
	return out, err
}

// This root client can inspect/create/delete its one managed network. A
// prefix check would also admit connect, disconnect and prune. Those actions
// must never be reachable from host provisioning, even during cleanup.
func allowedDockerNetworkRequest(method, path string) bool {
	switch method {
	case http.MethodGet:
		if path == "/v1.44/networks" || path == "/v1.44/networks/"+ControlNetworkName {
			return true
		}
		id, ok := strings.CutPrefix(path, "/v1.44/networks/")
		return ok && digestPattern.MatchString(id)
	case http.MethodPost:
		return path == "/v1.44/networks/create"
	case http.MethodDelete:
		id, ok := strings.CutPrefix(path, "/v1.44/networks/")
		return ok && digestPattern.MatchString(id)
	default:
		return false
	}
}

func (c *dockerClient) createControlNetwork(ctx context.Context, plan ControlNetworkPlan) error {
	body := map[string]any{
		"Name": plan.Name, "Driver": "bridge", "CheckDuplicate": true, "Internal": true,
		"Labels": map[string]string{"dev.anas.owner": "incus-host-provision", "dev.anas.owner_id": plan.OwnershipID, "dev.anas.component": "incus-control"},
		"Options": map[string]string{
			"com.docker.network.bridge.name":                 plan.Bridge,
			"com.docker.network.bridge.enable_icc":           "false",
			"com.docker.network.bridge.enable_ip_masquerade": "false",
		},
		"IPAM": map[string]any{"Driver": "default", "Config": []map[string]string{{"Subnet": plan.Subnet, "Gateway": plan.Gateway}}},
	}
	var created struct {
		ID      string `json:"Id"`
		Warning string `json:"Warning"`
	}
	if err := c.do(ctx, "POST", "/v1.44/networks/create", body, &created); err != nil {
		return err
	}
	if !digestPattern.MatchString(created.ID) || created.Warning != "" {
		return ErrExternalEffects
	}
	n, err := c.inspectNetwork(ctx, plan.Name)
	if err != nil {
		return err
	}
	if !n.ownedBy(plan.OwnershipID) || n.ID != created.ID || len(n.IPAM.Config) != 1 || n.IPAM.Config[0].Subnet != plan.Subnet || n.IPAM.Config[0].Gateway != plan.Gateway {
		return ErrExternalEffects
	}
	return nil
}

func (c *dockerClient) listNetworkCIDRs(ctx context.Context) ([]string, error) {
	var out []dockerNetwork
	if err := c.do(ctx, "GET", "/v1.44/networks", nil, &out); err != nil {
		return nil, err
	}
	var cidrs []string
	for _, network := range out {
		for _, cfg := range network.IPAM.Config {
			if cfg.Subnet != "" {
				cidrs = append(cidrs, cfg.Subnet)
			}
		}
	}
	return cidrs, nil
}

func (c *dockerClient) deleteNetwork(ctx context.Context, id string) error {
	if !digestPattern.MatchString(id) {
		return ErrInvalid
	}
	err := c.do(ctx, "DELETE", "/v1.44/networks/"+url.PathEscape(id), nil, nil)
	if errors.Is(err, errIncusNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = c.inspectNetwork(ctx, id)
	if errors.Is(err, errIncusNotFound) {
		return nil
	}
	return ErrExternalEffects
}
