package runner

// TEST_CASES: TEMP-T-010

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/compose"
)

// NewWorkspaceQueryService binds the live probe used by CLI and HTTP queries,
// so persisted active.yml runtime_status cannot masquerade as current state.
func NewWorkspaceQueryService(workspace string) *application.Service {
	return application.NewService(workspace).WithRuntimeProbe(workspaceRuntimeProbe{})
}

type workspaceRuntimeProbe struct{}

type composePSRecord struct {
	Service string `json:"Service"`
	State   string `json:"State"`
	Health  string `json:"Health"`
}

func (workspaceRuntimeProbe) InspectRuntime(ctx context.Context, workspace, deploymentID string) (application.RuntimeSummary, error) {
	if ctx == nil {
		return application.RuntimeSummary{}, errors.New("runtime probe context is nil")
	}
	if err := validateDeploymentID(deploymentID); err != nil {
		return application.RuntimeSummary{}, err
	}
	cli, err := compose.DetectContext(ctx, runtimeProbeEnvironment())
	if err != nil {
		return application.RuntimeSummary{}, fmt.Errorf("detect Compose: %w", err)
	}
	app, moduleRoot, _, err := loadDeploymentApp(stateDir(workspace), deploymentID, cli)
	if err != nil {
		return application.RuntimeSummary{}, fmt.Errorf("load active deployment: %w", err)
	}
	app.commandContext = ctx
	app.restrictedProcessEnvironment = true
	temporary, temporaryErr := app.temporaryStorageStatus()
	modules := make([]application.ModuleRuntimeStatus, 0, len(app.order))
	var inspectionErrors []error
	for _, name := range app.order {
		module := app.reg[name]
		if module.RuntimeType != "compose" {
			modules = append(modules, application.ModuleRuntimeStatus{
				Module: name, Runtime: "not_applicable", Health: "not_applicable",
			})
			continue
		}
		dir := filepath.Join(moduleRoot, name)
		if module.SourceDir != "" {
			dir = module.SourceDir
		}
		output, outputErr := app.outputCompose(dir, name, app.releaseComposeFile(name), app.moduleEnv(dir), "ps", "--all", "--format", "json")
		summary := application.ModuleRuntimeStatus{Module: name, Runtime: "unknown", Health: "none"}
		if outputErr != nil {
			inspectionErrors = append(inspectionErrors, fmt.Errorf("inspect module %s containers: %w", name, outputErr))
		} else {
			records, parseErr := parseComposePSRecords([]byte(output))
			if parseErr != nil {
				inspectionErrors = append(inspectionErrors, fmt.Errorf("parse module %s container status: %w", name, parseErr))
			} else {
				summary = summarizeModuleRuntime(name, records)
			}
		}
		summary.TempStorage = summarizeModuleTemporaryStorage(module, temporary, temporaryErr)
		if summary.TempStorage != nil && summary.TempStorage.State != "ok" && summary.TempStorage.State != "not_applicable" {
			summary.Health = "unhealthy"
		}
		modules = append(modules, summary)
	}
	return summarizeDeploymentRuntime(modules), errors.Join(inspectionErrors...)
}

// Runtime queries use the operator's Docker endpoint while keeping ambient
// process values restricted. Deployment values cannot select another daemon.
func runtimeProbeEnvironment() []string {
	environment := restrictedBaseProcessEnvironment()
	for _, key := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH"} {
		if value, present := os.LookupEnv(key); present {
			environment = append(environment, key+"="+value)
		}
	}
	return environment
}

