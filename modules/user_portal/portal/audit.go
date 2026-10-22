package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/anas-project/ANAS/internal/securefs"
)

type Record struct {
	ID     string    `json:"id"`
	Time   time.Time `json:"time"`
	Phase  string    `json:"phase"`
	Actor  string    `json:"actor"`
	Target string    `json:"target"`
	Action string    `json:"action"`
	Rule   string    `json:"rule,omitempty"`
	Group  string    `json:"group,omitempty"`
	// Reason is retained only to read historical audit records.
	Reason         string   `json:"reason,omitempty"`
	Directory      string   `json:"directory,omitempty"`
	Ownership      string   `json:"ownership,omitempty"`
	Membership     string   `json:"membership,omitempty"`
	IAM            string   `json:"iam,omitempty"`
	Service        string   `json:"service,omitempty"`
	ConfigDigest   string   `json:"config_digest"`
	ForceChange    *bool    `json:"force_change,omitempty"`
	ExpectedDirect *bool    `json:"expected_direct,omitempty"`
	ChangedFields  []string `json:"changed_fields,omitempty"`
}

// Journal contains only stable references and outcomes. Passwords, tokens,
// LDAP payloads and temporary reset passwords have no representable field.
type Journal struct {
	mu         sync.Mutex
	file       *os.File
	directory  *os.File
	path, root string
	records    []Record
	broken     bool
	maxBytes   int64
}

func openJournal(root string, maxBytes int64) (*Journal, error) {
	dir, created, err := securefs.OpenDirectory(root, "portal audit directory")
	if err != nil {
		return nil, err
	}
	if err := securefs.SyncCreatedDirectoryEntries(created); err != nil {
		dir.Close()
		return nil, err
	}
	path := filepath.Join(root, "audit.jsonl")
	f, newFile, err := securefs.OpenNamedFile(path, "portal audit journal")
	if err != nil {
		dir.Close()
		return nil, err
	}
	j := &Journal{file: f, directory: dir, path: path, root: root, maxBytes: maxBytes, records: []Record{}}
	locked, err := securefs.TryLock(f)
	if err != nil || !locked {
		j.close()
		return nil, errors.New("audit journal already in use")
	}
	if newFile {
		if err := f.Sync(); err != nil {
			j.close()
			return nil, err
		}
		if err := securefs.SyncParentDirectory(path); err != nil {
			j.close()
			return nil, err
		}
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 16<<10)
	pending := map[string]bool{}
	for scanner.Scan() {
		var r Record
		if json.Unmarshal(scanner.Bytes(), &r) != nil || r.ID == "" || r.Phase != "intent" && r.Phase != "result" {
			j.broken = true
			break
		}
		j.records = append(j.records, r)
		if r.Phase == "intent" {
			if pending[r.ID] {
				j.broken = true
				break
			}
			pending[r.ID] = true
		} else {
			delete(pending, r.ID)
		}
	}
	if scanner.Err() != nil {
		j.broken = true
	}
	// A final non-newline fragment may decode as JSON, but is not a durable
	// completed record. Never truncate, repair or replay it automatically.
	info, err := f.Stat()
	if err != nil {
		j.close()
		return nil, err
	}
	if info.Size() > 0 {
		if _, err := f.Seek(-1, io.SeekEnd); err != nil {
			j.close()
			return nil, err
		}
		b := make([]byte, 1)
		if _, err := io.ReadFull(f, b); err != nil || b[0] != '\n' {
			j.broken = true
		}
	}
	if info.Size() > maxBytes {
		j.broken = true
	}
	return j, nil
}

func (j *Journal) close() {
	if j.file != nil {
		j.file.Close()
	}
	if j.directory != nil {
		j.directory.Close()
	}
}

func (j *Journal) append(r Record) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.broken {
		return errors.New("audit unavailable")
	}
	if securefs.VerifyOpenDirectory(j.directory, j.root, "portal audit directory") != nil || securefs.VerifyOpenNamedFile(j.file, j.path, "portal audit journal") != nil {
		j.broken = true
		return errors.New("audit unavailable")
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	i, err := j.file.Stat()
	if err != nil || i.Size()+int64(len(b)) > j.maxBytes {
		j.broken = true
		return errors.New("audit capacity reached")
	}
	if err := securefs.WriteAll(j.file, b); err != nil {
		j.broken = true
		return errors.New("audit write failed")
	}
	if err := j.file.Sync(); err != nil {
		j.broken = true
		return errors.New("audit sync failed")
	}
	j.records = append(j.records, r)
	return nil
}

func (j *Journal) list() []Record {
	j.mu.Lock()
	defer j.mu.Unlock()
	result := append([]Record{}, j.records...)
	finished := map[string]bool{}
	for _, r := range result {
		if r.Phase == "result" {
			finished[r.ID] = true
		}
	}
	for i := range result {
		if result[i].Phase == "intent" && !finished[result[i].ID] {
			result[i].Directory = "unknown"
			if result[i].Ownership != "" {
				result[i].Ownership, result[i].Membership = "unknown", "unknown"
			}
		}
	}
	return result
}
