package runner

// Conformance tests for docs/contracts/README.md.
//
// The contract is what external non-interactive callers branch on and what the
// shared application service must remain compatible with. The two parts that
// break silently are the two asserted here for every command:
// stdout must hold exactly one JSON document, and the exit code must be the
// number from the table rather than merely non-zero. A command that returns 1
// where the table says 4 still looks like it works from a shell prompt and
// tells a programmatic caller nothing it can act on.
//
// These drive Main in-process and read ExitCode, which is what the process
// wrapper does. The end-to-end suites in test-env/scripts drive a built binary
// for the same assertions, because `go run` collapses every non-zero status
// to 1.

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/buildinfo"
)

// capture runs one command with stdout and stderr redirected, and reports what
// each stream received alongside the exit code the process wrapper would use.
func capture(t *testing.T, args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	outFile, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	errFile, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	realOut, realErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outFile, errFile
	err = Main(args)
	os.Stdout, os.Stderr = realOut, realErr
	exit = ExitCode(err)

	read := func(f *os.File) string {
		if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
			t.Fatal(seekErr)
		}
		b, readErr := io.ReadAll(f)
		if readErr != nil {
			t.Fatal(readErr)
		}
		_ = f.Close()
		return string(b)
	}
	return read(outFile), read(errFile), exit
}

var snakeCase = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// requireSingleDocument is the "callers can JSON.parse(stdout) directly" rule.
// Decoding one value and then requiring EOF is the assertion: a second value,
// a stray log line or a YAML document all fail here, and all three are ways
// the rule has actually been broken.
func requireSingleDocument(t *testing.T, label, stdout string) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(stdout))
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("%s: stdout is not a JSON document (%v); got %q", label, err, stdout)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("%s: stdout carries more than one JSON document; got %q", label, stdout)
	}
	if document["api_version"] != cliAPIVersion {
		t.Errorf("%s: api_version = %v, want %q", label, document["api_version"], cliAPIVersion)
	}
	if _, ok := document["ok"].(bool); !ok {
		t.Errorf("%s: ok is missing or not a boolean; got %v", label, document["ok"])
	}
	return document
}

