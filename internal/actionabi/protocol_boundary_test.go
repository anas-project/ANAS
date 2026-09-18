package actionabi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// Partial boundary coverage: ACTABI-R-002 (bounded replay), ACTABI-R-003
// (confirmed cancellation), ACTABI-R-004/R-005 (bounded control data and
// exact progress). These tests do not establish host or process acceptance.
func TestActionRequestClosedSchema(t *testing.T) {
	base := `{"abi":"anas.action/v1","job_id":"job-1","invocation_id":"call-1","action":"module.incus.status","parameters":{}}`
	cases := map[string]string{
		"duplicate":         strings.Replace(base, `"job_id":"job-1"`, `"job_id":"job-1","job_id":"job-2"`, 1) + "\n",
		"escaped_duplicate": strings.Replace(base, `"job_id":"job-1"`, `"job_id":"job-1","\u006aob_id":"job-2"`, 1) + "\n",
		"case_alias":        strings.Replace(base, `"job_id"`, `"Job_ID"`, 1) + "\n",
		"unknown":           strings.Replace(base, `"parameters":{}`, `"parameters":{},"command":"not-accepted"`, 1) + "\n",
		"null":              strings.Replace(base, `"parameters":{}`, `"parameters":null`, 1) + "\n",
		"array":             strings.Replace(base, `"parameters":{}`, `"parameters":[]`, 1) + "\n",
		"missing":           strings.Replace(base, `,"parameters":{}`, "", 1) + "\n",
		"missing_lf":        base,
		"crlf":              base + "\r\n",
		"extra_line":        base + "\n\n",
		"extra_json":        base + " {}\n",
		"nested_duplicate":  strings.Replace(base, `"parameters":{}`, `"parameters":{"target":{"name":1,"name":2}}`, 1) + "\n",
		"too_deep":          strings.Replace(base, `"parameters":{}`, `"parameters":{"nested":`+strings.Repeat("[", 18)+"0"+strings.Repeat("]", 18)+`}`, 1) + "\n",
		"invalid_utf8":      strings.Replace(base, `"parameters":{}`, "\"parameters\":{\"value\":\""+string([]byte{0xff})+"\"}", 1) + "\n",
		"object_too_large":  strings.Replace(base, `"parameters":{}`, `"parameters":{"value":"`+strings.Repeat("a", MaxObjectBytes)+`"}`, 1) + "\n",
		"frame_too_large":   strings.Repeat(" ", MaxFrameBytes) + base + "\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeRequest([]byte(body)); !errors.Is(err, ErrProtocol) {
				t.Fatalf("invalid request accepted: %v", err)
			}
		})
	}
	request, err := ReadRequest(strings.NewReader(base + "\n"))
	if err != nil || request.JobID != "job-1" {
		t.Fatalf("valid request: %v", err)
	}
	encoded, err := EncodeRequest(request)
	if err != nil || !bytes.Equal(encoded, []byte(base+"\n")) {
		t.Fatalf("request round trip failed: %v", err)
	}
}

