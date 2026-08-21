package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

const (
	vikunjaBinary = "/app/vikunja/vikunja"
	vikunjaFiles  = "/app/vikunja/files"
	vikunjaUID    = 1000
	vikunjaGID    = 1000
)

func main() {
	// Docker healthchecks start with the image's configured root user. They do
	// not need to touch the attachment tree, so drop immediately and keep the
	// recurring database/API probe unprivileged as well.
	if len(os.Args) < 2 || os.Args[1] != "healthcheck" {
		if err := prepareFiles(vikunjaFiles, vikunjaUID, vikunjaGID); err != nil {
			fatal(err)
		}
	}
	if err := dropPrivileges(vikunjaUID, vikunjaGID); err != nil {
		fatal(err)
	}

	argv := append([]string{vikunjaBinary}, os.Args[1:]...)
	if err := syscall.Exec(vikunjaBinary, argv, os.Environ()); err != nil {
		fatal(fmt.Errorf("exec Vikunja: %w", err))
	}
}

func prepareFiles(root string, uid, gid int) error {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return fmt.Errorf("create attachment directory: %w", err)
	}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// WalkDir never follows a symlink. Lchown also avoids following one if a
		// restored attachment tree contains an application-created symlink.
		if err := os.Lchown(path, uid, gid); err != nil {
			return fmt.Errorf("set attachment ownership for %s: %w", path, err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("prepare attachment ownership: %w", err)
	}
	return nil
}

func dropPrivileges(uid, gid int) error {
	if err := syscall.Setgroups([]int{gid}); err != nil {
		return fmt.Errorf("set supplementary groups: %w", err)
	}
	if err := syscall.Setgid(gid); err != nil {
		return fmt.Errorf("set gid: %w", err)
	}
	if err := syscall.Setuid(uid); err != nil {
		return fmt.Errorf("set uid: %w", err)
	}
	syscall.Umask(0o027)
	return nil
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "anas-vikunja: %v\n", err)
	os.Exit(1)
}
