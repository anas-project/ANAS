//go:build linux || darwin

package computeingressruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/anas-project/ANAS/internal/securefs"
)

const workspaceFenceSchema = "anas.compute-http-workspace-fence/v1"

type workspaceFence struct {
	path     string
	roots    []*os.Root
	infos    []os.FileInfo
	lock     *os.File
	expected []byte
	active   bool
}

func (s WorkspaceStateStore) WithExclusive(ctx context.Context, run func(Journal) error) (result error) {
	if ctx == nil || run == nil || !filepath.IsAbs(s.Workspace) || filepath.Clean(s.Workspace) != s.Workspace ||
		!filepath.IsAbs(s.Directory) || filepath.Clean(s.Directory) != s.Directory {
		return ErrWorkspaceIngressState
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.layout != nil {
		if err := s.layout.check(ctx); err != nil {
			return err
		}
	}
	fence, err := openWorkspaceFence(s.Workspace)
	if err != nil {
		return err
	}
	defer func() {
		if err := fence.close(); err != nil {
			result = errors.Join(result, err)
		}
	}()
	// Establish the durable marker while holding the same exclusive lock as
	// writers, then downgrade. A writer winning the conversion window sees
	// the synced marker and refuses; no publisher effect has happened yet.
	locked, err := securefs.TryLock(fence.lock)
	if err != nil {
		return ErrWorkspaceIngressState
	}
	if !locked {
		return ErrWorkspaceIngressBusy
	}
	before, err := fence.read(ctx)
	if err != nil {
		return err
	}
	return (FileStateStore{Directory: s.Directory}).WithExclusive(ctx, func(j Journal) error {
		// No Core lock is reacquired here. This checks only captured identities
		// and bytes before initializing a journal or writing the lifetime marker.
		if s.layout != nil {
			if err := s.layout.check(ctx); err != nil {
				return err
			}
		}
		files, ok := j.(*fileJournal)
		if !ok {
			return ErrWorkspaceIngressState
		}
		directory, err := files.root.Stat(".")
		if err != nil {
			return ErrWorkspaceIngressState
		}
		stat, ok := directory.Sys().(*syscall.Stat_t)
		if !ok {
			return ErrWorkspaceIngressState
		}
		digest := sha256.Sum256([]byte(s.Directory))
		marker, err := json.Marshal(struct {
			Schema            string `json:"schema"`
			DirectoryDigest   string `json:"state_directory_digest"`
			DirectoryIdentity string `json:"state_directory_identity"`
		}{workspaceFenceSchema, hex.EncodeToString(digest[:]), strconv.FormatUint(uint64(stat.Dev), 16) + ":" + strconv.FormatUint(uint64(stat.Ino), 16)})
		if err != nil {
			return ErrWorkspaceIngressState
		}
		marker = append(marker, '\n')
		if len(before) != 0 && !bytes.Equal(before, marker) {
			return ErrWorkspaceIngressState
		}
		_, statErr := files.root.Lstat(executorStateFile)
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return ErrWorkspaceIngressState
		}
		// A retained marker must never be paired with a newly invented empty
		// journal. A missing/invalid old journal requires offline evidence.
		if len(before) != 0 && statErr != nil {
			return ErrWorkspaceIngressState
		}
		state, err := j.Load(ctx)
		if err != nil {
			return err
		}
		if statErr != nil {
			if err := j.Save(ctx, state); err != nil {
				return err
			}
		}
		if len(before) == 0 {
			n, err := fence.lock.WriteAt(marker, 0)
			if err != nil || n != len(marker) || fence.lock.Sync() != nil {
				return ErrWorkspaceIngressState
			}
		}
		// The file may have been provisioned immediately before this start.
		// Sync existing directory entries as well as its marker before effects.
		for i := len(fence.roots) - 1; i >= 0; i-- {
			if syncRoot(fence.roots[i]) != nil {
				return ErrWorkspaceIngressState
			}
		}
		fence.expected = marker
		if err := syscall.Flock(int(fence.lock.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
			return ErrWorkspaceIngressBusy
		}
		journal := &workspaceFencedJournal{Journal: j, fence: fence}
		if err := journal.Check(ctx); err != nil {
			return err
		}
		// Returning from run never implicitly clears the marker. Only the
		// executor's verified recovery/shutdown path can complete this fence.
		return run(journal)
	})
}

func openWorkspaceFence(workspace string) (_ *workspaceFence, result error) {
	f := &workspaceFence{path: workspace, active: true}
	keep := false
	defer func() {
		if !keep {
			result = errors.Join(result, f.close())
		}
	}()
	paths := []string{workspace, filepath.Join(workspace, ".anas"), filepath.Join(workspace, ".anas", "state")}
	for i, path := range paths {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 || securefs.ValidateCurrentOwner(info, "workspace fence directory") != nil {
			return nil, ErrWorkspaceIngressState
		}
		if i != 0 && securefs.ValidateDirectoryInfo(info, "workspace private state") != nil {
			return nil, ErrWorkspaceIngressState
		}
		var root *os.Root
		if i == 0 {
			root, err = os.OpenRoot(path)
		} else {
			root, err = f.roots[i-1].OpenRoot(filepath.Base(path))
		}
		if err != nil {
			return nil, ErrWorkspaceIngressState
		}
		f.roots = append(f.roots, root)
		opened, err := root.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			return nil, ErrWorkspaceIngressState
		}
		f.infos = append(f.infos, opened)
	}
	root := f.roots[2]
	info, err := root.Lstat("lock")
	if err != nil || securefs.ValidateFileInfo(info, "workspace runtime lock") != nil {
		return nil, ErrWorkspaceIngressState
	}
	f.lock, err = root.OpenFile("lock", os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrWorkspaceIngressState
	}
	opened, err := f.lock.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, ErrWorkspaceIngressState
	}
	f.infos = append(f.infos, opened)
	if err = f.checkIdentity(context.Background()); err != nil {
		return nil, err
	}
	keep = true
	return f, nil
}

