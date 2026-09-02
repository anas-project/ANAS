package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/application"
)

const (
	defaultMaximumConfigRequestBytes int64 = 1 << 20
	maximumConfigRequestBytes        int64 = 8 << 20

	ConfigAuditAttempt       = "attempt"
	ConfigAuditAuthorized    = "authorized"
	ConfigAuditSuccess       = "success"
	ConfigAuditFailure       = "failure"
	ConfigAuditIndeterminate = "indeterminate"

	configTerminalAuditTimeout = 5 * time.Second
)

// ConfigAuditEvent is deliberately a closed, value-free projection. It never
// carries a request body or a sensitive value; even sensitive changes contain
// only their schema path and add/change/remove classification.
type ConfigAuditEvent struct {
	Stage              string
	OperationID        string
	Actor              string
	WorkspaceID        string
	CurrentValidator   string
	CandidateValidator string
	Changes            []ConfigAuditChange
	FailureCode        string
}

type ConfigAuditChange struct {
	Path      string
	Change    string
	Effect    string
	Apply     string
	Sensitive bool
	Editable  bool
}

type ConfigAuditSink interface {
	RecordConfigEvent(context.Context, ConfigAuditEvent) error
}

type ConfigAuditSinkFunc func(context.Context, ConfigAuditEvent) error

func (function ConfigAuditSinkFunc) RecordConfigEvent(ctx context.Context, event ConfigAuditEvent) error {
	if function == nil {
		return errors.New("config audit sink is unavailable")
	}
	return function(ctx, event)
}

type ConfigOptions struct {
	Factory         application.ConfigServiceFactory
	Audit           ConfigAuditSink
	MaxRequestBytes int64
}

type configHTTPState struct {
	factory         application.ConfigServiceFactory
	audit           ConfigAuditSink
	maxRequestBytes int64
}

func newConfigHTTPState(options ConfigOptions) (*configHTTPState, error) {
	if options.Factory == nil {
		return nil, errors.New("config service factory is required")
	}
	if options.Audit == nil {
		return nil, errors.New("config audit sink is required")
	}
	if options.MaxRequestBytes < 0 || options.MaxRequestBytes > maximumConfigRequestBytes {
		return nil, errors.New("config request limit must be between 1 and 8388608 bytes")
	}
	if options.MaxRequestBytes == 0 {
		options.MaxRequestBytes = defaultMaximumConfigRequestBytes
	}
	return &configHTTPState{factory: options.Factory, audit: options.Audit, maxRequestBytes: options.MaxRequestBytes}, nil
}

type configSnapshotResponse struct {
	APIVersion       string                     `json:"api_version"`
	WorkspaceID      string                     `json:"workspace_id"`
	Managed          bool                       `json:"managed"`
	Config           application.ConfigDocument `json:"config"`
	AvailableModules []string                   `json:"available_modules"`
	Fields           []application.ConfigField  `json:"fields"`
}

type configValidationResponse struct {
	APIVersion    string                     `json:"api_version"`
	WorkspaceID   string                     `json:"workspace_id"`
	BaseValidator string                     `json:"base_validator,omitempty"`
	Config        application.ConfigDocument `json:"config"`
	Changes       []application.ConfigChange `json:"changes"`
}

type configPutResponse struct {
	APIVersion        string                     `json:"api_version"`
	WorkspaceID       string                     `json:"workspace_id"`
	PreviousValidator string                     `json:"previous_validator,omitempty"`
	Validator         string                     `json:"validator"`
	Config            application.ConfigDocument `json:"config"`
	AvailableModules  []string                   `json:"available_modules"`
	Fields            []application.ConfigField  `json:"fields"`
	Changes           []application.ConfigChange `json:"changes"`
}

func (h *handler) getConfig(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	service, ok := h.workspaceConfigService(w, params["ws"])
	if !ok {
		return
	}
	result, err := service.GetConfig(r.Context())
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	if result.Managed {
		etag, valid := strongConfigETag(result.Validator)
		if !valid {
			writeProblem(w, http.StatusInternalServerError, "config_validator_invalid", "workspace configuration is unavailable")
			return
		}
		w.Header().Set("ETag", etag)
	}
	writeJSON(w, http.StatusOK, configSnapshotResponse{
		APIVersion: APIVersion, WorkspaceID: params["ws"], Managed: result.Managed,
		Config: nonNilConfigDocument(result.Config), AvailableModules: nonNilConfigModules(result.AvailableModules),
		Fields: nonNilConfigFields(result.Fields),
	})
}

