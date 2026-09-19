//go:build linux

package incusingresshost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestNativeNamespaceKernelIdentityAndCookie(t *testing.T) {
	file, err := openKernelNetworkNamespace(strconv.Itoa(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	boot, err := readNamespaceProc("/proc/sys/kernel/random/boot_id", 64)
	if err != nil {
		t.Fatal(err)
	}
	body, err := readNamespaceProc("/proc/"+strconv.Itoa(os.Getpid())+"/stat", 8192)
	if err != nil {
		t.Fatal(err)
	}
	ticks, err := processStartTicks(body, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		t.Fatal(err)
	}
	cookie, err := networkNamespaceCookie()
	if err != nil {
		t.Fatal(err)
	}
	pin := RouteNamespacePin{PID: os.Getpid(), BootID: strings.TrimSpace(string(boot)), StartTimeTicks: ticks, NetNSDevice: uint64(stat.Dev), NetNSInode: stat.Ino, NetNSCookie: cookie}
	if err := verifyNamespaceProcess(pin); err != nil {
		t.Fatal(err)
	}
	if err := verifyProcessNamespace(pin); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := inOpenedNetworkNamespace(context.Background(), file, cookie, func() error { called = true; return nil }); err != nil || !called {
		t.Fatalf("same namespace execution failed: %v", err)
	}
	called = false
	if err := inOpenedNetworkNamespace(context.Background(), file, cookie+1, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("wrong namespace cookie authorized an effect")
	}
	pin.StartTimeTicks++
	if err := verifyNamespaceProcess(pin); err == nil {
		t.Fatal("changed process start identity accepted")
	}
}

func TestNativeCommandPinsExecutableDescriptor(t *testing.T) {
	path, err := filepath.EvalSymlinks("/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	command, file, err := prepareCommandPath(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if command != "/proc/self/fd/3" || file == nil {
		t.Fatal("executable was not mapped to an explicit inherited descriptor")
	}
	runner := commandRunner{config: backendConfig{CommandTimeout: time.Second}}
	if err := runner.run(context.Background(), path, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestNativeNamespaceSwitchRestoresOriginal(t *testing.T) {
	type created struct {
		file   *os.File
		cookie uint64
		err    error
	}
	done := make(chan created, 1)
	go func() {
		runtime.LockOSThread()
		if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
			runtime.UnlockOSThread()
			done <- created{err: err}
			return
		}
		file, err := openKernelNetworkNamespace("thread-self")
		if err != nil {
			done <- created{err: err}
			return
		}
		cookie, err := networkNamespaceCookie()
		done <- created{file: file, cookie: cookie, err: err}
		// The isolated thread exits locked. Do not contaminate the scheduler.
	}()
	namespace := <-done
	if namespace.file != nil {
		defer namespace.file.Close()
	}
	if namespace.err != nil {
		if os.Getenv("ANAS_REQUIRE_INGRESS_NATIVE") == "1" || (!errors.Is(namespace.err, unix.EPERM) && !errors.Is(namespace.err, unix.EACCES)) {
			t.Fatal(namespace.err)
		}
		t.Skip("requires isolated Linux CAP_SYS_ADMIN; native ingress gate makes this a failure")
	}
	before, err := networkNamespaceCookie()
	if err != nil || before == namespace.cookie {
		t.Fatalf("namespace was not isolated: %v", err)
	}
	for _, mode := range []string{"success", "error", "panic", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			called := false
			err := inOpenedNetworkNamespace(ctx, namespace.file, namespace.cookie, func() error {
				observed, err := networkNamespaceCookie()
				if err != nil || observed != namespace.cookie {
					return errors.New("effect ran outside the opened namespace")
				}
				called = true
				switch mode {
				case "error":
					return errors.New("injected effect failure")
				case "panic":
					panic("injected effect panic")
				case "cancel":
					cancel()
				}
				return nil
			})
			if !called || (mode == "success" && err != nil) || (mode != "success" && err == nil) {
				t.Fatalf("unexpected effect result: called=%v error=%v", called, err)
			}
			after, err := networkNamespaceCookie()
			if err != nil || after != before {
				t.Fatalf("caller namespace was changed: %v", err)
			}
		})
	}
}
