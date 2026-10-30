// TEST_CASES: MAILMOD-T-076
package cloudrelay

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestBootstrapAccessPlanRejectsInjectedOrMultipleKeys(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	key := string(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	plan, err := PrepareBootstrapAccess([]byte(strings.TrimSpace(key) + " operator-comment\n"))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Account != "anasmaildeploy" || plan.Home != "/var/lib/anas-mail-relay-bootstrap" || plan.Shell != "/bin/sh" || plan.AuthorizedKeys != "restrict,command=\"/usr/bin/sudo -n /usr/local/libexec/anas-mail-relay-bootstrap/cloudrelay bootstrap\" "+key || plan.Sudoers != "anasmaildeploy ALL=(root) NOPASSWD: /usr/local/libexec/anas-mail-relay-bootstrap/cloudrelay bootstrap\n" {
		t.Fatal("incorrect fixed policy")
	}
	raw, _ := json.Marshal(plan)
	if strings.Contains(string(raw), "operator-comment") {
		t.Fatal("untrusted comment propagated")
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaSigner, err := ssh.NewSignerFromKey(rsaKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", key + key, "command=\"id\" " + key, "restrict " + key, string(ssh.MarshalAuthorizedKey(rsaSigner.PublicKey())), "not-a-key", key + "\x00", "\n# comment only\n"} {
		if _, err := PrepareBootstrapAccess([]byte(bad)); !errors.Is(err, ErrPublishConflict) {
			t.Fatal("unsafe public key accepted")
		}
	}
}
func TestBootstrapAccessPlanFileSafety(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-owned public input check")
	}
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(private)
	dir := t.TempDir()
	path := filepath.Join(dir, "key.pub")
	if err := os.WriteFile(path, ssh.MarshalAuthorizedKey(signer.PublicKey()), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := BootstrapAccessPlanFile(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := BootstrapAccessPlanFile(filepath.Join(dir, "link")); err == nil {
		t.Fatal("followed key symlink")
	}
	if err := os.Link(path, filepath.Join(dir, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if _, err := BootstrapAccessPlanFile(path); err == nil {
		t.Fatal("accepted multiply linked key")
	}
	if err := os.Remove(filepath.Join(dir, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if _, err := BootstrapAccessPlanFile("relative.pub"); err == nil {
		t.Fatal("accepted relative key path")
	}
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := BootstrapAccessPlanFile(path); err == nil {
		t.Fatal("accepted writable key")
	}
}
func TestBootstrapSSHDContextMustExcludeOtherAuthentication(t *testing.T) {
	raw := `pubkeyauthentication yes
passwordauthentication no
kbdinteractiveauthentication no
gssapiauthentication no
hostbasedauthentication no
authorizedkeyscommand none
trustedusercakeys none
permituserenvironment no
permitemptypasswords no
forcecommand none
pubkeyacceptedalgorithms ssh-ed25519,rsa-sha2-512
authorizedkeysfile .ssh/authorized_keys .ssh/authorized_keys2
authenticationmethods any
permituserrc yes
strictmodes yes
usepam yes
`
	if err := InspectBootstrapSSHD([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	for _, changed := range []string{strings.Replace(raw, "passwordauthentication no", "passwordauthentication yes", 1), strings.Replace(raw, "trustedusercakeys none", "trustedusercakeys /etc/ssh/ca.pub", 1), strings.Replace(raw, "authorizedkeyscommand none", "authorizedkeyscommand /usr/bin/lookup", 1), strings.Replace(raw, "authorizedkeysfile .ssh/authorized_keys .ssh/authorized_keys2", "authorizedkeysfile /tmp/authorized_keys", 1), strings.Replace(raw, "authenticationmethods any", "authenticationmethods publickey,password", 1), strings.Replace(raw, "permituserenvironment no", "permituserenvironment yes", 1), strings.Replace(raw, "permitemptypasswords no", "permitemptypasswords yes", 1), strings.Replace(raw, "strictmodes yes", "strictmodes no", 1), strings.Replace(raw, "usepam yes", "usepam no", 1), strings.Replace(raw, "forcecommand none", "forcecommand /usr/bin/id", 1), strings.Replace(raw, "pubkeyacceptedalgorithms ssh-ed25519,rsa-sha2-512", "pubkeyacceptedalgorithms rsa-sha2-512", 1), strings.Replace(raw, "hostbasedauthentication no\n", "", 1), raw + "pubkeyauthentication yes\n"} {
		if err := InspectBootstrapSSHD([]byte(changed)); !errors.Is(err, ErrPublishConflict) {
			t.Fatal("unsafe policy accepted")
		}
	}
}

func TestBootstrapSudoRequiresSingleExactEffectiveGrant(t *testing.T) {
	valid := "Matching Defaults entries for anasmaildeploy on host:\n    env_reset\n\nUser anasmaildeploy may run the following commands on host:\n\nSudoers entry:\n    RunAsUsers: root\n    Options: !authenticate\n    Commands:\n\t/usr/local/libexec/anas-mail-relay-bootstrap/cloudrelay bootstrap\n"
	if err := InspectBootstrapSudo([]byte(valid)); err != nil {
		t.Fatal("exact grant rejected", err)
	}
	withSource := strings.Replace(valid, "Sudoers entry:", "Sudoers entry: /etc/sudoers.d/anas-mail-relay-bootstrap", 1)
	if err := InspectBootstrapSudo([]byte(withSource)); err != nil {
		t.Fatal("exact grant with sudoers source rejected", err)
	}
	for name, raw := range map[string]string{
		"other-user":        strings.Replace(valid, "User anasmaildeploy ", "User someoneelse ", 1),
		"all-commands":      strings.Replace(valid, bootstrapCommand, "ALL", 1),
		"extra-argument":    strings.Replace(valid, bootstrapCommand, bootstrapCommand+" *", 1),
		"extra-command":     valid + "\t/usr/bin/id\n",
		"extra-entry":       valid + "\nSudoers entry:\n    RunAsUsers: ALL\n    Commands:\n        ALL\n",
		"earlier-entry":     "Sudoers entry:\n    Commands:\n        ALL\n" + valid,
		"earlier-source":    "Sudoers entry: /etc/sudoers\n    Commands:\n        ALL\n" + valid,
		"unexpected-source": strings.Replace(valid, "Sudoers entry:", "Sudoers entry: /etc/sudoers", 1),
		"run-as-all":        strings.Replace(valid, "RunAsUsers: root", "RunAsUsers: ALL", 1),
		"run-as-group":      strings.Replace(valid, "    Options:", "    RunAsGroups: ALL\n    Options:", 1),
		"password-needed":   strings.Replace(valid, "Options: !authenticate", "Options: authenticate", 1),
		"extra-option":      strings.Replace(valid, "Options: !authenticate", "Options: !authenticate, setenv", 1),
		"missing-header":    strings.Replace(valid, "User anasmaildeploy may run", "Nobody may run", 1),
		"duplicate-header":  valid + "User anasmaildeploy may run the following commands on host:\n",
		"carriage-return":   valid + "\r",
		"embedded-nul":      valid + "\x00",
	} {
		t.Run(name, func(t *testing.T) {
			if err := InspectBootstrapSudo([]byte(raw)); !errors.Is(err, ErrPublishConflict) {
				t.Fatal("unsafe policy accepted", err)
			}
		})
	}
}