func TestActionExecutorCannotAssignSequenceOrTruncation(t *testing.T) {
	base := `{"abi":"anas.action/v1","job_id":"job-1","invocation_id":"call-1","type":"progress","progress":{"phase":"checking"}}`
	for _, sequence := range []string{"0", "1", "null"} {
		body := strings.Replace(base, `"type":"progress"`, `"seq":`+sequence+`,"type":"progress"`, 1) + "\n"
		if _, err := DecodeExecutorEvent([]byte(body)); !errors.Is(err, ErrProtocol) {
			t.Fatalf("executor assigned seq %s: %v", sequence, err)
		}
	}
	marker := Event{ABI: Version, JobID: "job-1", InvocationID: "call-1", Seq: 10, Type: "truncated", Truncated: &Truncated{FromSeq: 1, ThroughSeq: 9}}
	frame, err := EncodeJournalEvent(marker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeExecutorEvent(frame); !errors.Is(err, ErrProtocol) {
		t.Fatal("executor was allowed to hide missing output")
	}
	if _, err := DecodeJournalEvent([]byte(base + "\n")); !errors.Is(err, ErrProtocol) {
		t.Fatal("journal accepted unsequenced event")
	}
}

func TestActionProgressPreservesExactCounters(t *testing.T) {
	current := ^uint64(0)
	estimated := uint64(2)
	event := boundaryProgress()
	event.Progress.Current, event.Progress.TotalEstimated = &current, &estimated
	event.Progress.Unit = "bytes"
	frame, err := EncodeExecutorEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeExecutorEvent(frame)
	if err != nil || *decoded.Progress.Current != current || *decoded.Progress.TotalEstimated != estimated {
		t.Fatalf("integer counter was lost or clamped: %v", err)
	}
	event.Progress.TotalEstimated = nil
	event.Progress.Total = &estimated
	if _, err := EncodeExecutorEvent(event); !errors.Is(err, ErrProtocol) {
		t.Fatal("exact total smaller than current accepted")
	}
	event.Progress.TotalEstimated = &estimated
	if _, err := EncodeExecutorEvent(event); !errors.Is(err, ErrProtocol) {
		t.Fatal("both total forms accepted")
	}
}

func TestActionTerminalRequiresEOFAndExitEvidence(t *testing.T) {
	cases := []struct {
		name    string
		outcome Outcome
		exit    ExitState
		readEOF bool
		want    Outcome
	}{
		{"success", Succeeded, ExitState{ProcessExited: true, ExitCode: 0}, true, Succeeded},
		{"failure", Failed, ExitState{ProcessExited: true, ExitCode: 1}, true, Failed},
		{"confirmed_cancel", Cancelled, ExitState{ProcessExited: true, ExitCode: 0, CancelRequested: true}, true, Cancelled},
		{"cancel_without_request", Cancelled, ExitState{ProcessExited: true, ExitCode: 0}, true, Unknown},
		{"forced_after_success", Succeeded, ExitState{ProcessExited: true, ExitCode: 0, Forced: true}, true, Unknown},
		{"forced_cancel", Cancelled, ExitState{ProcessExited: true, ExitCode: 0, Forced: true, CancelRequested: true}, true, Unknown},
		{"not_reaped", Succeeded, ExitState{ExitCode: 0}, true, Unknown},
		{"signaled", Failed, ExitState{ProcessExited: true, ExitCode: -1}, true, Unknown},
		{"contradictory_success", Succeeded, ExitState{ProcessExited: true, ExitCode: 1}, true, Unknown},
		{"contradictory_failure", Failed, ExitState{ProcessExited: true, ExitCode: 0}, true, Unknown},
		{"claimed_but_unread_eof", Succeeded, ExitState{ProcessExited: true, StreamEOF: true, ExitCode: 0}, false, Unknown},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			frame := boundaryFrame(t, boundaryTerminal(test.outcome))
			reader, err := NewExecutionReader(bytes.NewReader(frame), "job-1", "call-1")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reader.Next(); err != nil {
				t.Fatal(err)
			}
			if test.readEOF {
				if _, err := reader.Next(); err != io.EOF {
					t.Fatalf("expected EOF: %v", err)
				}
			}
			outcome, err := reader.Finish(test.exit)
			if outcome != test.want || (test.want == Unknown && !errors.Is(err, ErrUnknownOutcome)) || (test.want != Unknown && err != nil) {
				t.Fatalf("outcome %s, error %v; wanted %s", outcome, err, test.want)
			}
			if _, err := reader.Finish(test.exit); !errors.Is(err, ErrProtocol) {
				t.Fatal("second terminal completion accepted")
			}
		})
	}
}

