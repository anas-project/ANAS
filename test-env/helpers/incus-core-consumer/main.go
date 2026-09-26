// Disposable Core/Compose acceptance consumer. It is not a product service.
// Credentials arrive solely through the actual per-resource projection; fixed
// read-only probes prove its own project is accessible and another is not.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/anas-project/ANAS/internal/computeclient"
)

var errProbe = errors.New("native Core consumer projection probe failed")

type probeFailure struct {
	stage  string
	status int
}

func (probeFailure) Error() string { return "native Core consumer projection probe failed" }

func reportProbe(module string, err error) map[string]any {
	result := map[string]any{"schema": "anas.native-core-consumer/v1", "consumer": module, "passed": err == nil}
	if err == nil {
		return result
	}
	code, status := "probe_failed", 0
	var failure probeFailure
	if errors.As(err, &failure) {
		switch failure.stage {
		case "container_marker", "process_permissions", "environment_scope", "lease_projection", "client_key_pair",
			"server_certificate", "server_pin", "peer_pin", "request_failed", "response_read", "response_metadata",
			"own_project_status", "foreign_project_status":
			code = failure.stage
			if failure.status >= 100 && failure.status <= 599 {
				status = failure.status
			}
		}
	}
	result["failure_code"], result["http_status"] = code, status
	return result
}

func parseInvocation(args []string) (module string, once, ok bool) {
	if len(args) != 1 && len(args) != 2 {
		return "", false, false
	}
	if args[0] != "core_one" && args[0] != "core_two" {
		return "", false, false
	}
	if len(args) == 2 && args[1] != "--once" {
		return "", false, false
	}
	return args[0], len(args) == 2, true
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	module, once, valid := parseInvocation(os.Args[1:])
	if !valid ||
		os.Getuid() != 65532 || os.Geteuid() != 65532 || os.Getgid() != 65532 || os.Getegid() != 65532 {
		fmt.Fprintln(os.Stderr, errProbe)
		os.Exit(1)
	}
	err := verify(ctx, module, once)
	result := reportProbe(module, err)
	result["probe_scope"] = "own_project_ready"
	if once {
		result["probe_scope"] = "existing_project_isolation"
	}
	if err != nil {
		_ = json.NewEncoder(os.Stdout).Encode(result)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(result)
	if once {
		return
	}
	// Stay alive so the independent VM driver can inspect the real container
	// and its two network bindings, then stop it through the product lifecycle.
	<-ctx.Done()
}

func verify(parent context.Context, module string, checkIsolation bool) error {
	if _, err := os.Stat("/.dockerenv"); err != nil {
		return probeFailure{stage: "container_marker"}
	}
	status, err := os.ReadFile("/proc/self/status")
	if err != nil || !isolatedIdentity(string(status)) {
		return probeFailure{stage: "process_permissions"}
	}
	if !isolatedEnvironment(os.Environ(), module) {
		return probeFailure{stage: "environment_scope"}
	}
	l, err := computeclient.LeaseFromEnv(module, "workers")
	if err != nil || l.Interface != computeclient.InterfaceContainer || l.Sandbox != "anas-"+strings.ReplaceAll(module, "_", "-") {
		return probeFailure{stage: "lease_projection"}
	}
	decode := func(value string) []byte { body, _ := base64.StdEncoding.DecodeString(value); return body }
	pair, err := tls.X509KeyPair(decode(l.ClientCertB64), decode(l.ClientKeyB64))
	if err != nil {
		return probeFailure{stage: "client_key_pair"}
	}
	block, rest := pem.Decode(decode(l.ServerCertB64))
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return probeFailure{stage: "server_certificate"}
	}
	server, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return probeFailure{stage: "server_certificate"}
	}
	pin := sha256.Sum256(server.Raw)
	if hex.EncodeToString(pin[:]) != l.ServerCertFingerprint {
		return probeFailure{stage: "server_pin"}
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair},
			InsecureSkipVerify: true, // Exact daemon DER pin, not hostname/CA inference.
			VerifyConnection: func(state tls.ConnectionState) error {
				if len(state.PeerCertificates) != 1 || sha256.Sum256(state.PeerCertificates[0].Raw) != pin {
					return probeFailure{stage: "peer_pin"}
				}
				return nil
			}}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	get := func(project string) (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			l.Endpoint+probePath(project, checkIsolation), nil)
		if err != nil {
			return 0, errProbe
		}
		response, err := client.Do(req)
		if err != nil {
			var failure probeFailure
			if errors.As(err, &failure) {
				return 0, failure
			}
			return 0, probeFailure{stage: "request_failed"}
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		if err != nil || len(body) > 1<<20 {
			return 0, probeFailure{stage: "response_read"}
		}
		if response.StatusCode == 200 && !validProbeMetadata(body, project, checkIsolation) {
			return 0, probeFailure{stage: "response_metadata"}
		}
		return response.StatusCode, nil
	}
	return verifyProjectAccess(l.Sandbox, checkIsolation, get)
}

