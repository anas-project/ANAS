package application

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/anas-project/ANAS/internal/deployment"
)

const moduleCommandMaxOutputBytes = 1 << 20

type moduleCommandExecutor interface {
	Execute(context.Context, moduleCommandExecution, EventSink) (moduleCommandExecutionResult, error)
}

type moduleCommandExecution struct {
	DeploymentID string
	Module       deployment.Module
	Command      deployment.ModuleCommand
	ModuleRoot   string
	Parameters   map[string]any
	Env          map[string]string
	Secrets      map[string]string
}

type moduleCommandExecutionResult struct {
	Changed bool
	Result  map[string]any
}

type processModuleCommandExecutor struct{}

type moduleCommandRequest struct {
	ABI          string                 `json:"abi"`
	InvocationID string                 `json:"invocation_id"`
	Module       moduleCommandModuleRef `json:"module"`
	DeploymentID string                 `json:"deployment_id"`
	Command      string                 `json:"command"`
	Handler      string                 `json:"handler"`
	Parameters   map[string]any         `json:"parameters"`
	Env          map[string]string      `json:"env"`
	Secrets      map[string]string      `json:"secrets"`
}

type moduleCommandModuleRef struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Revision int    `json:"revision"`
}

type moduleCommandRecord struct {
	Type    string          `json:"type"`
	Phase   string          `json:"phase,omitempty"`
	Current *int64          `json:"current,omitempty"`
	Total   *int64          `json:"total,omitempty"`
	Unit    string          `json:"unit,omitempty"`
	Code    string          `json:"code,omitempty"`
	Message string          `json:"message,omitempty"`
	Changed *bool           `json:"changed,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

func (processModuleCommandExecutor) Execute(ctx context.Context, execution moduleCommandExecution, events EventSink) (moduleCommandExecutionResult, error) {
	if len(execution.Module.CommandExecutor.Command) != 1 {
		return moduleCommandExecutionResult{}, errors.New("module command executor is not frozen")
	}
	relative := filepath.Clean(execution.Module.CommandExecutor.Command[0])
	if filepath.IsAbs(relative) || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return moduleCommandExecutionResult{}, errors.New("module command executor path is invalid")
	}
	executable := filepath.Join(execution.ModuleRoot, relative)
	info, err := os.Lstat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return moduleCommandExecutionResult{}, errors.New("module command executor is missing or not executable")
	}
	invocationID, err := newModuleCommandInvocationID()
	if err != nil {
		return moduleCommandExecutionResult{}, fmt.Errorf("create module command invocation ID: %w", err)
	}
	requestBody, err := json.Marshal(moduleCommandRequest{
		ABI: "anas.module-command/v1", InvocationID: invocationID,
		Module:       moduleCommandModuleRef{Name: execution.Module.Name, Version: execution.Module.Version, Revision: execution.Module.Revision},
		DeploymentID: execution.DeploymentID, Command: execution.Command.ID, Handler: execution.Command.Handler,
		Parameters: execution.Parameters, Env: execution.Env, Secrets: execution.Secrets,
	})
	if err != nil {
		return moduleCommandExecutionResult{}, fmt.Errorf("encode module command request: %w", err)
	}
	requestBody = append(requestBody, '\n')

	command := exec.CommandContext(ctx, executable)
	command.Dir = execution.ModuleRoot
	command.Env = []string{}
	command.Stdin = bytes.NewReader(requestBody)
	command.Stderr = io.Discard
	configureModuleCommandProcess(command)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return moduleCommandExecutionResult{}, fmt.Errorf("open module command stdout: %w", err)
	}
	if err := command.Start(); err != nil {
		return moduleCommandExecutionResult{}, fmt.Errorf("start module command: %w", err)
	}

	sensitiveValues := make([]string, 0, len(execution.Secrets))
	for _, value := range execution.Secrets {
		if value != "" {
			sensitiveValues = append(sensitiveValues, value)
		}
	}
	result, protocolErr := decodeModuleCommandOutput(stdout, events, sensitiveValues...)
	if protocolErr != nil && command.Process != nil {
		_ = terminateModuleCommandProcess(command)
	}
	waitErr := command.Wait()
	if protocolErr != nil {
		return moduleCommandExecutionResult{}, fmt.Errorf("%w: %v", errModuleCommandProtocol, protocolErr)
	}
	if waitErr != nil {
		return moduleCommandExecutionResult{}, fmt.Errorf("module command exited unsuccessfully")
	}
	return result, nil
}

func decodeModuleCommandOutput(stdout io.Reader, events EventSink, sensitiveValues ...string) (moduleCommandExecutionResult, error) {
	limited := &io.LimitedReader{R: stdout, N: moduleCommandMaxOutputBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 64*1024), moduleCommandMaxOutputBytes)
	seenResult := false
	result := moduleCommandExecutionResult{}
	for scanner.Scan() {
		if seenResult {
			return moduleCommandExecutionResult{}, errors.New("module command protocol has records after result")
		}
		var record moduleCommandRecord
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		decoder.UseNumber()
		if err := decoder.Decode(&record); err != nil {
			return moduleCommandExecutionResult{}, fmt.Errorf("decode module command record: %w", err)
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			return moduleCommandExecutionResult{}, errors.New("module command record contains trailing JSON")
		}
		if moduleCommandRecordContainsSensitiveValue(record, sensitiveValues) {
			return moduleCommandExecutionResult{}, errors.New("module command record contains a sensitive value")
		}
		switch record.Type {
		case "progress":
			if record.Phase == "" || record.Unit == "" || record.Current == nil || record.Changed != nil || len(record.Result) != 0 || record.Code != "" || record.Message != "" {
				return moduleCommandExecutionResult{}, errors.New("invalid module command progress record")
			}
			if *record.Current < 0 || (record.Total != nil && (*record.Total <= 0 || *record.Current > *record.Total)) {
				return moduleCommandExecutionResult{}, errors.New("invalid module command progress range")
			}
			total := int64(0)
			if record.Total != nil {
				total = *record.Total
			}
			events.Progress(ProgressEvent{Phase: record.Phase, Current: *record.Current, Total: total, Unit: record.Unit})
		case "warning":
			if record.Code == "" || record.Message == "" || record.Current != nil || record.Total != nil || record.Changed != nil || len(record.Result) != 0 || record.Phase != "" || record.Unit != "" {
				return moduleCommandExecutionResult{}, errors.New("invalid module command warning record")
			}
			events.Warning(WarningEvent{Code: record.Code, Message: record.Message})
		case "result":
			if record.Changed == nil || len(record.Result) == 0 || bytes.Equal(record.Result, []byte("null")) || record.Current != nil || record.Total != nil || record.Phase != "" || record.Unit != "" || record.Code != "" || record.Message != "" {
				return moduleCommandExecutionResult{}, errors.New("invalid module command result record")
			}
			var rawResult map[string]any
			resultDecoder := json.NewDecoder(bytes.NewReader(record.Result))
			resultDecoder.UseNumber()
			if err := resultDecoder.Decode(&rawResult); err != nil || rawResult == nil {
				return moduleCommandExecutionResult{}, errors.New("module command result must be an object")
			}
			normalized, err := normalizeModuleCommandResult(rawResult)
			if err != nil {
				return moduleCommandExecutionResult{}, err
			}
			if moduleCommandValueContainsSensitiveValue(normalized, sensitiveValues) {
				return moduleCommandExecutionResult{}, errors.New("module command result contains a sensitive value")
			}
			result = moduleCommandExecutionResult{Changed: *record.Changed, Result: normalized}
			seenResult = true
		default:
			return moduleCommandExecutionResult{}, errors.New("unknown module command record type")
		}
	}
	if err := scanner.Err(); err != nil {
		return moduleCommandExecutionResult{}, fmt.Errorf("read module command output: %w", err)
	}
	if limited.N <= 0 {
		return moduleCommandExecutionResult{}, errors.New("module command output exceeds 1 MiB")
	}
	if !seenResult {
		return moduleCommandExecutionResult{}, errors.New("module command result record is missing")
	}
	return result, nil
}

func moduleCommandRecordContainsSensitiveValue(record moduleCommandRecord, sensitiveValues []string) bool {
	publicText := []string{record.Type, record.Phase, record.Unit, record.Code, record.Message, string(record.Result)}
	for _, sensitive := range sensitiveValues {
		if sensitive == "" {
			continue
		}
		for _, value := range publicText {
			if strings.Contains(value, sensitive) {
				return true
			}
		}
	}
	return false
}

func moduleCommandValueContainsSensitiveValue(value any, sensitiveValues []string) bool {
	switch typed := value.(type) {
	case string:
		for _, sensitive := range sensitiveValues {
			if sensitive != "" && strings.Contains(typed, sensitive) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if moduleCommandValueContainsSensitiveValue(item, sensitiveValues) {
				return true
			}
		}
	case map[string]any:
		for key, item := range typed {
			if moduleCommandValueContainsSensitiveValue(key, sensitiveValues) || moduleCommandValueContainsSensitiveValue(item, sensitiveValues) {
				return true
			}
		}
	}
	return false
}

func normalizeModuleCommandResult(input map[string]any) (map[string]any, error) {
	if input == nil {
		return map[string]any{}, nil
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		if key == "" || len(key) > 63 {
			return nil, errors.New("module command result key is invalid")
		}
		normalized, err := normalizeModuleCommandResultValue(value)
		if err != nil {
			return nil, fmt.Errorf("module command result field is invalid: %w", err)
		}
		result[key] = normalized
	}
	return result, nil
}

func normalizeModuleCommandResultValue(value any) (any, error) {
	switch typed := value.(type) {
	case nil, string, bool:
		return typed, nil
	case json.Number:
		integer, err := typed.Int64()
		if err != nil {
			return nil, errors.New("must be an integer")
		}
		return integer, nil
	case []any:
		if len(typed) > 1024 {
			return nil, errors.New("array exceeds 1024 items")
		}
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			switch item.(type) {
			case nil, string, bool, json.Number:
			default:
				return nil, errors.New("array items must be scalar")
			}
			normalized, err := normalizeModuleCommandResultValue(item)
			if err != nil {
				return nil, err
			}
			out = append(out, normalized)
		}
		return out, nil
	default:
		return nil, errors.New("must be a scalar or scalar array")
	}
}

func newModuleCommandInvocationID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
