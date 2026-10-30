package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

var errDirectoryJournal = errors.New("mail directory journal unavailable or incomplete")
var eventAttributePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,127}$`)

type directoryEvent struct {
	Sequence   int64           `json:"seq"`
	Time       json.RawMessage `json:"ts"`
	Operation  string          `json:"op"`
	DN         string          `json:"dn"`
	Attributes []string        `json:"attributes"`
}
type journalState struct {
	events []directoryEvent
	files  []os.FileInfo
}
type directoryCursorStore interface {
	DirectoryCursor(context.Context) (int64, error)
	SaveDirectoryCursor(context.Context, int64, int64) error
}

func readDirectoryJournal(path string) (journalState, error) {
	state := journalState{files: make([]os.FileInfo, 2)}
	seen := map[int64][]byte{}
	for n, name := range []string{path + ".1", path} {
		info, err := os.Lstat(name)
		if errors.Is(err, os.ErrNotExist) && n == 0 {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() > 6<<20 {
			return journalState{}, errDirectoryJournal
		}
		file, err := os.Open(name)
		if err != nil {
			return journalState{}, errDirectoryJournal
		}
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) {
			file.Close()
			return journalState{}, errDirectoryJournal
		}
		raw, err := io.ReadAll(io.LimitReader(file, 6<<20+1))
		file.Close()
		current, statErr := os.Lstat(name)
		if err != nil || len(raw) > 6<<20 || statErr != nil || !os.SameFile(info, current) || current.Size() < info.Size() {
			return journalState{}, errDirectoryJournal
		}
		state.files[n] = info
		// Never consume an incomplete tail, including a syntactically valid JSON
		// record whose final newline has not been durably appended yet.
		end := bytes.LastIndexByte(raw, '\n')
		if end < 0 {
			continue
		}
		scanner := bufio.NewScanner(bytes.NewReader(raw[:end+1]))
		scanner.Buffer(make([]byte, 4096), 65536)
		var previous int64
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var event directoryEvent
			decoder := json.NewDecoder(bytes.NewReader(line))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&event) != nil || event.Sequence < 1 || event.Sequence < previous || event.DN == "" || len(event.DN) > 4096 || len(event.Attributes) > 128 || event.Operation != "Add" && event.Operation != "Modify" && event.Operation != "Delete" {
				return journalState{}, errDirectoryJournal
			}
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				return journalState{}, errDirectoryJournal
			}
			for _, attr := range event.Attributes {
				if !eventAttributePattern.MatchString(attr) {
					return journalState{}, errDirectoryJournal
				}
			}
			previous = event.Sequence
			if existing, ok := seen[event.Sequence]; ok {
				if !bytes.Equal(existing, line) {
					return journalState{}, errDirectoryJournal
				}
				continue
			}
			seen[event.Sequence] = append([]byte(nil), line...)
			state.events = append(state.events, event)
			if len(state.events) > 65536 {
				return journalState{}, errDirectoryJournal
			}
		}
		if scanner.Err() != nil {
			return journalState{}, errDirectoryJournal
		}
	}
	sort.Slice(state.events, func(i, j int) bool { return state.events[i].Sequence < state.events[j].Sequence })
	return state, nil
}
func (s journalState) unchanged(path string) bool {
	for n, name := range []string{path + ".1", path} {
		info, err := os.Lstat(name)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && s.files[n] == nil && n == 0 {
				continue
			}
			return false
		}
		old := s.files[n]
		if old == nil || !os.SameFile(old, info) || old.Size() != info.Size() || !old.ModTime().Equal(info.ModTime()) {
			return false
		}
	}
	return true
}
func (e directoryEvent) relevant() bool {
	if e.Operation != "Modify" {
		return true
	}
	for _, attribute := range e.Attributes {
		switch strings.ToLower(attribute) {
		case "member", "memberof", "primarygroupid", "useraccountcontrol", "lockouttime", "accountexpires", "samaccountname", "userprincipalname", "displayname", "mail", "anasidentityanchor", "proxyaddresses", "pwdlastset", "telephonenumber", "mobile", "department", "title", "givenname", "sn", "description":
			return true
		}
		if strings.HasPrefix(strings.ToLower(attribute), "anasmail") {
			return true
		}
	}
	return false
}
func (s journalState) pending(cursor int64) (next int64, refresh bool) {
	if len(s.events) == 0 {
		return 0, cursor > 0
	}
	next = s.events[len(s.events)-1].Sequence
	if cursor == 0 || next < cursor || s.events[0].Sequence > cursor+1 {
		return next, true
	}
	previous := cursor
	for _, event := range s.events {
		if event.Sequence <= cursor {
			continue
		}
		if event.Sequence != previous+1 || event.relevant() {
			refresh = true
		}
		previous = event.Sequence
	}
	return next, refresh
}

func (c *mailCoordinator) consumeJournal(ctx context.Context, data directoryCursorStore, state journalState, cursor int64, force bool) (bool, error) {
	next, refresh := state.pending(cursor)
	full := force || refresh
	if full {
		err := c.reconcile(ctx)
		c.recordResult(err)
		if err != nil {
			return true, err
		}
	}
	if next != cursor {
		if err := data.SaveDirectoryCursor(ctx, cursor, next); err != nil {
			return full, err
		}
	}
	return full, nil
}
func (c *mailCoordinator) eventLoop(ctx context.Context, data directoryCursorStore, path string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var state journalState
	var haveState bool
	var lastFull, retryAt time.Time
	for {
		if time.Now().Before(retryAt) {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			continue
		}
		work, cancel := context.WithTimeout(ctx, 45*time.Second)
		cursor, storeErr := data.DirectoryCursor(work)
		journalErr := error(nil)
		if !haveState || !state.unchanged(path) {
			state, journalErr = readDirectoryJournal(path)
			haveState = journalErr == nil
		}
		due := lastFull.IsZero() || time.Since(lastFull) >= 15*time.Second
		var err error
		if storeErr == nil && journalErr == nil {
			var full bool
			full, err = c.consumeJournal(work, data, state, cursor, due)
			if full && err == nil {
				lastFull = time.Now()
			}
		} else if due {
			err = c.reconcile(work)
			c.recordResult(err)
			if err == nil {
				lastFull = time.Now()
			}
		}
		if journalErr != nil || storeErr != nil || err != nil {
			c.recordResult(errDirectoryJournal)
			retryAt = time.Now().Add(5 * time.Second)
		} else {
			retryAt = time.Time{}
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
