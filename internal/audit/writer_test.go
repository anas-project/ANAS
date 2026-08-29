package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOpenCreatesSecureDirectoryAndFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "console", "audit")
	writer, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	dirInfo, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !dirInfo.IsDir() || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode = %v, want directory 0700", dirInfo.Mode())
	}
	fileInfo, err := os.Lstat(filepath.Join(dir, Filename))
	if err != nil {
		t.Fatal(err)
	}
	if !fileInfo.Mode().IsRegular() || fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, want regular 0600", fileInfo.Mode())
	}
	lockInfo, err := os.Lstat(filepath.Join(dir, lockFilename))
	if err != nil {
		t.Fatal(err)
	}
	if !lockInfo.Mode().IsRegular() || lockInfo.Mode().Perm() != 0o600 {
		t.Fatalf("lock mode = %v, want regular 0600", lockInfo.Mode())
	}
}

func TestOpenRejectsInsecureExistingPaths(t *testing.T) {
	t.Run("wide directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "audit")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dir); err == nil || !strings.Contains(err.Error(), "0700") {
			t.Fatalf("Open error = %v, want directory mode rejection", err)
		}
	})

	t.Run("wide file", func(t *testing.T) {
		dir := secureTestDir(t)
		if err := os.WriteFile(filepath.Join(dir, Filename), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(dir, Filename), 0o640); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dir); err == nil || !strings.Contains(err.Error(), "0600") {
			t.Fatalf("Open error = %v, want file mode rejection", err)
		}
	})

	t.Run("non-regular file", func(t *testing.T) {
		dir := secureTestDir(t)
		if err := os.Mkdir(filepath.Join(dir, Filename), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dir); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("Open error = %v, want regular file rejection", err)
		}
	})

	if runtime.GOOS != "windows" {
		t.Run("symlink directory", func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "target")
			if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, "link")
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(link); err == nil || !strings.Contains(err.Error(), "non-symlink directory") {
				t.Fatalf("Open error = %v, want symlink rejection", err)
			}
		})

		t.Run("symlink file", func(t *testing.T) {
			dir := secureTestDir(t)
			target := filepath.Join(t.TempDir(), "target.jsonl")
			if err := os.WriteFile(target, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(dir, Filename)); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(dir); err == nil || !strings.Contains(err.Error(), "regular file") {
				t.Fatalf("Open error = %v, want symlink rejection", err)
			}
		})
	}
}

func TestAppendAssignsDurableIncreasingSequences(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, time.August, 29, 4, 5, 6, 7, time.FixedZone("fixture", 8*60*60))
	writer.now = func() time.Time { return fixed }
	first, err := writer.Append(Event{Type: "bootstrap.token.issued", Actor: "local-cli"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := writer.Append(Event{Type: "bootstrap.token.exchanged", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Sequence != 1 || second.Sequence != 2 {
		t.Fatalf("sequences = %d, %d", first.Sequence, second.Sequence)
	}
	if !first.Timestamp.Equal(fixed) || first.Timestamp.Location() != time.UTC {
		t.Fatalf("timestamp = %v, want %v in UTC", first.Timestamp, fixed)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	third, err := reopened.Append(Event{Type: "bootstrap.session.revoked"})
	if err != nil {
		t.Fatal(err)
	}
	if third.Sequence != 3 {
		t.Fatalf("reopened sequence = %d, want 3", third.Sequence)
	}

	events := readEvents(t, filepath.Join(dir, Filename))
	if got := []uint64{events[0].Sequence, events[1].Sequence, events[2].Sequence}; !reflect.DeepEqual(got, []uint64{1, 2, 3}) {
		t.Fatalf("persisted sequences = %v", got)
	}
}

func TestAppendSerializesConcurrentWriters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	const count = 64
	sequences := make(chan uint64, count)
	errorsFound := make(chan error, count)
	var group sync.WaitGroup
	for index := 0; index < count; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			event, err := writer.Append(Event{Type: "concurrent", Details: map[string]any{"index": index}})
			if err != nil {
				errorsFound <- err
				return
			}
			sequences <- event.Sequence
		}(index)
	}
	group.Wait()
	close(sequences)
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	got := make([]int, 0, count)
	for sequence := range sequences {
		got = append(got, int(sequence))
	}
	sort.Ints(got)
	for index, sequence := range got {
		if sequence != index+1 {
			t.Fatalf("sorted sequences[%d] = %d", index, sequence)
		}
	}
	if events := readEvents(t, filepath.Join(dir, Filename)); len(events) != count {
		t.Fatalf("persisted events = %d, want %d", len(events), count)
	}
}

func TestAppendSerializesIndependentOpenWriters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	const perWriter = 20
	errorsFound := make(chan error, perWriter*2)
	var group sync.WaitGroup
	for _, writer := range []*Writer{first, second} {
		for index := 0; index < perWriter; index++ {
			group.Add(1)
			go func(writer *Writer, index int) {
				defer group.Done()
				_, err := writer.Append(Event{Type: "multi-process-shape", Details: map[string]any{"index": index}})
				if err != nil {
					errorsFound <- err
				}
			}(writer, index)
		}
	}
	group.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	events := readEvents(t, filepath.Join(dir, Filename))
	if len(events) != perWriter*2 {
		t.Fatalf("persisted events = %d, want %d", len(events), perWriter*2)
	}
	for index, event := range events {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("event %d sequence = %d", index, event.Sequence)
		}
	}
}

