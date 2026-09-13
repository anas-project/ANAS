package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The directory watcher closes the gap between a Samba change and Forgejo
// noticing it (FORGEJO-R-065).
//
// With an LDAP source configured, Forgejo only re-reads the directory when its
// own "sync external users" cron runs. Removing someone from APP_forgejo, or
// disabling their account, would therefore keep them signed in until that
// schedule came round. The watcher tails Samba's durable event journal and asks
// Forgejo to run the same cron task immediately after a relevant change. The
// periodic cron stays enabled as the authoritative fallback: the journal is an
// accelerator, not a second source of truth.
//
// What this does NOT cover is team membership. Forgejo 15's LDAP source cannot
// be configured with group synchronisation from the command line, so groups
// still reach teams through the OIDC group claim at login. Consumers that need
// team changes to take effect immediately must read the journal themselves.

type directoryWatchSettings struct {
	enabled         bool
	eventFile       string
	cursorFile      string
	healthFile      string
	endpoint        string
	username        string
	password        string
	operations      map[string]bool
	attributes      map[string]bool
	debounce        time.Duration
	minimumInterval time.Duration
	pollInterval    time.Duration
}

func directoryWatchSettingsFromEnv(getenv func(string) string) (directoryWatchSettings, error) {
	seconds := func(key, fallback string) (time.Duration, error) {
		value := strings.TrimSpace(getenv(key))
		if value == "" {
			value = fallback
		}
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || parsed <= 0 {
			return 0, fmt.Errorf("%s must be a positive number of seconds", key)
		}
		return time.Duration(parsed * float64(time.Second)), nil
	}
	settings := directoryWatchSettings{
		enabled:    getenv("FORGEJO_DIRWATCH_ENABLED") == "true",
		eventFile:  valueOr(getenv("FORGEJO_DIRWATCH_EVENT_FILE"), "/var/lib/anas-directory-events/events.jsonl"),
		cursorFile: valueOr(getenv("FORGEJO_DIRWATCH_CURSOR_FILE"), "/data/anas-dirwatch/cursor.json"),
		healthFile: valueOr(getenv("FORGEJO_DIRWATCH_HEALTH_FILE"), "/data/anas-dirwatch/health.json"),
		endpoint:   strings.TrimRight(strings.TrimSpace(getenv("FORGEJO_DIRWATCH_ENDPOINT")), "/"),
		username:   strings.TrimSpace(getenv("FORGEJO_DIRWATCH_USERNAME")),
		password:   getenv("FORGEJO_DIRWATCH_PASSWORD"),
		operations: csvSet(valueOr(getenv("FORGEJO_DIRWATCH_OPERATIONS"), "Add,Modify,Delete"), false),
		attributes: csvSet(getenv("FORGEJO_DIRWATCH_ATTRIBUTES"), true),
	}
	if !settings.enabled {
		return settings, nil
	}
	var err error
	if settings.debounce, err = seconds("FORGEJO_DIRWATCH_DEBOUNCE_SECONDS", "5"); err != nil {
		return directoryWatchSettings{}, err
	}
	if settings.minimumInterval, err = seconds("FORGEJO_DIRWATCH_MIN_INTERVAL_SECONDS", "60"); err != nil {
		return directoryWatchSettings{}, err
	}
	if settings.pollInterval, err = seconds("FORGEJO_DIRWATCH_POLL_SECONDS", "1"); err != nil {
		return directoryWatchSettings{}, err
	}
	for key, value := range map[string]string{
		"FORGEJO_DIRWATCH_ENDPOINT": settings.endpoint,
		"FORGEJO_DIRWATCH_USERNAME": settings.username,
		"FORGEJO_DIRWATCH_PASSWORD": settings.password,
	} {
		if value == "" {
			return directoryWatchSettings{}, fmt.Errorf("%s is required", key)
		}
	}
	return settings, nil
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func csvSet(value string, fold bool) map[string]bool {
	result := map[string]bool{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if fold {
			item = strings.ToLower(item)
		}
		result[item] = true
	}
	return result
}

type directoryEvent struct {
	Seq        int64    `json:"seq"`
	Operation  string   `json:"op"`
	DN         string   `json:"dn"`
	Attributes []string `json:"attributes"`
}

// interestingDirectoryEvent keeps only what can change who may use Forgejo.
// Adds and deletes always matter; a modify matters when it touches one of the
// configured attributes. The journal carries attribute names, never values.
func interestingDirectoryEvent(event directoryEvent, settings directoryWatchSettings) bool {
	if !settings.operations[event.Operation] {
		return false
	}
	if len(settings.attributes) == 0 || event.Operation == "Add" || event.Operation == "Delete" {
		return true
	}
	for _, attribute := range event.Attributes {
		if settings.attributes[strings.ToLower(attribute)] {
			return true
		}
	}
	return false
}

type directoryJournalReader struct {
	path   string
	cursor int64
	file   *os.File
	reader *bufio.Reader
}

func (r *directoryJournalReader) close() {
	if r.file != nil {
		_ = r.file.Close()
	}
	r.file = nil
	r.reader = nil
}

func (r *directoryJournalReader) open() error {
	file, err := os.Open(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	r.file = file
	r.reader = bufio.NewReader(file)
	return nil
}

// events returns records past the cursor. It follows rotation by draining the
// renamed generation before opening its replacement; sequence numbers rather
// than byte offsets suppress duplicates across the switch.
func (r *directoryJournalReader) events() ([]directoryEvent, error) {
	if r.file == nil {
		if err := r.open(); err != nil || r.file == nil {
			return nil, err
		}
	}
	current, err := os.Stat(r.path)
	if errors.Is(err, os.ErrNotExist) {
		current = nil
	} else if err != nil {
		return nil, err
	}
	opened, err := r.file.Stat()
	if err != nil {
		return nil, err
	}
	if current != nil && os.SameFile(opened, current) {
		return r.readOpen()
	}
	result, err := r.readOpen()
	if err != nil {
		return nil, err
	}
	r.close()
	if err := r.open(); err != nil || r.file == nil {
		return result, err
	}
	more, err := r.readOpen()
	return append(result, more...), err
}

func (r *directoryJournalReader) readOpen() ([]directoryEvent, error) {
	result := []directoryEvent{}
	for {
		line, err := r.reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			// A publisher writes whole records. Leave an incomplete tail for the
			// next poll instead of parsing half a line.
			if len(bytes.TrimSpace(line)) != 0 {
				if _, seekErr := r.file.Seek(-int64(len(line)), io.SeekCurrent); seekErr != nil {
					return nil, seekErr
				}
				r.reader.Reset(r.file)
			}
			return result, nil
		}
		if err != nil {
			return nil, err
		}
		var event directoryEvent
		if err := json.Unmarshal(bytes.TrimSpace(line), &event); err != nil {
			log.Printf("skipping malformed directory journal line: %v", err)
			continue
		}
		if event.Seq > r.cursor {
			result = append(result, event)
		}
	}
}

// directorySyncer asks Forgejo to re-read the directory.
type directorySyncer interface {
	sync() error
}

// cronSyncer runs Forgejo's own "sync external users" task. Using the task
// rather than reimplementing the sync keeps one definition of what an LDAP
// refresh means: the same code path the periodic schedule already runs.
type cronSyncer struct {
	settings directoryWatchSettings
	client   *http.Client
}

func (s *cronSyncer) sync() error {
	request, err := http.NewRequest(http.MethodPost, s.settings.endpoint+"/api/v1/admin/cron/sync_external_users", nil)
	if err != nil {
		return err
	}
	request.SetBasicAuth(s.settings.username, s.settings.password)
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("trigger Forgejo external user sync: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("trigger Forgejo external user sync: status %d", response.StatusCode)
	}
	return nil
}

type directoryWatchHealth struct {
	Ready         bool   `json:"ready"`
	StartedAt     int64  `json:"started_at"`
	Cursor        int64  `json:"cursor"`
	LastTriggerAt int64  `json:"last_trigger_at"`
	TriggerCount  int64  `json:"trigger_count"`
	LastError     string `json:"last_error"`
}

type directoryWatcher struct {
	settings      directoryWatchSettings
	reader        *directoryJournalReader
	syncer        directorySyncer
	cursor        int64
	uncommitted   int64
	pendingSince  time.Time
	lastTriggered time.Time
	health        directoryWatchHealth
}

func newDirectoryWatcher(settings directoryWatchSettings, syncer directorySyncer) *directoryWatcher {
	cursor := readDirectoryCursor(settings.cursorFile)
	return &directoryWatcher{
		settings:    settings,
		reader:      &directoryJournalReader{path: settings.eventFile, cursor: cursor},
		syncer:      syncer,
		cursor:      cursor,
		uncommitted: cursor,
		health:      directoryWatchHealth{Ready: true, StartedAt: time.Now().Unix(), Cursor: cursor},
	}
}

// poll reads new records and triggers at most one sync. A burst of changes is
// debounced into one refresh, and refreshes are spaced by a minimum interval so
// a bulk directory import cannot turn into a sync storm. The cursor only moves
// past a relevant record once the sync it asked for has succeeded; a failure
// leaves the record pending for the next poll.
func (w *directoryWatcher) poll(now time.Time) (bool, error) {
	events, err := w.reader.events()
	if err != nil {
		return false, err
	}
	matched := false
	for _, event := range events {
		if event.Seq > w.uncommitted {
			w.uncommitted = event.Seq
		}
		if interestingDirectoryEvent(event, w.settings) {
			matched = true
			log.Printf("directory change seq=%d op=%s dn=%s attrs=%s", event.Seq, event.Operation, event.DN, strings.Join(event.Attributes, ","))
		}
	}
	if matched && w.pendingSince.IsZero() {
		w.pendingSince = now
	}
	if !matched && w.uncommitted > w.cursor && w.pendingSince.IsZero() {
		if err := w.commit(w.uncommitted); err != nil {
			return false, err
		}
	}
	if w.pendingSince.IsZero() || now.Sub(w.pendingSince) < w.settings.debounce {
		return false, nil
	}
	if !w.lastTriggered.IsZero() && now.Sub(w.lastTriggered) < w.settings.minimumInterval {
		return false, nil
	}
	if err := w.syncer.sync(); err != nil {
		return false, err
	}
	w.pendingSince = time.Time{}
	w.lastTriggered = now
	w.health.LastTriggerAt = now.Unix()
	w.health.TriggerCount++
	if err := w.commit(w.uncommitted); err != nil {
		return false, err
	}
	return true, nil
}

func (w *directoryWatcher) commit(cursor int64) error {
	if err := writeJSONAtomic(w.settings.cursorFile, map[string]int64{"seq": cursor}); err != nil {
		return err
	}
	w.cursor = cursor
	w.reader.cursor = cursor
	w.health.Cursor = cursor
	return nil
}

func readDirectoryCursor(path string) int64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var cursor struct {
		Seq int64 `json:"seq"`
	}
	if json.Unmarshal(data, &cursor) != nil || cursor.Seq < 0 {
		return 0
	}
	return cursor.Seq
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".anas-dirwatch-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := json.NewEncoder(temporary).Encode(value); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// runDirectoryWatch is the `directory-watch` entrypoint. With directory
// synchronisation switched off it exits successfully at once, so the service can
// exist in every deployment without an Incus-style conditional service set.
func runDirectoryWatch(args []string) error {
	settings, err := directoryWatchSettingsFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	if len(args) == 1 && args[0] == "--healthcheck" {
		if !settings.enabled {
			return nil
		}
		data, err := os.ReadFile(settings.healthFile)
		if err != nil {
			return err
		}
		var health directoryWatchHealth
		if err := json.Unmarshal(data, &health); err != nil {
			return err
		}
		if !health.Ready {
			return fmt.Errorf("directory watcher is not ready: %s", health.LastError)
		}
		return nil
	}
	if len(args) != 0 {
		return fmt.Errorf("directory-watch accepts only --healthcheck")
	}
	if !settings.enabled {
		log.Printf("directory synchronisation is disabled; the watcher has nothing to do")
		return nil
	}

	watcher := newDirectoryWatcher(settings, &cronSyncer{settings: settings, client: &http.Client{Timeout: 2 * time.Minute}})
	defer watcher.reader.close()
	log.Printf("watching %s (debounce=%s min-interval=%s)", settings.eventFile, settings.debounce, settings.minimumInterval)
	for {
		if _, err := watcher.poll(time.Now()); err != nil {
			watcher.health.Ready = false
			watcher.health.LastError = err.Error()
			log.Printf("directory watcher poll failed: %v", err)
		} else {
			watcher.health.Ready = true
			watcher.health.LastError = ""
		}
		if err := writeJSONAtomic(settings.healthFile, watcher.health); err != nil {
			log.Printf("cannot write directory watcher health: %v", err)
		}
		time.Sleep(settings.pollInterval)
	}
}
