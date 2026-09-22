//go:build linux || darwin

package computeingressruntime

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func workspaceFenceFixture(t *testing.T) WorkspaceStateStore {
	t.Helper()
	workspace := t.TempDir()
	state := filepath.Join(workspace, ".anas", "state")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(state, "http-ingress")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	return WorkspaceStateStore{Workspace: workspace, Directory: directory}
}

func fenceLockPath(s WorkspaceStateStore) string {
	return filepath.Join(s.Workspace, ".anas", "state", "lock")
}

func inspectWorkspaceWriter(t *testing.T, s WorkspaceStateStore, busy, dirty bool) {
	t.Helper()
	file, err := os.OpenFile(fenceLockPath(s), os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if busy {
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			t.Fatalf("workspace lifetime lock lost: %v", err)
		}
	} else {
		if err != nil {
			t.Fatal("workspace lock not released", err)
		}
		defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	}
	err = CheckWorkspaceMutationLock(context.Background(), file, fenceLockPath(s))
	if dirty && !errors.Is(err, ErrWorkspaceIngressActive) {
		t.Fatal("retained fence not enforced", err)
	}
	if !dirty && err != nil {
		t.Fatal("confirmed drain still fences mutation", err)
	}
}

func TestWorkspaceFenceRetainsBothLocksUntilSuccessfulServiceDrain(t *testing.T) {
	s := workspaceFenceFixture(t)
	owner, _, _, effects := controllerServiceFixture(t)
	owner.controller.Executor.Store = s
	before, err := os.Lstat(fenceLockPath(s))
	if err != nil {
		t.Fatal(err)
	}
	_, done := startControllerService(t, owner)
	awaitServiceSignal(t, owner.Ready())
	inspectWorkspaceWriter(t, s, true, true)
	// Independent Core observers can still take a read lock during runtime.
	read, err := os.Open(fenceLockPath(s))
	if err != nil {
		t.Fatal(err)
	}
	if err = syscall.Flock(int(read.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	_ = read.Close()
	effects.failRemove.Store(true)
	if err = stopControllerService(t, owner); !errors.Is(err, ErrControllerDrain) {
		t.Fatal(err)
	}
	inspectWorkspaceWriter(t, s, true, true)
	assertControllerLeaseHeld(t, FileStateStore{Directory: s.Directory})
	if effects.closed.Load() != 0 {
		t.Fatal("failed drain released original readers")
	}
	effects.failRemove.Store(false)
	if err = owner.RetryDrain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if owner.Phase() != ControllerStopped {
		t.Fatal("successful workspace release misclassified", owner.Phase())
	}
	inspectWorkspaceWriter(t, s, false, false)
	after, err := os.Lstat(fenceLockPath(s))
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("drain replaced the shared lock", err)
	}
}

func TestWorkspaceFenceRequiresExplicitInventoryEvenWithEmptyJournal(t *testing.T) {
	s := workspaceFenceFixture(t)
	if err := s.WithExclusive(context.Background(), func(Journal) error { return nil }); err != nil {
		t.Fatal(err)
	}
	inspectWorkspaceWriter(t, s, false, true)
	f, e, _ := newExecutorFixture()
	e.Store = s
	f.orphan = true
	if err := e.Recover(context.Background()); err == nil {
		t.Fatal("orphan inventory erased fence")
	}
	inspectWorkspaceWriter(t, s, false, true)
	f.orphan = false
	if err := e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	inspectWorkspaceWriter(t, s, false, false)
}

func TestWorkspaceFenceRejectsMissingSubstitutedOrCorruptRecoveryEvidence(t *testing.T) {
	for _, scenario := range []string{"missing", "corrupt", "symlink", "hardlink", "other-directory", "recreated-directory", "unknown-marker"} {
		t.Run(scenario, func(t *testing.T) {
			s := workspaceFenceFixture(t)
			if err := s.WithExclusive(context.Background(), func(Journal) error { return nil }); err != nil {
				t.Fatal(err)
			}
			marker, err := os.ReadFile(fenceLockPath(s))
			if err != nil {
				t.Fatal(err)
			}
			journal := filepath.Join(s.Directory, executorStateFile)
			switch scenario {
			case "missing":
				if err = os.Remove(journal); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err = os.WriteFile(journal, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err = os.Rename(journal, journal+".old"); err != nil {
					t.Fatal(err)
				}
				if err = os.Symlink(journal+".old", journal); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err = os.Link(journal, journal+".link"); err != nil {
					t.Fatal(err)
				}
			case "other-directory":
				s.Directory = t.TempDir()
			case "recreated-directory":
				if err = os.Rename(s.Directory, s.Directory+".old"); err != nil {
					t.Fatal(err)
				}
				if err = os.Mkdir(s.Directory, 0700); err != nil {
					t.Fatal(err)
				}
				body, err := marshalExecutorState(ExecutorState{Schema: executorStateSchema})
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(journal, body, 0600); err != nil {
					t.Fatal(err)
				}
			case "unknown-marker":
				marker = []byte("unrecognized or partial fence\n")
				if err = os.WriteFile(fenceLockPath(s), marker, 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, e, _ := newExecutorFixture()
			e.Store = s
			if err = e.Recover(context.Background()); err == nil {
				t.Fatal("recovery manufactured clean state")
			}
			after, err := os.ReadFile(fenceLockPath(s))
			if err != nil || !bytes.Equal(after, marker) {
				t.Fatal("failed recovery destroyed marker", err)
			}
			if scenario == "missing" {
				if _, err = os.Lstat(journal); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("missing journal recreated", err)
				}
			}
		})
	}
}

func TestWorkspaceFenceDetectsLockAndAncestorReplacement(t *testing.T) {
	for _, scenario := range []string{"lock", "state-directory", "hardlink", "marker", "removed-journal"} {
		t.Run(scenario, func(t *testing.T) {
			s := workspaceFenceFixture(t)
			err := s.WithExclusive(context.Background(), func(j Journal) error {
				path := fenceLockPath(s)
				body, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				switch scenario {
				case "lock":
					if err = os.Rename(path, path+".old"); err != nil {
						return err
					}
					err = os.WriteFile(path, body, 0600)
				case "state-directory":
					dir := filepath.Dir(path)
					if err = os.Rename(dir, dir+".old"); err != nil {
						return err
					}
					err = os.Mkdir(dir, 0700)
				case "hardlink":
					err = os.Link(path, path+".link")
				case "marker":
					err = os.WriteFile(path, []byte("tampered"), 0600)
				case "removed-journal":
					err = os.Remove(filepath.Join(s.Directory, executorStateFile))
				}
				if err != nil {
					return err
				}
				if err = j.Check(context.Background()); err == nil {
					t.Fatal("changed session identity accepted")
				}
				if _, loadErr := j.Load(context.Background()); loadErr == nil {
					t.Fatal("lost evidence became an empty journal")
				}
				return err
			})
			if err == nil {
				t.Fatal("invalid fence did not fail closed")
			}
		})
	}
}

func TestWorkspaceFenceDoesNotCreateMissingInstallationOrTouchCanceledStart(t *testing.T) {
	s := WorkspaceStateStore{Workspace: t.TempDir(), Directory: t.TempDir()}
	if err := s.WithExclusive(context.Background(), func(Journal) error { t.Fatal("missing workspace admitted"); return nil }); err == nil {
		t.Fatal("missing workspace accepted")
	}
	if _, err := os.Lstat(filepath.Join(s.Workspace, ".anas")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("implicit installation", err)
	}
	s = workspaceFenceFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.WithExclusive(ctx, func(Journal) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	inspectWorkspaceWriter(t, s, false, false)
	if _, err := os.Lstat(filepath.Join(s.Directory, executorStateFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled launch created journal", err)
	}
}

func TestWorkspaceFenceReaderReleaseFailureCannotAuthorizeMutation(t *testing.T) {
	s := workspaceFenceFixture(t)
	owner, _, _, effects := controllerServiceFixture(t)
	owner.controller.Executor.Store = s
	effects.closeError = true
	_, done := startControllerService(t, owner)
	awaitServiceSignal(t, owner.Ready())
	if err := stopControllerService(t, owner); !errors.Is(err, ErrControllerRelease) {
		t.Fatal(err)
	}
	<-done
	inspectWorkspaceWriter(t, s, false, true)
}

// The child only operates on parent-created temporary directories. It records
// synthetic effects; no daemon, root command or actual packet is involved.
func TestWorkspaceFenceSubprocessPublisher(t *testing.T) {
	if os.Getenv("ANAS_TEST_FENCE_CHILD") != "1" {
		return
	}
	s := WorkspaceStateStore{Workspace: os.Getenv("ANAS_TEST_FENCE_WORKSPACE"), Directory: os.Getenv("ANAS_TEST_FENCE_DIRECTORY")}
	err := s.WithExclusive(context.Background(), func(j Journal) error {
		_, _, target := newExecutorFixture()
		state, err := j.Load(context.Background())
		if err != nil {
			return err
		}
		state.Publications = []AppliedPublication{{Target: target, AddressHeld: true, GuestRouteReady: true, PermitReady: true, RoutePublished: true}}
		if err = j.Save(context.Background(), state); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "FENCE_READY")
		var b [1]byte
		_, err = os.Stdin.Read(b[:])
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceFenceSurvivesPublisherProcessDeathAndRecoversOriginalJournal(t *testing.T) {
	s := workspaceFenceFixture(t)
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-test.run=^TestWorkspaceFenceSubprocessPublisher$")
	cmd.Env = append(os.Environ(), "ANAS_TEST_FENCE_CHILD=1", "ANAS_TEST_FENCE_WORKSPACE="+s.Workspace, "ANAS_TEST_FENCE_DIRECTORY="+s.Directory)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "FENCE_READY" {
		t.Fatal("child did not establish durable fence", err, line)
	}
	inspectWorkspaceWriter(t, s, true, true)
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err == nil {
		t.Fatal("test did not terminate the publisher process")
	}
	// Kernel exclusion disappeared, but a completely independent writer still
	// sees the durable marker and cannot mutate the old cleanup dependencies.
	inspectWorkspaceWriter(t, s, false, true)
	f, e, _ := newExecutorFixture()
	e.Store = s
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal("original recovery", err, stderr.String())
	}
	if !reflect.DeepEqual(f.steps, closingSteps) {
		t.Fatal("did not retire original recorded target", f.steps)
	}
	inspectWorkspaceWriter(t, s, false, false)
	if err = (FileStateStore{Directory: s.Directory}).WithExclusive(context.Background(), func(j Journal) error {
		state, err := j.Load(context.Background())
		if err != nil {
			return err
		}
		if len(state.Publications) != 0 || len(state.Retired) != 1 {
			t.Fatal("retirement history lost")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