// requireFailureDocument additionally checks the error shape. code is an
// enumeration a caller switches on, so a value with spaces or capitals in it
// is free text wearing a code's clothes.
func requireFailureDocument(t *testing.T, label, stdout string) map[string]any {
	t.Helper()
	document := requireSingleDocument(t, label, stdout)
	if ok, _ := document["ok"].(bool); ok {
		t.Errorf("%s: ok = true on a failing command", label)
	}
	failure, ok := document["error"].(map[string]any)
	if !ok {
		t.Fatalf("%s: no error object; got %v", label, document)
	}
	code, _ := failure["code"].(string)
	if !snakeCase.MatchString(code) {
		t.Errorf("%s: error.code = %q, want a snake_case enumeration value", label, code)
	}
	if message, _ := failure["message"].(string); message == "" {
		t.Errorf("%s: error.message is empty; it is the only thing a human is shown", label)
	}
	return document
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// newWorkspace creates one through `anas init` rather than by hand, so the
// tests below operate on exactly the layout the command produces. -y carries
// it past the non-Btrfs warning on hosts where the temp directory is ext4,
// which is most of them.
func newWorkspace(t *testing.T) string {
	t.Helper()
	workspace := filepath.Join(t.TempDir(), "ws")
	if _, _, exit := capture(t, "init", workspace, "-y", "--json"); exit != 0 {
		t.Fatalf("init %s: exit %d", workspace, exit)
	}
	return workspace
}

// TestEveryCommandEmitsOneDocumentAndTheDocumentedExitCode walks the commands
// that need no running deployment. The cases are grouped by the exit code they
// assert because the code is the part of the contract most likely to be got
// wrong quietly.
func TestEveryCommandEmitsOneDocumentAndTheDocumentedExitCode(t *testing.T) {
	root := repoRoot(t)
	workspace := newWorkspace(t)
	fresh := filepath.Join(t.TempDir(), "fresh")

	cases := []struct {
		name string
		args []string
		exit int
	}{
		// ---- 0: success ------------------------------------------------
		{"help", []string{"help", "--json"}, 0},
		{"version", []string{"version", "--json"}, 0},
		{"init", []string{"init", fresh, "-y", "--json"}, 0},
		{"status", []string{"status", "-w", workspace, "--json"}, 0},
		{"deployments list", []string{"deployments", "list", "-w", workspace, "--json"}, 0},
		{"config explain", []string{"config", "explain", "global.base_domain", "--root", root, "--json"}, 0},
		{"config set", []string{"config", "set", "global.timezone", "UTC", "-w", workspace, "--root", root, "--json"}, 0},
		{"config plan", []string{"config", "plan", "-w", workspace, "--root", root, "--json"}, 0},
		{"config secret list", []string{"config", "secret", "list", "-w", workspace, "--json"}, 0},
		{"admin local list", []string{"admin", "local", "list", "-w", workspace, "--json"}, 0},
		{"plan", []string{"plan", "-w", workspace, "--root", root, "--json"}, 0},
		// snapshot and backup already conformed; they are here so a
		// regression in the shared plumbing fails against them too.
		{"snapshot list", []string{"snapshot", "list", "-w", workspace, "--json"}, 0},
		{"backup capabilities", []string{"backup", "capabilities", "-w", workspace, "--json"}, 0},

		// ---- 2: usage --------------------------------------------------
		{"unknown command", []string{"frobnicate", "--json"}, 2},
		{"config with no subcommand", []string{"config", "--json"}, 2},
		{"unknown config subcommand", []string{"config", "wat", "-w", workspace, "--root", root, "--json"}, 2},
		{"config explain unknown module", []string{"config", "explain", "nosuchmodule.thing", "--root", root, "--json"}, 2},
		{"deployments with no subcommand", []string{"deployments", "--json"}, 2},
		{"unknown deployments subcommand", []string{"deployments", "wat", "-w", workspace, "--json"}, 2},
		{"plan without a config", []string{"plan", "--root", root, "--json"}, 2},
		{"init with two paths", []string{"init", "a", "b", "--json"}, 2},
		{"init with a bad shell-init", []string{"init", fresh, "--shell-init", "maybe", "--json"}, 2},
		{"apply with conflicting snapshot flags", []string{"apply", "-w", workspace, "--snapshot", "--no-snapshot", "--json"}, 2},
		{"status with a stray argument", []string{"status", "-w", workspace, "extra", "--json"}, 2},
		{"secret get without a key", []string{"config", "secret", "get", "-w", workspace, "--json"}, 2},
		{"admin with no subcommand", []string{"admin", "--json"}, 2},
		{"module with no subcommand", []string{"module", "--json"}, 2},
		{"unknown module subcommand", []string{"module", "wat", "--json"}, 2},
		{"module install without release", []string{"module", "install", "nextcloud", "--json"}, 2},
		{"rollback without -w", []string{"rollback", "--json"}, 2},
		{"unknown snapshot subcommand", []string{"snapshot", "wat", "-w", workspace, "--json"}, 2},
		{"unknown backup subcommand", []string{"backup", "wat", "--json"}, 2},
		{"unrecognised flag", []string{"status", "-w", workspace, "--nope", "--json"}, 2},

		// ---- 4: precondition unmet -------------------------------------
		{"plan with a missing config", []string{"plan", "-w", workspace, "-c", filepath.Join(root, "no-such-config.yml"), "--root", root, "--json"}, 4},
		{"lock with a missing config", []string{"lock", "-w", workspace, "-c", filepath.Join(root, "no-such-config.yml"), "--json"}, 4},
		{"inspect a missing deployment", []string{"deployments", "inspect", "nosuchdeployment", "-w", workspace, "--json"}, 4},
		{"get a missing secret", []string{"config", "secret", "get", "NO_SUCH_SECRET", "-w", workspace, "--json"}, 4},
		{"get a missing local admin", []string{"admin", "local", "credential", "ddns_go", "-w", workspace, "--json"}, 4},
		{"rotate a missing local admin", []string{"admin", "local", "rotate", "ddns_go", "-w", workspace, "--json"}, 4},
		{"rollback with no previous deployment", []string{"rollback", "-w", workspace, "--json"}, 4},
		{"start with no active deployment", []string{"start", "-w", workspace, "--json"}, 4},
		{"stop with no active deployment", []string{"stop", "-w", workspace, "--json"}, 4},
		{"restart with no active deployment", []string{"restart", "-w", workspace, "--json"}, 4},
		{"apply an unknown deployment", []string{"apply", "-w", workspace, "--deployment", "nosuchdeployment", "--json"}, 4},
		{"init over an existing workspace", []string{"init", workspace, "-y", "--json"}, 4},
		{"module sync without a lock", []string{"module", "sync", "-w", workspace, "--json"}, 4},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			stdout, _, exit := capture(t, testCase.args...)
			if exit != testCase.exit {
				t.Errorf("exit = %d, want %d (stdout %q)", exit, testCase.exit, stdout)
			}
			if testCase.exit == 0 {
				requireSingleDocument(t, testCase.name, stdout)
				return
			}
			requireFailureDocument(t, testCase.name, stdout)
		})
	}
}

