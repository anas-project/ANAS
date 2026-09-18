package actionabi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
)

// Request input is one LF-terminated JSON object followed by EOF. The launcher
// must close stdin or set its own I/O deadline; this codec owns no connection.
func ReadRequest(reader io.Reader) (Request, error) {
	if reader == nil {
		return Request{}, ErrProtocol
	}
	frame, err := io.ReadAll(io.LimitReader(reader, MaxFrameBytes+1))
	if err != nil {
		return Request{}, ErrProtocol
	}
	return DecodeRequest(frame)
}

func DecodeRequest(frame []byte) (Request, error) {
	body, err := frameBody(frame)
	if err != nil {
		return Request{}, err
	}
	var request Request
	if strictObject(body, &request) != nil || request.validate() != nil {
		return Request{}, ErrProtocol
	}
	return request, nil
}

func EncodeRequest(request Request) ([]byte, error) {
	if request.validate() != nil {
		return nil, ErrProtocol
	}
	frame, err := encodeFrame(request)
	if err != nil {
		return nil, err
	}
	if _, err := DecodeRequest(frame); err != nil {
		return nil, err
	}
	return frame, nil
}

// Executor output has no sequence numbers. Only the job journal assigns
// durable sequence numbers and emits truncated markers after public projection.
func DecodeExecutorEvent(frame []byte) (Event, error) {
	event, err := decodeEvent(frame)
	if err != nil || event.Seq != 0 || event.Type == "truncated" {
		return Event{}, ErrProtocol
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(frame, &fields) != nil {
		return Event{}, ErrProtocol
	}
	if _, exists := fields["seq"]; exists {
		return Event{}, ErrProtocol
	}
	return event, nil
}

func DecodeJournalEvent(frame []byte) (Event, error) {
	event, err := decodeEvent(frame)
	if err != nil || event.Seq == 0 {
		return Event{}, ErrProtocol
	}
	return event, nil
}

func decodeEvent(frame []byte) (Event, error) {
	body, err := frameBody(frame)
	if err != nil {
		return Event{}, err
	}
	var event Event
	if strictObject(body, &event) != nil || event.validate() != nil {
		return Event{}, ErrProtocol
	}
	return event, nil
}

func EncodeExecutorEvent(event Event) ([]byte, error) {
	if event.validate() != nil || event.Seq != 0 || event.Type == "truncated" {
		return nil, ErrProtocol
	}
	frame, err := encodeFrame(event)
	if err != nil {
		return nil, err
	}
	if _, err := DecodeExecutorEvent(frame); err != nil {
		return nil, err
	}
	return frame, nil
}

func EncodeJournalEvent(event Event) ([]byte, error) {
	if event.validate() != nil || event.Seq == 0 {
		return nil, ErrProtocol
	}
	frame, err := encodeFrame(event)
	if err != nil {
		return nil, err
	}
	if _, err := DecodeJournalEvent(frame); err != nil {
		return nil, err
	}
	return frame, nil
}

func encodeFrame(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil || len(body)+1 > MaxFrameBytes {
		return nil, ErrProtocol
	}
	return append(body, '\n'), nil
}

func frameBody(frame []byte) ([]byte, error) {
	if len(frame) < 3 || len(frame) > MaxFrameBytes || frame[len(frame)-1] != '\n' {
		return nil, ErrProtocol
	}
	body := frame[:len(frame)-1]
	if bytes.ContainsAny(body, "\r\n") {
		return nil, ErrProtocol
	}
	return body, nil
}

// Decoder reads one bounded frame at a time. EOF only means the connection or
// stream ended; it is never, by itself, confirmation of a job outcome. Callers
// own timeouts, cancellation and process lifetime. A malformed stream is poisoned
// and cannot resume at a subsequent well-formed line.
type Decoder struct {
	reader *bufio.Reader
	replay bool
	failed bool
	eof    bool
}

func NewExecutorDecoder(reader io.Reader) (*Decoder, error) {
	return newDecoder(reader, false)
}

func NewReplayDecoder(reader io.Reader) (*Decoder, error) {
	return newDecoder(reader, true)
}

func newDecoder(reader io.Reader, replay bool) (*Decoder, error) {
	if reader == nil {
		return nil, ErrProtocol
	}
	return &Decoder{reader: bufio.NewReaderSize(reader, MaxFrameBytes), replay: replay}, nil
}

func (d *Decoder) Next() (Event, error) {
	if d == nil || d.reader == nil || d.failed {
		return Event{}, ErrProtocol
	}
	if d.eof {
		return Event{}, io.EOF
	}
	frame, err := d.reader.ReadSlice('\n')
	if err == io.EOF && len(frame) == 0 {
		d.eof = true
		return Event{}, io.EOF
	}
	if err != nil {
		d.failed = true
		return Event{}, ErrProtocol
	}
	var event Event
	if d.replay {
		event, err = DecodeJournalEvent(frame)
	} else {
		event, err = DecodeExecutorEvent(frame)
	}
	if err != nil {
		d.failed = true
	}
	return event, err
}