func TestAppendRedactsNestedCredentialsAndFreeTextPatterns(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	secrets := []string{
		"password-value-7f91",
		"api-token-value-8862",
		"authorization-value-3190",
		"cookie-value-7712",
		"csrf-value-0091",
		"handoff-value-1928",
		"message-token-6148",
		"message-password-8490",
		"message-bearer-1155",
		"map-key-secret-5744",
		"client-secret-3430",
		"api-key-secret-1802",
		"camel-token-secret-6712",
		"private-key-secret-9901",
		"compound-handoff-secret-4438",
		"spaced-password-tail-8834",
		"pwd-secret-7311",
		"passphrase-secret-4209",
		"otp-secret-8086",
		"assertion-secret-1120",
	}
	event, err := writer.Append(Event{
		Type:      "credential.audit",
		Actor:     "operator",
		Outcome:   "Authorization: Bearer " + secrets[2],
		Timestamp: time.Date(2026, time.August, 29, 0, 0, 0, 0, time.UTC),
		Details: map[string]any{
			"password":                            secrets[0],
			"pwd":                                 secrets[16],
			"otp_code":                            secrets[18],
			"saml_assertion":                      secrets[19],
			"Authorization: Bearer " + secrets[9]: "value hidden with its sensitive key",
			"nested": map[string]any{
				"api_token": secrets[1],
				"headers": map[string]string{
					"Authorization": "Bearer " + secrets[2],
					"Cookie":        "session=" + secrets[3],
				},
			},
			"items": []any{
				map[string]any{"csrf-token": secrets[4]},
				map[string]any{"handoff": secrets[5]},
			},
			"messages": []string{
				"token=" + secrets[6] + " password=" + secrets[7] + " Bearer " + secrets[8],
				`client_secret="` + secrets[10] + `"`,
				"x-api-key: " + secrets[11],
				"authToken=" + secrets[12],
				"private-key=" + secrets[13],
				"bootstrap_handoff_token=" + secrets[14],
				"password=prefix-value " + secrets[15] + "; safe-after-delimiter",
				"passphrase=" + secrets[17],
			},
			"safe": "visible-value",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.Details["safe"] != "visible-value" {
		t.Fatalf("safe field = %#v", event.Details["safe"])
	}

	body, err := os.ReadFile(filepath.Join(dir, Filename))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if strings.Contains(string(body), secret) {
			t.Errorf("audit log contains raw credential %q", secret)
		}
	}
	if !strings.Contains(string(body), "redacted") || !strings.Contains(string(body), "visible-value") {
		t.Fatalf("audit log did not preserve safe data and redaction marker: %s", body)
	}
	returned, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(returned), secrets[9]) {
		t.Fatalf("returned event contains raw credential-bearing map key: %s", returned)
	}
}

func TestSanitizeMapKeyCollisionsAreDeterministicAndLossless(t *testing.T) {
	input := Event{Type: "collision", Details: map[string]any{
		"<redacted-key>":   "safe-base",
		"<redacted-key>#2": "safe-suffix",
		"api_token":        "first-secret",
		"client_secret":    "second-secret",
	}}
	first, err := sanitizeEvent(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := sanitizeEvent(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Details, second.Details) {
		t.Fatalf("sanitized maps differ: %#v != %#v", first.Details, second.Details)
	}
	want := map[string]any{
		"<redacted-key>":   "safe-base",
		"<redacted-key>#2": "safe-suffix",
		"<redacted-key>#3": Redacted,
		"<redacted-key>#4": Redacted,
	}
	if !reflect.DeepEqual(first.Details, want) {
		t.Fatalf("sanitized details = %#v, want %#v", first.Details, want)
	}
	body, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"api_token", "client_secret", "first-secret", "second-secret"} {
		if strings.Contains(string(body), leaked) {
			t.Fatalf("sanitized event leaked %q: %s", leaked, body)
		}
	}
}

func TestOpenTruncatesOnlyIncompleteFinalTail(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(Event{Type: "complete"}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, Filename)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"sequence":2,"timestamp":"2026-08-29T00:00:00Z"`); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	recovered, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("recovered log = %q, want %q", after, before)
	}
	event, err := recovered.Append(Event{Type: "after-recovery"})
	if err != nil {
		t.Fatal(err)
	}
	if event.Sequence != 2 {
		t.Fatalf("sequence after recovery = %d, want 2", event.Sequence)
	}
}

func TestOpenRejectsCompleteCorruptOrOutOfSequenceLines(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{
			name: "middle corruption",
			body: validLine(1, "first") + "{not-json}\n" + validLine(2, "third"),
		},
		{
			name: "sequence gap",
			body: validLine(1, "first") + validLine(3, "third"),
		},
		{
			name: "empty line",
			body: validLine(1, "first") + "\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := secureTestDir(t)
			if err := os.WriteFile(filepath.Join(dir, Filename), []byte(test.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(dir); err == nil {
				t.Fatal("Open succeeded for corrupt complete record")
			}
		})
	}
}

func TestAppendSyncsBeforeSuccessAndFailsClosed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	recording := &recordingLogFile{logFile: writer.file}
	writer.file = recording
	if _, err := writer.Append(Event{Type: "synced"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recording.calls, []string{"write", "sync"}) {
		t.Fatalf("calls = %v, want write then sync", recording.calls)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	dir = filepath.Join(t.TempDir(), "audit")
	writer, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	recording = &recordingLogFile{logFile: writer.file, syncErr: errors.New("disk unavailable")}
	writer.file = recording
	if _, err := writer.Append(Event{Type: "must-fail"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Append error = %v, want ErrUnavailable", err)
	}
	callsAfterFailure := len(recording.calls)
	if _, err := writer.Append(Event{Type: "must-stay-failed"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("second Append error = %v, want ErrUnavailable", err)
	}
	if len(recording.calls) != callsAfterFailure {
		t.Fatalf("unavailable writer performed more I/O: %v", recording.calls)
	}
	_ = writer.Close()
}

func TestAppendRejectsInvalidEventsWithoutWriting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for _, event := range []Event{
		{},
		{Sequence: 10, Type: "caller-sequence"},
		{Type: "unsupported", Details: map[string]any{"channel": make(chan int)}},
	} {
		if _, err := writer.Append(event); !errors.Is(err, ErrInvalidEvent) {
			t.Errorf("Append(%#v) error = %v, want ErrInvalidEvent", event, err)
		}
	}
	if body, err := os.ReadFile(filepath.Join(dir, Filename)); err != nil || len(body) != 0 {
		t.Fatalf("log after invalid events = %q, %v", body, err)
	}
}

func TestAppendAfterCloseReturnsUnavailable(t *testing.T) {
	writer, err := Open(filepath.Join(t.TempDir(), "audit"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(Event{Type: "after-close"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Append error = %v, want ErrUnavailable", err)
	}
}

type recordingLogFile struct {
	logFile
	calls   []string
	syncErr error
}

func (file *recordingLogFile) Write(body []byte) (int, error) {
	file.calls = append(file.calls, "write")
	return file.logFile.Write(body)
}

func (file *recordingLogFile) Sync() error {
	file.calls = append(file.calls, "sync")
	if file.syncErr != nil {
		return file.syncErr
	}
	return file.logFile.Sync()
}

func secureTestDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "audit")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readEvents(t *testing.T, path string) []Event {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	events := make([]Event, len(lines))
	for index, line := range lines {
		if err := json.Unmarshal([]byte(line), &events[index]); err != nil {
			t.Fatalf("decode line %d: %v", index+1, err)
		}
	}
	return events
}

func validLine(sequence uint64, eventType string) string {
	return fmt.Sprintf(`{"sequence":%d,"timestamp":"2026-08-29T00:00:00Z","type":%q}`+"\n", sequence, eventType)
}
