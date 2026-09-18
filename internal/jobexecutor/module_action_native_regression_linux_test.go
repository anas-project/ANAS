//go:build linux

package jobexecutor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"golang.org/x/sys/unix"
)

// The test binary doubles as a frozen, native ELF executor. This is compiled
// into tests only; production executables do not have this dispatch branch.
// No shell, package installation, network, capabilities or server is involved.
func init() {
	if len(os.Args) != 0 && os.Args[0] == "anas-module-action" {
		os.Exit(moduleActionNativeFixture())
	}
}

func moduleActionNativeFixture() int {
	request, err := actionabi.ReadRequest(os.Stdin)
	if err != nil {
		return 97
	}
	if os.Getenv("ANAS_NATIVE_TEST_SECRET") != "" || os.Getenv("ANAS_ACTION_CANCEL_FD") != "4" {
		return 98
	}
	marker, err := os.ReadFile("fixture-marker")
	if err != nil || string(marker) != "frozen-module-directory" {
		return 99
	}
	emit := func(event actionabi.Event) bool {
		event.ABI, event.JobID, event.InvocationID = actionabi.Version, request.JobID, request.InvocationID
		frame, err := actionabi.EncodeExecutorEvent(event)
		if err != nil {
			return false
		}
		_, err = os.Stdout.Write(frame)
		return err == nil
	}
	mode := strings.TrimPrefix(request.Action, "module.sample.")
	if mode == "cancel" || mode == "ignore_cancel" {
		if !emit(actionabi.Event{Type: "progress", Progress: &actionabi.Progress{Phase: "ready"}}) {
			return 96
		}
		if mode == "ignore_cancel" {
			time.Sleep(30 * time.Second)
			return 0
		}
		pipe := os.NewFile(4, "module-cancellation")
		if pipe == nil {
			return 95
		}
		defer pipe.Close()
		if actionabi.ReadModuleCancellation(pipe) != nil {
			return 94
		}
		if !emit(actionabi.Event{Type: "result", Result: &actionabi.Result{Outcome: actionabi.Cancelled}}) {
			return 93
		}
		return 0
	}
	if mode == "no_terminal" {
		return 0
	}
	if mode == "stderr_overflow" {
		_, _ = io.WriteString(os.Stderr, strings.Repeat("private-stderr-fixture", 4000))
		return 0
	}
	if mode == "failure" || mode == "failed_zero" {
		if !emit(actionabi.Event{Type: "error", Error: &actionabi.Failure{Outcome: actionabi.Failed, Code: "fixture_failed", Message: "Fixture failed"}}) {
			return 92
		}
		if mode == "failed_zero" {
			return 0
		}
		return 3
	}
	changed := false
	if !emit(actionabi.Event{Type: "result", Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: json.RawMessage(`{}`)}}) {
		return 91
	}
	if mode == "trailing" {
		_, _ = io.WriteString(os.Stdout, `{"partial"`)
	}
	if mode == "hang_after_result" {
		time.Sleep(30 * time.Second)
	}
	return 0
}

func prepareNativeRegressionProgram(t *testing.T) (ModuleActionDefinition, *moduleActionProgram) {
	t.Helper()
	if !unprivilegedModuleActionProcess() {
		t.Skip("native Module supervision requires an unprivileged Linux process with no capabilities")
	}
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		t.Skip("native supervision requires pidfd support")
	}
	_ = unix.Close(fd)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > maxModuleActionExecutable {
		t.Skip("test binary exceeds the production native-executable size limit")
	}
	root := t.TempDir()
	target, err := os.OpenFile(filepath.Join(root, "executor"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(target, digest), source)
	closeErr := target.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fixture-marker"), []byte("frozen-module-directory"), 0600); err != nil {
		t.Fatal(err)
	}
	definition := ModuleActionDefinition{
		Name: "module.sample.success", Module: "sample", DeploymentID: "deployment-test", DescriptorDigest: strings.Repeat("a", 64),
		ModuleRoot: root, Executable: "executor", ExecutableDigest: hex.EncodeToString(digest.Sum(nil)),
		Risk: "normal", Cancellable: "true", Timeout: 4 * time.Second, CancelGrace: time.Second,
	}
	program, err := prepareModuleActionProgram(definition)
	if err != nil {
		t.Fatalf("prepare native fixture: %v", err)
	}
	t.Cleanup(func() {
		if err := program.Close(); err != nil {
			t.Error(err)
		}
	})
	return definition, program
}