func TestActionInvalidTrailingOutputPoisonsSuccess(t *testing.T) {
	terminal := boundaryFrame(t, boundaryTerminal(Succeeded))
	progress := boundaryFrame(t, boundaryProgress())
	for name, tail := range map[string][]byte{
		"trailing_event":     progress,
		"duplicate_terminal": terminal,
		"invalid_json":       []byte("not-json\n"),
		"unterminated_json":  []byte("{}"),
		"blank_line":         []byte("\n"),
	} {
		t.Run(name, func(t *testing.T) {
			body := append(append([]byte(nil), terminal...), tail...)
			reader, err := NewExecutionReader(bytes.NewReader(body), "job-1", "call-1")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reader.Next(); err != nil {
				t.Fatal(err)
			}
			if _, err := reader.Next(); !errors.Is(err, ErrProtocol) {
				t.Fatalf("trailing output not rejected: %v", err)
			}
			// A caller continuing after an error cannot turn it into success.
			_, _ = reader.Next()
			outcome, err := reader.Finish(ExitState{ProcessExited: true, StreamEOF: true, ExitCode: 0})
			if outcome != Unknown || !errors.Is(err, ErrUnknownOutcome) {
				t.Fatalf("trailing output became success: %s %v", outcome, err)
			}
		})
	}
}

func TestActionReplayRequiresRealTruncationAndBinding(t *testing.T) {
	marker := Event{ABI: Version, JobID: "job-1", InvocationID: "call-1", Seq: 10, Type: "truncated", Truncated: &Truncated{FromSeq: 1, ThroughSeq: 9}}
	for _, from := range []uint64{0, 5, 9} {
		stream, err := NewReplayStream("job-1", "call-1", from)
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.Accept(marker); err != nil {
			t.Fatalf("valid marker after %d: %v", from, err)
		}
		next := boundaryTerminal(Succeeded)
		next.Seq = 11
		if err := stream.Accept(next); err != nil {
			t.Fatal(err)
		}
		if err := stream.Accept(next); !errors.Is(err, ErrProtocol) {
			t.Fatal("duplicate terminal accepted")
		}
	}
	for name, event := range map[string]Event{
		"unmarked_gap":      {ABI: Version, JobID: "job-1", InvocationID: "call-1", Seq: 10, Type: "progress", Progress: &Progress{Phase: "checking"}},
		"wrong_invocation":  {ABI: Version, JobID: "job-1", InvocationID: "call-2", Seq: 1, Type: "progress", Progress: &Progress{Phase: "checking"}},
		"incomplete_marker": {ABI: Version, JobID: "job-1", InvocationID: "call-1", Seq: 10, Type: "truncated", Truncated: &Truncated{FromSeq: 2, ThroughSeq: 9}},
	} {
		t.Run(name, func(t *testing.T) {
			stream, _ := NewReplayStream("job-1", "call-1", 0)
			if err := stream.Accept(event); !errors.Is(err, ErrProtocol) {
				t.Fatalf("invalid replay accepted: %v", err)
			}
			valid := boundaryProgress()
			valid.Seq = 1
			if err := stream.Accept(valid); !errors.Is(err, ErrProtocol) {
				t.Fatal("poisoned stream resumed")
			}
		})
	}
}

func TestActionFormattingNeverIncludesOpaquePayload(t *testing.T) {
	const secret = "private-sentinel-DO-NOT-LOG"
	request := Request{ABI: Version, JobID: "job-1", InvocationID: "call-1", Action: "module.incus.status", Parameters: json.RawMessage(`{"text":"` + secret + `"}`)}
	event := boundaryTerminal(Succeeded)
	event.Result.Value = request.Parameters
	for _, value := range []any{request, event} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
			if strings.Contains(fmt.Sprintf(format, value), secret) {
				t.Fatalf("format %s leaked payload", format)
			}
		}
	}
}

func boundaryProgress() Event {
	return Event{ABI: Version, JobID: "job-1", InvocationID: "call-1", Type: "progress", Progress: &Progress{Phase: "checking"}}
}

func boundaryTerminal(outcome Outcome) Event {
	event := Event{ABI: Version, JobID: "job-1", InvocationID: "call-1"}
	if outcome == Failed || outcome == Unknown {
		event.Type = "error"
		event.Error = &Failure{Outcome: outcome, Code: "operation_failed", Message: "Action failed"}
	} else {
		event.Type = "result"
		event.Result = &Result{Outcome: outcome}
		if outcome == Succeeded {
			changed := false
			event.Result.Changed = &changed
			event.Result.Value = json.RawMessage(`{}`)
		}
	}
	return event
}

func boundaryFrame(t *testing.T, event Event) []byte {
	t.Helper()
	body, err := EncodeExecutorEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
