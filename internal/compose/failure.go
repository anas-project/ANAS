package compose

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"unicode"
)

// CommandFailure contains only bounded diagnostic text. Quiet calls never
// allocate a capture buffer and therefore cannot retain sensitive output.
type CommandFailure struct {
	Project   string `json:"project"`
	Phase     string `json:"phase"`
	ExitCode  int    `json:"exit_code"`
	Summary   string `json:"summary,omitempty"`
	Truncated bool   `json:"truncated"`
	cause     error
}

func (f *CommandFailure) Error() string {
	if f.Summary == "" {
		return f.cause.Error()
	}
	return fmt.Sprintf("%v: %s", f.cause, f.Summary)
}
func (f *CommandFailure) Unwrap() error { return f.cause }

const failureTailLimit = 4096

// tailWriter strips CSI/OSC controls incrementally, including sequences split
// across writes, before keeping the bounded UTF-8 tail.
type tailWriter struct {
	data      []byte
	truncated bool
	state     byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		switch w.state {
		case 1:
			if b == '[' {
				w.state = 2
			} else if b == ']' {
				w.state = 3
			} else {
				w.state = 0
			}
			continue
		case 2:
			if b >= 0x40 && b <= 0x7e {
				w.state = 0
			}
			continue
		case 3:
			if b == 7 {
				w.state = 0
			} else if b == 27 {
				w.state = 4
			}
			continue
		case 4:
			if b == '\\' {
				w.state = 0
			} else {
				w.state = 3
			}
			continue
		}
		if b == 27 {
			w.state = 1
			continue
		}
		if b == '\r' {
			b = '\n'
		}
		if b < 32 && b != '\n' && b != '\t' || b == 127 {
			continue
		}
		w.data = append(w.data, b)
		if len(w.data) > failureTailLimit {
			w.data = w.data[len(w.data)-failureTailLimit:]
			w.truncated = true
		}
	}
	return len(p), nil
}
func commandFailure(err error, project string, args []string, tail *tailWriter) error {
	if err == nil {
		return nil
	}
	f := &CommandFailure{Project: project, ExitCode: -1, cause: err}
	if len(args) > 0 {
		f.Phase = args[0]
	}
	var exited *exec.ExitError
	if errors.As(err, &exited) {
		f.ExitCode = exited.ExitCode()
	}
	if tail != nil {
		f.Summary = strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				return -1
			}
			return r
		}, strings.ToValidUTF8(string(tail.data), "")))
		f.Truncated = tail.truncated
	}
	return f
}
func runCommand(cmd *exec.Cmd, project string, args []string, capture bool) error {
	var tail *tailWriter
	if capture {
		tail = &tailWriter{}
		if cmd.Stderr == nil {
			cmd.Stderr = tail
		} else {
			cmd.Stderr = io.MultiWriter(cmd.Stderr, tail)
		}
	}
	return commandFailure(cmd.Run(), project, args, tail)
}