// TestReadOnlyQueryPayloadsStayStable pins the four payloads being migrated to
// the shared application service. The general contract table above protects
// the envelope and exit status; these assertions protect the fields, nulls and
// empty arrays that an external CLI consumer already sees.
func TestReadOnlyQueryPayloadsStayStable(t *testing.T) {
	workspace := newWorkspace(t)
	base := stateDir(workspace)
	deploymentID := "20260818T010203Z-deadbeef"
	deploymentRoot := filepath.Join(base, "deployments", deploymentID)
	if err := os.MkdirAll(deploymentRoot, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := deploymentManifest{
		APIVersion:        deploymentAPIVersion,
		ID:                deploymentID,
		CreatedAt:         "2026-08-18T01:02:03Z",
		ConfigFingerprint: "sha256:test",
		ModuleOrder:       []string{},
		Modules:           map[string]deploymentModule{},
	}
	if err := writeYAMLAtomic(filepath.Join(deploymentRoot, "deployment.yml"), &manifest, 0600); err != nil {
		t.Fatal(err)
	}
	state := deploymentState{
		APIVersion: activeStateVersion,
		ID:         deploymentID,
		Status:     "ready",
		CreatedAt:  "2026-08-18T01:02:03Z",
	}
	if err := saveDeploymentState(base, state); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		args []string
		want map[string]any
	}{
		{
			name: "version",
			args: []string{"version", "--json"},
			want: map[string]any{
				"api_version": cliAPIVersion,
				"ok":          true,
				"version":     buildinfo.Version,
				"commit":      buildinfo.Commit,
				"date":        buildinfo.Date,
			},
		},
		{
			name: "status",
			args: []string{"status", "-w", workspace, "--json"},
			want: map[string]any{
				"api_version":          cliAPIVersion,
				"ok":                   true,
				"workspace":            workspace,
				"active_deployment":    nil,
				"activated_at":         nil,
				"verified_at":          nil,
				"previous_deployments": []any{},
			},
		},
		{
			name: "deployments list",
			args: []string{"deployments", "list", "-w", workspace, "--json"},
			want: map[string]any{
				"api_version": cliAPIVersion,
				"ok":          true,
				"workspace":   workspace,
				"deployments": []any{
					map[string]any{
						"id":             deploymentID,
						"status":         "ready",
						"created_at":     "2026-08-18T01:02:03Z",
						"activated_at":   nil,
						"deactivated_at": nil,
						"verified_at":    nil,
						"predecessor":    nil,
						"failure":        nil,
					},
				},
			},
		},
		{
			name: "deployments inspect",
			args: []string{"deployments", "inspect", deploymentID, "-w", workspace, "--json"},
			want: map[string]any{
				"api_version":     cliAPIVersion,
				"ok":              true,
				"workspace":       workspace,
				"deployment_path": deploymentRoot,
				"deployment": map[string]any{
					"api_version":        deploymentAPIVersion,
					"id":                 deploymentID,
					"created_at":         "2026-08-18T01:02:03Z",
					"config_fingerprint": "sha256:test",
					"images_built":       false,
					"build_acceleration": false,
					"module_order":       []any{},
					"modules":            map[string]any{},
					"snapshot":           map[string]any{"backend": "", "source": "", "root": "", "keep_auto": float64(0)},
				},
				"state": map[string]any{
					"id":             deploymentID,
					"status":         "ready",
					"created_at":     "2026-08-18T01:02:03Z",
					"activated_at":   nil,
					"deactivated_at": nil,
					"verified_at":    nil,
					"predecessor":    nil,
					"failure":        nil,
				},
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			stdout, _, exit := capture(t, testCase.args...)
			if exit != 0 {
				t.Fatalf("exit = %d; stdout %q", exit, stdout)
			}
			got := requireSingleDocument(t, testCase.name, stdout)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("payload changed\ngot:  %#v\nwant: %#v", got, testCase.want)
			}
		})
	}
}

// TestNonInteractiveConfirmationExitsThree pins the rule the contract calls the
// one non-interactive callers need most: without -y and without a terminal, a
// command that needs confirmation returns immediately with 3 rather than
// blocking on input nobody is there to provide.
//
// The shell-init collision is used because it depends only on $HOME and $SHELL.
// The other confirmation paths need a Btrfs filesystem or a running
// deployment, so they are asserted end to end in test-env/scripts instead.
func TestNonInteractiveConfirmationExitsThree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")

	first := filepath.Join(t.TempDir(), "one")
	if _, _, exit := capture(t, "init", first, "-y", "--shell-init", "write", "--json"); exit != 0 {
		t.Fatalf("seeding the profile: exit %d", exit)
	}

	// A second workspace wants the same block. Replacing it is a question, and
	// there is no terminal to ask.
	second := filepath.Join(t.TempDir(), "two")
	stdout, _, exit := capture(t, "init", second, "--shell-init", "write", "--json")
	if exit != exitConfirmation {
		t.Fatalf("exit = %d, want %d; stdout %q", exit, exitConfirmation, stdout)
	}
	document := requireFailureDocument(t, "shell-init collision", stdout)
	failure := document["error"].(map[string]any)
	if failure["code"] != "confirmation_required" {
		t.Errorf("error.code = %v, want confirmation_required", failure["code"])
	}
}

