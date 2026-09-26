package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fixtureRunnerCA(t *testing.T, ca bool, expired bool) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "private native fixture CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: ca,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	if expired {
		cert.NotAfter = time.Now().Add(-time.Minute)
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestRunnerTrustAcceptsOnlyBoundedCurrentPublicCAs(t *testing.T) {
	ca := fixtureRunnerCA(t, true, false)
	for _, input := range [][]byte{ca, append(bytes.Clone(ca), fixtureRunnerCA(t, true, false)...)} {
		got, err := normalizeRunnerTrust(input, time.Now())
		if err != nil || !bytes.Equal(got, input) {
			t.Fatal("valid explicit public CA projection rejected", err)
		}
	}
	for _, input := range [][]byte{nil, {}, []byte("private response"),
		fixtureRunnerCA(t, false, false), fixtureRunnerCA(t, true, true),
		append([]byte("unparsed-prefix\n"), ca...), append(bytes.Clone(ca), []byte("unparsed-suffix")...),
		append([]byte("-----BEGIN CERTIFICATE-----\nmalformed\n-----END CERTIFICATE-----\n"), ca...),
		append(bytes.Clone(ca), ca...), bytes.Repeat([]byte("x"), maxRunnerTrustBytes+1),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("private-marker")})} {
		if _, err := normalizeRunnerTrust(input, time.Now()); err == nil || strings.Contains(err.Error(), "private-marker") {
			t.Fatal("untrusted or secret input was accepted or disclosed")
		}
	}
}

func TestRunnerTrustFileDoesNotFollowLinksOrAcceptWritableInput(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "root-ca.pem")
	ca := fixtureRunnerCA(t, true, false)
	if err := os.WriteFile(path, ca, 0644); err != nil {
		t.Fatal(err)
	}
	got, err := readRunnerTrustFile(path, os.Geteuid())
	if err != nil || !bytes.Equal(got, ca) {
		t.Fatal("public CA file read failed", err)
	}
	link := filepath.Join(root, "link.pem")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readRunnerTrustFile(link, os.Geteuid()); err == nil {
		t.Fatal("followed a symbolic trust input")
	}
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := readRunnerTrustFile(path, os.Geteuid()); err == nil {
		t.Fatal("accepted group/other-writable trust")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, filepath.Join(root, "alias.pem")); err != nil {
		t.Fatal(err)
	}
	if _, err := readRunnerTrustFile(path, os.Geteuid()); err == nil {
		t.Fatal("accepted a multiply named CA file")
	}
}

func TestRunnerTrustTravelsOnlyInBoundedStdinFrame(t *testing.T) {
	c, api, compute, store, _ := controllerFixture()
	ca := fixtureRunnerCA(t, true, false)
	c.cfg.RunnerTrustPEM = ca
	api.jobs = []ActionJob{{ID: 42, Handle: "private-ca-job", Status: "waiting", RunsOn: []string{"docker"}}}
	if err := c.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if api.created != 1 || compute.stdin != api.registration.Token+string(ca) {
		t.Fatal("stdin did not contain the exact token then the validated public trust bytes")
	}
	sum := sha256.Sum256(ca)
	want := []string{"--trust-size", strconv.Itoa(len(ca)), "--trust-sha256", hex.EncodeToString(sum[:])}
	if len(compute.execArg) < 4 || !slices.Equal(compute.execArg[len(compute.execArg)-4:], want) {
		t.Fatal("guest does not receive a bounded public framing commitment")
	}
	args := strings.Join(compute.execArg, " ")
	state, err := json.Marshal(store.state)
	if err != nil || strings.Contains(args, api.registration.Token) || strings.Contains(args, string(ca)) ||
		bytes.Contains(state, []byte(api.registration.Token)) || bytes.Contains(state, ca) {
		t.Fatal("payload escaped stdin into argv or durable state")
	}
}

func TestBadRunnerTrustCannotCreateARegistrationOrGuest(t *testing.T) {
	c, api, compute, store, _ := controllerFixture()
	c.cfg.RunnerTrustPEM = []byte("-----BEGIN PRIVATE KEY-----\nprivate-marker")
	api.jobs = []ActionJob{{ID: 42, Handle: "bad-ca-job", Status: "waiting", RunsOn: []string{"docker"}}}
	err := c.Reconcile(context.Background())
	if err == nil || strings.Contains(err.Error(), "private-marker") || api.created != 0 ||
		len(compute.created) != 0 || compute.stdin != "" || len(store.state.Workloads) != 0 {
		t.Fatal("invalid trust caused registration, guest effects or private output")
	}
}
