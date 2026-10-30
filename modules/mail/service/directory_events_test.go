package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

func journalLine(t *testing.T, seq int64, attrs ...string) []byte {
	t.Helper()
	raw, err := json.Marshal(directoryEvent{Sequence: seq, Operation: "Modify", DN: "CN=Alice,OU=People,DC=example,DC=test", Attributes: attrs})
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}
func writeJournal(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestJournalRotationGapsPartialRecordsAndReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	first := journalLine(t, 1, "member")
	second := journalLine(t, 2, "anasMailState")
	writeJournal(t, path, append(append([]byte{}, first...), second[:len(second)-1]...))
	s, err := readDirectoryJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if next, refresh := s.pending(0); next != 1 || !refresh {
		t.Fatal("partial record consumed", next)
	}
	writeJournal(t, path, append(append([]byte{}, first...), second...))
	if s.unchanged(path) {
		t.Fatal("append not observed")
	}
	s, err = readDirectoryJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if next, refresh := s.pending(1); next != 2 || !refresh {
		t.Fatal("completed tail lost")
	}
	writeJournal(t, path+".1", append(first, second...))
	writeJournal(t, path, journalLine(t, 3, "memberOf"))
	s, err = readDirectoryJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if next, refresh := s.pending(2); next != 3 || !refresh {
		t.Fatal("rotation lost event")
	}
	if _, refresh := s.pending(3); refresh {
		t.Fatal("already consumed records caused sync storm")
	}
	writeJournal(t, path+".1", journalLine(t, 8, "unknownAttribute"))
	writeJournal(t, path, journalLine(t, 9, "unknownAttribute"))
	s, err = readDirectoryJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if next, refresh := s.pending(3); next != 9 || !refresh {
		t.Fatal("retention gap did not force full refresh")
	}
	os.Remove(path + ".1")
	writeJournal(t, path, journalLine(t, 1, "unknownAttribute"))
	s, err = readDirectoryJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if next, refresh := s.pending(9); next != 1 || !refresh {
		t.Fatal("producer sequence reset ignored")
	}
}
func TestJournalRejectsCorruptionValuesAndUnsafeFiles(t *testing.T) {
	for _, mode := range []string{"malformed", "unknown-value", "conflicting-duplicate", "oversized", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "events.jsonl")
			raw := journalLine(t, 1, "member")
			switch mode {
			case "malformed":
				raw = append(raw, []byte("{broken}\n")...)
			case "unknown-value":
				raw = []byte("{\"seq\":1,\"op\":\"Modify\",\"dn\":\"person\",\"attributes\":[],\"password\":\"private\"}\n")
			case "conflicting-duplicate":
				raw = append(raw, journalLine(t, 1, "mail")...)
			case "oversized":
				raw = make([]byte, (6<<20)+1)
			}
			writeJournal(t, path, raw)
			if mode == "symlink" {
				os.Rename(path, path+".real")
				if err := os.Symlink(path+".real", path); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := readDirectoryJournal(path); !errors.Is(err, errDirectoryJournal) {
				t.Fatal("unsafe journal accepted", err)
			}
		})
	}
}

type cursorMemory struct {
	sequence int64
	saves    int
	err      error
}

func (s *cursorMemory) DirectoryCursor(context.Context) (int64, error) { return s.sequence, s.err }
func (s *cursorMemory) SaveDirectoryCursor(_ context.Context, expected, next int64) error {
	if s.err != nil {
		return s.err
	}
	if s.sequence != expected {
		return errors.New("cursor changed")
	}
	s.sequence = next
	s.saves++
	return nil
}

type eventProjectionStore struct {
	projectionStore
	calls        int
	err          error
	companyCalls int
	companyErr   error
}

func (s *eventProjectionStore) SaveCompanyDirectory(context.Context, identity.CompanySnapshot) (string, error) {
	s.companyCalls++
	return "company", s.companyErr
}

func (s *eventProjectionStore) KnownPersons(context.Context) ([]string, error) {
	s.calls++
	return nil, s.err
}
func (s *eventProjectionStore) MailboxReferences(context.Context) ([]identity.MailboxReference, error) {
	return nil, nil
}

type eventDirectory struct {
	configurationDirectory
	companyErr error
}

