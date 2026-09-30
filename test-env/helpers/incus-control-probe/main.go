// A stdin-only, fixed-GET network probe for disposable Incus acceptance VMs.
// This helper is never part of an ANAS release or a privileged action registry.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
)

const schema = "anas.control-probe/v1"
const maximumInput = 64 << 10

var errProbe = errors.New("control bridge probe failed")
var errPin = errors.New("control bridge certificate pin rejected")

type probeRequest struct {
	Schema            string `json:"schema"`
	Mode              string `json:"mode"`
	Endpoint          string `json:"endpoint"`
	ServerCertificate string `json:"server_certificate_pem"`
	ClientCertificate string `json:"client_certificate_pem,omitempty"`
	ClientKey         string `json:"client_private_key_pem,omitempty"`
}

func main() {
	// The outer runner owns creation and readback of the exact test container.
	// Do not let this helper become an arbitrary root-host network diagnostic.
	status, err := os.ReadFile("/proc/self/status")
	_, markerErr := os.Stat("/.dockerenv")
	if len(os.Args) != 1 || os.Geteuid() == 0 || os.Getuid() != os.Geteuid() ||
		os.Getegid() == 0 || os.Getgid() != os.Getegid() || err != nil || markerErr != nil ||
		!probeIdentity(string(status)) {
		fmt.Fprintln(os.Stderr, "control bridge probe requires its isolated unprivileged container")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := run(ctx, os.Stdin, os.Stdout); err != nil {
		// Never print transport errors, the request, endpoints or credentials.
		fmt.Fprintln(os.Stderr, errProbe)
		os.Exit(1)
	}
}

func probeIdentity(status string) bool {
	needed := map[string]string{"CapEff:": "0000000000000000", "CapPrm:": "0000000000000000",
		"CapInh:": "0000000000000000", "CapBnd:": "0000000000000000", "CapAmb:": "0000000000000000", "NoNewPrivs:": "1"}
	for _, line := range strings.Split(status, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if want, ok := needed[fields[0]]; ok {
			if want != fields[1] {
				return false
			}
			delete(needed, fields[0])
		}
	}
	return len(needed) == 0
}

func run(ctx context.Context, input io.Reader, output io.Writer) error {
	if ctx == nil || input == nil || output == nil || ctx.Err() != nil {
		return errProbe
	}
	body, err := io.ReadAll(io.LimitReader(input, maximumInput+1))
	defer clear(body)
	var request probeRequest
	if err != nil || len(body) > maximumInput || actionabi.DecodeTypedObject(body, &request) != nil ||
		request.Schema != schema || !validRequest(request) {
		return errProbe
	}
	config, err := probeTLS(request)
	if err != nil {
		return errProbe
	}
	var connected atomic.Bool
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: config, DisableKeepAlives: true,
		TLSHandshakeTimeout: 2 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, network, address)
			if err == nil {
				connected.Store(true)
			}
			return conn, err
		}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 4 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, request.Endpoint+"/1.0", nil)
	if err != nil {
		return errProbe
	}
	response, err := client.Do(req)
	if response != nil {
		defer response.Body.Close()
	}
	switch request.Mode {
	case "network_blocked":
		var timeout net.Error
		if err == nil || connected.Load() || !errors.As(err, &timeout) || !timeout.Timeout() {
			return errProbe
		}
	case "pin_rejected":
		if !connected.Load() || !errors.Is(err, errPin) {
			return errProbe
		}
	default:
		if err != nil || response == nil || response.StatusCode != http.StatusOK {
			return errProbe
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		if err != nil || len(body) > 1<<20 || verifyResponse(body, request.Mode, strings.TrimPrefix(request.Endpoint, "https://")) != nil {
			return errProbe
		}
	}
	return writeResult(output, request.Mode)
}

func validRequest(request probeRequest) bool {
	if !strings.HasPrefix(request.Endpoint, "https://") {
		return false
	}
	address, err := netip.ParseAddrPort(strings.TrimPrefix(request.Endpoint, "https://"))
	if err != nil || !address.Addr().Is4() || !address.Addr().IsPrivate() || address.Port() != 8443 ||
		request.Endpoint != "https://"+address.String() {
		return false
	}
	switch request.Mode {
	case "untrusted":
		return request.ClientCertificate == "" && request.ClientKey == ""
	case "trusted", "pin_rejected", "network_blocked":
		return request.ClientCertificate != "" && request.ClientKey != ""
	default:
		return false
	}
}

func probeTLS(request probeRequest) (*tls.Config, error) {
	block, rest := pem.Decode([]byte(request.ServerCertificate))
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errProbe
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, errProbe
	}
	pin := sha256.Sum256(certificate.Raw)
	if request.Mode == "pin_rejected" {
		pin[0] ^= 1
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true, // exact DER pin below
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) != 1 || sha256.Sum256(state.PeerCertificates[0].Raw) != pin {
				return errPin
			}
			return nil
		}}
	if request.ClientCertificate != "" || request.ClientKey != "" {
		pair, err := tls.X509KeyPair([]byte(request.ClientCertificate), []byte(request.ClientKey))
		if err != nil {
			return nil, errProbe
		}
		config.Certificates = []tls.Certificate{pair}
	}
	return config, nil
}

// verifyResponse also proves that the trusted daemon itself serves the
// probed gateway address: Incus listens there directly, with no relay.
func verifyResponse(body []byte, mode, listen string) error {
	var response struct {
		Type       string `json:"type"`
		StatusCode int    `json:"status_code"`
		Metadata   struct {
			Auth   string            `json:"auth"`
			Config map[string]string `json:"config"`
		} `json:"metadata"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if decoder.Decode(&response) != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		response.Type != "sync" || response.StatusCode != http.StatusOK || response.Metadata.Auth != mode ||
		(mode == "trusted" && response.Metadata.Config["core.https_address"] != listen) {
		return errProbe
	}
	return nil
}

func writeResult(output io.Writer, mode string) error {
	return json.NewEncoder(output).Encode(map[string]any{"schema": schema, "mode": mode, "passed": true})
}
