package main

// TEST_CASES: TEMP-T-021, TEMP-T-022

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func TestCollaboraContainerEntryDispatch(t *testing.T) {
	sentinel := errors.New("entry failed")
	for _, tc := range []struct {
		name    string
		args    []string
		handled bool
		call    string
		wantErr bool
	}{
		{"native probe", []string{"--container-probe"}, true, "probe", true},
		{"probe command rejected", []string{"--container-probe", "/bin/sh"}, true, "", true},
		{"probe options rejected", []string{"--container-probe", "--o:child_root_path=/other"}, true, "", true},
		{"start forwards options", []string{"--container-start", "--use-env-vars", "--o:logging.color=false"}, true, "start", true},
		{"hook stdin entry", nil, false, "", false},
		{"unknown entry", []string{"--other"}, false, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := ""
			handled, err := runContainerEntry(tc.args, func(args []string) error {
				called = "start"
				if !reflect.DeepEqual(args, tc.args[1:]) {
					t.Fatalf("startup arguments changed: %v", args)
				}
				return sentinel
			}, func() error {
				called = "probe"
				return sentinel
			})
			if handled != tc.handled || called != tc.call || (err != nil) != tc.wantErr {
				t.Fatalf("handled=%v called=%q err=%v", handled, called, err)
			}
			if tc.call != "" && err != sentinel {
				t.Fatal("container entry replaced the underlying failure")
			}
		})
	}
}

func TestCollaboraProbeExecutesOnlyNativeProbeWithoutStartup(t *testing.T) {
	var calls []string
	ops := collaboraProcessOps{
		setgroups: func(groups []int) error {
			if len(groups) != 0 {
				t.Fatalf("supplementary groups retained: %v", groups)
			}
			calls = append(calls, "groups")
			return nil
		},
		setgid: func(gid int) error {
			if gid != 1001 {
				t.Fatalf("gid=%d", gid)
			}
			calls = append(calls, "gid")
			return nil
		},
		setuid: func(uid int) error {
			if uid != 1001 {
				t.Fatalf("uid=%d", uid)
			}
			calls = append(calls, "uid")
			return nil
		},
		exec: func(path string, args, env []string) error {
			if path != "/usr/bin/coolwsd" || !reflect.DeepEqual(args, []string{path, "--probe"}) {
				t.Fatalf("probe command changed: %q %v", path, args)
			}
			if !reflect.DeepEqual(env, os.Environ()) {
				t.Fatal("probe environment changed")
			}
			calls = append(calls, "exec")
			return nil
		},
	}
	handled, err := runContainerEntry([]string{"--container-probe"}, func([]string) error {
		t.Fatal("probe entered startup template/child/cache initialization")
		return nil
	}, func() error { return probeContainerWithOps(ops) })
	if !handled || err != nil || !reflect.DeepEqual(calls, []string{"groups", "gid", "uid", "exec"}) {
		t.Fatalf("handled=%v err=%v calls=%v", handled, err, calls)
	}
}

func TestCollaboraPrivilegeDropFailuresPreventExec(t *testing.T) {
	for index, phase := range []string{"groups", "gid", "uid"} {
		t.Run(phase, func(t *testing.T) {
			sentinel := errors.New("permission denied")
			var calls []string
			step := func(name string) error {
				calls = append(calls, name)
				if name == phase {
					return sentinel
				}
				return nil
			}
			err := probeContainerWithOps(collaboraProcessOps{
				setgroups: func([]int) error { return step("groups") },
				setgid:    func(int) error { return step("gid") },
				setuid:    func(int) error { return step("uid") },
				exec: func(string, []string, []string) error {
					t.Fatal("exec occurred after failed privilege drop")
					return nil
				},
			})
			wantCalls := []string{"groups", "gid", "uid"}[:index+1]
			if !errors.Is(err, sentinel) || !reflect.DeepEqual(calls, wantCalls) {
				t.Fatalf("err=%v calls=%v", err, calls)
			}
			if !strings.Contains(err.Error(), map[string]string{"groups": "supplementary groups", "gid": "gid", "uid": "uid"}[phase]) {
				t.Fatalf("missing failure phase: %v", err)
			}
		})
	}
}

