package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"github.com/anas-project/ANAS/internal/consoleconfig"
	"github.com/anas-project/ANAS/internal/serviceauth"
)

func TestServiceAPIStartupDeliveryAndLiveHostRevocation(t *testing.T) {
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	initial := consoleconfig.Config{Workspaces: []consoleconfig.Workspace{{ID: "main", Path: filepath.Join(directory, "workspace")}}, ServiceAPI: &serviceauth.Config{Directory: filepath.Join(directory, "service-clients"), Clients: []serviceauth.Client{{ID: "portal", Enabled: true, UID: os.Geteuid(), GID: os.Getegid(), Grants: []serviceauth.Grant{{Permission: "application.credentials.read", Workspace: "main", Resources: []string{"traefik-admin"}}}}}}}
	var mu sync.Mutex
	current := initial
	reloadErr := error(nil)
	loader := func() (consoleconfig.Config, error) { mu.Lock(); defer mu.Unlock(); return current, reloadErr }
	appender := &recordingConfigAuditAppender{}
	options, stop, err := configureServiceAPI(context.Background(), initial, loader, appender, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	body, err := os.ReadFile(filepath.Join(initial.ServiceAPI.Directory, "portal", "token"))
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(body))
	authenticate := func() error {
		_, err := options.Authenticate(context.Background(), token, "application.credentials.read", "main", "traefik-admin")
		return err
	}
	if err := authenticate(); err != nil {
		t.Fatalf("delivered token failed: %v", err)
	}
	mu.Lock()
	current.ServiceAPI = &serviceauth.Config{Directory: initial.ServiceAPI.Directory}
	mu.Unlock()
	if err := authenticate(); !errors.Is(err, serviceauth.ErrUnauthenticated) {
		t.Fatalf("host removal did not immediately revoke: %v", err)
	}
	mu.Lock()
	current = initial
	reloadErr = errors.New("invalid root configuration")
	mu.Unlock()
	if err := authenticate(); !errors.Is(err, serviceauth.ErrUnavailable) {
		t.Fatalf("load failure retained grant: %v", err)
	}
	mu.Lock()
	reloadErr = nil
	current = initial
	current.Workspaces = []consoleconfig.Workspace{{ID: "main", Path: filepath.Join(directory, "different")}}
	mu.Unlock()
	if err := authenticate(); !errors.Is(err, serviceauth.ErrUnavailable) {
		t.Fatalf("workspace remapping was accepted live: %v", err)
	}
	if err := options.Audit(context.Background(), httpapi.ServiceCredentialAuditEvent{ClientID: "portal", DeclaredActor: "user-anchor", WorkspaceID: "main", Application: "traefik-admin", RequestID: "read-1"}); err != nil {
		t.Fatal(err)
	}
	if len(appender.events) != 1 {
		t.Fatalf("events %#v", appender.events)
	}
	event := appender.events[0]
	if event.Actor != "service:portal" || event.Details["actor_source"] != "service_declared" || event.Details["declared_actor"] != "user-anchor" || event.Details["application"] != "traefik-admin" {
		t.Fatalf("event %#v", event)
	}
}

func TestServiceAPIAbsentRequiresNoTokenDirectory(t *testing.T) {
	options, stop, err := configureServiceAPI(context.Background(), consoleconfig.Config{}, nil, nil, nil)
	if err != nil || options != nil || stop == nil {
		t.Fatalf("disabled service API = %v, %v", options, err)
	}
	stop()
}
