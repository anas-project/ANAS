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

func regressionRequest() Request {
	return Request{ABI: Version, JobID: "job-example", InvocationID: "call-example", Action: "module.sample.status", Parameters: json.RawMessage(`{}`)}
}

func regressionTerminal(outcome Outcome) Event {
	event := Event{ABI: Version, JobID: "job-example", InvocationID: "call-example"}
	switch outcome {
	case Succeeded:
		changed := false
		event.Type, event.Result = "result", &Result{Outcome: outcome, Changed: &changed, Value: json.RawMessage(`{}`)}
	case Cancelled:
		event.Type, event.Result = "result", &Result{Outcome: outcome}
	default:
		event.Type, event.Error = "error", &Failure{Outcome: outcome, Code: "operation_failed", Message: "Operation failed"}
	}
	return event
}

func regressionFrame(t *testing.T, event Event) []byte {
	t.Helper()
	frame, err := EncodeExecutorEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func TestActionRequestStrictBoundaries(t *testing.T) {
	request := regressionRequest()
	valid, err := EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRequest(bytes.NewReader(valid)); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	tests := map[string][]byte{
		"missing LF":        bytes.TrimSuffix(valid, []byte{'\n'}),
		"CRLF":              append(append([]byte(nil), valid[:len(valid)-1]...), '\r', '\n'),
		"extra line":        append(append([]byte(nil), valid...), '\n'),
		"two requests":      append(append([]byte(nil), valid...), valid...),
		"unknown field":     bytes.Replace(valid, []byte(`"abi":`), []byte(`"extra":true,"abi":`), 1),
		"case alias":        bytes.Replace(valid, []byte(`"job_id":`), []byte(`"Job_ID":`), 1),
		"duplicate":         bytes.Replace(valid, []byte(`"job_id":`), []byte(`"job_id":"another","job_id":`), 1),
		"escaped duplicate": bytes.Replace(valid, []byte(`"job_id":`), []byte(`"job\u005fid":"another","job_id":`), 1),
		"null params":       bytes.Replace(valid, []byte(`"parameters":{}`), []byte(`"parameters":null`), 1),
		"array params":      bytes.Replace(valid, []byte(`"parameters":{}`), []byte(`"parameters":[]`), 1),
		"duplicate params":  bytes.Replace(valid, []byte(`"parameters":{}`), []byte(`"parameters":{"x":1,"x":2}`), 1),
		"invalid UTF8":      bytes.Replace(valid, []byte(`"parameters":{}`), []byte("\"parameters\":{\"x\":\"\xff\"}"), 1),
		"empty":             []byte{'\n'},
	}
	for name, frame := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeRequest(frame); !errors.Is(err, ErrProtocol) {
				t.Fatalf("want protocol error, got %v", err)
			}
		})
	}
	if _, err := ReadRequest(nil); !errors.Is(err, ErrProtocol) {
		t.Fatalf("nil reader: %v", err)
	}
}

func TestActionObjectAndFrameLimits(t *testing.T) {
	request := regressionRequest()
	makeObject := func(size int) json.RawMessage {
		return json.RawMessage(`{"payload":"` + strings.Repeat("x", size-len(`{"payload":""}`)) + `"}`)
	}
	request.Parameters = makeObject(MaxObjectBytes)
	if _, err := EncodeRequest(request); err != nil {
		t.Fatalf("exact object limit rejected: %v", err)
	}
	request.Parameters = makeObject(MaxObjectBytes + 1)
	if _, err := EncodeRequest(request); !errors.Is(err, ErrProtocol) {
		t.Fatalf("oversize object accepted: %v", err)
	}
	request.Parameters = json.RawMessage(`{}`)
	frame, err := EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	body := append([]byte(nil), frame[:len(frame)-1]...)
	body = append(body, bytes.Repeat([]byte{' '}, MaxFrameBytes-len(body))...)
	if _, err := DecodeRequest(append(body, '\n')); !errors.Is(err, ErrProtocol) {
		t.Fatalf("oversize frame accepted: %v", err)
	}
	request.Parameters = json.RawMessage(strings.Repeat(`{"x":`, 20) + `{}` + strings.Repeat("}", 20))
	if _, err := EncodeRequest(request); !errors.Is(err, ErrProtocol) {
		t.Fatalf("deep object accepted: %v", err)
	}
}

