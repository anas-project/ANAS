package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/anas-project/ANAS/internal/application"
)

// NewWorkspaceQueryService is the daemon query boundary. In contrast with the
// CLI-compatible application constructor it always binds a live Compose probe,
// so persisted active.yml runtime_status can never masquerade as current state.
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
	cli, err := detectComposeForExecution(ctx, true)
	if err != nil {
		return application.RuntimeSummary{}, fmt.Errorf("detect Compose: %w", err)
	}
	app, moduleRoot, _, err := loadDeploymentApp(stateDir(workspace), deploymentID, cli)
	if err != nil {
		return application.RuntimeSummary{}, fmt.Errorf("load active deployment: %w", err)
	}
	app.commandContext = ctx
	app.restrictedProcessEnvironment = true
	modules := make([]application.ModuleRuntimeStatus, 0, len(app.order))
	for _, name := range app.order {
		module := app.reg[name]
		if module.RuntimeType != "compose" {
			modules = append(modules, application.ModuleRuntimeStatus{
				Module: name, Runtime: "not_applicable", Health: "not_applicable",
			})
			continue
		}
		dir := filepath.Join(moduleRoot, name)
		environment := app.commandEnvironment(app.moduleEnv(dir))
		output, outputErr := cli.OutputFileContext(ctx, dir, name, app.releaseComposeFile(name), environment, true,
			"ps", "--all", "--format", "json")
		if outputErr != nil {
			return application.RuntimeSummary{}, fmt.Errorf("inspect module %s containers: %w", name, outputErr)
		}
		records, parseErr := parseComposePSRecords([]byte(output))
		if parseErr != nil {
			return application.RuntimeSummary{}, fmt.Errorf("parse module %s container status: %w", name, parseErr)
		}
		modules = append(modules, summarizeModuleRuntime(name, records))
	}
	return summarizeDeploymentRuntime(modules), nil
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
