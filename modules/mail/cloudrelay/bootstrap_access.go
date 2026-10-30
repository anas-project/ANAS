package cloudrelay

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

const bootstrapAccount = "anasmaildeploy"
const bootstrapCommand = "/usr/local/libexec/anas-mail-relay-bootstrap/cloudrelay bootstrap"
const bootstrapForcedCommand = "/usr/bin/sudo -n " + bootstrapCommand

// BootstrapAccessPlan contains only public configuration. Applying it also
// requires exclusive account creation, SSH policy checks and audited recovery.
type BootstrapAccessPlan struct {
	Account        string `json:"account"`
	Home           string `json:"home"`
	Shell          string `json:"shell"`
	AuthorizedKeys string `json:"authorized_keys"`
	Sudoers        string `json:"sudoers"`
	ForcedCommand  string `json:"forced_command"`
}

// PrepareBootstrapAccess accepts exactly one bare Ed25519 public key. Existing
// options and comments are discarded only after options have been rejected;
// administrators cannot smuggle commands or additional keys into a policy.
func PrepareBootstrapAccess(raw []byte) (BootstrapAccessPlan, error) {
	if len(raw) == 0 || len(raw) > 8192 || bytes.IndexByte(raw, 0) >= 0 {
		return BootstrapAccessPlan{}, ErrPublishConflict
	}
	key, _, options, rest, err := ssh.ParseAuthorizedKey(raw)
	if err != nil || len(options) != 0 || len(bytes.TrimSpace(rest)) != 0 || key.Type() != ssh.KeyAlgoED25519 {
		return BootstrapAccessPlan{}, ErrPublishConflict
	}
	public := strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(key)), "\n")
	return BootstrapAccessPlan{Account: bootstrapAccount, Home: "/var/lib/anas-mail-relay-bootstrap", Shell: "/bin/sh", AuthorizedKeys: "restrict,command=\"" + bootstrapForcedCommand + "\" " + public + "\n", Sudoers: bootstrapAccount + " ALL=(root) NOPASSWD: " + bootstrapCommand + "\n", ForcedCommand: bootstrapForcedCommand}, nil
}

// BootstrapAccessPlanFile is read-only and only accepts a root-owned,
// non-writable regular input. It follows neither a symlink nor multiple links.
func BootstrapAccessPlanFile(path string) (BootstrapAccessPlan, error) {
	if os.Geteuid() != 0 || !strings.HasPrefix(path, "/") {
		return BootstrapAccessPlan{}, ErrPublishConflict
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return BootstrapAccessPlan{}, ErrPublishConflict
	}
	f := os.NewFile(uintptr(fd), "public key")
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != 0 || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0022 != 0 || st.Nlink != 1 || st.Size < 1 || st.Size > 8192 {
		return BootstrapAccessPlan{}, ErrPublishConflict
	}
	raw, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil || len(raw) > 8192 {
		return BootstrapAccessPlan{}, ErrPublishConflict
	}
	return PrepareBootstrapAccess(raw)
}

// InspectBootstrapSSHD checks one effective sshd -T -C result. It does not
// examine other client addresses or prove future policy remains unchanged.
func InspectBootstrapSSHD(raw []byte) error {
	if len(raw) == 0 || len(raw) > 1<<20 || bytes.IndexByte(raw, 0) >= 0 {
		return ErrPublishConflict
	}
	expected := map[string]string{"pubkeyauthentication": "yes", "passwordauthentication": "no", "kbdinteractiveauthentication": "no", "gssapiauthentication": "no", "hostbasedauthentication": "no", "authorizedkeyscommand": "none", "trustedusercakeys": "none", "permituserenvironment": "no", "permitemptypasswords": "no", "strictmodes": "yes", "usepam": "yes"}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if want, required := expected[key]; required {
			if seen[key] || value != want {
				return ErrPublishConflict
			}
			seen[key] = true
		}
		if key == "authorizedkeysfile" {
			if seen[key] || value != ".ssh/authorized_keys .ssh/authorized_keys2" && value != ".ssh/authorized_keys" {
				return ErrPublishConflict
			}
			seen[key] = true
		}
		if key == "forcecommand" {
			if seen[key] || value != "none" && value != bootstrapForcedCommand {
				return ErrPublishConflict
			}
			seen[key] = true
		}
		if key == "pubkeyacceptedalgorithms" {
			found := false
			for _, algorithm := range strings.Split(value, ",") {
				if algorithm == ssh.KeyAlgoED25519 {
					found = true
				}
			}
			if seen[key] || !found {
				return ErrPublishConflict
			}
			seen[key] = true
		}
		if key == "authenticationmethods" {
			if seen[key] || value != "any" && value != "publickey" {
				return ErrPublishConflict
			}
			seen[key] = true
		}
	}
	for key := range expected {
		if !seen[key] {
			return ErrPublishConflict
		}
	}
	if !seen["authorizedkeysfile"] || !seen["authenticationmethods"] || !seen["forcecommand"] || !seen["pubkeyacceptedalgorithms"] {
		return ErrPublishConflict
	}
	return nil
}