func (f *workspaceFence) checkIdentity(ctx context.Context) error {
	if ctx == nil || !f.active || len(f.roots) != 3 || len(f.infos) != 4 || f.lock == nil {
		return ErrWorkspaceIngressState
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	paths := []string{f.path, filepath.Join(f.path, ".anas"), filepath.Join(f.path, ".anas", "state")}
	for i, path := range paths {
		current, err := os.Lstat(path)
		opened, openErr := f.roots[i].Stat(".")
		if err != nil || openErr != nil || !current.IsDir() || !os.SameFile(f.infos[i], current) || !os.SameFile(f.infos[i], opened) ||
			current.Mode().Perm()&0022 != 0 || securefs.ValidateCurrentOwner(current, "workspace fence directory") != nil {
			return ErrWorkspaceIngressState
		}
		if i != 0 && securefs.ValidateDirectoryInfo(current, "workspace private state") != nil {
			return ErrWorkspaceIngressState
		}
	}
	path := filepath.Join(paths[2], "lock")
	if securefs.VerifyOpenNamedFile(f.lock, path, "workspace runtime lock") != nil {
		return ErrWorkspaceIngressState
	}
	info, err := f.lock.Stat()
	if err != nil || !os.SameFile(f.infos[3], info) {
		return ErrWorkspaceIngressState
	}
	return ctx.Err()
}

func (f *workspaceFence) read(ctx context.Context) ([]byte, error) {
	if err := f.checkIdentity(ctx); err != nil {
		return nil, err
	}
	before, err := f.lock.Stat()
	if err != nil || before.Size() > 4096 {
		return nil, ErrWorkspaceIngressState
	}
	body, err := io.ReadAll(io.NewSectionReader(f.lock, 0, 4097))
	after, statErr := f.lock.Stat()
	if err != nil || statErr != nil || int64(len(body)) != before.Size() || after.Size() != before.Size() || !before.ModTime().Equal(after.ModTime()) || f.checkIdentity(ctx) != nil {
		return nil, ErrWorkspaceIngressState
	}
	return body, nil
}

func (f *workspaceFence) check(ctx context.Context) error {
	body, err := f.read(ctx)
	if err != nil {
		return err
	}
	if !bytes.Equal(body, f.expected) {
		return ErrWorkspaceIngressState
	}
	return nil
}

func (f *workspaceFence) close() error {
	var result error
	f.active = false
	if f.lock != nil {
		result = errors.Join(securefs.Unlock(f.lock), f.lock.Close())
		f.lock = nil
	}
	for i := len(f.roots) - 1; i >= 0; i-- {
		result = errors.Join(result, f.roots[i].Close())
	}
	f.roots = nil
	return result
}

type workspaceFencedJournal struct {
	Journal
	fence *workspaceFence
}

func (j *workspaceFencedJournal) Check(ctx context.Context) error {
	if err := j.Journal.Check(ctx); err != nil {
		return err
	}
	files, ok := j.Journal.(*fileJournal)
	if !ok {
		return ErrWorkspaceIngressState
	}
	info, err := files.root.Lstat(executorStateFile)
	if err != nil || !privateOwned(info, false) {
		return ErrWorkspaceIngressState
	}
	return j.fence.check(ctx)
}
func (j *workspaceFencedJournal) Load(ctx context.Context) (ExecutorState, error) {
	if err := j.Check(ctx); err != nil {
		return ExecutorState{}, err
	}
	return j.Journal.Load(ctx)
}
func (j *workspaceFencedJournal) Save(ctx context.Context, state ExecutorState) error {
	if err := j.Check(ctx); err != nil {
		return err
	}
	if err := j.Journal.Save(ctx, state); err != nil {
		return err
	}
	return j.Check(ctx)
}
func (j *workspaceFencedJournal) completeWorkspaceDrain(ctx context.Context) error {
	state, err := j.Load(ctx)
	if err != nil {
		return err
	}
	if len(state.Publications) != 0 {
		return ErrWorkspaceIngressActive
	}
	if err = j.Check(ctx); err != nil {
		return err
	}
	// The controller still holds both locks. Network inventory was checked
	// by recover; closing readers must also have succeeded for managed owners.
	if j.fence.lock.Truncate(0) != nil || j.fence.lock.Sync() != nil {
		return ErrWorkspaceIngressState
	}
	j.fence.expected = nil
	return j.Check(ctx)
}
