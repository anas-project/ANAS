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
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type directoryWatchSettings struct {
	eventFile       string
	cursorFile      string
	healthFile      string
	endpoint        string
	ldapID          string
	clientID        string
	clientSecret    string
	operations      map[string]bool
	attributes      map[string]bool
	debounce        time.Duration
	minimumInterval time.Duration
	pollInterval    time.Duration
}

func directoryWatchSettingsFromEnv() (directoryWatchSettings, error) {
	seconds := func(key, fallback string) (time.Duration, error) {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			value = fallback
		}
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || parsed <= 0 {
			return 0, fmt.Errorf("%s must be a positive number of seconds", key)
		}
		return time.Duration(parsed * float64(time.Second)), nil
	}
	debounce, err := seconds("CASDOOR_DIRWATCH_DEBOUNCE_SECONDS", "5")
	if err != nil {
		return directoryWatchSettings{}, err
	}
	minimumInterval, err := seconds("CASDOOR_DIRWATCH_MIN_INTERVAL_SECONDS", "60")
	if err != nil {
		return directoryWatchSettings{}, err
	}
	pollInterval, err := seconds("CASDOOR_DIRWATCH_POLL_SECONDS", "1")
	if err != nil {
		return directoryWatchSettings{}, err
	}

	settings := directoryWatchSettings{
		eventFile:       defaultString(os.Getenv("CASDOOR_DIRWATCH_EVENT_FILE"), "/var/lib/anas-directory-events/events.jsonl"),
		cursorFile:      defaultString(os.Getenv("CASDOOR_DIRWATCH_CURSOR_FILE"), "/data/anas-dirwatch/cursor.json"),
		healthFile:      defaultString(os.Getenv("CASDOOR_DIRWATCH_HEALTH_FILE"), "/data/anas-dirwatch/health.json"),
		endpoint:        strings.TrimRight(strings.TrimSpace(os.Getenv("CASDOOR_DIRWATCH_ENDPOINT")), "/"),
		ldapID:          strings.TrimSpace(os.Getenv("CASDOOR_DIRWATCH_LDAP_ID")),
		clientID:        os.Getenv("CASDOOR_DIRWATCH_CLIENT_ID"),
		clientSecret:    os.Getenv("CASDOOR_DIRWATCH_CLIENT_SECRET"),
		operations:      csvSet(defaultString(os.Getenv("CASDOOR_DIRWATCH_OPERATIONS"), "Add,Modify,Delete"), false),
		attributes:      csvSet(os.Getenv("CASDOOR_DIRWATCH_ATTRIBUTES"), true),
		debounce:        debounce,
		minimumInterval: minimumInterval,
		pollInterval:    pollInterval,
	}
	for key, value := range map[string]string{
		"CASDOOR_DIRWATCH_ENDPOINT":      settings.endpoint,
		"CASDOOR_DIRWATCH_LDAP_ID":       settings.ldapID,
		"CASDOOR_DIRWATCH_CLIENT_ID":     settings.clientID,
		"CASDOOR_DIRWATCH_CLIENT_SECRET": settings.clientSecret,
	} {
		if value == "" {
			return directoryWatchSettings{}, fmt.Errorf("%s is required", key)
		}
	}
	return settings, nil
}

