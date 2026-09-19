//go:build linux

package hostaction

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func requireBrokerPIDFD(t *testing.T) {
	t.Helper()
	client, _ := brokerTestPair(t)
	raw, err := client.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	fd := -1
	var optionErr error
	err = raw.Control(func(socket uintptr) {
		fd, optionErr = unix.GetsockoptInt(int(socket), unix.SOL_SOCKET, unix.SO_PEERPIDFD)
	})
	if fd >= 0 {
		_ = unix.Close(fd)
	}
	if errors.Is(optionErr, unix.ENOPROTOOPT) {
		if os.Getenv("ANAS_REQUIRE_HOST_BROKER_NATIVE") == "1" {
			t.Fatal("SO_PEERPIDFD is required by the native broker gate")
		}
		t.Skip("kernel lacks SO_PEERPIDFD; production broker fails closed")
	}
	if err != nil || optionErr != nil {
		t.Fatal(err, optionErr)
	}
}

func TestBrokerNativeChild(t *testing.T) {
	if os.Getenv("ANAS_BROKER_CHILD_FIXTURE") != "1" {
		return
	}
	args := os.Args[len(os.Args)-4:]
	pid, err1 := strconv.Atoi(args[1])
	uid, err2 := strconv.ParseUint(args[2], 10, 32)
	gid, err3 := strconv.ParseUint(args[3], 10, 32)
	if err1 != nil || err2 != nil || err3 != nil {
		os.Exit(71)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connection, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", args[0])
	if err != nil {
		os.Exit(72)
	}
	original := PeerIdentity{int32(pid), uint32(uid), uint32(gid)}
	remote, err := remoteBrokerOnConnection(connection.(*net.UnixConn), original)
	if err != nil {
		os.Exit(73)
	}
	if err := remote.WithHostInvocation(ctx, testRequest(), installedRelease(), original, func(context.Context) error { return nil }); err != nil {
		os.Exit(74)
	}
	if remote.Close() != nil {
		os.Exit(75)
	}
	ready := os.NewFile(3, "ready")
	if _, err := ready.Write([]byte("bound\n")); err != nil {
		os.Exit(76)
	}
	_ = ready.Close()
	// Stay alive AFTER the handshake and all broker sockets close. The owner
	// must retain execution ownership until this process, not its stream, exits.
	exitGate := os.NewFile(4, "exit-gate")
	var one [1]byte
	if _, err := exitGate.Read(one[:]); err != io.EOF {
		os.Exit(77)
	}
	_ = exitGate.Close()
	os.Exit(0)
}

func TestBrokerNativeProcessPinSurvivesHandshakeAndReaping(t *testing.T) {
	if os.Getuid() == 0 || os.Getgid() == 0 {
		if os.Getenv("ANAS_REQUIRE_HOST_BROKER_NATIVE") == "1" {
			t.Fatal("native broker fixture requires a non-root owner")
		}
		t.Skip("positive broker fixture owner must be non-root")
	}
	requireBrokerPIDFD(t)
	dir, err := os.MkdirTemp("", "anas-broker-child-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "s"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ready, readyChild, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer ready.Close()
	defer readyChild.Close()
	gateChild, gate, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	defer gateChild.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBrokerNativeChild$", "--", listener.Addr().String(), strconv.Itoa(os.Getpid()), strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid()))
	cmd.Env = []string{"ANAS_BROKER_CHILD_FIXTURE=1"}
	cmd.ExtraFiles = []*os.File{readyChild, gateChild}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = gate.Close()
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	_ = readyChild.Close()
	_ = gateChild.Close()
	if err := listener.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	connection, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	self := PeerIdentity{int32(os.Getpid()), uint32(os.Getuid()), uint32(os.Getgid())}
	session, err := acceptJobBrokerAs(connection, self, self.UID, self.GID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = gate.Close()
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		_ = session.Close()
	}()
	if session.peer.pid != int32(cmd.Process.Pid) {
		t.Fatal("not the actual child peer")
	}
	if err := session.Serve(ctx, installedRelease(), brokerRunBinding(), &memoryAudit{}); err != nil {
		t.Fatal(err)
	}
	if err := ready.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(ready)
	if err != nil || string(body) != "bound\n" {
		t.Fatal("child handshake failed", err)
	}
	if err := session.Close(); !errors.Is(err, ErrBrokerExecutorRunning) {
		t.Fatal("socket completion released a live root peer", err)
	}
	short, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	err = session.WaitExecutor(short)
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("live peer mistaken for exited", err)
	}
	_ = gate.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal("fixture exit", err)
	}
	if err := session.WaitExecutor(ctx); err != nil {
		t.Fatal("pidfd lost identity after reaping", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBrokerKernelIdentityCannotBeOverridden(t *testing.T) {
	requireBrokerPIDFD(t)
	client, server := brokerTestPair(t)
	peer, watch, err := socketBrokerProcess(client)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.close()
	if peer.pid != int32(os.Getpid()) || peer.uid != uint32(os.Geteuid()) || peer.gid != uint32(os.Getegid()) {
		t.Fatal("wrong kernel identity")
	}
	_, err = remoteBrokerOnConnection(client, PeerIdentity{PID: int32(os.Getpid()) + 10000, UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())})
	if err == nil {
		t.Fatal("claimed peer PID accepted")
	}
	if s, err := AcceptJobBroker(server); err == nil {
		_ = s.Close()
		t.Fatal("same-user or root-owned broker admitted itself")
	}
}

func TestBrokerEndpointPinsPrivateDirectoryAndSocket(t *testing.T) {
	for _, mutation := range []string{"none", "group writable parent", "shared broker directory", "socket permissions", "socket replaced", "directory replaced", "socket symlink"} {
		t.Run(mutation, func(t *testing.T) {
			root, err := os.MkdirTemp("", "anas-broker-endpoint-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(root)
			dir := filepath.Join(root, "run", "anas-job-broker")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "socket")
			l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			if err := os.Chmod(path, 0600); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			pin, err := openBrokerEndpoint(int(f.Fd()), uint32(os.Geteuid()), uint32(os.Geteuid()), uint32(os.Getegid()))
			if err != nil {
				t.Fatal(err)
			}
			defer pin.close()
			switch mutation {
			case "group writable parent":
				err = os.Chmod(filepath.Join(root, "run"), 0770)
			case "shared broker directory":
				err = os.Chmod(dir, 0755)
			case "socket permissions":
				err = os.Chmod(path, 0660)
			case "socket replaced":
				err = os.Rename(path, path+".old")
			case "directory replaced":
				err = os.Rename(dir, dir+".old")
			case "socket symlink":
				if err = os.Rename(path, path+".old"); err == nil {
					err = os.Symlink(path+".old", path)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := pin.check(); (err == nil) != (mutation == "none") {
				t.Fatal(fmt.Sprintf("mutation %s: %v", mutation, err))
			}
		})
	}
}