func (h *handler) validateConfig(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	service, ok := h.workspaceConfigService(w, params["ws"])
	if !ok {
		return
	}
	candidate, ok := h.decodeConfigCandidate(w, r)
	if !ok {
		return
	}
	result, err := service.ValidateConfig(r.Context(), candidate)
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, configValidationResponse{
		APIVersion: APIVersion, WorkspaceID: params["ws"], BaseValidator: result.BaseValidator,
		Config:  nonNilConfigDocument(result.Config),
		Changes: nonNilConfigChanges(result.Changes),
	})
}

func (h *handler) putConfig(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if h.config == nil {
		writeProblem(w, http.StatusServiceUnavailable, "config_unavailable", "workspace configuration is unavailable")
		return
	}
	if h.config.audit == nil {
		writeProblem(w, http.StatusServiceUnavailable, "audit_unavailable", "configuration audit is unavailable")
		return
	}
	principal, ok := PrincipalFromContext(r.Context())
	if !ok || principal.ID == "" {
		writeProblem(w, http.StatusInternalServerError, "authentication_unavailable", "authentication is unavailable")
		return
	}
	operationID, err := newConfigAuditOperationID()
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "audit_unavailable", "configuration audit is unavailable")
		return
	}
	baseAudit := ConfigAuditEvent{
		Stage: ConfigAuditAttempt, OperationID: operationID, Actor: principal.ID, WorkspaceID: params["ws"],
	}
	if err := h.config.audit.RecordConfigEvent(r.Context(), baseAudit); err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "audit_unavailable", "configuration audit is unavailable")
		return
	}

	observer := &configCommitAuditObserver{sink: h.config.audit, event: baseAudit}
	finishStage := ConfigAuditFailure
	failureCode := "request_rejected"
	defer func() {
		event := observer.event
		event.Stage = finishStage
		event.FailureCode = ""
		if finishStage == ConfigAuditFailure || finishStage == ConfigAuditIndeterminate {
			event.FailureCode = failureCode
		}
		// The durable attempt event above and the authorized event inside the
		// application lock are the fail-closed boundary. This terminal record is
		// supplementary because a successful config commit cannot be rolled back
		// if this independent journal becomes unavailable afterwards.
		auditContext, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), configTerminalAuditTimeout)
		defer cancel()
		_ = h.config.audit.RecordConfigEvent(auditContext, event)
	}()
	if _, ok := supportedQuery(w, r); !ok {
		failureCode = "invalid_query"
		return
	}
	service, ok := h.workspaceConfigService(w, params["ws"])
	if !ok {
		return
	}

	precondition, expectedValidator, ok := parseConfigPrecondition(w, r)
	if !ok {
		failureCode = "invalid_precondition"
		return
	}
	candidate, ok := h.decodeConfigCandidate(w, r)
	if !ok {
		failureCode = "invalid_request"
		return
	}
	result, err := service.PutConfig(r.Context(), application.ConfigPutRequest{
		OperationID: operationID, Candidate: candidate, Precondition: precondition, ExpectedValidator: expectedValidator,
	}, observer)
	if err != nil {
		failureCode = configApplicationErrorCode(err)
		if failureCode == "config_recovery_required" {
			// The WAL is durable or its manifest publication is durability-
			// ambiguous. A surviving manifest rolls forward; one lost before its
			// directory fsync leaves the old tuple. Either way, describing this as
			// an ordinary rejected write would be false.
			finishStage = ConfigAuditIndeterminate
		}
		writeApplicationError(w, err)
		return
	}
	etag, valid := strongConfigETag(result.Validator)
	if !valid {
		failureCode = "config_validator_invalid"
		writeProblem(w, http.StatusInternalServerError, failureCode, "workspace configuration is unavailable")
		return
	}
	finishStage = ConfigAuditSuccess
	w.Header().Set("ETag", etag)
	writeJSON(w, http.StatusOK, configPutResponse{
		APIVersion: APIVersion, WorkspaceID: params["ws"], PreviousValidator: result.PreviousValidator,
		Validator: result.Validator, Config: nonNilConfigDocument(result.Config),
		AvailableModules: nonNilConfigModules(result.AvailableModules),
		Fields:           nonNilConfigFields(result.Fields), Changes: nonNilConfigChanges(result.Changes),
	})
}

type configCommitAuditObserver struct {
	sink  ConfigAuditSink
	event ConfigAuditEvent
}

func (observer *configCommitAuditObserver) BeforeConfigCommit(ctx context.Context, intent application.ConfigCommitIntent) error {
	if observer == nil || observer.sink == nil {
		return errors.New("config audit sink is unavailable")
	}
	event := observer.event
	if intent.OperationID == "" || intent.OperationID != event.OperationID {
		return errors.New("config audit operation ID does not match commit intent")
	}
	event.Stage = ConfigAuditAuthorized
	event.CurrentValidator = intent.CurrentValidator
	event.CandidateValidator = intent.CandidateValidator
	event.Changes = configAuditChanges(intent.Changes)
	if err := observer.sink.RecordConfigEvent(ctx, event); err != nil {
		return err
	}
	observer.event = event
	return nil
}