func (d eventDirectory) CompanySnapshot(context.Context) (identity.CompanySnapshot, error) {
	return identity.CompanySnapshot{Schema: identity.CompanySchema, People: []identity.CompanyContact{}, Groups: []identity.CompanyGroup{}}, d.companyErr
}

func (eventDirectory) MailboxReferences(context.Context) ([]identity.MailboxReference, error) {
	return nil, nil
}
func TestJournalCommitsOnlySuccessfulWorkAndCoalescesBursts(t *testing.T) {
	source := &eventProjectionStore{}
	cursor := &cursorMemory{sequence: 3}
	c := mailCoordinator{store: source, directory: eventDirectory{}}
	state := journalState{events: []directoryEvent{{Sequence: 4, Operation: "Modify", Attributes: []string{"member"}}, {Sequence: 5, Operation: "Delete"}, {Sequence: 6, Operation: "Add"}}}
	source.err = identity.ErrDirectoryUnavailable
	if _, err := c.consumeJournal(t.Context(), cursor, state, 3, false); err == nil || cursor.sequence != 3 || cursor.saves != 0 {
		t.Fatal("failed work committed cursor")
	}
	source.err = nil
	if full, err := c.consumeJournal(t.Context(), cursor, state, 3, false); err != nil || !full || cursor.sequence != 6 || source.calls != 2 {
		t.Fatal("burst not reconciled as one unit", err)
	}
	if full, err := c.consumeJournal(t.Context(), cursor, state, 6, false); err != nil || full || source.calls != 2 || cursor.saves != 1 {
		t.Fatal("duplicate event storm", err)
	}
	state.events = append(state.events, directoryEvent{Sequence: 7, Operation: "Modify", Attributes: []string{"unknownAttribute"}})
	if full, err := c.consumeJournal(t.Context(), cursor, state, 6, false); err != nil || full || cursor.sequence != 7 || source.calls != 2 {
		t.Fatal("irrelevant event not explicitly acknowledged", err)
	}
	if full, err := c.consumeJournal(t.Context(), cursor, state, 7, true); err != nil || !full || source.calls != 3 {
		t.Fatal("periodic fallback missing", err)
	}
	cursor.err = errors.New("database unavailable")
	state.events = append(state.events, directoryEvent{Sequence: 8, Operation: "Delete"})
	if _, err := c.consumeJournal(t.Context(), cursor, state, 7, false); err == nil || cursor.sequence != 7 {
		t.Fatal("failed cursor persistence claimed progress")
	}
}

func TestJournalWaitsForCompanyRefreshBeforeCursorCommit(t *testing.T) {
	source := &eventProjectionStore{}
	cursor := &cursorMemory{sequence: 1}
	c := mailCoordinator{store: source, directory: eventDirectory{companyErr: identity.ErrDirectoryUnavailable}, companyEnabled: true}
	state := journalState{events: []directoryEvent{{Sequence: 2, Operation: "Modify", Attributes: []string{"telephoneNumber"}}}}
	if _, err := c.consumeJournal(t.Context(), cursor, state, 1, false); err == nil || cursor.sequence != 1 || source.companyCalls != 0 {
		t.Fatal("failed company read committed progress")
	}
	c.directory = eventDirectory{}
	source.companyErr = store.ErrCompanyDirectoryUnavailable
	if _, err := c.consumeJournal(t.Context(), cursor, state, 1, false); err == nil || cursor.sequence != 1 {
		t.Fatal("failed company write committed progress")
	}
	source.companyErr = nil
	if _, err := c.consumeJournal(t.Context(), cursor, state, 1, false); err != nil || cursor.sequence != 2 || source.companyCalls != 2 {
		t.Fatal("company refresh did not recover", err)
	}
}

func TestJournalPrimaryGroupChangeRefreshesAdmissionAndCompany(t *testing.T) {
	source := &eventProjectionStore{}
	cursor := &cursorMemory{sequence: 1}
	c := mailCoordinator{store: source, directory: eventDirectory{}, companyEnabled: true}
	state := journalState{events: []directoryEvent{{Sequence: 2, Operation: "Modify", Attributes: []string{"primaryGroupID"}}}}
	full, err := c.consumeJournal(t.Context(), cursor, state, 1, false)
	if err != nil || !full || cursor.sequence != 2 || source.calls != 1 || source.companyCalls != 1 {
		t.Fatal("primary-group change skipped admission/company reconciliation", err)
	}
}
