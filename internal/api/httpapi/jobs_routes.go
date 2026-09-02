package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
)

const (
	defaultSSEConnectionLimit = 32
	defaultSSEHeartbeat       = 15 * time.Second
	defaultSSEPollInterval    = 250 * time.Millisecond
	defaultSSEWriteTimeout    = 5 * time.Second
	defaultSSEReplayLimit     = 100
	maximumJobIDLength        = 256
	jobEventsRoutePattern     = "/api/v1/jobs/{id}/events"
)

// JobQueryStore is the read-only durable boundary consumed by the HTTP
// adapter. *consolejobs.Store implements it directly.
type JobQueryStore interface {
	List(context.Context) ([]consolejobs.Job, error)
	Get(context.Context, string) (consolejobs.Job, error)
	Replay(context.Context, string, consolejobs.ReplayOptions) (consolejobs.EventPage, error)
}

// JobQueryOptions controls the independent SSE resource boundary. Durations
// are per stream rather than inherited from the ordinary JSON handler timeout.
type JobQueryOptions struct {
	Store              JobQueryStore
	Cancel             func(context.Context, string) (consolejobs.Job, error)
	MaxSSEConnections  int
	SSEHeartbeat       time.Duration
	SSEPollInterval    time.Duration
	SSEWriteTimeout    time.Duration
	SSEReplayBatchSize int
}

type jobHTTPState struct {
	store        JobQueryStore
	cancel       func(context.Context, string) (consolejobs.Job, error)
	connections  chan struct{}
	heartbeat    time.Duration
	pollInterval time.Duration
	writeTimeout time.Duration
	replayBatch  int
}

func newJobHTTPState(options JobQueryOptions) (*jobHTTPState, error) {
	if options.Store == nil {
		return nil, errors.New("job query store is required")
	}
	if options.MaxSSEConnections < 0 || options.SSEHeartbeat < 0 || options.SSEPollInterval < 0 ||
		options.SSEWriteTimeout < 0 || options.SSEReplayBatchSize < 0 {
		return nil, errors.New("job query limits and durations must not be negative")
	}
	if options.MaxSSEConnections == 0 {
		options.MaxSSEConnections = defaultSSEConnectionLimit
	}
	if options.MaxSSEConnections > 4096 {
		return nil, errors.New("SSE connection limit must not exceed 4096")
	}
	if options.SSEHeartbeat == 0 {
		options.SSEHeartbeat = defaultSSEHeartbeat
	}
	if options.SSEPollInterval == 0 {
		options.SSEPollInterval = defaultSSEPollInterval
	}
	if options.SSEWriteTimeout == 0 {
		options.SSEWriteTimeout = defaultSSEWriteTimeout
	}
	if options.SSEReplayBatchSize == 0 {
		options.SSEReplayBatchSize = defaultSSEReplayLimit
	}
	if options.SSEReplayBatchSize > 1000 {
		return nil, errors.New("SSE replay batch size must not exceed 1000")
	}
	return &jobHTTPState{
		store: options.Store, cancel: options.Cancel, connections: make(chan struct{}, options.MaxSSEConnections),
		heartbeat: options.SSEHeartbeat, pollInterval: options.SSEPollInterval,
		writeTimeout: options.SSEWriteTimeout, replayBatch: options.SSEReplayBatchSize,
	}, nil
}

