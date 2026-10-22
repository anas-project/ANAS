package consoleconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceAPIConfigGrantsAndIsolation(t *testing.T) {
	dir := t.TempDir()
	base := fmt.Sprintf("api_version: anas.console-config/v1\nconsole_store: %s/console\nworkspaces: [{id: main, path: %s/workspace}]\n", dir, dir)
	service := fmt.Sprintf("service_api:\n  credentials_dir: %s/clients\n  clients:\n    - id: user-portal\n      enabled: true\n      uid: 65532\n      gid: 65532\n      grants:\n        - permission: application.credentials.read\n          workspace: main\n          resources: [traefik-admin]\n", dir)
	config, err := Parse([]byte(base + service))
	if err != nil {
		t.Fatal(err)
	}
	if config.ServiceAPI == nil || config.ServiceAPI.Clients[0].ID != "user-portal" {
		t.Fatalf("config %#v", config.ServiceAPI)
	}
	for _, test := range []struct{ name, source string }{
		{"workspace delivery", strings.Replace(service, dir+"/clients", dir+"/workspace/tokens", 1)},
		{"workspace parent delivery", strings.Replace(service, dir+"/clients", dir, 1)},
		{"unknown permission", strings.Replace(service, "application.credentials.read", "secrets.read", 1)},
		{"unknown workspace", strings.Replace(service, "workspace: main", "workspace: unknown", 1)},
		{"wildcard resource", strings.Replace(service, "[traefik-admin]", "['*']", 1)},
		{"unknown schema field", service + "  arbitrary_path: /etc/shadow\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Parse([]byte(base + test.source)); err == nil {
				t.Fatal("unsafe service config accepted")
			}
		})
	}
	workspace := filepath.Join(dir, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, "anasd.yml")
	if err := os.WriteFile(path, []byte(base+service), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, CurrentUIDFilePolicy()); err == nil || !strings.Contains(err.Error(), "outside registered workspace") {
		t.Fatalf("workspace config authority accepted: %v", err)
	}
}

func TestServiceAPIDeliveryIsolationResolvesAliases(t *testing.T) {
	for _, target := range []string{"workspace", "backup"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			workspace := filepath.Join(dir, "workspace")
			clients := filepath.Join(dir, "clients")
			for _, path := range []string{workspace, clients} {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			alias := filepath.Join(dir, "alias")
			destination := workspace
			if target == "backup" {
				destination = clients
			}
			if err := os.Symlink(destination, alias); err != nil {
				t.Fatal(err)
			}
			delivery := clients
			backup := ""
			if target == "workspace" {
				delivery = filepath.Join(alias, "tokens")
			} else {
				backup = fmt.Sprintf("backup_targets: [{id: usb, path: %s}]\n", alias)
			}
			body := fmt.Sprintf("api_version: anas.console-config/v1\nconsole_store: %s/console\nworkspaces: [{id: main, path: %s}]\nservice_api: {credentials_dir: %s, clients: []}\n%s", dir, workspace, delivery, backup)
			path := filepath.Join(dir, "anasd.yml")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path, CurrentUIDFilePolicy()); err == nil {
				t.Fatal("resolved storage boundary was bypassed")
			}
		})
	}
}
