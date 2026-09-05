package httpapi

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// writeProblem literals are the console's user-facing error vocabulary. The
// frontend holds its own zh/en table (CONSOLE-R-128) and falls back to the bare
// enum when a code is missing, so a new route can silently ship an untranslated
// English identifier into a P0 confirmation flow. This gate keeps the two sides
// in step instead of relying on that fallback.
func TestProblemCodesHaveBilingualConsoleMessages(t *testing.T) {
	emitted := emittedProblemCodes(t)
	if len(emitted) < 50 {
		t.Fatalf("scanned %d problem codes, want the full handler vocabulary", len(emitted))
	}
	zh, en := consoleProblemMessageTables(t)
	if len(zh) == 0 || len(en) == 0 {
		t.Fatal("console problem message tables are empty")
	}
	var missing []string
	for _, code := range emitted {
		_, hasZH := zh[code]
		_, hasEN := en[code]
		if !hasZH || !hasEN {
			missing = append(missing, code)
		}
	}
	if len(missing) != 0 {
		t.Fatalf("web/src/api/problems.ts has no zh/en message for: %s", strings.Join(missing, ", "))
	}
}

var problemCodeLiteral = regexp.MustCompile(`writeProblem\((?:[^,]+,){2}\s*"([a-z][a-z0-9_]*)"`)

func emittedProblemCodes(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(packageDirectory(t))
	if err != nil {
		t.Fatal(err)
	}
	unique := map[string]struct{}{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(packageDirectory(t), name))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range problemCodeLiteral.FindAllStringSubmatch(string(body), -1) {
			unique[match[1]] = struct{}{}
		}
	}
	codes := make([]string, 0, len(unique))
	for code := range unique {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

var consoleMessageEntry = regexp.MustCompile(`(?m)^[ \t]+([a-z][a-z0-9_]*):\s*"`)

// consoleProblemMessageTables reads the two locale objects without a Node
// toolchain so the gate runs in the same `go test ./...` pass as the routes it
// guards.
func consoleProblemMessageTables(t *testing.T) (map[string]struct{}, map[string]struct{}) {
	t.Helper()
	body, err := os.ReadFile(filepath.Clean(filepath.Join(packageDirectory(t), "..", "..", "..", "web", "src", "api", "problems.ts")))
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	zhStart := strings.Index(source, "\n  zh: {")
	enStart := strings.Index(source, "\n  en: {")
	if zhStart < 0 || enStart < 0 || enStart < zhStart {
		t.Fatal("problems.ts does not declare zh and en message tables in that order")
	}
	collect := func(section string) map[string]struct{} {
		codes := map[string]struct{}{}
		for _, match := range consoleMessageEntry.FindAllStringSubmatch(section, -1) {
			codes[match[1]] = struct{}{}
		}
		return codes
	}
	return collect(source[zhStart:enStart]), collect(source[enStart:])
}

func packageDirectory(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate package source directory")
	}
	return filepath.Dir(source)
}
