package computeingressruntime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
)

const executorStateFile = "http-publications.json"

// FileStateStore locks one administrator-owned local directory for the entire
// executor session, including external operations. All publishers targeting
// the same ingress must use this one directory; it must not be on NFS.
type FileStateStore struct {
	Directory string
}

type fileJournal struct {
	root  *os.Root
	check func(context.Context) error
}

func (s *fileJournal) Check(ctx context.Context) error { return s.check(ctx) }

func (s *fileJournal) Load(ctx context.Context) (ExecutorState, error) {
	if err := s.Check(ctx); err != nil {
		return ExecutorState{}, err
	}
	root := s.root
	info, err := root.Lstat(executorStateFile)
	if os.IsNotExist(err) {
		return ExecutorState{Schema: executorStateSchema}, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4<<20 {
		return ExecutorState{}, fmt.Errorf("HTTP executor state must be a private bounded regular file")
	}
	file, err := root.Open(executorStateFile)
	if err != nil {
		return ExecutorState{}, err
	}
	body, readErr := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	after, statErr := file.Stat()
	linked, linkErr := root.Lstat(executorStateFile)
	_ = file.Close()
	if readErr != nil || statErr != nil || linkErr != nil || !linked.Mode().IsRegular() || !os.SameFile(after, linked) || !os.SameFile(info, after) || after.Size() != info.Size() || len(body) > 4<<20 || int64(len(body)) != info.Size() || !info.ModTime().Equal(after.ModTime()) {
		return ExecutorState{}, fmt.Errorf("HTTP executor state changed while reading")
	}
	var state ExecutorState
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return ExecutorState{}, fmt.Errorf("decode HTTP executor state: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return ExecutorState{}, err
	}
	if err := validateState(state); err != nil {
		return ExecutorState{}, err
	}
	canonical, err := marshalExecutorState(state)
	if err != nil || !bytes.Equal(body, canonical) {
		return ExecutorState{}, fmt.Errorf("HTTP executor state is not canonical")
	}
	if err := s.Check(ctx); err != nil {
		return ExecutorState{}, err
	}
	return state, nil
}

func (s *fileJournal) Save(ctx context.Context, state ExecutorState) error {
	if err := s.Check(ctx); err != nil {
		return err
	}
	body, err := marshalExecutorState(state)
	if err != nil {
		return err
	}
	root := s.root
	if info, err := root.Lstat(executorStateFile); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("refuse to replace invalid HTTP executor state file")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("create HTTP state temporary name")
	}
	temporary := "." + executorStateFile + "." + hex.EncodeToString(nonce[:]) + ".tmp"
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := s.Check(ctx); err != nil {
		return err
	}
	if err := root.Rename(temporary, executorStateFile); err != nil {
		return err
	}
	if err := syncRoot(root); err != nil {
		return err
	}
	return s.Check(ctx)
}

func marshalExecutorState(state ExecutorState) ([]byte, error) {
	state.Publications = append([]AppliedPublication(nil), state.Publications...)
	state.Retired = append([]PublicationTarget(nil), state.Retired...)
	sort.Slice(state.Publications, func(i, j int) bool {
		return state.Publications[i].Target.Publication.Reservation < state.Publications[j].Target.Publication.Reservation
	})
	sort.Slice(state.Retired, func(i, j int) bool {
		return state.Retired[i].Publication.Reservation < state.Retired[j].Publication.Reservation
	})
	if err := validateState(state); err != nil {
		return nil, err
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(body)+1 > 4<<20 {
		return nil, fmt.Errorf("HTTP executor state exceeds 4 MiB; retirement history requires offline maintenance")
	}
	return append(body, '\n'), nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("HTTP executor state has trailing JSON")
	}
	return nil
}