func TestModuleActionLinuxSupervisorFixture(t *testing.T) {
	definition, program := prepareNativeRegressionProgram(t)
	t.Setenv("ANAS_NATIVE_TEST_SECRET", "must-not-reach-the-executor")
	for _, test := range []struct {
		mode string
		want actionabi.Outcome
	}{
		{"success", actionabi.Succeeded},
		{"failure", actionabi.Failed},
		{"cancel", actionabi.Cancelled},
		{"ignore_cancel", actionabi.Unknown},
		{"failed_zero", actionabi.Unknown},
		{"no_terminal", actionabi.Unknown},
		{"trailing", actionabi.Unknown},
		{"hang_after_result", actionabi.Unknown},
		{"stderr_overflow", actionabi.Unknown},
	} {
		t.Run(test.mode, func(t *testing.T) {
			lease, err := consolejobs.AcquireExecutionLease(context.Background(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := lease.Close(); err != nil {
					t.Error(err)
				}
			}()
			store := &recorderRegressionStore{job: recorderRegressionJob()}
			options := recorderRegressionOptions(store, nil)
			options.Lease = lease
			cancelRequest := make(chan struct{})
			var once sync.Once
			options.Project = func(event actionabi.Event) (actionabi.Event, error) {
				if (test.mode == "cancel" || test.mode == "ignore_cancel") && event.Type == "progress" {
					once.Do(func() { close(cancelRequest) })
				}
				return event, nil // Synthetic fixture frames only.
			}
			current := definition
			current.Name = "module.sample." + test.mode
			if test.mode == "hang_after_result" {
				current.Timeout = time.Second
			}
			request := actionabi.Request{ABI: actionabi.Version, JobID: store.job.ID, InvocationID: store.job.Action.InvocationID, Action: current.Name, Parameters: json.RawMessage(`{}`)}
			_, err = runModuleActionProcess(context.Background(), current, program, request, options, cancelRequest)
			if test.want != actionabi.Unknown && err != nil {
				t.Fatalf("confirmed fixture failed: %v", err)
			}
			if len(store.terminals) != 1 {
				t.Fatalf("want one durable terminal, got %d; error %v", len(store.terminals), err)
			}
			terminal := store.terminals[0]
			var outcome actionabi.Outcome
			if terminal.Result != nil {
				outcome = terminal.Result.Outcome
			} else if terminal.Error != nil {
				outcome = terminal.Error.Outcome
			}
			if outcome != test.want {
				t.Fatalf("got %s, want %s; error %v", outcome, test.want, err)
			}
			body, err := json.Marshal(terminal)
			if err != nil || strings.Contains(string(body), "private-stderr-fixture") || strings.Contains(string(body), "must-not-reach") {
				t.Fatal("raw environment or stderr reached the terminal")
			}
		})
	}
}

func TestModuleActionProcessStateHandlesParenthesesInComm(t *testing.T) {
	state, group, err := parseModuleActionProcessState([]byte("321 (worker ) (copy\n) Z 1 321 0 0\n"), 321)
	if err != nil || state != 'Z' || group != 321 {
		t.Fatalf("valid proc comm rejected: state=%c group=%d err=%v", state, group, err)
	}
	for _, body := range []string{
		"321 worker Z 1 321", "322 (worker) Z 1 321", "321 (worker) ? 1 321", "321 (worker) Z 1 -1", "321 (worker) Z 1",
	} {
		if _, _, err := parseModuleActionProcessState([]byte(body), 321); !errors.Is(err, ErrModuleActionContainment) {
			t.Fatalf("malformed process state accepted: %v", err)
		}
	}
}
