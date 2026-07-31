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
	"flag"
	"fmt"
	"os"
	"path/filepath"
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

// emitOK writes the success document. Every command routes its result through
// here so `api_version` and `ok` cannot be forgotten on one path and present on
// another — a caller that has to check whether the envelope exists has no
// envelope.
func emitOK(fields map[string]any) error {
	document := map[string]any{"api_version": cliAPIVersion, "ok": true}
	for key, value := range fields {
		document[key] = value
	}
	return emitJSON(document)
}

// emitEmptyOK is the whole result for a command that has nothing to report but
// whether it worked — `stop` either stopped the containers or did not. The
// alternative was inventing a payload so the document would look substantial,
// which would give a caller fields it must not come to depend on.
func emitEmptyOK(jsonMode bool, fields map[string]any) error {
	if !jsonMode {
		return nil
	}
	return emitOK(fields)
}

// registerJSONFlag declares --json on a command's own flag set. wantsJSON has
// already scanned the raw arguments by the time parsing happens, so this exists
// to stop the flag package rejecting a flag the contract requires, not to
// discover the mode.
func registerJSONFlag(fs *flag.FlagSet) {
	fs.Bool("json", false, "machine-readable output")
}

// absolutePath enforces the contract's path rule in one place. A relative path
// is meaningless to a caller that did not share this process's working
// directory, which is every caller the contract is written for.
func absolutePath(path string) string {
	if path == "" {
		return ""
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return absolute
}

// absolutePaths maps absolutePath over a list, preserving order.
func absolutePaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		out = append(out, absolutePath(path))
	}
	return out
}

// emitWarning writes one warning to stderr: a JSON Lines record under --json,
// prose otherwise. The code is the enumeration a caller branches on; the
// message is only ever shown to a human.
func emitWarning(jsonMode bool, code, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	if !jsonMode {
		fmt.Fprintf(os.Stderr, "warning: %s\n", message)
		return
	}
	b, err := json.Marshal(map[string]any{"type": "warning", "code": code, "message": message})
	if err != nil {
		return
	}
	fmt.Fprintf(os.Stderr, "%s\n", b)
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