func TestActionProgressPreservesIntegersAndEstimatedOverrun(t *testing.T) {
	current, estimated := uint64(9007199254740993), uint64(9007199254740992)
	event := Event{ABI: Version, JobID: "job-example", InvocationID: "call-example", Type: "progress",
		Progress: &Progress{Phase: "copying", Current: &current, TotalEstimated: &estimated, Unit: "bytes"}}
	frame := regressionFrame(t, event)
	decoded, err := DecodeExecutorEvent(frame)
	if err != nil || *decoded.Progress.Current != current || *decoded.Progress.TotalEstimated != estimated {
		t.Fatalf("counter changed or estimated overrun rejected: %v", err)
	}
	event.Progress.Total = &estimated
	if _, err := EncodeExecutorEvent(event); !errors.Is(err, ErrProtocol) {
		t.Fatalf("simultaneous exact/estimated total accepted: %v", err)
	}
	event.Progress.TotalEstimated = nil
	if _, err := EncodeExecutorEvent(event); !errors.Is(err, ErrProtocol) {
		t.Fatalf("exact total below current accepted: %v", err)
	}
	for name, bad := range map[string][]byte{
		"explicit zero seq": bytes.Replace(frame, []byte(`"type":`), []byte(`"seq":0,"type":`), 1),
		"chosen seq":        bytes.Replace(frame, []byte(`"type":`), []byte(`"seq":99,"type":`), 1),
		"fractional count":  bytes.Replace(frame, []byte(`9007199254740993`), []byte(`1.5`), 1),
		"null count":        bytes.Replace(frame, []byte(`9007199254740993`), []byte(`null`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeExecutorEvent(bad); !errors.Is(err, ErrProtocol) {
				t.Fatalf("want rejection, got %v", err)
			}
		})
	}
}

func TestActionOutcomeNeedsActualEOFAndMatchingExit(t *testing.T) {
	tests := []struct {
		name    string
		outcome Outcome
		exit    ExitState
		want    Outcome
	}{
		{"success", Succeeded, ExitState{ProcessExited: true, ExitCode: 0}, Succeeded},
		{"failed", Failed, ExitState{ProcessExited: true, ExitCode: 2}, Failed},
		{"cancel confirmed", Cancelled, ExitState{ProcessExited: true, ExitCode: 0, CancelRequested: true}, Cancelled},
		{"cancel unsolicited", Cancelled, ExitState{ProcessExited: true, ExitCode: 0}, Unknown},
		{"cancel forced", Cancelled, ExitState{ProcessExited: true, ExitCode: 0, CancelRequested: true, Forced: true}, Unknown},
		{"success forced", Succeeded, ExitState{ProcessExited: true, ExitCode: 0, Forced: true}, Unknown},
		{"success nonzero", Succeeded, ExitState{ProcessExited: true, ExitCode: 1}, Unknown},
		{"failed zero", Failed, ExitState{ProcessExited: true, ExitCode: 0}, Unknown},
		{"unreaped", Succeeded, ExitState{ExitCode: 0}, Unknown},
		{"signalled", Failed, ExitState{ProcessExited: true, ExitCode: -1}, Unknown},
		{"unknown terminal", Unknown, ExitState{ProcessExited: true, ExitCode: 1}, Unknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader, err := NewExecutionReader(bytes.NewReader(regressionFrame(t, regressionTerminal(test.outcome))), "job-example", "call-example")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reader.Next(); err != nil {
				t.Fatal(err)
			}
			if _, err := reader.Next(); err != io.EOF {
				t.Fatalf("expected actual EOF: %v", err)
			}
			got, err := reader.Finish(test.exit)
			if got != test.want || (test.want == Unknown && !errors.Is(err, ErrUnknownOutcome)) || (test.want != Unknown && err != nil) {
				t.Fatalf("outcome %s, error %v; want %s", got, err, test.want)
			}
		})
	}
	reader, err := NewExecutionReader(bytes.NewReader(regressionFrame(t, regressionTerminal(Succeeded))), "job-example", "call-example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Next(); err != nil {
		t.Fatal(err)
	}
	if outcome, err := reader.Finish(ExitState{ProcessExited: true, StreamEOF: true, ExitCode: 0}); outcome != Unknown || !errors.Is(err, ErrUnknownOutcome) {
		t.Fatalf("caller-claimed EOF accepted: %s %v", outcome, err)
	}
}