func summarizeModuleTemporaryStorage(module Module, storage TemporaryStorageStatus, inspectionErr error) *application.ModuleTemporaryStorageStatus {
	if len(module.TemporaryDirectories) == 0 {
		return nil
	}
	result := &application.ModuleTemporaryStorageStatus{State: "ok", Issues: []application.ModuleTemporaryStorageIssue{}}
	if inspectionErr != nil {
		result.State = "unknown"
		result.Issues = append(result.Issues, application.ModuleTemporaryStorageIssue{Code: "temp_inspection_failed", Name: ""})
		return result
	}
	appliedRoot := storage.AppliedRoot
	if appliedRoot == "" {
		appliedRoot = storage.DesiredRoot
	}
	expected := map[string]TemporaryDirectory{}
	for _, declaration := range module.TemporaryDirectories {
		expected[declaration.Service+"\x00"+declaration.Name] = declaration
	}
	present := map[string]bool{}
	active := map[string]bool{}
	for _, directory := range storage.Directories {
		if directory.Module != module.Name || (directory.State != "active" && directory.State != "reserved") || directory.Root != appliedRoot {
			continue
		}
		key := directory.Service + "\x00" + directory.Name
		if _, declared := expected[key]; !declared {
			continue
		}
		active[directory.ID] = true
		present[key] = true
		if directory.FreeBytes != nil && (result.FreeBytes == nil || *directory.FreeBytes < *result.FreeBytes) {
			result.FreeBytes = directory.FreeBytes
		}
		if directory.FreeInodes != nil && (result.FreeInodes == nil || *directory.FreeInodes < *result.FreeInodes) {
			result.FreeInodes = directory.FreeInodes
		}
	}
	for _, issue := range storage.Issues {
		if issue.Module != module.Name || !active[issue.LeaseID] {
			continue
		}
		name := ""
		for _, declaration := range module.TemporaryDirectories {
			if declaration.Name == issue.Name {
				name = declaration.Name
				break
			}
		}
		result.Issues = append(result.Issues, application.ModuleTemporaryStorageIssue{Code: issue.Code, Name: name})
		switch issue.Code {
		case "temp_low_space":
			if result.State == "ok" {
				result.State = "low_space"
			}
		default:
			result.State = "unavailable"
		}
	}
	for _, declaration := range module.TemporaryDirectories {
		if !present[declaration.Service+"\x00"+declaration.Name] {
			result.State = "unavailable"
			result.Issues = append(result.Issues, application.ModuleTemporaryStorageIssue{Code: "temp_lease_missing", Name: declaration.Name})
		}
	}
	return result
}

func parseComposePSRecords(body []byte) ([]composePSRecord, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return []composePSRecord{}, nil
	}
	if body[0] == '[' {
		var records []composePSRecord
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&records); err != nil {
			// Compose adds fields over time. Retry without rejecting fields while
			// retaining strict shape validation for the fields ANAS consumes.
			if err := json.Unmarshal(body, &records); err != nil {
				return nil, err
			}
		}
		if records == nil {
			records = []composePSRecord{}
		}
		return records, nil
	}
	records := []composePSRecord{}
	for _, line := range bytes.Split(body, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var record composePSRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func summarizeModuleRuntime(module string, records []composePSRecord) application.ModuleRuntimeStatus {
	result := application.ModuleRuntimeStatus{Module: module, Runtime: "stopped", Health: "none", Containers: len(records)}
	if len(records) == 0 {
		return result
	}
	running := 0
	healthSeen, healthy, starting, unhealthy := false, 0, 0, 0
	for _, record := range records {
		if strings.EqualFold(strings.TrimSpace(record.State), "running") {
			running++
		}
		switch strings.ToLower(strings.TrimSpace(record.Health)) {
		case "healthy":
			healthSeen, healthy = true, healthy+1
		case "starting":
			healthSeen, starting = true, starting+1
		case "unhealthy":
			healthSeen, unhealthy = true, unhealthy+1
		}
	}
	switch {
	case running == len(records):
		result.Runtime = "running"
	case running == 0:
		result.Runtime = "stopped"
	default:
		result.Runtime = "degraded"
	}
	switch {
	case unhealthy > 0:
		result.Health = "unhealthy"
	case starting > 0:
		result.Health = "starting"
	case healthSeen && healthy > 0:
		result.Health = "healthy"
	}
	return result
}

func summarizeDeploymentRuntime(modules []application.ModuleRuntimeStatus) application.RuntimeSummary {
	result := application.RuntimeSummary{Status: "not_applicable", Modules: append([]application.ModuleRuntimeStatus{}, modules...)}
	composeModules, runningModules, stoppedModules := 0, 0, 0
	healthSeen, allHealthy := false, true
	for _, module := range modules {
		if module.Runtime == "not_applicable" {
			continue
		}
		composeModules++
		switch module.Runtime {
		case "running":
			runningModules++
		case "stopped":
			stoppedModules++
		}
		switch module.Health {
		case "healthy":
			healthSeen = true
		case "unhealthy", "starting":
			healthSeen, allHealthy = true, false
		}
	}
	switch {
	case composeModules == 0:
		result.Status = "not_applicable"
	case runningModules == composeModules:
		result.Status = "running"
	case stoppedModules == composeModules:
		result.Status = "stopped"
	default:
		result.Status = "degraded"
	}
	if healthSeen {
		value := allHealthy
		result.Healthy = &value
	}
	return result
}
