package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbeRejectsUnboundedOrNonFixedRequests(t *testing.T) {
	for _, body := range []string{
		`null`, `{}`, `{"schema":"wrong"}`, strings.Repeat(" ", maximumInput+1),
		`{"schema":"anas.control-probe/v1","mode":"trusted","endpoint":"https://example.com:18443"}`,
		`{"schema":"anas.control-probe/v1","mode":"trusted","endpoint":"https://10.1.0.1:8443"}`,
		`{"schema":"anas.control-probe/v1","mode":"trusted","endpoint":"https://10.1.0.1:18443","endpoint":"https://10.2.0.1:18443"}`,
		`{"schema":"anas.control-probe/v1","mode":"trusted","endpoint":"https://10.1.0.1:18443","command":"private-data"}`,
	} {
		var output bytes.Buffer
		if err := run(context.Background(), strings.NewReader(body), &output); err == nil || output.Len() != 0 || strings.Contains(err.Error(), "private-data") {
			t.Fatal("invalid probe input ran or echoed its contents")
		}
	}
}

func TestProbePinRequiresTheExactServerCertificate(t *testing.T) {
	server := httptest.NewTLSServer(nil)
	defer server.Close()
	certificate := server.Certificate()
	request := probeRequest{Schema: schema, Mode: "untrusted", Endpoint: "https://10.1.0.1:18443",
		ServerCertificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}))}
	config, err := probeTLS(request)
	if err != nil || config.VerifyConnection == nil || !config.InsecureSkipVerify {
		t.Fatal("test probe requires an explicit DER verifier, never blind TLS", err)
	}
	state := tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}}
	if err := config.VerifyConnection(state); err != nil {
		t.Fatal(err)
	}
	request.Mode = "pin_rejected"
	wrong, err := probeTLS(request)
	if err != nil || !errors.Is(wrong.VerifyConnection(state), errPin) {
		t.Fatal("negative pin test did not exercise the verifier", err)
	}
	if !errors.Is(config.VerifyConnection(tls.ConnectionState{}), errPin) {
		t.Fatal("empty peer certificate was trusted")
	}
}

func TestProbeResponseMustProveAuthenticatedDaemon(t *testing.T) {
	good := []byte(`{"type":"sync","status_code":200,"metadata":{"auth":"trusted","config":{"core.https_address":"127.0.0.1:8443"}}}`)
	if err := verifyResponse(good, "trusted"); err != nil {
		t.Fatal(err)
	}
	for _, body := range [][]byte{
		[]byte(`{}`), []byte(`{"type":"error","status_code":200}`),
		bytes.Replace(good, []byte(`"trusted"`), []byte(`"untrusted"`), 1),
		bytes.Replace(good, []byte(`127.0.0.1:8443`), []byte(`0.0.0.0:8443`), 1),
		append(bytes.Clone(good), []byte(`{}`)...),
	} {
		if verifyResponse(body, "trusted") == nil {
			t.Fatal("missing/rejected/wrong daemon response counted as reachability")
		}
	}
	if err := verifyResponse([]byte(`{"type":"sync","status_code":200,"metadata":{"auth":"untrusted"}}`), "untrusted"); err != nil {
		t.Fatal(err)
	}
	if verifyResponse(good, "untrusted") == nil {
		t.Fatal("anonymous probe unexpectedly authorized")
	}
}

func TestProbeNeverPublishesConnectionMaterial(t *testing.T) {
	var output bytes.Buffer
	if err := writeResult(&output, "trusted"); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if json.Unmarshal(output.Bytes(), &result) != nil || len(result) != 3 || result["schema"] != schema || result["mode"] != "trusted" || result["passed"] != true {
		t.Fatal("probe output is not the fixed non-secret evidence shape")
	}
}