func TestActionSuccessCannotHideTrailingData(t *testing.T) {
	good := regressionFrame(t, regressionTerminal(Succeeded))
	for name, tail := range map[string][]byte{"partial": []byte(`{"broken"`), "blank": []byte{'\n'}, "second terminal": good} {
		t.Run(name, func(t *testing.T) {
			reader, err := NewExecutionReader(bytes.NewReader(append(append([]byte(nil), good...), tail...)), "job-example", "call-example")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reader.Next(); err != nil {
				t.Fatal(err)
			}
			if _, err := reader.Next(); !errors.Is(err, ErrProtocol) {
				t.Fatalf("trailing data accepted: %v", err)
			}
			if outcome, err := reader.Finish(ExitState{ProcessExited: true, ExitCode: 0}); outcome != Unknown || !errors.Is(err, ErrUnknownOutcome) {
				t.Fatalf("trailing data produced %s: %v", outcome, err)
			}
		})
	}
	bad := append([]byte("not-json\n"), good...)
	decoder, err := NewExecutorDecoder(bytes.NewReader(bad))
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := decoder.Next(); !errors.Is(err, ErrProtocol) {
			t.Fatalf("poisoned decoder resumed on attempt %d: %v", attempt, err)
		}
	}
}

func TestActionReplayRequiresRealTruncationCoverage(t *testing.T) {
	marker := Event{ABI: Version, JobID: "job-example", InvocationID: "call-example", Type: "truncated", Seq: 11,
		Truncated: &Truncated{FromSeq: 1, ThroughSeq: 10}}
	terminal := regressionTerminal(Succeeded)
	terminal.Seq = 12
	for _, from := range []uint64{0, 3, 10} {
		stream, err := NewReplayStream("job-example", "call-example", from)
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.Accept(marker); err != nil {
			t.Fatalf("marker rejected from %d: %v", from, err)
		}
		if err := stream.Accept(terminal); err != nil {
			t.Fatal(err)
		}
		if err := stream.Accept(terminal); !errors.Is(err, ErrProtocol) {
			t.Fatalf("duplicate terminal accepted: %v", err)
		}
	}
	stream, _ := NewReplayStream("job-example", "call-example", 3)
	if err := stream.Accept(terminal); !errors.Is(err, ErrProtocol) {
		t.Fatalf("unmarked gap accepted: %v", err)
	}
	stream, _ = NewReplayStream("job-example", "call-example", 3)
	marker.Truncated.FromSeq = 5
	if err := stream.Accept(marker); !errors.Is(err, ErrProtocol) {
		t.Fatalf("insufficient marker accepted: %v", err)
	}
	if _, err := EncodeExecutorEvent(marker); !errors.Is(err, ErrProtocol) {
		t.Fatalf("executor can emit truncation: %v", err)
	}
}

func TestActionFormattingDoesNotRevealPayload(t *testing.T) {
	request := regressionRequest()
	request.Parameters = json.RawMessage(`{"payload":"private-fixture-material"}`)
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(format, request), "private-fixture-material") {
			t.Fatalf("format %s exposed request", format)
		}
	}
}