func probePath(project string, checkIsolation bool) string {
	if checkIsolation {
		// Use the named-project permission check, not a foreign instance-list
		// handler's internal error. HTTP 500 is never isolation evidence.
		return "/1.0/projects/" + url.PathEscape(project)
	}
	return "/1.0/instances?recursion=1&project=" + url.QueryEscape(project)
}

func validProbeMetadata(body []byte, project string, checkIsolation bool) bool {
	var envelope struct {
		Type     string          `json:"type"`
		Code     int             `json:"status_code"`
		Metadata json.RawMessage `json:"metadata"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Type != "sync" || envelope.Code != 200 {
		return false
	}
	if checkIsolation {
		var value struct {
			Name   string            `json:"name"`
			Config map[string]string `json:"config"`
		}
		return json.Unmarshal(envelope.Metadata, &value) == nil && value.Name == project &&
			value.Config != nil && value.Config["restricted"] == "true"
	}
	var instances []json.RawMessage
	return json.Unmarshal(envelope.Metadata, &instances) == nil && instances != nil && len(instances) == 0
}

func verifyProjectAccess(own string, checkIsolation bool, get func(string) (int, error)) error {
	if (own != "anas-core-one" && own != "anas-core-two") || get == nil {
		return errProbe
	}
	other := "anas-core-one"
	if own == other {
		other = "anas-core-two"
	}
	// Core starts consumers in dependency order. Its service readiness must
	// not depend on later consumers already having a project. The finite
	// post-apply mode is invoked only after the native driver independently
	// confirms both real projects exist; it retains all negative controls.
	projects := []string{own}
	if checkIsolation {
		projects = []string{own, other, "default", own}
	}
	for _, project := range projects {
		code, err := get(project)
		if err != nil {
			return err
		}
		if project == own && code != 200 {
			return probeFailure{stage: "own_project_status", status: code}
		}
		if project != own && code != 403 && code != 404 {
			return probeFailure{stage: "foreign_project_status", status: code}
		}
	}
	return nil
}

func isolatedEnvironment(values []string, module string) bool {
	prefix := computeclient.EnvPrefix + strings.ToUpper(module) + "__WORKERS__"
	for _, row := range values {
		name, _, _ := strings.Cut(row, "=")
		switch name {
		case "INCUS_ENDPOINT", "INCUS_SERVER_CERTIFICATE_B64", "INCUS_SERVER_CERT_B64", "INCUS_ADMIN_CERTIFICATE_B64", "INCUS_ADMIN_CERT_B64", "INCUS_ADMIN_KEY_B64":
			return false
		}
		if strings.HasPrefix(name, "INCUS_HOST_CONNECTION_") || (strings.HasPrefix(name, computeclient.EnvPrefix) && !strings.HasPrefix(name, prefix)) {
			return false
		}
	}
	return true
}

func isolatedIdentity(status string) bool {
	wanted := map[string]string{"CapEff:": "0000000000000000", "CapPrm:": "0000000000000000", "CapInh:": "0000000000000000",
		"CapBnd:": "0000000000000000", "CapAmb:": "0000000000000000", "NoNewPrivs:": "1"}
	seen := map[string]bool{}
	for _, line := range strings.Split(status, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			if seen[fields[0]] {
				return false
			}
			if value, ok := wanted[fields[0]]; ok {
				if fields[1] != value {
					return false
				}
				delete(wanted, fields[0])
				seen[fields[0]] = true
			}
		}
	}
	return len(wanted) == 0
}
