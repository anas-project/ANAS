package dms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

func (h Handler) serveIntake(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/intake/")
	release := strings.HasSuffix(id, "/release")
	if release {
		id = strings.TrimSuffix(id, "/release")
	}
	if !intakeID.MatchString(id) {
		reply(w, 400, map[string]string{"error": "invalid-intake"})
		return
	}
	var receipt IntakeReceipt
	var err error
	switch r.Method {
	case "GET":
		if release {
			reply(w, 404, map[string]string{"error": "not-found"})
			return
		}
		receipt, err = h.Adapter.IntakeStatus(r.Context(), id)
	case "POST":
		if release {
			var input struct {
				Receipt      IntakeReceipt `json:"receipt"`
				Acknowledged bool          `json:"cloud_acknowledged"`
			}
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
			d.DisallowUnknownFields()
			var extra any
			if r.Header.Get("Content-Type") != "application/json" || d.Decode(&input) != nil || d.Decode(&extra) != io.EOF || input.Receipt.ID != id {
				reply(w, 400, map[string]string{"error": "invalid-intake"})
				return
			}
			receipt, err = h.Adapter.ReleaseIntake(r.Context(), input.Receipt, input.Acknowledged)
			break
		}
		media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil || media != "message/rfc822" || len(r.Header.Values("Content-Type")) != 1 || len(r.Header.Values("X-ANAS-Intake")) != 1 || len(r.Header.Get("X-ANAS-Intake")) > 2048 {
			reply(w, 400, map[string]string{"error": "invalid-intake"})
			return
		}
		var m IntakeMetadata
		decoder := json.NewDecoder(strings.NewReader(r.Header.Get("X-ANAS-Intake")))
		decoder.DisallowUnknownFields()
		var extra any
		if decoder.Decode(&m) != nil || decoder.Decode(&extra) != io.EOF || !m.valid() || m.Key() != id {
			reply(w, 400, map[string]string{"error": "invalid-intake"})
			return
		}
		receipt, err = h.Adapter.Intake(r.Context(), id, m, http.MaxBytesReader(w, r.Body, IntakeMaxBytes))
	default:
		reply(w, 404, map[string]string{"error": "not-found"})
		return
	}
	status := 200
	code := ""
	if errors.Is(err, ErrConflict) {
		status, code = 409, "intake-conflict"
	} else if err != nil {
		status, code = 503, "intake-not-confirmed"
	}
	reply(w, status, struct {
		IntakeReceipt
		Error string `json:"error,omitempty"`
	}{receipt, code})
}
func (c *Client) intakeRequest(ctx context.Context, id string, m *IntakeMetadata, body io.Reader) (IntakeReceipt, error) {
	if !intakeID.MatchString(id) || m != nil && (!m.valid() || m.Key() != id) {
		return IntakeReceipt{}, ErrConflict
	}
	method := "GET"
	if m != nil {
		method = "POST"
	}
	r, err := http.NewRequestWithContext(ctx, method, c.base+"/v1/intake/"+id, body)
	if err != nil {
		return IntakeReceipt{}, ErrUnavailable
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	if m != nil {
		raw, _ := json.Marshal(m)
		r.Header.Set("X-ANAS-Intake", string(raw))
		r.Header.Set("Content-Type", "message/rfc822")
	}
	response, err := c.http.Do(r)
	if err != nil {
		return IntakeReceipt{}, ErrUnavailable
	}
	defer response.Body.Close()
	return c.decodeIntake(response, id, m)
}
func (c *Client) decodeIntake(response *http.Response, id string, m *IntakeMetadata) (IntakeReceipt, error) {
	var err error
	if response.StatusCode == 409 {
		return IntakeReceipt{}, ErrConflict
	}
	if response.StatusCode != 200 && response.StatusCode != 503 {
		return IntakeReceipt{}, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || len(raw) > 4096 {
		return IntakeReceipt{}, ErrUnavailable
	}
	var state struct {
		IntakeReceipt
		Error string `json:"error"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&state) != nil || d.Decode(&extra) != io.EOF || state.ID != id || !uuid.MatchString(state.MailboxID) || !intakeID.MatchString(state.SHA256) || !intakeState(state.State) || !intakeCheck(state.Check) || !intakeCode(state.SMTPCode) {
		return IntakeReceipt{}, ErrUnavailable
	}
	if m != nil && (state.MailboxID != m.MailboxID || state.SHA256 != m.SHA256) {
		return IntakeReceipt{}, ErrUnavailable
	}
	if response.StatusCode != 200 || state.State != "stored" && state.State != "processed" || state.Error != "" {
		return state.IntakeReceipt, ErrUnavailable
	}
	return state.IntakeReceipt, nil
}
func (c *Client) ReleaseIntake(ctx context.Context, receipt IntakeReceipt) (IntakeReceipt, error) {
	if !intakeID.MatchString(receipt.ID) || !uuid.MatchString(receipt.MailboxID) || !intakeID.MatchString(receipt.SHA256) || receipt.State != "stored" && receipt.State != "processed" {
		return IntakeReceipt{}, ErrConflict
	}
	input := struct {
		Receipt      IntakeReceipt `json:"receipt"`
		Acknowledged bool          `json:"cloud_acknowledged"`
	}{receipt, true}
	raw, _ := json.Marshal(input)
	r, err := http.NewRequestWithContext(ctx, "POST", c.base+"/v1/intake/"+receipt.ID+"/release", bytes.NewReader(raw))
	if err != nil {
		return IntakeReceipt{}, ErrUnavailable
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	r.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(r)
	if err != nil {
		return IntakeReceipt{}, ErrUnavailable
	}
	defer response.Body.Close()
	out, err := c.decodeIntake(response, receipt.ID, &IntakeMetadata{MailboxID: receipt.MailboxID, SHA256: receipt.SHA256})
	if err != nil || !out.RawReleased || out.State != receipt.State {
		return out, ErrUnavailable
	}
	return out, nil
}
func (c *Client) Intake(ctx context.Context, m IntakeMetadata, raw io.Reader) (IntakeReceipt, error) {
	if raw == nil {
		return IntakeReceipt{}, ErrConflict
	}
	return c.intakeRequest(ctx, m.Key(), &m, raw)
}
func (c *Client) IntakeStatus(ctx context.Context, id string) (IntakeReceipt, error) {
	return c.intakeRequest(ctx, id, nil, nil)
}
