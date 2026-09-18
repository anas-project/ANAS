package actionabi

import "io"

// ExecutionReader is the process-adapter entrypoint. It couples framing and
// sequence validation so malformed trailing bytes cannot be mistaken for EOF
// after an otherwise valid terminal. Call Next until EOF, reap the process,
// then Finish. It never waits for or kills the process itself.
type ExecutionReader struct {
	decoder *Decoder
	stream  *StreamValidator
}

func NewExecutionReader(reader io.Reader, jobID, invocationID string) (*ExecutionReader, error) {
	decoder, err := NewExecutorDecoder(reader)
	if err != nil {
		return nil, err
	}
	stream, err := NewExecutorStream(jobID, invocationID)
	if err != nil {
		return nil, err
	}
	return &ExecutionReader{decoder: decoder, stream: stream}, nil
}

func (r *ExecutionReader) Next() (Event, error) {
	if r == nil || r.decoder == nil || r.stream == nil {
		return Event{}, ErrProtocol
	}
	event, err := r.decoder.Next()
	if err != nil {
		return Event{}, err
	}
	if err := r.stream.Accept(event); err != nil {
		return Event{}, err
	}
	return event, nil
}

func (r *ExecutionReader) Finish(exit ExitState) (Outcome, error) {
	if r == nil || r.decoder == nil || r.stream == nil {
		return Unknown, ErrProtocol
	}
	// Ignore a caller's claimed EOF; only the actual decoder can establish it.
	exit.StreamEOF = r.decoder.eof && !r.decoder.failed
	return r.stream.Finish(exit)
}

// StreamValidator validates either one executor invocation or a replay tail.
// It owns no process and persists nothing. It is used synchronously, not shared
// between goroutines. The job writer, not the executor, owns event sequencing.
type StreamValidator struct {
	jobID        string
	invocationID string
	lastSeq      uint64
	replay       bool
	failed       bool
	terminal     bool
	finished     bool
	outcome      Outcome
}

func NewExecutorStream(jobID, invocationID string) (*StreamValidator, error) {
	return newStream(jobID, invocationID, 0, false)
}

// fromSeq is the last sequence already consumed. An empty or nonterminal replay
// tail is valid: disconnecting a subscriber does not terminate the job. A client
// that already consumed a terminal event must use get rather than infer another
// outcome from an empty tail.
func NewReplayStream(jobID, invocationID string, fromSeq uint64) (*StreamValidator, error) {
	return newStream(jobID, invocationID, fromSeq, true)
}

func newStream(jobID, invocationID string, fromSeq uint64, replay bool) (*StreamValidator, error) {
	if !opaqueID.MatchString(jobID) || !opaqueID.MatchString(invocationID) {
		return nil, ErrProtocol
	}
	return &StreamValidator{jobID: jobID, invocationID: invocationID, lastSeq: fromSeq, replay: replay}, nil
}

func (s *StreamValidator) Accept(event Event) error {
	if s == nil {
		return ErrProtocol
	}
	fail := func() error {
		s.failed = true
		return ErrProtocol
	}
	if s.failed || s.terminal || s.finished || event.JobID != s.jobID || event.InvocationID != s.invocationID {
		return fail()
	}
	if s.replay {
		if _, err := EncodeJournalEvent(event); err != nil || s.lastSeq == ^uint64(0) {
			return fail()
		}
		next := s.lastSeq + 1
		if event.Type == "truncated" {
			// A subscriber already at through_seq still receives the marker;
			// it is informational for that subscriber, not a sequence gap.
			if event.Seq < next || (event.Seq > next && (event.Truncated.FromSeq > next || event.Truncated.ThroughSeq < next)) {
				return fail()
			}
		} else if event.Seq != next {
			return fail()
		}
		s.lastSeq = event.Seq
	} else if _, err := EncodeExecutorEvent(event); err != nil {
		return fail()
	}
	switch event.Type {
	case "result":
		s.terminal, s.outcome = true, event.Result.Outcome
	case "error":
		s.terminal, s.outcome = true, event.Error.Outcome
	}
	return nil
}

// ExitState is supplied by the trusted process adapter only, after draining the
// stream and reaping the child. Zero values intentionally cannot report success.
// Forced covers process-group killing, including a cancellation grace timeout.
// Protocol errors must leave StreamEOF false even if a terminal frame preceded
// malformed trailing output. Transport completion is not subscriber completion.
type ExitState struct {
	ProcessExited   bool
	StreamEOF       bool
	Forced          bool
	ExitCode        int
	CancelRequested bool
}

// Finish is for executor streams, never subscriber/replay disconnects. A result
// cannot override a forced kill or incomplete read. Cancellation is confirmed
// only by an explicit cancelled terminal, an actual cancel request and a clean
// process exit; it is not inferred from context.Canceled or an exit signal.
func (s *StreamValidator) Finish(exit ExitState) (Outcome, error) {
	if s == nil || s.replay || s.finished {
		return Unknown, ErrProtocol
	}
	s.finished = true
	if s.failed || !s.terminal || !exit.ProcessExited || !exit.StreamEOF || exit.Forced {
		return Unknown, ErrUnknownOutcome
	}
	switch s.outcome {
	case Succeeded:
		if exit.ExitCode == 0 {
			return Succeeded, nil
		}
	case Failed:
		if exit.ExitCode > 0 {
			return Failed, nil
		}
	case Cancelled:
		if exit.CancelRequested && exit.ExitCode == 0 {
			return Cancelled, nil
		}
	}
	return Unknown, ErrUnknownOutcome
}
