package compose

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFailureTailSanitizesSplitControlsAndBoundsUnicode(t *testing.T) {
	w := &tailWriter{}
	for _, s := range []string{strings.Repeat("界", 3000), "\x1b[", "31mfailure\r\n", "\x1b]hidden", "\x1b", "\\visible"} {
		_, _ = w.Write([]byte(s))
	}
	err := commandFailure(errors.New("exit"), "project", []string{"up"}, w).(*CommandFailure)
	if !err.Truncated || len(err.Summary) > failureTailLimit || !utf8.ValidString(err.Summary) || strings.ContainsAny(err.Summary, "\x1b\r") || strings.Contains(err.Summary, "hidden") || !strings.HasSuffix(err.Summary, "failure\n\nvisible") {
		t.Fatalf("invalid tail: %#v", err)
	}
}

func TestCommandFailureKeepsStatusAndQuietDiscardsText(t *testing.T) {
	for _, capture := range []bool{true, false} {
		cmd := exec.Command("sh", "-c", "printf PRIVATE >&2; exit 17")
		err := runCommand(cmd, "project", []string{"up"}, capture)
		var failure *CommandFailure
		var exit *exec.ExitError
		if !errors.As(err, &failure) || !errors.As(err, &exit) || failure.ExitCode != 17 || failure.Phase != "up" || failure.Project != "project" {
			t.Fatalf("lost process context: %v", err)
		}
		if strings.Contains(err.Error(), "PRIVATE") != capture {
			t.Fatalf("capture=%v: %v", capture, err)
		}
	}
}
