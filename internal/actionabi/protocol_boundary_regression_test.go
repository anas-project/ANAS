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

// ACTABI-R-002/R-003: wire validation must not silently repair malformed input,
// trust executor-owned sequence numbers, or infer an outcome from disconnection.
func TestProtocolBoundaryRequestRejections(t *testing.T) {
	const valid = `{"abi":"anas.action/v1","job_id":"job-1","invocation_id":"call-1","action":"module.incus.status","parameters":{}}`
	cases := map[string]string{
		"missing LF":          valid,
		"CRLF":                valid + "\r\n",
		"empty line":          "\n",
		"extra JSON":          valid + "{}\n",
		"extra frame":         valid + "\n" + valid + "\n",
		"duplicate field":     strings.Replace(valid, `"job_id":"job-1"`, `"job_id":"job-1","job_id":"job-1"`, 1) + "\n",
		"escaped duplicate":   strings.Replace(valid, `"job_id":"job-1"`, `"job_id":"job-1","job\u005fid":"job-1"`, 1) + "\n",
		"case alias":          strings.Replace(valid, `"job_id"`, `"Job_ID"`, 1) + "\n",
		"unknown control":     strings.Replace(valid, `"parameters":{}`, `"parameters":{},"argv":[]`, 1) + "\n",
		"null parameters":     strings.Replace(valid, `"parameters":{}`, `"parameters":null`, 1) + "\n",
		"array parameters":    strings.Replace(valid, `"parameters":{}`, `"parameters":[]`, 1) + "\n",
		"duplicate parameter": strings.Replace(valid, `"parameters":{}`, `"parameters":{"x":1,"x":2}`, 1) + "\n",
		"oversize parameter":  strings.Replace(valid, `"parameters":{}`, `"parameters":{"x":"`+strings.Repeat("x", MaxObjectBytes)+`"}`, 1) + "\n",
		"deep parameter":      strings.Replace(valid, `"parameters":{}`, `"parameters":`+strings.Repeat(`{"x":`, 20)+`{}`+strings.Repeat("}", 20), 1) + "\n",
		"invalid UTF8":        strings.Replace(valid, `"parameters":{}`, "\"parameters\":{\"x\":\"\xff\"}", 1) + "\n",
	}
	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadRequest(strings.NewReader(wire)); !errors.Is(err, ErrProtocol) {
				t.Fatalf("expected protocol rejection, got %v", err)
			}
		})
	}
	if _, err := ReadRequest(strings.NewReader(valid + "\n")); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
}

func TestProtocolBoundaryObjectLimitAndRedaction(t *testing.T) {
	request := Request{ABI: Version, JobID: "job-1", InvocationID: "call-1", Action: "module.incus.status",
		Parameters: json.RawMessage(`{"v":"` + strings.Repeat("x", MaxObjectBytes-8) + `"}`)}
	if len(request.Parameters) != MaxObjectBytes {
		t.Fatal("incorrect boundary fixture")
	}
	wire, err := EncodeRequest(request)
	if err != nil {
		t.Fatalf("exact object limit rejected: %v", err)
	}
	decoded, err := DecodeRequest(wire)
	if err != nil || !bytes.Equal(decoded.Parameters, request.Parameters) {
		t.Fatalf("boundary request did not round trip: %v", err)
	}
	request.Parameters = json.RawMessage(`{"password":"sensitive-test-marker"}`)
	for _, formatted := range []string{fmt.Sprint(request), fmt.Sprintf("%#v", request)} {
		if strings.Contains(formatted, "sensitive-test-marker") {
			t.Fatal("request formatting exposed its payload")
		}
	}
}

func boundaryRegressionTerminal(outcome Outcome) Event {
	event := Event{ABI: Version, JobID: "job-1", InvocationID: "call-1", Type: "result"}
	switch outcome {
	case Succeeded:
		changed := false
		event.Result = &Result{Outcome: outcome, Changed: &changed, Value: json.RawMessage(`{}`)}
	case Cancelled:
		event.Result = &Result{Outcome: outcome}
	default:
		event.Type = "error"
		event.Error = &Failure{Outcome: outcome, Code: "action_failed", Message: "Action failed"}
	}
	return event
}

