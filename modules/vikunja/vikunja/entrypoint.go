package main

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	vikunjaBinary       = "/app/vikunja/vikunja"
	vikunjaFiles        = "/app/vikunja/files"
	vikunjaUID          = 1000
	vikunjaGID          = 1000
	healthcheckURL      = "http://127.0.0.1:3456/api/v1/info"
	oidcDiscoveryEnv    = "ANAS_IAM_BINDING__VIKUNJA__OIDC_DISCOVERY_URL"
	oidcReadyAttempts   = 60
	oidcReadyRetryDelay = 2 * time.Second
)

var healthcheckClient = &http.Client{Timeout: 3 * time.Second}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

func main() {
	// Docker healthchecks start with the image's configured root user. They do
	// not need to touch the attachment tree, so drop immediately and keep the
	// recurring database/API probe unprivileged as well.
	healthcheck := len(os.Args) >= 2 && os.Args[1] == "healthcheck"
	if !healthcheck {
		if err := prepareFiles(vikunjaFiles, vikunjaUID, vikunjaGID); err != nil {
			fatal(err)
		}
	}
	if err := dropPrivileges(vikunjaUID, vikunjaGID); err != nil {
		fatal(err)
	}
	if healthcheck {
		// Upstream's healthcheck initializes the application and therefore runs
		// migrations. Gate it on the already-running HTTP server so Docker cannot
		// race the main process during a fresh or upgraded database migration.
		if err := probeHTTPReady(healthcheckClient, healthcheckURL); err != nil {
			fatal(err)
		}
	} else if discoveryURL := strings.TrimSpace(os.Getenv(oidcDiscoveryEnv)); discoveryURL != "" {
		// ANAS activates provider Modules before consumers, but a started IAM
		// container may still be applying its client registrations. Wait on the
		// generic binding rather than teaching Vikunja about any one provider.
		if err := waitForHTTPReady(healthcheckClient, discoveryURL, oidcReadyAttempts, oidcReadyRetryDelay); err != nil {
			fatal(fmt.Errorf("wait for OIDC discovery: %w", err))
		}
	}

	argv := append([]string{vikunjaBinary}, os.Args[1:]...)
	if err := syscall.Exec(vikunjaBinary, argv, os.Environ()); err != nil {
		fatal(fmt.Errorf("exec Vikunja: %w", err))
	}
}

func waitForHTTPReady(client httpDoer, url string, attempts int, retryDelay time.Duration) error {
	if attempts < 1 {
		return fmt.Errorf("attempts must be positive")
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := probeHTTPReady(client, url); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt < attempts {
			time.Sleep(retryDelay)
		}
	}
	return fmt.Errorf("not ready after %d attempts: %w", attempts, lastErr)
}

func probeHTTPReady(client httpDoer, url string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build Vikunja HTTP readiness request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("probe Vikunja HTTP readiness: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("probe Vikunja HTTP readiness: status %d", resp.StatusCode)
	}
	return nil
}

func prepareFiles(root string, uid, gid int) error {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return fmt.Errorf("create attachment directory: %w", err)
	}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// WalkDir never follows a symlink. Lchown also avoids following one if a
		// restored attachment tree contains an application-created symlink.
		if err := os.Lchown(path, uid, gid); err != nil {
			return fmt.Errorf("set attachment ownership for %s: %w", path, err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("prepare attachment ownership: %w", err)
	}
	return nil
}

func dropPrivileges(uid, gid int) error {
	if err := syscall.Setgroups([]int{gid}); err != nil {
		return fmt.Errorf("set supplementary groups: %w", err)
	}
	if err := syscall.Setgid(gid); err != nil {
		return fmt.Errorf("set gid: %w", err)
	}
	if err := syscall.Setuid(uid); err != nil {
		return fmt.Errorf("set uid: %w", err)
	}
	syscall.Umask(0o027)
	return nil
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "anas-vikunja: %v\n", err)
	os.Exit(1)
}
