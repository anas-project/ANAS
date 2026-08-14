package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var verifyTraefikDashboard = verifyTraefikDashboardHTTP

func traefikAuthConfig(username, password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("http:\n  middlewares:\n    auth:\n      basicAuth:\n        users:\n          - %q\n", username+":"+string(hash)), nil
}

func handleLocalAccount(req hookRequest) error {
	op := req.LocalAccount
	if req.Module != "traefik" || op == nil || op.AccountID != "primary" ||
		(op.Handler != "apply-traefik-local-admin" && op.Handler != "rotate-traefik-local-admin") {
		return fmt.Errorf("traefik: unsupported local account handler")
	}
	current, candidate := req.Secrets[op.SecretKey], req.Secrets[op.CandidateSecretKey]
	if current == "" || candidate == "" {
		return fmt.Errorf("traefik: current or candidate local administrator secret is missing")
	}
	runtimeRoot := req.Env["ANAS_MODULE_RUNTIME_STATE_PATH"]
	if runtimeRoot == "" {
		return fmt.Errorf("Traefik runtime-state path is missing")
	}
	if !filepath.IsAbs(runtimeRoot) {
		runtimeRoot = filepath.Join(req.Workdir, filepath.FromSlash(runtimeRoot))
	}
	path := filepath.Join(filepath.Clean(runtimeRoot), "dynamic", "dashboard-auth.yml")
	before, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read Traefik dashboard authentication state: %w", err)
	}
	content, err := traefikAuthConfig(op.Username, candidate)
	if err != nil {
		return err
	}
	if err := writeTraefikAuthAtomic(path, []byte(content)); err != nil {
		return err
	}
	if err := verifyTraefikAuth(path, op.Username, candidate); err != nil {
		if restoreErr := writeTraefikAuthAtomic(path, before); restoreErr != nil {
			return fmt.Errorf("Traefik verification failed (%v) and rollback failed (%v)", err, restoreErr)
		}
		return fmt.Errorf("Traefik verification failed; old credential restored: %w", err)
	}
	entryIP := req.Env["ANAS_RUNTIME_ENTRY_IP"]
	if entryIP == "" {
		entryIP = req.Env["HOST_IP"]
	}
	if err := verifyTraefikDashboard(req.Env["TRAEFIK_DASHBOARD_URL"], entryIP, op.Username, candidate); err != nil {
		if restoreErr := writeTraefikAuthAtomic(path, before); restoreErr != nil {
			return fmt.Errorf("Traefik dashboard verification failed (%v) and rollback failed (%v)", err, restoreErr)
		}
		return fmt.Errorf("Traefik dashboard verification failed; old credential restored: %w", err)
	}
	return nil
}

func verifyTraefikDashboardHTTP(rawURL, hostIP, username, password string) error {
	if rawURL == "" {
		return fmt.Errorf("Traefik dashboard URL is missing")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true} // #nosec G402: the module verifies an internal endpoint whose certificate may still be bootstrapping.
	transport := &http.Transport{TLSClientConfig: tlsConfig}
	// The dashboard hostname often has no public DNS record yet during the
	// first deployment. Dial the deployment entry IP while retaining the
	// original URL, Host header and SNI, so verification exercises the actual
	// router without depending on external DNS propagation.
	if hostIP != "" {
		port := parsed.Port()
		if port == "" {
			port = "443"
		}
		target := net.JoinHostPort(hostIP, port)
		dialer := &net.Dialer{Timeout: 2 * time.Second}
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", target)
		}
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: transport}
	var last error
	// Docker and file providers initialize asynchronously. On a cold daemon the
	// dashboard router can return 404 for several seconds even though the
	// container is already healthy; allow the provider convergence window but
	// still require the candidate credential to produce a successful response.
	for attempt := 0; attempt < 120; attempt++ {
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			return err
		}
		req.SetBasicAuth(username, password)
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 400 {
				return nil
			}
			err = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		last = err
		time.Sleep(500 * time.Millisecond)
	}
	return last
}

func verifyTraefikAuth(path, username, password string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	prefix := []byte(username + ":")
	i := bytes.Index(b, prefix)
	if i < 0 {
		return fmt.Errorf("Traefik username verification failed")
	}
	hash := b[i+len(prefix):]
	if j := bytes.IndexAny(hash, "\"\r\n"); j >= 0 {
		hash = hash[:j]
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil {
		return fmt.Errorf("Traefik password verification failed")
	}
	return nil
}

func writeTraefikAuthAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".dashboard-auth-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