func TestProtocolBoundaryActualExitEvidence(t *testing.T) {
	cases := []struct {
		name     string
		terminal Outcome
		exit     ExitState
		want     Outcome
	}{
		{"success", Succeeded, ExitState{ProcessExited: true, ExitCode: 0}, Succeeded},
		{"failure", Failed, ExitState{ProcessExited: true, ExitCode: 2}, Failed},
		{"cancel acknowledged", Cancelled, ExitState{ProcessExited: true, CancelRequested: true}, Cancelled},
		{"cancel without request", Cancelled, ExitState{ProcessExited: true}, Unknown},
		{"cancel killed", Cancelled, ExitState{ProcessExited: true, CancelRequested: true, Forced: true}, Unknown},
		{"success killed", Succeeded, ExitState{ProcessExited: true, Forced: true}, Unknown},
		{"success nonzero", Succeeded, ExitState{ProcessExited: true, ExitCode: 1}, Unknown},
		{"failed zero", Failed, ExitState{ProcessExited: true}, Unknown},
		{"unreaped", Succeeded, ExitState{StreamEOF: true}, Unknown},
		{"signalled", Failed, ExitState{ProcessExited: true, ExitCode: -1}, Unknown},
		{"executor unknown", Unknown, ExitState{ProcessExited: true, ExitCode: 1}, Unknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wire, err := EncodeExecutorEvent(boundaryRegressionTerminal(tc.terminal))
			if err != nil {
				t.Fatal(err)
			}
			reader, err := NewExecutionReader(bytes.NewReader(wire), "job-1", "call-1")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reader.Next(); err != nil {
				t.Fatal(err)
			}
			if _, err := reader.Next(); err != io.EOF {
				t.Fatalf("expected EOF, got %v", err)
			}
			outcome, err := reader.Finish(tc.exit)
			if outcome != tc.want || (tc.want == Unknown && !errors.Is(err, ErrUnknownOutcome)) || (tc.want != Unknown && err != nil) {
				t.Fatalf("outcome=%s error=%v; want %s", outcome, err, tc.want)
			}
		})
	}
}

func TestProtocolBoundarySuccessCannotHideTrailingData(t *testing.T) {
	wire, err := EncodeExecutorEvent(boundaryRegressionTerminal(Succeeded))
	if err != nil {
		t.Fatal(err)
	}
	for name, suffix := range map[string]string{"partial JSON": "{", "blank frame": "\n", "second terminal": string(wire)} {
		t.Run(name, func(t *testing.T) {
			reader, err := NewExecutionReader(strings.NewReader(string(wire)+suffix), "job-1", "call-1")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reader.Next(); err != nil {
				t.Fatal(err)
			}
			if _, err := reader.Next(); !errors.Is(err, ErrProtocol) {
				t.Fatalf("trailing bytes accepted: %v", err)
			}
			outcome, err := reader.Finish(ExitState{ProcessExited: true, StreamEOF: true})
			if outcome != Unknown || !errors.Is(err, ErrUnknownOutcome) {
				t.Fatalf("outcome=%s error=%v", outcome, err)
			}
		})
	}
	reader, err := NewExecutionReader(bytes.NewReader(wire), "job-1", "call-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Next(); err != nil {
		t.Fatal(err)
	}
	// The caller has not actually consumed EOF, despite claiming it below.
	if outcome, err := reader.Finish(ExitState{ProcessExited: true, StreamEOF: true}); outcome != Unknown || err == nil {
		t.Fatalf("claimed EOF was trusted: %s %v", outcome, err)
	}
}

func TestProtocolBoundaryReplayAndExactCounters(t *testing.T) {
	current, estimate := ^uint64(0), uint64(1)
	event := Event{ABI: Version, JobID: "job-1", InvocationID: "call-1", Type: "progress",
		Progress: &Progress{Phase: "sending", Current: &current, TotalEstimated: &estimate, Unit: "bytes"}}
	wire, err := EncodeExecutorEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeExecutorEvent(wire)
	if err != nil || decoded.Progress == nil || *decoded.Progress.Current != current || *decoded.Progress.TotalEstimated != estimate {
		t.Fatalf("exact counter was clamped or rounded: %v", err)
	}
	// Even explicit seq:0 is prohibited on executor output.
	withSeq := strings.Replace(string(wire), `"type":`, `"seq":0,"type":`, 1)
	if _, err := DecodeExecutorEvent([]byte(withSeq)); !errors.Is(err, ErrProtocol) {
		t.Fatal("executor sequence accepted")
	}
	marker := Event{ABI: Version, JobID: "job-1", InvocationID: "call-1", Type: "truncated", Seq: 9,
		Truncated: &Truncated{FromSeq: 1, ThroughSeq: 8}}
	if _, err := EncodeExecutorEvent(marker); !errors.Is(err, ErrProtocol) {
		t.Fatal("executor truncation accepted")
	}
	for _, from := range []uint64{0, 4, 8} {
		replay, err := NewReplayStream("job-1", "call-1", from)
		if err != nil {
			t.Fatal(err)
		}
		if err := replay.Accept(marker); err != nil {
			t.Fatalf("marker rejected from %d: %v", from, err)
		}
		event.Seq = 10
		if err := replay.Accept(event); err != nil {
			t.Fatal(err)
		}
		if err := replay.Accept(event); !errors.Is(err, ErrProtocol) {
			t.Fatal("duplicate replay sequence accepted")
		}
	}
	marker.Truncated.FromSeq = 5
	replay, err := NewReplayStream("job-1", "call-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := replay.Accept(marker); !errors.Is(err, ErrProtocol) {
		t.Fatal("uncovered replay gap accepted")
	}
}
