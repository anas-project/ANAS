package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const collaboraTempRoot = "/var/lib/anas-collabora"
const collaboraTemplateVersion = "26.04.2.4.1"

type collaboraProcessOps struct {
	setgroups func([]int) error
	setgid    func(int) error
	setuid    func(int) error
	exec      func(string, []string, []string) error
}

func systemCollaboraProcessOps() collaboraProcessOps {
	return collaboraProcessOps{syscall.Setgroups, syscall.Setgid, syscall.Setuid, syscall.Exec}
}

// The frozen hook also supplies the container entry point. This image has no
// shell or copy utility, so initialization must not depend on either of them.
func startContainer(originalArgs []string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("Collabora temporary-tree initialization requires container uid 0")
	}
	if err := prepareContainerTrees(collaboraTempRoot, "/opt/cool/systemplate", "/opt/collaboraoffice"); err != nil {
		return err
	}
	args := append([]string{"/usr/bin/coolwsd"}, originalArgs...)
	args = append(args,
		"--lo-template-path="+collaboraTempRoot+"/office",
		"--o:sys_template_path="+collaboraTempRoot+"/systemplate",
		"--o:child_root_path="+collaboraTempRoot+"/child-roots",
		"--o:cache_files.path="+collaboraTempRoot+"/cache",
		"--o:mount_jail_tree=false")
	return execCollabora(args, os.Environ(), systemCollaboraProcessOps())
}

func probeContainer() error {
	return probeContainerWithOps(systemCollaboraProcessOps())
}

func probeContainerWithOps(ops collaboraProcessOps) error {
	// A health check must not initialize templates or clear an active jail/cache.
	return execCollabora([]string{"/usr/bin/coolwsd", "--probe"}, os.Environ(), ops)
}

func execCollabora(args, env []string, ops collaboraProcessOps) error {
	if err := ops.setgroups([]int{}); err != nil {
		return fmt.Errorf("clear Collabora supplementary groups: %w", err)
	}
	if err := ops.setgid(1001); err != nil {
		return fmt.Errorf("drop Collabora gid: %w", err)
	}
	if err := ops.setuid(1001); err != nil {
		return fmt.Errorf("drop Collabora uid: %w", err)
	}
	return ops.exec(args[0], args, env)
}

func prepareContainerTrees(root, systemSource, officeSource string) error {
	return prepareContainerTreesForUser(root, systemSource, officeSource, 1001, 1001)
}

func prepareContainerTreesForUser(root, systemSource, officeSource string, uid, gid int) error {
	if err := realDirectory(root); err != nil {
		return fmt.Errorf("managed Collabora mount: %w", err)
	}
	marker := filepath.Join(root, ".templates-version")
	if info, err := os.Lstat(marker); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("invalid Collabora template marker")
	}
	current, err := os.ReadFile(marker)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.TrimSpace(string(current)) != collaboraTemplateVersion {
		for _, tree := range []struct{ name, source string }{{"systemplate", systemSource}, {"office", officeSource}} {
			if err := realDirectory(tree.source); err != nil {
				return fmt.Errorf("Collabora %s source: %w", tree.name, err)
			}
			stage := filepath.Join(root, "."+tree.name+".init")
			if err := os.RemoveAll(stage); err != nil {
				return err
			}
			if err := copyTemplateTree(tree.source, stage); err != nil {
				return fmt.Errorf("initialize Collabora %s: %w", tree.name, err)
			}
			dest := filepath.Join(root, tree.name)
			if err := os.RemoveAll(dest); err != nil {
				return err
			}
			if err := os.Rename(stage, dest); err != nil {
				return err
			}
		}
		if err := os.WriteFile(marker, []byte(collaboraTemplateVersion+"\n"), 0600); err != nil {
			return err
		}
	}
	for _, name := range []string{"systemplate", "office"} {
		if err := realDirectory(filepath.Join(root, name)); err != nil {
			return fmt.Errorf("Collabora %s template: %w", name, err)
		}
	}
	for _, name := range []string{"child-roots", "cache"} {
		dir := filepath.Join(root, name)
		// Only this instance's disposable child/cache directories are cleared.
		// RemoveAll unlinks a symlink instead of following it.
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
		if err := os.Mkdir(dir, 0755); err != nil {
			return err
		}
		if err := os.Chown(dir, uid, gid); err != nil {
			return err
		}
	}
	return nil
}

func realDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a real directory", path)
	}
	return nil
}

type templateInode struct{ dev, ino uint64 }

// Copy only regular files, directories, and links. Preserve owners, modes and
// hardlinks without following links in the source. Unexpected special files
// reject initialization instead of silently omitting required runtime content.
func copyTemplateTree(source, dest string) error {
	links := make(map[templateInode]string)
	return copyTemplateEntry(source, dest, links)
}

func copyTemplateEntry(source, dest string, links map[templateInode]string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot determine owner of %s", source)
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(source)
		if err != nil {
			return err
		}
		if err := os.Symlink(target, dest); err != nil {
			return err
		}
		return os.Lchown(dest, int(stat.Uid), int(stat.Gid))
	case info.IsDir():
		if err := os.Mkdir(dest, 0700); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyTemplateEntry(filepath.Join(source, entry.Name()), filepath.Join(dest, entry.Name()), links); err != nil {
				return err
			}
		}
	case info.Mode().IsRegular():
		key := templateInode{uint64(stat.Dev), uint64(stat.Ino)}
		if first := links[key]; first != "" {
			return os.Link(first, dest)
		}
		in, err := os.Open(source)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		links[key] = dest
	default:
		return fmt.Errorf("unsupported special file %s", source)
	}
	if err := os.Chown(dest, int(stat.Uid), int(stat.Gid)); err != nil {
		return err
	}
	return os.Chmod(dest, info.Mode())
}