// TestProgressGoesToStderrAsJSONLines checks the stream split for the records
// that accompany a long operation. They share stderr with the human-readable
// warnings, so the test also pins that each line stands alone as JSON: a
// caller reads this stream line by line while the operation is still running,
// and cannot wait for a closing brace that only arrives at the end.
func TestProgressGoesToStderrAsJSONLines(t *testing.T) {
	realErr := os.Stderr
	file, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = file
	emitProgress(true, "stop-containers", 8, 13, "modules")
	emitProgress(true, "send-data", 734003200, 0, "bytes")
	emitWarning(true, "plaintext_secrets_leaving_host", "secrets leave the host: %d%% of the way", 50)
	emitProgress(false, "not-emitted", 1, 2, "modules")
	os.Stderr = realErr

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d records, want 3 (human mode must emit none): %q", len(lines), string(b))
	}

	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("record 1 is not JSON: %v", err)
	}
	if first["type"] != "progress" || first["phase"] != "stop-containers" ||
		first["current"] != float64(8) || first["total"] != float64(13) || first["unit"] != "modules" {
		t.Errorf("record 1 = %v", first)
	}

	var second map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("record 2 is not JSON: %v", err)
	}
	// An unknown total is omitted rather than written as 0, which a caller
	// would otherwise have to special-case as though it were a real magnitude.
	if _, present := second["total"]; present {
		t.Errorf("record 2 carries a total for an unknown magnitude: %v", second)
	}

	var third map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &third); err != nil {
		t.Fatalf("record 3 is not JSON: %v", err)
	}
	if third["type"] != "warning" || third["code"] != "plaintext_secrets_leaving_host" {
		t.Errorf("record 3 = %v", third)
	}
	if !strings.Contains(third["message"].(string), "50%") {
		t.Errorf("record 3 message = %v", third["message"])
	}
}

// TestJSONDocumentsCarryAbsolutePathsAndByteSizes covers the two formatting
// rules that are invisible until a caller on the other side of a web request
// tries to use the value: a relative path means nothing to a process that did
// not share this working directory, and "1.3G" cannot be compared or summed.
func TestJSONDocumentsCarryAbsolutePathsAndByteSizes(t *testing.T) {
	workspace := newWorkspace(t)

	// Deliberately relative: the command must absolutise before reporting.
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Dir(workspace)); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(previous) }()

	stdout, _, exit := capture(t, "status", "-w", "ws", "--json")
	if exit != 0 {
		t.Fatalf("status: exit %d (%s)", exit, stdout)
	}
	document := requireSingleDocument(t, "status", stdout)
	if path, _ := document["workspace"].(string); !filepath.IsAbs(path) {
		t.Errorf("workspace = %q, want an absolute path", path)
	}

	stdout, _, exit = capture(t, "backup", "capabilities", "-w", "ws", "--json")
	if exit != 0 {
		t.Fatalf("backup capabilities: exit %d (%s)", exit, stdout)
	}
	document = requireSingleDocument(t, "backup capabilities", stdout)
	estimate, ok := document["estimate"].(map[string]any)
	if !ok {
		t.Fatalf("no estimate in %v", document)
	}
	for key := range estimate {
		if !strings.HasSuffix(key, "_bytes") {
			t.Errorf("estimate.%s does not end in _bytes; sizes are byte integers in _bytes fields", key)
		}
	}
}

// TestHumanOutputStaysOffTheJSONPath guards the inverse of the stdout rule.
// Human-readable output is explicitly not a contract, but it must not appear
// alongside the document, which is how the "exactly one document" rule gets
// broken in practice — by a Println left behind next to an emit.
func TestHumanOutputStaysOffTheJSONPath(t *testing.T) {
	root := repoRoot(t)
	workspace := newWorkspace(t)

	for _, args := range [][]string{
		{"status", "-w", workspace, "--json"},
		{"deployments", "list", "-w", workspace, "--json"},
		{"config", "plan", "-w", workspace, "--root", root, "--json"},
		{"config", "secret", "list", "-w", workspace, "--json"},
		{"config", "explain", "global.base_domain", "--root", root, "--json"},
		{"plan", "-w", workspace, "--root", root, "--json"},
	} {
		label := strings.Join(args, " ")
		stdout, _, exit := capture(t, args...)
		if exit != 0 {
			t.Errorf("%s: exit %d", label, exit)
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(stdout), "{") {
			t.Errorf("%s: stdout does not begin with the JSON document: %q", label, stdout)
		}
		requireSingleDocument(t, label, stdout)
	}
}
