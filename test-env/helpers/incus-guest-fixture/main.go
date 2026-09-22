//go:build linux

// incus-guest-fixture is a tiny native container fixture, not an ANAS release
// image or a Forgejo runner. It exposes only the bounded checks below.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	if os.Getpid() == 1 {
		// Incus prepares proc/dev/sys. Keep fixture markers on tmpfs, never on
		// the quota-tested root disk, and reap orphaned exec children.
		if err := syscall.Mount("tmpfs", "/run", "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV, "mode=0755,size=8m"); err != nil {
			os.Exit(90)
		}
		signals := make(chan os.Signal, 4)
		signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGCHLD)
		for sig := range signals {
			if sig != syscall.SIGCHLD {
				os.Exit(0)
			}
			for {
				var s syscall.WaitStatus
				pid, _ := syscall.Wait4(-1, &s, syscall.WNOHANG, nil)
				if pid <= 0 {
					break
				}
			}
		}
	}
	if filepath.Base(os.Args[0]) == "test" {
		if len(os.Args) != 3 || os.Args[1] != "-x" {
			os.Exit(64)
		}
		info, err := os.Stat(os.Args[2])
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			os.Exit(1)
		}
		return
	}
	if err := run(os.Args[1:]); err != nil {
		// Never echo the supplied digest, stdin, pathname or raw OS error.
		fmt.Fprintln(os.Stderr, "native fixture check failed")
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 1 && args[0] == "holding" {
		_, err := os.Stat("/run/anas-fixture-holding")
		return err
	}
	if len(args) == 1 && args[0] == "quota" {
		return checkQuota()
	}
	if len(args) != 2 || (args[0] != "probe" && args[0] != "hold") {
		return errors.New("invalid fixture command")
	}
	expected, err := hex.DecodeString(args[1])
	if err != nil || len(expected) != 32 {
		return errors.New("invalid digest")
	}
	body, err := io.ReadAll(io.LimitReader(os.Stdin, 4097))
	defer clear(body)
	if err != nil || len(body) < 32 || len(body) > 4096 {
		return errors.New("invalid stream")
	}
	digest := sha256.Sum256(body)
	if hex.EncodeToString(digest[:]) != args[1] {
		return errors.New("stream mismatch")
	}
	if args[0] == "hold" {
		if err := os.WriteFile("/run/anas-fixture-holding", []byte("ready\n"), 0600); err != nil {
			return err
		}
		time.Sleep(5 * time.Minute)
	}
	return nil
}

func checkQuota() error {
	const limit = int64(4 << 30)
	file, err := os.OpenFile("/quota-check", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer os.Remove("/quota-check")
	defer file.Close()
	block := make([]byte, 1<<20)
	var written int64
	for written <= limit {
		n, writeErr := file.Write(block)
		written += int64(n)
		if writeErr == nil && written%(64<<20) == 0 {
			writeErr = file.Sync()
		}
		if writeErr != nil {
			if written < 16<<20 || written > limit || (!errors.Is(writeErr, syscall.ENOSPC) && !errors.Is(writeErr, syscall.EDQUOT)) {
				return errors.New("unexpected write boundary")
			}
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"disk_limit_bytes": limit, "bytes_written": written, "write_limit_enforced": true})
		}
	}
	return errors.New("quota not enforced")
}