func configAuditChanges(changes []application.ConfigChange) []ConfigAuditChange {
	if len(changes) == 0 {
		return nil
	}
	result := make([]ConfigAuditChange, 0, len(changes))
	for _, change := range changes {
		result = append(result, ConfigAuditChange{
			Path: change.Path, Change: change.Change, Effect: change.Effect, Apply: change.Apply,
			Sensitive: change.Sensitive, Editable: change.Editable,
		})
	}
	return result
}

func (h *handler) workspaceConfigService(w http.ResponseWriter, workspaceID string) (application.ConfigService, bool) {
	workspacePath, ok := h.registry.Resolve(workspaceID)
	if !ok {
		writeProblem(w, http.StatusNotFound, "workspace_not_found", "workspace was not found")
		return nil, false
	}
	if h.config == nil || h.config.factory == nil {
		writeProblem(w, http.StatusServiceUnavailable, "config_unavailable", "workspace configuration is unavailable")
		return nil, false
	}
	service := h.config.factory(workspacePath)
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, "config_unavailable", "workspace configuration is unavailable")
		return nil, false
	}
	return service, true
}

func (h *handler) decodeConfigCandidate(w http.ResponseWriter, r *http.Request) (application.ConfigCandidate, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return application.ConfigCandidate{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.config.maxRequestBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeProblem(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large")
		} else {
			writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		}
		return application.ConfigCandidate{}, false
	}
	defer clear(body)
	if err := validateUniqueJSONKeys(body); err != nil || validateConfigCandidateEnvelope(body) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		return application.ConfigCandidate{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var candidate application.ConfigCandidate
	if err := decoder.Decode(&candidate); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		return application.ConfigCandidate{}, false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must contain one JSON object")
		return application.ConfigCandidate{}, false
	}
	if candidate.Document == nil {
		writeProblem(w, http.StatusBadRequest, "config_candidate_invalid", "config must be a JSON object")
		return application.ConfigCandidate{}, false
	}
	if candidate.Sensitive == nil {
		candidate.Sensitive = map[string]application.ConfigSensitiveMutation{}
	}
	return candidate, true
}

func validateUniqueJSONKeys(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := validateUniqueJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON contains trailing data")
		}
		return err
	}
	return nil
}

func validateUniqueJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 128 {
		return errors.New("JSON nesting is too deep")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is invalid")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("JSON object contains a duplicate key")
			}
			seen[key] = struct{}{}
			if err := validateUniqueJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("JSON object is not closed")
		}
	case '[':
		for decoder.More() {
			if err := validateUniqueJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("JSON array is not closed")
		}
	default:
		return errors.New("JSON delimiter is invalid")
	}
	return nil
}

func validateConfigCandidateEnvelope(body []byte) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil || envelope == nil {
		return errors.New("config candidate must be an object")
	}
	for key := range envelope {
		if key != "config" && key != "sensitive" {
			return errors.New("config candidate contains an unknown field")
		}
	}
	if _, exists := envelope["config"]; !exists {
		return errors.New("config candidate omits config")
	}
	rawSensitive, exists := envelope["sensitive"]
	if !exists {
		return nil
	}
	var sensitive map[string]json.RawMessage
	if err := json.Unmarshal(rawSensitive, &sensitive); err != nil || sensitive == nil {
		return errors.New("sensitive operations must be an object")
	}
	for _, rawMutation := range sensitive {
		var mutation map[string]json.RawMessage
		if err := json.Unmarshal(rawMutation, &mutation); err != nil || mutation == nil {
			return errors.New("sensitive operation must be an object")
		}
		for key := range mutation {
			if key != "operation" && key != "value" {
				return errors.New("sensitive operation contains an unknown field")
			}
		}
		rawOperation, exists := mutation["operation"]
		if !exists {
			return errors.New("sensitive operation omits operation")
		}
		var operation application.ConfigSensitiveOperation
		if err := json.Unmarshal(rawOperation, &operation); err != nil {
			return errors.New("sensitive operation name must be a string")
		}
		rawValue, hasValue := mutation["value"]
		switch operation {
		case application.ConfigSensitiveUnchanged, application.ConfigSensitiveUnset:
			if hasValue {
				return errors.New("unchanged and unset operations must omit value")
			}
		case application.ConfigSensitiveSet:
			var value any
			if !hasValue || json.Unmarshal(rawValue, &value) != nil {
				return errors.New("set operation requires a string value")
			}
			if _, ok := value.(string); !ok {
				return errors.New("set operation requires a string value")
			}
		default:
			return errors.New("sensitive operation name is invalid")
		}
	}
	return nil
}