type jobListResponse struct {
	APIVersion string          `json:"api_version"`
	Items      []jobSummaryDTO `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}

type jobDetailResponse struct {
	APIVersion string       `json:"api_version"`
	Job        jobDetailDTO `json:"job"`
}

type jobCancelResponse struct {
	APIVersion string       `json:"api_version"`
	Job        jobDetailDTO `json:"job"`
}

type jobSummaryDTO struct {
	ID          string             `json:"id"`
	Kind        string             `json:"kind"`
	WorkspaceID string             `json:"workspace_id"`
	Mutating    bool               `json:"mutating"`
	Status      consolejobs.Status `json:"status"`
	Progress    int                `json:"progress"`
	CreatedAt   time.Time          `json:"created_at"`
	StartedAt   *time.Time         `json:"started_at,omitempty"`
	FinishedAt  *time.Time         `json:"finished_at,omitempty"`
	Revision    uint64             `json:"revision"`
}

type jobDetailDTO struct {
	jobSummaryDTO
	Warnings               []string              `json:"warnings"`
	Result                 map[string]any        `json:"result,omitempty"`
	Error                  *consolejobs.JobError `json:"error,omitempty"`
	NeedsCompensationCheck bool                  `json:"needs_compensation_check"`
}

type jobEventDTO struct {
	APIVersion string         `json:"api_version"`
	ID         uint64         `json:"id"`
	JobID      string         `json:"job_id"`
	Timestamp  time.Time      `json:"timestamp"`
	Kind       string         `json:"kind"`
	Data       map[string]any `json:"data,omitempty"`
}

type eventGapProblem struct {
	problem
	JobID           string `json:"job_id"`
	RequestedAfter  uint64 `json:"requested_after"`
	PrunedThrough   uint64 `json:"pruned_through"`
	OldestAvailable uint64 `json:"oldest_available"`
	LatestID        uint64 `json:"latest_id"`
}

type jobCursor struct {
	Version   int       `json:"v"`
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

func (h *handler) listJobs(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if h.jobs == nil {
		writeProblem(w, http.StatusServiceUnavailable, "jobs_unavailable", "job history is unavailable")
		return
	}
	limit, cursor, ok := parseJobPagination(w, r)
	if !ok {
		return
	}
	principal, state, ok := h.jobRequestPrincipal(w, r)
	if !ok {
		return
	}
	jobs, err := h.jobs.store.List(r.Context())
	if err != nil {
		writeJobStoreError(w, err)
		return
	}
	visible := make([]consolejobs.Job, 0, len(jobs))
	for _, job := range jobs {
		if h.canReadJob(state, principal, job) {
			visible = append(visible, job)
		}
	}
	sort.Slice(visible, func(left, right int) bool {
		if visible[left].CreatedAt.Equal(visible[right].CreatedAt) {
			return visible[left].ID > visible[right].ID
		}
		return visible[left].CreatedAt.After(visible[right].CreatedAt)
	})
	start := 0
	if cursor != nil {
		found := false
		for index := range visible {
			if visible[index].ID == cursor.ID && visible[index].CreatedAt.Equal(cursor.CreatedAt) {
				start = index + 1
				found = true
				break
			}
		}
		if !found {
			writeProblem(w, http.StatusBadRequest, "invalid_cursor", "cursor is invalid or no longer visible")
			return
		}
	}
	end := start + limit
	if end > len(visible) {
		end = len(visible)
	}
	items := make([]jobSummaryDTO, 0, end-start)
	for _, job := range visible[start:end] {
		items = append(items, newJobSummaryDTO(job))
	}
	var next *string
	if end < len(visible) && end > start {
		encoded, err := encodeJobCursor(visible[end-1])
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "response_encoding_failed", "job cursor could not be encoded")
			return
		}
		next = &encoded
	}
	writeJSON(w, http.StatusOK, jobListResponse{APIVersion: APIVersion, Items: items, NextCursor: next})
}

func (h *handler) getJob(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if h.jobs == nil {
		writeProblem(w, http.StatusServiceUnavailable, "jobs_unavailable", "job history is unavailable")
		return
	}
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	principal, state, ok := h.jobRequestPrincipal(w, r)
	if !ok {
		return
	}
	job, ok := h.authorizedJob(w, r, params["id"], state, principal)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, jobDetailResponse{APIVersion: APIVersion, Job: newJobDetailDTO(job)})
}

func (h *handler) cancelJob(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if h.jobs == nil || h.jobs.cancel == nil {
		writeProblem(w, http.StatusServiceUnavailable, "job_cancel_unavailable", "job cancellation is unavailable")
		return
	}
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	if !h.decodeEmptyDeploymentRequest(w, r) {
		return
	}
	principal, state, ok := h.jobRequestPrincipal(w, r)
	if !ok {
		return
	}
	job, ok := h.authorizedJob(w, r, params["id"], state, principal)
	if !ok {
		return
	}
	canceled, err := h.jobs.cancel(r.Context(), job.ID)
	if err != nil {
		writeJobStoreError(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/jobs/"+job.ID)
	writeJSON(w, http.StatusAccepted, jobCancelResponse{APIVersion: APIVersion, Job: newJobDetailDTO(canceled)})
}

func (h *handler) streamJobEvents(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if h.jobs == nil {
		writeProblem(w, http.StatusServiceUnavailable, "jobs_unavailable", "job history is unavailable")
		return
	}
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	if !acceptsEventStream(r.Header.Values("Accept")) {
		writeProblem(w, http.StatusNotAcceptable, "event_stream_required", "Accept must allow text/event-stream")
		return
	}
	afterID, ok := parseLastEventID(w, r.Header.Values("Last-Event-ID"))
	if !ok {
		return
	}
	principal, state, ok := h.jobRequestPrincipal(w, r)
	if !ok {
		return
	}
	authorization, ok := h.jobEventsAuthorization(params)
	if !ok {
		writeProblem(w, http.StatusInternalServerError, "authorization_unavailable", "stream authorization is unavailable")
		return
	}
	job, ok := h.authorizedJob(w, r, params["id"], state, principal)
	if !ok {
		return
	}
	page, err := h.jobs.store.Replay(r.Context(), job.ID, consolejobs.ReplayOptions{AfterID: afterID, Limit: h.jobs.replayBatch})
	if err != nil {
		writeJobReplayError(w, err)
		return
	}
	if afterID != nil && *afterID > page.LatestID {
		writeProblem(w, http.StatusBadRequest, "invalid_last_event_id", "Last-Event-ID is ahead of this job's event stream")
		return
	}
	// HTTP 204 tells a browser EventSource that this terminal stream is
	// complete and must not be reconnected. Keep this after replay validation
	// so an expired or ahead cursor still receives its precise problem response.
	if terminalJobStatus(job.Status) && len(page.Events) == 0 {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	select {
	case h.jobs.connections <- struct{}{}:
		defer func() { <-h.jobs.connections }()
	default:
		w.Header().Set("Retry-After", "1")
		writeProblem(w, http.StatusTooManyRequests, "sse_connection_limit", "too many job event streams are open")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeProblem(w, http.StatusInternalServerError, "streaming_unsupported", "streaming is unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if !writeSSEChunk(w, flusher, h.jobs.writeTimeout, []byte(": connected\n\n")) {
		return
	}

	lastID := uint64(0)
	if afterID != nil {
		lastID = *afterID
	} else {
		// A client without Last-Event-ID starts at the oldest retained event,
		// not at the beginning of an already-pruned history.
		lastID = page.PrunedThrough
	}
	poll := time.NewTicker(h.jobs.pollInterval)
	heartbeat := time.NewTicker(h.jobs.heartbeat)
	defer poll.Stop()
	defer heartbeat.Stop()

	for {
		var authorized bool
		job, authorized, err = h.reauthorizeJobStream(r, authorization, state, job.ID)
		if err != nil {
			h.writeStreamingJobError(w, flusher, err)
			return
		}
		if !authorized {
			return
		}
		// The job status and event stream are separate durable records. Once a
		// terminal status is observed, refresh the replay page before deciding
		// that the stream is drained: the executor writes its final event before
		// committing the terminal transition.
		if terminalJobStatus(job.Status) {
			page, err = h.jobs.store.Replay(r.Context(), job.ID, consolejobs.ReplayOptions{AfterID: &lastID, Limit: h.jobs.replayBatch})
			if !h.handleStreamingReplayError(w, flusher, err) {
				return
			}
		}
		for _, event := range page.Events {
			body, err := encodeSSEEvent(event)
			if err != nil || !writeSSEChunk(w, flusher, h.jobs.writeTimeout, body) {
				return
			}
			lastID = event.ID
		}
		if lastID < page.LatestID {
			page, err = h.jobs.store.Replay(r.Context(), job.ID, consolejobs.ReplayOptions{AfterID: &lastID, Limit: h.jobs.replayBatch})
			if !h.handleStreamingReplayError(w, flusher, err) {
				return
			}
			continue
		}
		if terminalJobStatus(job.Status) {
			return
		}

		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			job, authorized, err = h.reauthorizeJobStream(r, authorization, state, job.ID)
			if err != nil {
				h.writeStreamingJobError(w, flusher, err)
				return
			}
			if !authorized {
				return
			}
			if terminalJobStatus(job.Status) {
				continue
			}
			if !writeSSEChunk(w, flusher, h.jobs.writeTimeout, []byte(": heartbeat\n\n")) {
				return
			}
		case <-poll.C:
			job, authorized, err = h.reauthorizeJobStream(r, authorization, state, job.ID)
			if err != nil {
				h.writeStreamingJobError(w, flusher, err)
				return
			}
			if !authorized {
				return
			}
			if terminalJobStatus(job.Status) {
				continue
			}
			page, err = h.jobs.store.Replay(r.Context(), job.ID, consolejobs.ReplayOptions{AfterID: &lastID, Limit: h.jobs.replayBatch})
			if !h.handleStreamingReplayError(w, flusher, err) {
				return
			}
		}
	}
}

// jobEventsAuthorization returns the exact immutable route policy selected by
// dispatch. Periodic stream checks must not reconstruct a second policy that
// could drift from the route inventory.
func (h *handler) jobEventsAuthorization(params map[string]string) (AuthorizationRequest, bool) {
	for _, route := range h.routes {
		if route.policy.Method == http.MethodGet && route.policy.Pattern == jobEventsRoutePattern {
			return AuthorizationRequest{
				Policy: route.policy, Params: copyParams(params), ObserveOnly: true,
			}, true
		}
	}
	return AuthorizationRequest{}, false
}

// reauthorizeJobStream repeats state, route, credential, principal, and object
// authorization at a long-lived response boundary. Any authorization failure
// is deliberately collapsed to a silent close after SSE headers have been
// sent; only job-store failures are safe to expose as stream error events.
func (h *handler) reauthorizeJobStream(r *http.Request, authorization AuthorizationRequest, establishedState ConsoleState, jobID string) (consolejobs.Job, bool, error) {
	currentState, err := h.security.State(r.Context())
	if err != nil || currentState != establishedState {
		return consolejobs.Job{}, false, nil
	}
	access, allowed := authorization.Policy.Access[currentState]
	if !allowed || access.Authentication == AuthenticationNone ||
		authorization.Policy.Method != r.Method ||
		!allowsTransport(access, requestTransport(r)) ||
		!allowsListener(authorization.Policy.Listeners, h.security.Listener) {
		return consolejobs.Job{}, false, nil
	}
	currentRequest := withConsoleState(r, currentState)
	principal, err := h.security.Authorize(currentRequest, authorization)
	if err != nil || !validJobRequestPrincipal(currentState, principal) {
		return consolejobs.Job{}, false, nil
	}
	job, err := h.jobs.store.Get(r.Context(), jobID)
	if err != nil {
		return consolejobs.Job{}, false, err
	}
	if !h.canReadJob(currentState, principal, job) {
		return consolejobs.Job{}, false, nil
	}
	return job, true, nil
}

func (h *handler) handleStreamingReplayError(w http.ResponseWriter, flusher http.Flusher, err error) bool {
	if err == nil {
		return true
	}
	var gap *consolejobs.EventGapError
	if errors.As(err, &gap) {
		h.writeSSEProblem(w, flusher, "gap", newEventGapProblem(gap))
	} else if !errors.Is(err, context.Canceled) {
		h.writeStreamingJobError(w, flusher, err)
	}
	return false
}

func (h *handler) writeStreamingJobError(w http.ResponseWriter, flusher http.Flusher, err error) {
	status, code, detail := publicJobStoreError(err)
	h.writeSSEProblem(w, flusher, "error", problem{
		APIVersion: APIVersion, Type: "about:blank", Title: http.StatusText(status),
		Status: status, Detail: detail, Code: code,
	})
}

func (h *handler) writeSSEProblem(w http.ResponseWriter, flusher http.Flusher, eventName string, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		return
	}
	chunk := append([]byte("event: "+eventName+"\ndata: "), body...)
	chunk = append(chunk, '\n', '\n')
	_ = writeSSEChunk(w, flusher, h.jobs.writeTimeout, chunk)
}

func (h *handler) authorizedJob(w http.ResponseWriter, r *http.Request, jobID string, state ConsoleState, principal Principal) (consolejobs.Job, bool) {
	if jobID == "" || len(jobID) > maximumJobIDLength {
		writeProblem(w, http.StatusBadRequest, "invalid_job_id", "job ID is invalid")
		return consolejobs.Job{}, false
	}
	job, err := h.jobs.store.Get(r.Context(), jobID)
	if err != nil {
		writeJobStoreError(w, err)
		return consolejobs.Job{}, false
	}
	if !h.canReadJob(state, principal, job) {
		writeProblem(w, http.StatusNotFound, "job_not_found", "job was not found")
		return consolejobs.Job{}, false
	}
	return job, true
}

func (h *handler) canReadJob(state ConsoleState, principal Principal, job consolejobs.Job) bool {
	if h == nil || h.registry == nil || job.WorkspaceID == "" || job.CreatedBy == "" {
		return false
	}
	if _, registered := h.registry.Resolve(job.WorkspaceID); !registered {
		return false
	}
	switch state {
	case StateBootstrap, StateEnrollment:
		if principal.TransactionID == "" || principal.Role != "bootstrap" && principal.Role != "enrollment" ||
			principal.Source != "bootstrap" && principal.Source != "enrollment" {
			return false
		}
		_, transactionID, valid := consolejobs.ParseTransactionPrincipal(job.CreatedBy)
		return valid && transactionID == principal.TransactionID
	case StateFull:
		return principal.Role == "owner"
	default:
		return false
	}
}

func (h *handler) jobRequestPrincipal(w http.ResponseWriter, r *http.Request) (Principal, ConsoleState, bool) {
	principal, principalOK := PrincipalFromContext(r.Context())
	state, stateOK := ConsoleStateFromContext(r.Context())
	if !principalOK || !stateOK {
		writeProblem(w, http.StatusForbidden, "forbidden", "request is not permitted")
		return Principal{}, "", false
	}
	if !validJobRequestPrincipal(state, principal) {
		writeProblem(w, http.StatusForbidden, "forbidden", "request is not permitted")
		return Principal{}, "", false
	}
	return principal, state, true
}

func validJobRequestPrincipal(state ConsoleState, principal Principal) bool {
	switch state {
	case StateBootstrap, StateEnrollment:
		kind, transactionID, parsed := consolejobs.ParseTransactionPrincipal(principal.ID)
		return parsed && transactionID == principal.TransactionID &&
			((kind == consolejobs.PrincipalBootstrap && principal.Role == "bootstrap" && principal.Source == "bootstrap") ||
				(kind == consolejobs.PrincipalEnrollment && principal.Role == "enrollment" && principal.Source == "enrollment"))
	case StateFull:
		return principal.Role == "owner"
	default:
		return false
	}
}

func parseJobPagination(w http.ResponseWriter, r *http.Request) (int, *jobCursor, bool) {
	query, ok := supportedQuery(w, r, "limit", "cursor")
	if !ok {
		return 0, nil, false
	}
	limit := defaultPageLimit
	if values, present := query["limit"]; present {
		if len(values) != 1 || values[0] == "" {
			writeProblem(w, http.StatusBadRequest, "invalid_limit", "limit must be a single integer between 1 and 100")
			return 0, nil, false
		}
		parsed, err := strconv.Atoi(values[0])
		if err != nil || parsed < 1 || parsed > maximumPageLimit {
			writeProblem(w, http.StatusBadRequest, "invalid_limit", "limit must be an integer between 1 and 100")
			return 0, nil, false
		}
		limit = parsed
	}
	var cursor *jobCursor
	if values, present := query["cursor"]; present {
		if len(values) != 1 || values[0] == "" || len(values[0]) > maximumCursorLen {
			writeProblem(w, http.StatusBadRequest, "invalid_cursor", "cursor must be a single non-empty opaque value")
			return 0, nil, false
		}
		parsed, err := decodeJobCursor(values[0])
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_cursor", "cursor is invalid")
			return 0, nil, false
		}
		cursor = &parsed
	}
	return limit, cursor, true
}

func encodeJobCursor(job consolejobs.Job) (string, error) {
	body, err := json.Marshal(jobCursor{Version: 1, CreatedAt: job.CreatedAt.UTC(), ID: job.ID})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func decodeJobCursor(value string) (jobCursor, error) {
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(body) != value {
		return jobCursor{}, errors.New("cursor encoding is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var cursor jobCursor
	if err := decoder.Decode(&cursor); err != nil {
		return jobCursor{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return jobCursor{}, errors.New("cursor has trailing data")
	}
	if cursor.Version != 1 || cursor.ID == "" || len(cursor.ID) > maximumJobIDLength || cursor.CreatedAt.IsZero() {
		return jobCursor{}, errors.New("cursor fields are invalid")
	}
	return cursor, nil
}

func acceptsEventStream(values []string) bool {
	if len(values) == 0 {
		return true
	}
	for _, line := range values {
		for _, item := range strings.Split(line, ",") {
			mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(item))
			if err != nil {
				continue
			}
			quality := 1.0
			if raw, present := parameters["q"]; present {
				quality, err = strconv.ParseFloat(raw, 64)
				if err != nil || quality < 0 || quality > 1 {
					continue
				}
			}
			if quality > 0 && (mediaType == "text/event-stream" || mediaType == "text/*" || mediaType == "*/*") {
				return true
			}
		}
	}
	return false
}

func parseLastEventID(w http.ResponseWriter, values []string) (*uint64, bool) {
	if len(values) == 0 {
		return nil, true
	}
	if len(values) != 1 || values[0] == "" || len(values[0]) > 20 || strings.TrimSpace(values[0]) != values[0] {
		writeProblem(w, http.StatusBadRequest, "invalid_last_event_id", "Last-Event-ID must be one decimal event ID")
		return nil, false
	}
	value, err := strconv.ParseUint(values[0], 10, 64)
	if err != nil || strconv.FormatUint(value, 10) != values[0] {
		writeProblem(w, http.StatusBadRequest, "invalid_last_event_id", "Last-Event-ID must be one decimal event ID")
		return nil, false
	}
	return &value, true
}

func encodeSSEEvent(event consolejobs.Event) ([]byte, error) {
	data, err := json.Marshal(jobEventDTO{
		APIVersion: APIVersion, ID: event.ID, JobID: event.JobID,
		Timestamp: event.Timestamp, Kind: event.Kind, Data: event.Data,
	})
	if err != nil {
		return nil, err
	}
	body := []byte(fmt.Sprintf("id: %d\nevent: job\ndata: ", event.ID))
	body = append(body, data...)
	body = append(body, '\n', '\n')
	return body, nil
}

func writeSSEChunk(w http.ResponseWriter, flusher http.Flusher, timeout time.Duration, body []byte) bool {
	controller := http.NewResponseController(w)
	deadlineSet := controller.SetWriteDeadline(time.Now().Add(timeout)) == nil
	if _, err := w.Write(body); err != nil {
		return false
	}
	flusher.Flush()
	if deadlineSet {
		_ = controller.SetWriteDeadline(time.Time{})
	}
	return true
}

func writeJobStoreError(w http.ResponseWriter, err error) {
	status, code, detail := publicJobStoreError(err)
	writeProblem(w, status, code, detail)
}

func publicJobStoreError(err error) (int, string, string) {
	switch {
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout, "request_canceled", "request was canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "deadline_exceeded", "request deadline was exceeded"
	case errors.Is(err, consolejobs.ErrNotFound):
		return http.StatusNotFound, "job_not_found", "job was not found"
	case errors.Is(err, consolejobs.ErrInvalid):
		return http.StatusBadRequest, "invalid_job_id", "job ID is invalid"
	case errors.Is(err, consolejobs.ErrUnavailable):
		return http.StatusServiceUnavailable, "jobs_unavailable", "job history is unavailable"
	default:
		return http.StatusInternalServerError, "internal_error", "internal server error"
	}
}

func writeJobReplayError(w http.ResponseWriter, err error) {
	var gap *consolejobs.EventGapError
	if errors.As(err, &gap) {
		w.Header().Set("Content-Type", "application/problem+json")
		writeJSONWithType(w, http.StatusGone, newEventGapProblem(gap))
		return
	}
	writeJobStoreError(w, err)
}

func newEventGapProblem(gap *consolejobs.EventGapError) eventGapProblem {
	return eventGapProblem{
		problem: problem{
			APIVersion: APIVersion, Type: "about:blank", Title: http.StatusText(http.StatusGone),
			Status: http.StatusGone, Detail: "requested job events are no longer retained", Code: "event_gap",
		},
		JobID: gap.JobID, RequestedAfter: gap.RequestedAfter, PrunedThrough: gap.PrunedThrough,
		OldestAvailable: gap.OldestAvailable, LatestID: gap.LatestID,
	}
}

func newJobSummaryDTO(job consolejobs.Job) jobSummaryDTO {
	return jobSummaryDTO{
		ID: job.ID, Kind: job.Kind, WorkspaceID: job.WorkspaceID, Mutating: job.Mutating,
		Status: job.Status, Progress: job.Progress, CreatedAt: job.CreatedAt,
		StartedAt: job.StartedAt, FinishedAt: job.FinishedAt, Revision: job.Revision,
	}
}

func newJobDetailDTO(job consolejobs.Job) jobDetailDTO {
	warnings := append([]string{}, job.Warnings...)
	result := make(map[string]any, len(job.Result))
	for key, value := range job.Result {
		if job.Kind == deploymentaudit.ActionPlan && key == "confirmation" {
			continue
		}
		result[key] = value
	}
	if len(result) == 0 {
		result = nil
	}
	return jobDetailDTO{
		jobSummaryDTO: newJobSummaryDTO(job), Warnings: warnings,
		Result: result, Error: job.Error, NeedsCompensationCheck: job.NeedsCompensationCheck,
	}
}

func terminalJobStatus(status consolejobs.Status) bool {
	switch status {
	case consolejobs.StatusSucceeded, consolejobs.StatusFailed, consolejobs.StatusCanceled, consolejobs.StatusInterrupted:
		return true
	default:
		return false
	}
}