// InspectBootstrapSudo accepts only sudo's C-locale long listing containing a
// single exact root command. Additional entries, commands, run-as groups or
// options are refused, including privileges granted outside our own rule.
func InspectBootstrapSudo(raw []byte) error {
	if len(raw) == 0 || len(raw) > 1<<20 || bytes.IndexByte(raw, 0) >= 0 || bytes.Contains(raw, []byte("\r")) {
		return ErrPublishConflict
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	header := "User " + bootstrapAccount + " may run the following commands on "
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, header) && strings.HasSuffix(line, ":") {
			if start != -1 || len(strings.TrimSuffix(strings.TrimPrefix(line, header), ":")) == 0 {
				return ErrPublishConflict
			}
			start = i
		}
	}
	if start < 0 {
		return ErrPublishConflict
	}
	for _, line := range lines[:start] {
		if strings.HasPrefix(strings.TrimSpace(line), "Sudoers entry:") {
			return ErrPublishConflict
		}
	}
	var tail []string
	for _, line := range lines[start+1:] {
		if v := strings.TrimSpace(line); v != "" {
			tail = append(tail, v)
		}
	}
	want := []string{"Sudoers entry:", "RunAsUsers: root", "Options: !authenticate", "Commands:", bootstrapCommand}
	if len(tail) != len(want) {
		return ErrPublishConflict
	}
	for i := range want {
		if tail[i] != want[i] && !(i == 0 && tail[i] == "Sudoers entry: /etc/sudoers.d/anas-mail-relay-bootstrap") {
			return ErrPublishConflict
		}
	}
	return nil
}

// CheckBootstrapSudo observes effective sudo policy for the fixed account.
// It does not freeze later policy changes or prove SSH policy for every peer.
func CheckBootstrapSudo(ctx context.Context) error {
	if os.Geteuid() != 0 {
		return ErrPublishConflict
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sudo", "-n", "-ll", "-U", bootstrapAccount)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	out := &bootstrapCheckOutput{}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil || out.overflow || ctx.Err() != nil {
		return ErrPublishUnknown
	}
	return InspectBootstrapSudo(out.data.Bytes())
}

var bootstrapRemoteHost = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,252}$`)

type bootstrapCheckOutput struct {
	data     bytes.Buffer
	overflow bool
}

func (b *bootstrapCheckOutput) Write(p []byte) (int, error) {
	if len(p) > 1<<20-b.data.Len() {
		b.overflow = true
		return len(p), nil
	}
	return b.data.Write(p)
}

// CheckBootstrapSSHDContext observes one concrete connection context. A pass
// cannot establish that other SSH Match blocks, sudo rules or later changes are safe.
func CheckBootstrapSSHDContext(ctx context.Context, remoteIP, remoteHost, localIP string, localPort int) error {
	if os.Geteuid() != 0 || net.ParseIP(remoteIP) == nil || net.ParseIP(localIP) == nil || !bootstrapRemoteHost.MatchString(remoteHost) || strings.Contains(remoteHost, "..") || strings.HasSuffix(remoteHost, ".") || localPort < 1 || localPort > 65535 {
		return ErrPublishConflict
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	match := "user=" + bootstrapAccount + ",addr=" + remoteIP + ",host=" + remoteHost + ",laddr=" + localIP + ",lport=" + strconv.Itoa(localPort)
	cmd := exec.CommandContext(ctx, "/usr/sbin/sshd", "-T", "-C", match)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	out := &bootstrapCheckOutput{}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil || out.overflow || ctx.Err() != nil {
		return ErrPublishUnknown
	}
	return InspectBootstrapSSHD(out.data.Bytes())
}