func defaultString(value, fallback string) string {
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

func (reader *directoryJournalReader) close() {
	if reader.file != nil {
		_ = reader.file.Close()
	}
	reader.file = nil
	reader.reader = nil
}

func (reader *directoryJournalReader) open() error {
	file, err := os.Open(reader.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	reader.file = file
	reader.reader = bufio.NewReader(file)
	return nil
}

func (reader *directoryJournalReader) events() ([]directoryEvent, error) {
	if reader.file == nil {
		if err := reader.open(); err != nil || reader.file == nil {
			return nil, err
		}
	}
	current, err := os.Stat(reader.path)
	if errors.Is(err, os.ErrNotExist) {
		current = nil
	} else if err != nil {
		return nil, err
	}
	opened, err := reader.file.Stat()
	if err != nil {
		return nil, err
	}
	if current != nil && os.SameFile(opened, current) {
		return reader.readOpen()
	}

	// Drain the renamed generation before opening its replacement. Sequence
	// numbers, rather than byte offsets, suppress duplicates across rotation.
	result, err := reader.readOpen()
	if err != nil {
		return nil, err
	}
	reader.close()
	if err := reader.open(); err != nil || reader.file == nil {
		return result, err
	}
	more, err := reader.readOpen()
	return append(result, more...), err
}

func (reader *directoryJournalReader) readOpen() ([]directoryEvent, error) {
	result := []directoryEvent{}
	for {
		line, err := reader.reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			// A publisher writes complete JSONL records. Keep an incomplete tail
			// unread so the next poll can parse it after the write completes.
			if len(bytes.TrimSpace(line)) != 0 {
				if _, seekErr := reader.file.Seek(-int64(len(line)), io.SeekCurrent); seekErr != nil {
					return nil, seekErr
				}
				reader.reader.Reset(reader.file)
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
		if event.Seq > reader.cursor {
			result = append(result, event)
		}
	}
}

type directorySyncer interface {
	sync([]directoryEvent) error
}

type casdoorLDAPSyncer struct {
	settings directoryWatchSettings
	client   *http.Client
}

type casdoorAPIResponse struct {
	Status string          `json:"status"`
	Msg    string          `json:"msg"`
	Data   json.RawMessage `json:"data"`
}

func (syncer *casdoorLDAPSyncer) request(method, action, id string, body io.Reader) (casdoorAPIResponse, error) {
	query := url.Values{}
	query.Set("id", id)
	return syncer.requestWithQuery(method, action, query, body)
}

func (syncer *casdoorLDAPSyncer) requestWithQuery(method, action string, query url.Values, body io.Reader) (casdoorAPIResponse, error) {
	endpoint, err := url.Parse(syncer.settings.endpoint + "/api/" + action)
	if err != nil {
		return casdoorAPIResponse{}, err
	}
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequest(method, endpoint.String(), body)
	if err != nil {
		return casdoorAPIResponse{}, err
	}
	req.SetBasicAuth(syncer.settings.clientID, syncer.settings.clientSecret)
	if body != nil {
		req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	}
	resp, err := syncer.client.Do(req)
	if err != nil {
		return casdoorAPIResponse{}, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return casdoorAPIResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return casdoorAPIResponse{}, fmt.Errorf("Casdoor %s returned %s", action, resp.Status)
	}
	var envelope casdoorAPIResponse
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return casdoorAPIResponse{}, fmt.Errorf("decode Casdoor %s response: %w", action, err)
	}
	if envelope.Status != "ok" {
		return casdoorAPIResponse{}, fmt.Errorf("Casdoor %s failed: %s", action, envelope.Msg)
	}
	return envelope, nil
}

type casdoorDirectoryUser struct {
	UID         string `json:"uid"`
	CN          string `json:"cn"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

func (syncer *casdoorLDAPSyncer) sync(events []directoryEvent) error {
	response, err := syncer.request(http.MethodGet, "get-ldap-users", syncer.settings.ldapID, nil)
	if err != nil {
		return err
	}
	var directory struct {
		Users json.RawMessage `json:"users"`
	}
	if err := json.Unmarshal(response.Data, &directory); err != nil {
		return fmt.Errorf("decode Casdoor LDAP users: %w", err)
	}
	if len(directory.Users) == 0 || directory.Users[0] != '[' {
		return fmt.Errorf("Casdoor LDAP response did not contain a user array")
	}
	if _, err = syncer.request(http.MethodPost, "sync-ldap-users", syncer.settings.ldapID, bytes.NewReader(directory.Users)); err != nil {
		return err
	}

	var users []casdoorDirectoryUser
	if err := json.Unmarshal(directory.Users, &users); err != nil {
		return fmt.Errorf("decode Casdoor LDAP user profiles: %w", err)
	}
	changedNames := directoryEventNames(events)
	owner, _, ok := strings.Cut(syncer.settings.ldapID, "/")
	if !ok || owner == "" {
		return fmt.Errorf("invalid Casdoor LDAP id %q", syncer.settings.ldapID)
	}
	for _, user := range users {
		if !changedNames[strings.ToLower(user.UID)] && !changedNames[strings.ToLower(user.CN)] {
			continue
		}
		username := user.UID
		if username == "" {
			username = user.CN
		}
		if username == "" {
			continue
		}
		displayName := user.DisplayName
		if displayName == "" {
			displayName = user.CN
		}
		profile, err := json.Marshal(map[string]string{
			"displayName": displayName,
			"email":       user.Email,
		})
		if err != nil {
			return err
		}
		query := url.Values{}
		query.Set("id", owner+"/"+username)
		query.Set("columns", "displayName,email")
		if _, err := syncer.requestWithQuery(http.MethodPost, "update-user", query, bytes.NewReader(profile)); err != nil {
			return fmt.Errorf("refresh Casdoor LDAP profile %s: %w", username, err)
		}
	}
	return nil
}

func directoryEventNames(events []directoryEvent) map[string]bool {
	result := map[string]bool{}
	for _, event := range events {
		firstRDN, _, _ := strings.Cut(event.DN, ",")
		_, value, ok := strings.Cut(firstRDN, "=")
		if ok && strings.TrimSpace(value) != "" {
			result[strings.ToLower(strings.TrimSpace(value))] = true
		}
	}
	return result
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
	pendingEvents []directoryEvent
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

func (watcher *directoryWatcher) poll(now time.Time) (bool, error) {
	events, err := watcher.reader.events()
	if err != nil {
		return false, err
	}
	matched := false
	for _, event := range events {
		if event.Seq > watcher.uncommitted {
			watcher.uncommitted = event.Seq
		}
		if interestingDirectoryEvent(event, watcher.settings) {
			matched = true
			watcher.pendingEvents = append(watcher.pendingEvents, event)
			log.Printf("directory change seq=%d op=%s dn=%s attrs=%s", event.Seq, event.Operation, event.DN, strings.Join(event.Attributes, ","))
		}
	}
	if matched && watcher.pendingSince.IsZero() {
		watcher.pendingSince = now
	}
	if !matched && watcher.uncommitted > watcher.cursor && watcher.pendingSince.IsZero() {
		if err := watcher.commit(watcher.uncommitted); err != nil {
			return false, err
		}
	}
	if watcher.pendingSince.IsZero() || now.Sub(watcher.pendingSince) < watcher.settings.debounce {
		return false, nil
	}
	if !watcher.lastTriggered.IsZero() && now.Sub(watcher.lastTriggered) < watcher.settings.minimumInterval {
		return false, nil
	}
	if err := watcher.syncer.sync(watcher.pendingEvents); err != nil {
		return false, err
	}
	watcher.pendingSince = time.Time{}
	watcher.pendingEvents = nil
	watcher.lastTriggered = now
	watcher.health.LastTriggerAt = time.Now().Unix()
	watcher.health.TriggerCount++
	if err := watcher.commit(watcher.uncommitted); err != nil {
		return false, err
	}
	return true, nil
}

func (watcher *directoryWatcher) commit(cursor int64) error {
	if err := writeJSONAtomic(watcher.settings.cursorFile, map[string]int64{"seq": cursor}); err != nil {
		return err
	}
	watcher.cursor = cursor
	watcher.reader.cursor = cursor
	watcher.health.Cursor = cursor
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
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".anas-dirwatch-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0600); err != nil {
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
	return os.Rename(temporaryName, path)
}

func runDirectoryWatch(args []string) error {
	settings, err := directoryWatchSettingsFromEnv()
	if err != nil {
		return err
	}
	if len(args) == 1 && args[0] == "--healthcheck" {
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
	if len(args) == 2 && args[0] == "--get-user" {
		syncer := &casdoorLDAPSyncer{settings: settings, client: &http.Client{Timeout: 15 * time.Second}}
		response, err := syncer.request(http.MethodGet, "get-user", args[1], nil)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(append(response.Data, '\n'))
		return err
	}
	if len(args) != 0 {
		return fmt.Errorf("directory-watch accepts only --healthcheck or --get-user ID")
	}

	syncer := &casdoorLDAPSyncer{settings: settings, client: &http.Client{Timeout: 2 * time.Minute}}
	watcher := newDirectoryWatcher(settings, syncer)
	defer watcher.reader.close()
	log.Printf("watching %s for ldap=%s (debounce=%s min-interval=%s)", settings.eventFile, settings.ldapID, settings.debounce, settings.minimumInterval)
	for {
		_, err := watcher.poll(time.Now())
		if err != nil {
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
