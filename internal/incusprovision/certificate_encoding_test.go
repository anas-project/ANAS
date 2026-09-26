package incusprovision

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestManagementCertificateCreationUsesBase64DER(t *testing.T) {
	credential, err := generateCredential()
	if err != nil {
		t.Fatal(err)
	}
	posts := 0
	client := &incusUnixClient{transport: incusRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		posts++
		if request.Method != http.MethodPost || request.URL.Path != "/1.0/certificates" {
			t.Fatal("certificate write left its fixed operation")
		}
		body, err := io.ReadAll(request.Body)
		if err != nil || strings.Contains(string(body), "PRIVATE KEY") || strings.Contains(string(body), credential.PrivateKey) {
			t.Fatal("private credential material entered the certificate creation request")
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(body, &fields) != nil || len(fields) != 4 {
			t.Fatal("certificate request contains extra fields")
		}
		var value struct {
			Name        string `json:"name"`
			Type        string `json:"type"`
			Certificate string `json:"certificate"`
			Restricted  bool   `json:"restricted"`
		}
		if json.Unmarshal(body, &value) != nil || value.Name != ManagementCertName || value.Type != "client" || value.Restricted {
			t.Fatal("management certificate authority changed")
		}
		der, err := base64.StdEncoding.Strict().DecodeString(value.Certificate)
		if err != nil {
			t.Fatal("certificate creation still sends PEM rather than the base64 DER API form")
		}
		certificate, err := x509.ParseCertificate(der)
		if err != nil || digestBytes(certificate.Raw) != credential.Fingerprint {
			t.Fatal("wire certificate differs from the durable approved credential")
		}
		return incusTestResponse(http.StatusCreated, `{"type":"sync","status_code":200,"metadata":{}}`), nil
	})}
	if err := client.addCertificate(context.Background(), credential); err != nil || posts != 1 {
		t.Fatal("certificate write failed or was retried", err)
	}
	// The existing readback format remains PEM. Do not rewrite durable state,
	// the private bundle or GET expectations to match POST's transport form.
	if block, rest := pem.Decode([]byte(credential.Certificate)); block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
		t.Fatal("conversion changed the stored certificate")
	}
}

func TestCertificateEncodingRejectsMismatchBeforeAnyWrite(t *testing.T) {
	credential, err := generateCredential()
	if err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*Credential){
		"wrong name":     func(c *Credential) { c.Name = "external" },
		"missing digest": func(c *Credential) { c.Fingerprint = "" },
		"wrong digest":   func(c *Credential) { c.Fingerprint = strings.Repeat("a", 64) },
		"missing PEM":    func(c *Credential) { c.Certificate = "" },
		"extra PEM":      func(c *Credential) { c.Certificate += c.Certificate },
		"private key":    func(c *Credential) { c.Certificate = c.PrivateKey },
		"garbage":        func(c *Credential) { c.Certificate += "private-request-text" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := credential
			edit(&candidate)
			writes := 0
			client := &incusUnixClient{transport: incusRoundTripFunc(func(*http.Request) (*http.Response, error) {
				writes++
				return incusTestResponse(200, `{"type":"sync","status_code":200,"metadata":{}}`), nil
			})}
			err := client.addCertificate(context.Background(), candidate)
			if !errors.Is(err, ErrInvalid) || writes != 0 || strings.Contains(err.Error(), "private-request-text") {
				t.Fatal("invalid certificate reached the daemon or leaked input", err)
			}
		})
	}
}
