// Package actionabi defines bounded anas.action/v1 executor frames and event
// validation. It does not dispatch actions, authorize peers, create jobs, start
// processes or persist events. Host and Module Command registries stay separate.
package actionabi

import (
	"encoding/json"
	"errors"
	"regexp"
	"unicode"
	"unicode/utf8"
)

const Version = "anas.action/v1"
const MaxFrameBytes = 64 << 10 // Includes the terminating LF on the wire.
const MaxObjectBytes = 32 << 10

var ErrProtocol = errors.New("invalid action ABI frame or event sequence")
var ErrUnknownOutcome = errors.New("action completion is unconfirmed")

// Request is delivered by the trusted dispatcher to an executor after creating
// a durable job and validating action-specific parameters. It is NOT the public
// invoke/attach/cancel API. Parameters carry no injected credentials. Each
// registry must validate its own closed typed schema and privileged boundaries.
type Request struct {
	ABI          string          `json:"abi"`
	JobID        string          `json:"job_id"`
	InvocationID string          `json:"invocation_id"`
	Action       string          `json:"action"`
	Parameters   json.RawMessage `json:"parameters"`
}

func (Request) String() string     { return "[action ABI request: redacted]" }
func (r Request) GoString() string { return r.String() }
func (Event) String() string       { return "[action ABI event: redacted]" }
func (e Event) GoString() string   { return e.String() }

type Outcome string

const (
	Succeeded Outcome = "succeeded"
	Failed    Outcome = "failed"
	Cancelled Outcome = "cancelled"
	Unknown   Outcome = "unknown"
)

// Event is the durable/subscriber envelope. Executor frames omit seq; the
// trusted job writer allocates it after validation. Exactly one payload must
// correspond to Type. Payloads must pass the registry's public projection before
// persistence: framing checks alone cannot identify secrets in free text.
type Event struct {
	ABI          string     `json:"abi"`
	JobID        string     `json:"job_id"`
	InvocationID string     `json:"invocation_id"`
	Seq          uint64     `json:"seq,omitempty"`
	Type         string     `json:"type"`
	Progress     *Progress  `json:"progress,omitempty"`
	Warning      *Warning   `json:"warning,omitempty"`
	Result       *Result    `json:"result,omitempty"`
	Error        *Failure   `json:"error,omitempty"`
	Truncated    *Truncated `json:"truncated,omitempty"`
}

type Progress struct {
	Phase          string  `json:"phase"`
	Current        *uint64 `json:"current,omitempty"`
	Total          *uint64 `json:"total,omitempty"`
	TotalEstimated *uint64 `json:"total_estimated,omitempty"`
	Unit           string  `json:"unit,omitempty"`
}

type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Result struct {
	Outcome Outcome         `json:"outcome"`
	Changed *bool           `json:"changed,omitempty"`
	Value   json.RawMessage `json:"value,omitempty"`
}

type Failure struct {
	Outcome Outcome `json:"outcome"`
	Code    string  `json:"code"`
	Message string  `json:"message"`
}

// Truncated is emitted only by the trusted journal. Its own sequence is one
// beyond ThroughSeq; it explicitly covers a missing range before the next
// retained event. An executor cannot claim that its own output was truncated.
type Truncated struct {
	FromSeq    uint64 `json:"from_seq"`
	ThroughSeq uint64 `json:"through_seq"`
}

var opaqueID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var actionID = regexp.MustCompile(`^[a-z][a-z0-9_-]*(?:\.[a-z][a-z0-9_-]*)*$`)
var labelID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

func validMessage(message string) bool {
	if len(message) == 0 || len(message) > 4096 || !utf8.ValidString(message) {
		return false
	}
	for _, r := range message {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (r Request) validate() error {
	if r.ABI != Version || !opaqueID.MatchString(r.JobID) || !opaqueID.MatchString(r.InvocationID) || len(r.Action) > 128 || !actionID.MatchString(r.Action) || !boundedObject(r.Parameters) {
		return ErrProtocol
	}
	return nil
}

func (e Event) validate() error {
	if e.ABI != Version || !opaqueID.MatchString(e.JobID) || !opaqueID.MatchString(e.InvocationID) {
		return ErrProtocol
	}
	count := 0
	for _, present := range []bool{e.Progress != nil, e.Warning != nil, e.Result != nil, e.Error != nil, e.Truncated != nil} {
		if present {
			count++
		}
	}
	if count != 1 {
		return ErrProtocol
	}
	switch e.Type {
	case "progress":
		p := e.Progress
		if p == nil || !labelID.MatchString(p.Phase) || (p.Total != nil && p.TotalEstimated != nil) {
			return ErrProtocol
		}
		if p.Current == nil {
			if p.Total != nil || p.TotalEstimated != nil || p.Unit != "" {
				return ErrProtocol
			}
		} else if !labelID.MatchString(p.Unit) || (p.Total != nil && *p.Current > *p.Total) {
			return ErrProtocol
		}
		// Current may exceed TotalEstimated. Never clamp exact byte counters.
	case "warning":
		if e.Warning == nil || !labelID.MatchString(e.Warning.Code) || !validMessage(e.Warning.Message) {
			return ErrProtocol
		}
	case "result":
		r := e.Result
		if r == nil {
			return ErrProtocol
		}
		switch r.Outcome {
		case Succeeded:
			if r.Changed == nil || !boundedObject(r.Value) {
				return ErrProtocol
			}
		case Cancelled:
			if r.Changed != nil || len(r.Value) != 0 {
				return ErrProtocol
			}
		default:
			return ErrProtocol
		}
	case "error":
		if e.Error == nil || (e.Error.Outcome != Failed && e.Error.Outcome != Unknown) || !labelID.MatchString(e.Error.Code) || !validMessage(e.Error.Message) {
			return ErrProtocol
		}
	case "truncated":
		t := e.Truncated
		if t == nil || t.FromSeq == 0 || t.ThroughSeq < t.FromSeq || t.ThroughSeq == ^uint64(0) || e.Seq != t.ThroughSeq+1 {
			return ErrProtocol
		}
	default:
		return ErrProtocol
	}
	return nil
}
