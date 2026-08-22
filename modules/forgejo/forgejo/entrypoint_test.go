package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestChownDataTreeDoesNotFollowSymlinks(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "forgejo")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(filepath.Join(root, "repositories"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := chownDataTree(root, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("data-tree symlink was followed or replaced")
	}
	body, err := os.ReadFile(outside)
	if err != nil || string(body) != "outside" {
		t.Fatalf("outside target changed: %q, %v", body, err)
	}
}

func TestEnsureLocalAdminAcceptsMatchingAdministrator(t *testing.T) {
	originalClient := httpClient
	defer func() { httpClient = originalClient }()
	httpClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		user, password, _ := request.BasicAuth()
		if request.URL.Path != "/api/v1/user" || user != "admin_forgejo" || password != "managed-secret" {
			return response(http.StatusUnauthorized, ""), nil
		}
		return response(http.StatusOK, `{"login":"admin_forgejo","is_admin":true}`), nil
	})}
	if err := ensureLocalAdmin(localAdminInput{Username: "admin_forgejo", Email: "admin@localhost.invalid", Password: "managed-secret"}, "http://forgejo.test"); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureLocalAdminBootstrapsWithoutDesiredPasswordInCLIArgv(t *testing.T) {
	original := runForgejoCommand
	originalClient := httpClient
	defer func() { runForgejoCommand = original; httpClient = originalClient }()
	created, updated := false, false
	runForgejoCommand = func(args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "managed-secret") {
			t.Fatal("desired password leaked into Forgejo CLI argv")
		}
		if strings.Contains(joined, "admin user create") {
			created = true
			return []byte("generated random password is 'temporary-bootstrap-secret'\n"), nil
		}
		return nil, nil
	}
	httpClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/v1/user":
			user, password, _ := request.BasicAuth()
			if updated && user == "admin_forgejo" && password == "managed-secret" {
				return response(http.StatusOK, `{"login":"admin_forgejo","is_admin":true}`), nil
			}
			return response(http.StatusUnauthorized, ""), nil
		case "/api/v1/users/admin_forgejo":
			return response(http.StatusNotFound, ""), nil
		case "/api/v1/admin/users/admin_forgejo":
			user, password, _ := request.BasicAuth()
			if request.Method != http.MethodPatch || user != "admin_forgejo" || password != "temporary-bootstrap-secret" {
				t.Fatalf("unexpected password update request: %s %s", request.Method, request.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["password"] != "managed-secret" {
				t.Fatalf("password body = %#v", body)
			}
			updated = true
			return response(http.StatusOK, ""), nil
		default:
			return response(http.StatusNotFound, ""), nil
		}
	})}
	if err := ensureLocalAdmin(localAdminInput{Username: "admin_forgejo", Email: "admin@localhost.invalid", Password: "managed-secret"}, "http://forgejo.test"); err != nil {
		t.Fatal(err)
	}
	if !created || !updated {
		t.Fatalf("created=%t updated=%t", created, updated)
	}
}

func TestEnsureOIDCAddsThenVerifiesSource(t *testing.T) {
	original := runForgejoCommand
	defer func() { runForgejoCommand = original }()
	calls := 0
	runForgejoCommand = func(args ...string) ([]byte, error) {
		calls++
		if calls == 1 {
			return []byte("ID Name Type Enabled\n"), nil
		}
		if calls == 2 {
			joined := strings.Join(args, " ")
			for _, fragment := range []string{"add-oauth", "--provider openidConnect", "--group-claim-name groups", "--admin-group Admins"} {
				if !strings.Contains(joined, fragment) {
					t.Fatalf("OIDC command missing %q: %s", fragment, joined)
				}
			}
			return nil, nil
		}
		return []byte("1 anas OAuth2 true\n"), nil
	}
	if err := ensureOIDC(oidcInput{Name: "anas", ClientID: "forgejo", ClientSecret: "secret", DiscoveryURL: "https://id/.well-known/openid-configuration", AdminGroup: "Admins"}); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestAuthSourceIDParsesPlainAndTableOutput(t *testing.T) {
	for _, output := range []string{"ID Name Type Enabled\n3 anas OAuth2 true\n", "| 7 | anas | OAuth2 | true |\n"} {
		if id, ok := authSourceID([]byte(output), "anas"); !ok || id != map[bool]int{true: 3, false: 7}[strings.Contains(output, "3 anas")] {
			// Keep failure output direct; the branch above only makes the two expected IDs compact.
			if strings.Contains(output, "3 anas") && id == 3 && ok {
				continue
			}
			if strings.Contains(output, "| 7") && id == 7 && ok {
				continue
			}
			t.Fatalf("parse %q = %d, %t", output, id, ok)
		}
	}
}

func TestEnsureOIDCRejectsDisabledSource(t *testing.T) {
	original := runForgejoCommand
	defer func() { runForgejoCommand = original }()
	calls := 0
	runForgejoCommand = func(args ...string) ([]byte, error) {
		calls++
		if calls == 1 {
			return []byte("ID Name Type Enabled\n9 anas OAuth2 false\n"), nil
		}
		if calls == 2 {
			return nil, nil
		}
		return []byte("ID Name Type Enabled\n9 anas OAuth2 false\n"), nil
	}
	err := ensureOIDC(oidcInput{Name: "anas", ClientID: "forgejo", ClientSecret: "secret", DiscoveryURL: "https://id/.well-known/openid-configuration", AdminGroup: "Admins"})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("error = %v", err)
	}
}
