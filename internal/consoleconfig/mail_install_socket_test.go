// TEST_CASES: MAILMOD-T-081
package consoleconfig

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/anas-project/ANAS/internal/serviceauth"
	"strings"
	"testing"
)

func TestMailInstallSocketConfigRequiresMatchingScopedGrant(t *testing.T) {
	base := `api_version: anas.console-config/v1
mode: loopback
port: 18445
console_store: /var/lib/anas/console
workspaces: [{id: main, path: /srv/anas/work}]
service_api:
  credentials_dir: /var/lib/anas/clients
  clients:
    - id: mail
      enabled: true
      uid: 65532
      gid: 65532
      grants:
        - permission: mail.ingress.install
          workspace: main
          resources: [mail]
mail_install_socket:
  directory: /run/anas/mail-install
  group_id: 65532
`
	if _, err := Parse([]byte(base)); err != nil {
		t.Fatal(err)
	}
	outbound := strings.Replace(base, "permission: mail.ingress.install", "permission: mail.outbound.configure", 1)
	if _, err := Parse([]byte(outbound)); err != nil {
		t.Fatal("scoped outbound-only installer rejected", err)
	}
	if _, err := Parse([]byte(strings.Replace(outbound, "resources: [mail]", "resources: [other]", 1))); err == nil {
		t.Fatal("outbound grant for another application accepted")
	}
	for _, tc := range []struct{ name, source string }{
		{"relative", strings.Replace(base, "/run/anas/mail-install", "mail-install", 1)},
		{"workspace", strings.Replace(base, "/run/anas/mail-install", "/srv/anas/work/socket", 1)},
		{"credentials", strings.Replace(base, "/run/anas/mail-install", "/var/lib/anas/clients/socket", 1)},
		{"console", strings.Replace(base, "/run/anas/mail-install", "/var/lib/anas/console/socket", 1)},
		{"private-install-state", strings.Replace(base, "/run/anas/mail-install", "/var/lib/anas/mail-installations/socket", 1)},
		{"root-group", strings.Replace(base, "group_id: 65532", "group_id: 0", 1)},
		{"wrong-group", strings.Replace(base, "group_id: 65532", "group_id: 65533", 1)},
		{"disabled-client", strings.Replace(base, "enabled: true", "enabled: false", 1)},
		{"wrong-permission", strings.Replace(base, "permission: mail.ingress.install", "permission: application.credentials.read", 1)},
		{"long-path", strings.Replace(base, "/run/anas/mail-install", fmt.Sprintf("/run/%s", strings.Repeat("a", 100)), 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.source)); err == nil {
				t.Fatal("unsafe socket config accepted")
			}
		})
	}
}

func TestMailInstallSocketRejectsResolvedPrivateStateAndConfig(t *testing.T) {
	root := t.TempDir()
	socket := filepath.Join(root, "socket")
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(socket, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(socket, alias); err != nil {
		t.Fatal(err)
	}
	cfg := Config{ConsoleStore: filepath.Join(root, "console"), Workspaces: []Workspace{{ID: "main", Path: workspace}}, ServiceAPI: &serviceauth.Config{Directory: filepath.Join(root, "clients")}, MailInstallSocket: &MailInstallSocketConfig{Directory: socket, GroupID: 123}}
	cfg.ConsoleStore = filepath.Join(alias, "console")
	if err := validateResolvedStorageBoundary(cfg); err == nil {
		t.Fatal("resolved console overlap accepted")
	}
	cfg.ConsoleStore = filepath.Join(root, "console")
	cfg.ServiceAPI.Directory = filepath.Join(alias, "clients")
	if err := validateResolvedStorageBoundary(cfg); err == nil {
		t.Fatal("resolved credentials overlap accepted")
	}
	parent := filepath.Join(root, "other")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(socket, filepath.Join(parent, "mail-installations")); err != nil {
		t.Fatal(err)
	}
	cfg.ServiceAPI.Directory = filepath.Join(parent, "clients")
	cfg.ServiceAPI.Clients = []serviceauth.Client{{Grants: []serviceauth.Grant{{Permission: "mail.ingress.install"}}}}
	if err := validateResolvedStorageBoundary(cfg); err == nil {
		t.Fatal("resolved installation state overlap accepted")
	}
	cfg.ServiceAPI.Directory = filepath.Join(root, "clients")
	cfg.ServiceAPI.Clients = nil
	cfg.TLS.Temporary = &TemporaryTLSPaths{PrivateKey: filepath.Join(alias, "key.pem")}
	if err := validateResolvedStorageBoundary(cfg); err == nil {
		t.Fatal("resolved private key overlap accepted")
	}
	cfg.TLS.Temporary = nil
	path := filepath.Join(socket, "anasd.yml")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateServiceConfigLocation(cfg, path); err == nil {
		t.Fatal("service configuration exposed by socket mount")
	}
}