func parseConfigPrecondition(w http.ResponseWriter, r *http.Request) (application.ConfigPreconditionMode, string, bool) {
	ifMatch := r.Header.Values("If-Match")
	ifNoneMatch := r.Header.Values("If-None-Match")
	if len(ifMatch) != 0 && len(ifNoneMatch) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_precondition", "If-Match and If-None-Match cannot be combined")
		return "", "", false
	}
	if len(ifMatch) == 0 && len(ifNoneMatch) == 0 {
		writeProblem(w, http.StatusPreconditionRequired, "config_precondition_required", "a configuration precondition is required")
		return "", "", false
	}
	if len(ifMatch) > 1 || len(ifNoneMatch) > 1 {
		writeProblem(w, http.StatusBadRequest, "invalid_precondition", "configuration precondition must contain one value")
		return "", "", false
	}
	if len(ifMatch) == 1 {
		value := strings.TrimSpace(ifMatch[0])
		if strings.Contains(value, ",") {
			writeProblem(w, http.StatusBadRequest, "invalid_precondition", "If-Match must contain one entity tag")
			return "", "", false
		}
		if value == "*" {
			writeProblem(w, http.StatusPreconditionFailed, "config_precondition_failed", "If-Match requires a strong configuration ETag")
			return "", "", false
		}
		if strings.HasPrefix(value, "W/") {
			if !validStrongEntityTag(strings.TrimPrefix(value, "W/")) {
				writeProblem(w, http.StatusBadRequest, "invalid_precondition", "If-Match is malformed")
				return "", "", false
			}
			writeProblem(w, http.StatusPreconditionFailed, "config_precondition_failed", "weak configuration ETags are not accepted")
			return "", "", false
		}
		if !validStrongEntityTag(value) {
			writeProblem(w, http.StatusBadRequest, "invalid_precondition", "If-Match is malformed")
			return "", "", false
		}
		return application.ConfigPreconditionMatch, value[1 : len(value)-1], true
	}

	value := strings.TrimSpace(ifNoneMatch[0])
	if strings.Contains(value, ",") {
		writeProblem(w, http.StatusBadRequest, "invalid_precondition", "If-None-Match must contain one value")
		return "", "", false
	}
	if value == "*" {
		return application.ConfigPreconditionMustCreate, "", true
	}
	if strings.HasPrefix(value, "W/") {
		if !validStrongEntityTag(strings.TrimPrefix(value, "W/")) {
			writeProblem(w, http.StatusBadRequest, "invalid_precondition", "If-None-Match is malformed")
			return "", "", false
		}
		writeProblem(w, http.StatusPreconditionFailed, "config_precondition_failed", "initial configuration requires If-None-Match: *")
		return "", "", false
	}
	if validStrongEntityTag(value) {
		writeProblem(w, http.StatusPreconditionFailed, "config_precondition_failed", "initial configuration requires If-None-Match: *")
		return "", "", false
	}
	writeProblem(w, http.StatusBadRequest, "invalid_precondition", "If-None-Match is malformed")
	return "", "", false
}

func validStrongEntityTag(value string) bool {
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return false
	}
	for index := 1; index < len(value)-1; index++ {
		char := value[index]
		if char == '"' || char == 0x7f || char < 0x21 {
			return false
		}
	}
	return true
}

func strongConfigETag(validator string) (string, bool) {
	if len(validator) != len("cfgv-")+64 || !strings.HasPrefix(validator, "cfgv-") {
		return "", false
	}
	for _, char := range validator[len("cfgv-"):] {
		if char < '0' || char > '9' && char < 'a' || char > 'f' {
			return "", false
		}
	}
	return `"` + validator + `"`, true
}

func configApplicationErrorCode(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return "request_canceled"
	}
	if applicationError, ok := application.ErrorOf(err); ok && applicationError.Code != "" {
		return applicationError.Code
	}
	return "internal_error"
}

func newConfigAuditOperationID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "cfg-" + hex.EncodeToString(random[:]), nil
}

func nonNilConfigDocument(document application.ConfigDocument) application.ConfigDocument {
	if document == nil {
		return application.ConfigDocument{}
	}
	return document
}

func nonNilConfigFields(fields []application.ConfigField) []application.ConfigField {
	if fields == nil {
		return []application.ConfigField{}
	}
	return fields
}

func nonNilConfigModules(modules []string) []string {
	if modules == nil {
		return []string{}
	}
	return modules
}

func nonNilConfigChanges(changes []application.ConfigChange) []application.ConfigChange {
	if changes == nil {
		return []application.ConfigChange{}
	}
	return changes
}
