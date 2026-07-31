package runner

// The machine-readable CLI contract from docs/contracts/README.md.
//
// Two rules drive everything here. stdout carries exactly one JSON document so
// a caller can hand it straight to a JSON parser without splitting lines or
// filtering; progress, warnings and logs go to stderr. And codes are enumerated
// machine values rather than prose, so a caller branches on `code` and only
// ever shows `message` to a human — which is what lets a second language be
// added without touching any output logic.

import (
	"encoding/json"
	"fmt"
	"os"
)

const cliAPIVersion = "anas.dev/cli/v1"

// Exit codes. A non-interactive caller needs to distinguish "you asked wrong"
// from "the machine is not in a state where this can work", because only the
// second is worth retrying later.
const (
	exitFailure      = 1 // failed while executing
	exitUsage        = 2 // missing, conflicting or unrecognised arguments
	exitConfirmation = 3 // needs -y, or stdin is not a tty
	exitPrecondition = 4 // not Btrfs, snapshot missing, target unwritable
)

// CLIError is an error that already carries its enumerated code and exit
// status. Reported records that the command has already written its JSON
// document to stdout, so the process wrapper must not print the message a
// second time on stderr.
type CLIError struct {
	Code     string
	Message  string
	Detail   map[string]any
	Exit     int
	Reported bool
}

func (e *CLIError) Error() string { return e.Message }

func cliErrorf(exit int, code, format string, args ...any) *CLIError {
	return &CLIError{Code: code, Message: fmt.Sprintf(format, args...), Exit: exit}
}

func usageErrorf(format string, args ...any) *CLIError {
	return cliErrorf(exitUsage, "usage", format, args...)
}

func preconditionErrorf(code, format string, args ...any) *CLIError {
	return cliErrorf(exitPrecondition, code, format, args...)
}

func failuref(code, format string, args ...any) *CLIError {
	return cliErrorf(exitFailure, code, format, args...)
}

func confirmationErrorf(format string, args ...any) *CLIError {
	return cliErrorf(exitConfirmation, "confirmation_required", format, args...)
}

// ExitCode maps an error to the process exit status. Anything that did not
// classify itself is an ordinary execution failure.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if e, ok := err.(*CLIError); ok && e.Exit != 0 {
		return e.Exit
	}
	return exitFailure
}

// Reported reports whether the command already emitted its result document.
func Reported(err error) bool {
	e, ok := err.(*CLIError)
	return ok && e.Reported
}

// wantsJSON is scanned from the raw arguments rather than read from a parsed
// flag because the output format has to be known before parsing can fail: a
// caller that passed --json is entitled to a JSON document describing the usage
// error too.
func wantsJSON(args []string) bool {
	for _, arg := range args {
		if arg == "--json" || arg == "-json" {
			return true
		}
		if arg == "--" {
			return false
		}
	}
	return false
}

func emitJSON(value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(os.Stdout, "%s\n", b)
	return err
}

// emitJSONError writes the failure document and marks the error as reported so
// it is not also printed on stderr. A non-zero exit still leaves a parseable
// document on stdout, which is the whole point of the split.
func emitJSONError(err error) error {
	e, ok := err.(*CLIError)
	if !ok {
		e = failuref("internal", "%s", err.Error())
	}
	document := map[string]any{
		"api_version": cliAPIVersion,
		"ok":          false,
		"error": map[string]any{
			"code":    e.Code,
			"message": e.Message,
		},
	}
	if len(e.Detail) > 0 {
		document["error"].(map[string]any)["detail"] = e.Detail
	}
	_ = emitJSON(document)
	reported := *e
	reported.Reported = true
	return &reported
}

// emitProgress writes one JSON Lines record to stderr. total is omitted when
// unknown rather than written as 0 or -1, both of which a caller would have to
// special-case as if they were real magnitudes.
func emitProgress(jsonMode bool, phase string, current, total int64, unit string) {
	if !jsonMode {
		return
	}
	record := map[string]any{"type": "progress", "phase": phase, "current": current, "unit": unit}
	if total > 0 {
		record["total"] = total
	}
	b, err := json.Marshal(record)
	if err != nil {
		return
	}
	fmt.Fprintf(os.Stderr, "%s\n", b)
}

// confirmDestructive enforces the contract's confirmation rule: -y is the only
// bypass, and a non-tty caller fails immediately with exit code 3 rather than
// blocking on input nobody is there to provide.
func confirmDestructive(prompt string, yes bool) error {
	if yes {
		return nil
	}
	if !isTerminal(os.Stdin.Fd()) {
		return confirmationErrorf("%s requires -y when stdin is not a terminal", prompt)
	}
	ok, err := confirm(prompt, false)
	if err != nil {
		return failuref("stdin_unavailable", "%s", err.Error())
	}
	if !ok {
		return confirmationErrorf("%s was declined", prompt)
	}
	return nil
}
