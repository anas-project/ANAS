package main

// TEST_CASES: VIK-T-001

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestProbeHTTPReadyRequiresSuccessfulResponse(t *testing.T) {
	for _, test := range []struct {
		name    string
		status  int
		doErr   error
		wantErr bool
	}{
		{name: "ready", status: http.StatusOK},
		{name: "not ready", status: http.StatusServiceUnavailable, wantErr: true},
		{name: "transport failure", doErr: errors.New("connection refused"), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := doerFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet {
					t.Fatalf("method = %s, want GET", req.Method)
				}
				if test.doErr != nil {
					return nil, test.doErr
				}
				return &http.Response{
					StatusCode: test.status,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			})
			err := probeHTTPReady(client, "http://127.0.0.1:3456/api/v1/info")
			if (err != nil) != test.wantErr {
				t.Fatalf("probeHTTPReady() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestWaitForHTTPReadyRetriesUntilSuccess(t *testing.T) {
	calls := 0
	client := doerFunc(func(_ *http.Request) (*http.Response, error) {
		calls++
		status := http.StatusServiceUnavailable
		if calls == 2 {
			status = http.StatusOK
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if err := waitForHTTPReady(client, "https://iam.example/.well-known/openid-configuration", 2, 0); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestWaitForHTTPReadyFailsAfterBoundedAttempts(t *testing.T) {
	calls := 0
	client := doerFunc(func(_ *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("connection refused")
	})
	if err := waitForHTTPReady(client, "https://iam.example/.well-known/openid-configuration", 3, 0); err == nil {
		t.Fatal("waitForHTTPReady() succeeded for a persistently unavailable provider")
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestPrepareFilesCreatesTreeAndDoesNotFollowSymlinks(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "files")
	outside := filepath.Join(base, "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "attachments"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "attachments", "task.txt"), []byte("task"), 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	if err := prepareFiles(root, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("attachment-tree symlink was followed or replaced")
	}
	body, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "outside" {
		t.Fatalf("symlink target content = %q, want unchanged", body)
	}
}