func TestCollaboraSharedExecPreservesStartupArgumentsAndExecFailure(t *testing.T) {
	args := []string{"/usr/bin/coolwsd", "--use-env-vars", "--lo-template-path=" + collaboraTempRoot + "/office"}
	env := []string{"SYNTHETIC=value"}
	sentinel := errors.New("native exec failed")
	err := execCollabora(args, env, collaboraProcessOps{
		setgroups: func([]int) error { return nil },
		setgid:    func(int) error { return nil },
		setuid:    func(int) error { return nil },
		exec: func(path string, gotArgs, gotEnv []string) error {
			if path != args[0] || !reflect.DeepEqual(args, gotArgs) || !reflect.DeepEqual(env, gotEnv) {
				t.Fatal("shared exec changed startup command/environment")
			}
			return sentinel
		},
	})
	if err != sentinel {
		t.Fatalf("native exec failure replaced: %v", err)
	}
}

func TestCollaboraTemplateCopyPreservesContentModeOwnerAndLinks(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	if err := os.Mkdir(source, 0751); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("synthetic-office-content"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(source, "file"), filepath.Join(source, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside")
	if err := os.WriteFile(outside, []byte("do-not-copy-through-link"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(source, "outside-link")); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(base, "dest")
	if err := copyTemplateTree(source, dest); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(dest, "file"))
	if err != nil || string(content) != "synthetic-office-content" {
		t.Fatalf("copied content: %q, %v", content, err)
	}
	for _, name := range []string{".", "file"} {
		a, err := os.Lstat(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.Lstat(filepath.Join(dest, name))
		if err != nil {
			t.Fatal(err)
		}
		if a.Mode() != b.Mode() {
			t.Fatalf("%s mode changed: %v != %v", name, a.Mode(), b.Mode())
		}
		as, bs := a.Sys().(*syscall.Stat_t), b.Sys().(*syscall.Stat_t)
		if as.Uid != bs.Uid || as.Gid != bs.Gid {
			t.Fatalf("%s owner changed", name)
		}
	}
	a, _ := os.Stat(filepath.Join(dest, "file"))
	b, _ := os.Stat(filepath.Join(dest, "hardlink"))
	if !os.SameFile(a, b) {
		t.Fatal("hardlink was expanded into an independent copy")
	}
	link, err := os.Readlink(filepath.Join(dest, "outside-link"))
	if err != nil || link != outside {
		t.Fatalf("source symlink followed instead of copied: %q, %v", link, err)
	}
}

func TestCollaboraTemplateInitializationReusesOnlyCompleteTemplates(t *testing.T) {
	base := t.TempDir()
	root, system, office := filepath.Join(base, "root"), filepath.Join(base, "system"), filepath.Join(base, "office")
	for _, dir := range []string{root, system, office} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(office, "program"), []byte("office"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(system, "lib"), []byte("system"), 0644); err != nil {
		t.Fatal(err)
	}
	prepare := func() error { return prepareContainerTreesForUser(root, system, office, os.Getuid(), os.Getgid()) }
	if err := prepare(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "child-roots", "stale"), []byte("disposable"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(office, "program"), []byte("source changed"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := prepare(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, "office", "program"))
	if err != nil || string(content) != "office" {
		t.Fatal("completed same-image templates unexpectedly replaced")
	}
	if _, err := os.Stat(filepath.Join(root, "child-roots", "stale")); !os.IsNotExist(err) {
		t.Fatal("startup did not clear disposable old child tree")
	}
	if err := os.RemoveAll(filepath.Join(root, "office")); err != nil {
		t.Fatal(err)
	}
	if err := prepare(); err == nil {
		t.Fatal("incomplete completed templates accepted")
	}
}

func TestCollaboraTemplateRejectsSpecialFilesAndSymlinkRoot(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	if err := os.Mkdir(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(source, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyTemplateTree(source, filepath.Join(base, "dest")); err == nil {
		t.Fatal("special file silently omitted")
	}
	if err := os.Symlink(source, filepath.Join(base, "root-link")); err != nil {
		t.Fatal(err)
	}
	if err := prepareContainerTreesForUser(filepath.Join(base, "root-link"), source, source, os.Getuid(), os.Getgid()); err == nil {
		t.Fatal("symlink managed mount accepted")
	}
}
