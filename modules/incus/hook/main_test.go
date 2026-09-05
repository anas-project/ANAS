package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func materials(t *testing.T) (certB64, keyB64 string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "incus"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return base64.StdEncoding.EncodeToString(certPEM), base64.StdEncoding.EncodeToString(keyPEM)
}

func validEnv(t *testing.T) map[string]string {
	t.Helper()
	certB64, keyB64 := materials(t)
	return map[string]string{
		"NETWORK_PREFIX":        "anas_",
		"INCUS_ENDPOINT":        "https://incus.example:8443",
		"INCUS_SERVER_CERT_B64": certB64,
		"INCUS_ADMIN_CERT_B64":  certB64,
		"INCUS_ADMIN_KEY_B64":   keyB64,
	}
}

func TestCalculateDerivesNetworkName(t *testing.T) {
	env := validEnv(t)
	if err := calculate("incus", env); err != nil {
		t.Fatalf("calculate: %v", err)
	}
	if got := env["INCUS_NETWORK_NAME"]; got != "anas_incus" {
		t.Fatalf("INCUS_NETWORK_NAME = %q, want anas_incus", got)
	}
}

func TestCalculateRefusesIncompleteCredentials(t *testing.T) {
	for name, mutate := range map[string]func(map[string]string){
		"no endpoint":        func(e map[string]string) { e["INCUS_ENDPOINT"] = "" },
		"plaintext endpoint": func(e map[string]string) { e["INCUS_ENDPOINT"] = "http://incus.example:8443" },
		"no server cert":     func(e map[string]string) { e["INCUS_SERVER_CERT_B64"] = "" },
		"no admin cert":      func(e map[string]string) { e["INCUS_ADMIN_CERT_B64"] = "" },
		"no admin key":       func(e map[string]string) { e["INCUS_ADMIN_KEY_B64"] = "" },
		"cert is not base64": func(e map[string]string) { e["INCUS_ADMIN_CERT_B64"] = "!!!" },
		"cert is not PEM": func(e map[string]string) {
			e["INCUS_ADMIN_CERT_B64"] = base64.StdEncoding.EncodeToString([]byte("nope"))
		},
		"key slot holds cert": func(e map[string]string) { e["INCUS_ADMIN_KEY_B64"] = e["INCUS_ADMIN_CERT_B64"] },
	} {
		t.Run(name, func(t *testing.T) {
			env := validEnv(t)
			mutate(env)
			if err := calculate("incus", env); err == nil {
				t.Fatalf("%s should be refused", name)
			}
		})
	}
}

func TestCalculateNeverEchoesCredentials(t *testing.T) {
	env := validEnv(t)
	secret := env["INCUS_ADMIN_KEY_B64"]
	env["INCUS_ADMIN_KEY_B64"] = secret + "!!not-base64"
	err := calculate("incus", env)
	if err == nil {
		t.Fatal("expected a validation failure")
	}
	if strings.Contains(err.Error(), secret[:32]) {
		t.Fatalf("hook error echoed key material: %v", err)
	}
}

func TestCalculateIgnoresOtherModules(t *testing.T) {
	env := map[string]string{}
	if err := calculate("forgejo", env); err != nil {
		t.Fatalf("calculate for another module must be inert: %v", err)
	}
	if len(env) != 0 {
		t.Fatalf("env was modified for another module: %v", env)
	}
}

func TestNetworkIPv6RequiresBothTheSwitchAndTheHost(t *testing.T) {
	for name, tc := range map[string]struct {
		ipv6, hostHas, want string
	}{
		"wanted and available":        {"", "true", "true"},
		"explicitly on and available": {"true", "true", "true"},
		"wanted but host has none":    {"", "false", "false"},
		"switched off but available":  {"false", "true", "false"},
		"neither":                     {"false", "false", "false"},
	} {
		t.Run(name, func(t *testing.T) {
			env := validEnv(t)
			if tc.ipv6 != "" {
				env["IPv6"] = tc.ipv6
			}
			env["HOST_HAS_IPV6"] = tc.hostHas
			if err := calculate("incus", env); err != nil {
				t.Fatal(err)
			}
			if got := env["INCUS_NETWORK_IPV6"]; got != tc.want {
				t.Fatalf("INCUS_NETWORK_IPV6 = %q, want %q", got, tc.want)
			}
		})
	}
}
