package runner

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/buildinfo"
	"github.com/anas-project/ANAS/internal/consoleclient"
	"github.com/anas-project/ANAS/internal/hostaction"
)

type hostConsoleClient interface {
	InvokeIncusPreflight(context.Context, string, string) (map[string]any, string, error)
	InvokeIncusPlan(context.Context, string, string, json.RawMessage, string) (map[string]any, string, error)
	InvokeIncusImagePrunePlan(context.Context, string, string) (map[string]any, string, error)
	IssueHostActionConfirmation(context.Context, string, string, string) (map[string]any, error)
	InvokeIncusApply(context.Context, string, string, string, string, json.RawMessage, string) (map[string]any, string, error)
	InvokeIncusImagePruneApply(context.Context, string, string, string, string) (map[string]any, string, error)
	GetJob(context.Context, string) (map[string]any, error)
}

var newHostConsoleClientFromStdin = func() (hostConsoleClient, error) {
	envelope, err := consoleclient.ReadEnvelope(os.Stdin)
	if err != nil {
		return nil, err
	}
	return consoleclient.New(envelope)
}

var newHostConsoleClientFromEnvelope = func(envelope consoleclient.Envelope) (hostConsoleClient, error) {
	return consoleclient.New(envelope)
}

// Inventory is safe without a workspace or root access. It deliberately does
// not query/activate a host socket or claim that the root executor is installed.
// All invoke/plan/apply/cancel forms remain unavailable until the shared job
// broker and actual supervised host execution have been wired and verified.
func runHost(args []string, jsonMode bool) error {
	if len(args) == 0 {
		return usageErrorf("usage: anas host actions|incus-preflight|incus-plan|incus-confirm|incus-apply|incus-prune-plan|incus-prune-apply|job ...")
	}
	switch args[0] {
	case "actions":
		return runHostActions(args[1:], jsonMode)
	case "incus-preflight":
		return runHostIncusPreflight(args[1:], jsonMode)
	case "incus-plan":
		return runHostIncusPlan(args[1:], jsonMode)
	case "incus-confirm":
		return runHostIncusConfirm(args[1:], jsonMode)
	case "incus-apply":
		return runHostIncusApply(args[1:], jsonMode)
	case "incus-prune-plan":
		return runHostIncusPrunePlan(args[1:], jsonMode)
	case "incus-prune-apply":
		return runHostIncusPruneApply(args[1:], jsonMode)
	case "job":
		return runHostJob(args[1:], jsonMode)
	default:
		return usageErrorf("usage: anas host actions|incus-preflight|incus-plan|incus-confirm|incus-apply|incus-prune-plan|incus-prune-apply|job ...")
	}
}

func runHostIncusPrunePlan(args []string, jsonMode bool) error {
	fs := flag.NewFlagSet("host incus-prune-plan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerJSONFlag(fs)
	workspace := fs.String("w", "", "workspace id")
	fs.StringVar(workspace, "workspace", "", "workspace id")
	sessionJSON := fs.String("session-json", "", "read console session envelope from stdin with '-'")
	idempotencyKey := fs.String("idempotency-key", "", "opaque idempotency key")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *workspace == "" || *sessionJSON != "-" {
		return usageErrorf("usage: anas host incus-prune-plan -w WORKSPACE --session-json - [--idempotency-key KEY] [--json]")
	}
	key, err := hostCLIKey(*idempotencyKey)
	if err != nil {
		return err
	}
	client, err := newHostConsoleClientFromStdin()
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	response, location, err := client.InvokeIncusImagePrunePlan(context.Background(), *workspace, key)
	if err != nil {
		return hostConsoleError(err, true, key)
	}
	return printHostJobQueued(jsonMode, hostaction.ActionImagePrunePlan, *workspace, key, location, response)
}

func runHostIncusPruneApply(args []string, jsonMode bool) error {
	fs := flag.NewFlagSet("host incus-prune-apply", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerJSONFlag(fs)
	workspace := fs.String("w", "", "workspace id")
	fs.StringVar(workspace, "workspace", "", "workspace id")
	requestJSON := fs.String("request-json", "", "read apply request envelope from stdin with '-'")
	idempotencyKey := fs.String("idempotency-key", "", "opaque idempotency key")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *workspace == "" || *requestJSON != "-" {
		return usageErrorf("usage: anas host incus-prune-apply -w WORKSPACE --request-json - [--idempotency-key KEY] [--json]")
	}
	key, err := hostCLIKey(*idempotencyKey)
	if err != nil {
		return err
	}
	request, err := readHostPruneApplyRequest(os.Stdin)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	client, err := newHostConsoleClientFromEnvelope(request.Session)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	response, location, err := client.InvokeIncusImagePruneApply(context.Background(), *workspace, request.PlanJobID, request.ConfirmationToken, key)
	if err != nil {
		return hostConsoleError(err, true, key)
	}
	return printHostJobQueued(jsonMode, hostaction.ActionImagePrune, *workspace, key, location, response)
}

func runHostIncusPlan(args []string, jsonMode bool) error {
	fs := flag.NewFlagSet("host incus-plan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerJSONFlag(fs)
	workspace := fs.String("w", "", "workspace id")
	fs.StringVar(workspace, "workspace", "", "workspace id")
	phase := fs.String("phase", "", "install|configure|enroll|uninstall|observer|forwarding-permission")
	request := fs.String("request-json", "{}", "typed Incus provisioning request JSON")
	sessionJSON := fs.String("session-json", "", "read console session envelope from stdin with '-'")
	idempotencyKey := fs.String("idempotency-key", "", "opaque idempotency key")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *workspace == "" || *sessionJSON != "-" || !validCLIIncusPhase(*phase) {
		return usageErrorf("usage: anas host incus-plan -w WORKSPACE --phase PHASE --request-json JSON --session-json - [--idempotency-key KEY] [--json]")
	}
	key, err := hostCLIKey(*idempotencyKey)
	if err != nil {
		return err
	}
	client, err := newHostConsoleClientFromStdin()
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	response, location, err := client.InvokeIncusPlan(context.Background(), *workspace, *phase, json.RawMessage(*request), key)
	if err != nil {
		return hostConsoleError(err, true, key)
	}
	action := "incus." + *phase + ".plan"
	if *phase == "observer" {
		action = hostaction.ActionObserverPlan
	}
	if *phase == "forwarding-permission" {
		action = hostaction.ActionForwardingPlan
	}
	return printHostJobQueued(jsonMode, action, *workspace, key, location, response)
}

func runHostIncusConfirm(args []string, jsonMode bool) error {
	fs := flag.NewFlagSet("host incus-confirm", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerJSONFlag(fs)
	workspace := fs.String("w", "", "workspace id")
	fs.StringVar(workspace, "workspace", "", "workspace id")
	planJob := fs.String("plan-job", "", "completed plan job id")
	action := fs.String("action", "", "incus.install|incus.configure|incus.enroll|incus.uninstall")
	sessionJSON := fs.String("session-json", "", "read console session envelope from stdin with '-'")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *workspace == "" || *planJob == "" || *sessionJSON != "-" || !validCLIIncusAction(*action) {
		return usageErrorf("usage: anas host incus-confirm -w WORKSPACE --plan-job JOB --action ACTION --session-json - [--json]")
	}
	client, err := newHostConsoleClientFromStdin()
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	response, err := client.IssueHostActionConfirmation(context.Background(), *workspace, *planJob, *action)
	if err != nil {
		return hostConsoleError(err, false, "")
	}
	if jsonMode {
		return emitOK(map[string]any{"response": response})
	}
	fmt.Printf("Confirmation token issued for %s.\n", *action)
	fmt.Println("Use --json and pass the token through host incus-apply --request-json -.")
	return nil
}

func runHostIncusApply(args []string, jsonMode bool) error {
	fs := flag.NewFlagSet("host incus-apply", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerJSONFlag(fs)
	workspace := fs.String("w", "", "workspace id")
	fs.StringVar(workspace, "workspace", "", "workspace id")
	phase := fs.String("phase", "", "install|configure|enroll|uninstall|observer|forwarding-permission")
	planJob := fs.String("plan-job", "", "completed plan job id")
	token := fs.String("confirmation-token", "", "unsupported; pass token through --request-json -")
	parameters := fs.String("parameters-json", "", "parameters from plan result")
	sessionJSON := fs.String("session-json", "", "read console session envelope from stdin with '-'")
	requestJSON := fs.String("request-json", "", "read apply request envelope from stdin with '-'")
	idempotencyKey := fs.String("idempotency-key", "", "opaque idempotency key")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *workspace == "" || !validCLIIncusPhase(*phase) {
		return usageErrorf("usage: anas host incus-apply -w WORKSPACE --phase PHASE --request-json - [--idempotency-key KEY] [--json]")
	}
	if *token != "" {
		return usageErrorf("--confirmation-token is not supported; pass the token through --request-json -")
	}
	if *requestJSON != "-" || *sessionJSON != "" || *planJob != "" || *parameters != "" {
		return usageErrorf("usage: anas host incus-apply -w WORKSPACE --phase PHASE --request-json - [--idempotency-key KEY] [--json]")
	}
	key, err := hostCLIKey(*idempotencyKey)
	if err != nil {
		return err
	}
	request, err := readHostApplyRequest(os.Stdin)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	client, err := newHostConsoleClientFromEnvelope(request.Session)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	response, location, err := client.InvokeIncusApply(context.Background(), *workspace, *phase, request.PlanJobID, request.ConfirmationToken, request.Parameters, key)
	if err != nil {
		return hostConsoleError(err, true, key)
	}
	action := "incus." + *phase
	if *phase == "observer" {
		action = hostaction.ActionObserverApply
	}
	if *phase == "forwarding-permission" {
		action = hostaction.ActionForwardingApply
	}
	return printHostJobQueued(jsonMode, action, *workspace, key, location, response)
}

type hostApplyRequestEnvelope struct {
	Session           consoleclient.Envelope `json:"session"`
	PlanJobID         string                 `json:"plan_job_id"`
	ConfirmationToken string                 `json:"confirmation_token"`
	Parameters        json.RawMessage        `json:"parameters"`
}

type hostPruneApplyRequestEnvelope struct {
	Session           consoleclient.Envelope `json:"session"`
	PlanJobID         string                 `json:"plan_job_id"`
	ConfirmationToken string                 `json:"confirmation_token"`
}

func readHostPruneApplyRequest(r io.Reader) (hostPruneApplyRequestEnvelope, error) {
	var request hostPruneApplyRequestEnvelope
	body, err := io.ReadAll(io.LimitReader(r, consoleclient.MaxEnvelopeBytes+1))
	defer clear(body)
	if err != nil || len(body) > consoleclient.MaxEnvelopeBytes {
		return request, errors.New("host prune apply request could not be read")
	}
	if err := actionabi.DecodeTypedObject(body, &request); err != nil {
		return request, errors.New("host prune apply request is invalid")
	}
	if request.PlanJobID == "" || request.ConfirmationToken == "" {
		return request, errors.New("host prune apply request is incomplete")
	}
	return request, nil
}

func readHostApplyRequest(r io.Reader) (hostApplyRequestEnvelope, error) {
	var request hostApplyRequestEnvelope
	body, err := io.ReadAll(io.LimitReader(r, consoleclient.MaxEnvelopeBytes+1))
	defer clear(body)
	if err != nil || len(body) > consoleclient.MaxEnvelopeBytes {
		return request, errors.New("host apply request could not be read")
	}
	if err := actionabi.DecodeTypedObject(body, &request); err != nil {
		return request, errors.New("host apply request is invalid")
	}
	if request.PlanJobID == "" || request.ConfirmationToken == "" || len(request.Parameters) == 0 {
		return request, errors.New("host apply request is incomplete")
	}
	return request, nil
}

func runHostActions(args []string, jsonMode bool) error {
	fs := flag.NewFlagSet("host actions", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerJSONFlag(fs)
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return usageErrorf("usage: anas host actions [--json]")
	}
	actions := hostaction.Catalog()
	if jsonMode {
		return emitOK(map[string]any{"source": "compiled-client", "version": buildinfo.Version, "commit": buildinfo.Commit,
			"installation_verified": false, "actions": actions})
	}
	fmt.Printf("Compiled host action inventory (%s, commit %s); installation NOT verified.\n", buildinfo.Version, buildinfo.Commit)
	for _, action := range actions {
		fmt.Printf("%s\tscope=%s\tread_only=%t\trequires_root=%t\timplementation=%s\n", action.Name, action.Scope, action.ReadOnly, action.RequiresRoot, action.Implementation)
	}
	return nil
}

func runHostIncusPreflight(args []string, jsonMode bool) error {
	fs := flag.NewFlagSet("host actions", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerJSONFlag(fs)
	workspace := fs.String("w", "", "workspace id")
	fs.StringVar(workspace, "workspace", "", "workspace id")
	sessionJSON := fs.String("session-json", "", "read console session envelope from stdin with '-'")
	idempotencyKey := fs.String("idempotency-key", "", "opaque idempotency key")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *workspace == "" || *sessionJSON != "-" {
		return usageErrorf("usage: anas host incus-preflight -w WORKSPACE --session-json - [--idempotency-key KEY] [--json]")
	}
	key, err := hostCLIKey(*idempotencyKey)
	if err != nil {
		return err
	}
	client, err := newHostConsoleClientFromStdin()
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	response, location, err := client.InvokeIncusPreflight(context.Background(), *workspace, key)
	if err != nil {
		return hostConsoleError(err, true, key)
	}
	return printHostJobQueued(jsonMode, "incus.status", *workspace, key, location, response)
}

func runHostJob(args []string, jsonMode bool) error {
	fs := flag.NewFlagSet("host job", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerJSONFlag(fs)
	sessionJSON := fs.String("session-json", "", "read console session envelope from stdin with '-'")
	jobArgs := args
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 || *sessionJSON != "-" {
		if len(args) == 0 || strings.HasPrefix(args[0], "-") {
			return usageErrorf("usage: anas host job JOB_ID --session-json - [--json]")
		}
		jobArgs = []string{args[0]}
		fs = flag.NewFlagSet("host job", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		registerJSONFlag(fs)
		sessionJSON = fs.String("session-json", "", "read console session envelope from stdin with '-'")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 || *sessionJSON != "-" {
			return usageErrorf("usage: anas host job JOB_ID --session-json - [--json]")
		}
	}
	client, err := newHostConsoleClientFromStdin()
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	response, err := client.GetJob(context.Background(), jobArgs[0])
	if err != nil {
		return hostConsoleError(err, false, "")
	}
	if jsonMode {
		return emitOK(map[string]any{"response": response})
	}
	if id := responseJobID(response); id != "" {
		fmt.Printf("Job: %s\n", id)
	}
	if status := responseJobStatus(response); status != "" {
		fmt.Printf("Status: %s\n", status)
	}
	return nil
}

func hostConsoleError(err error, mayHaveExecuted bool, idempotencyKey string) error {
	var problem *consoleclient.ProblemError
	if errors.As(err, &problem) {
		code := problem.Code
		if code == "" {
			code = "console_request_failed"
		}
		exit := exitPrecondition
		if problem.Status >= 500 {
			exit = exitFailure
		}
		return cliErrorf(exit, code, "console rejected the request")
	}
	if mayHaveExecuted {
		e := failuref("unknown_execution", "request outcome is unknown; the CLI did not retry")
		e.Detail = map[string]any{"idempotency_key": idempotencyKey}
		return e
	}
	return failuref("console_unavailable", "console request failed")
}

func hostCLIKey(value string) (string, error) {
	if value == "" {
		generated, err := consoleclient.NewIdempotencyKey()
		if err != nil {
			return "", failuref("random_unavailable", "could not generate an idempotency key")
		}
		value = generated
	}
	if !consoleclient.ValidIdempotencyKey(value) {
		return "", usageErrorf("idempotency key is invalid")
	}
	return value, nil
}

func printHostJobQueued(jsonMode bool, action, workspace, key, location string, response map[string]any) error {
	if jsonMode {
		return emitOK(map[string]any{"action": action, "workspace_id": workspace, "idempotency_key": key, "location": location, "response": response})
	}
	fmt.Printf("%s queued for workspace %s.\n", action, workspace)
	if id := responseJobID(response); id != "" {
		fmt.Printf("Job: %s\n", id)
	}
	if location != "" {
		fmt.Printf("Location: %s\n", location)
	}
	fmt.Printf("Idempotency-Key: %s\n", key)
	return nil
}

func validCLIIncusPhase(value string) bool {
	return value == "install" || value == "configure" || value == "enroll" || value == "uninstall" || value == "observer" || value == "forwarding-permission"
}

func validCLIIncusAction(value string) bool {
	return value == "incus.install" || value == "incus.configure" || value == "incus.enroll" || value == "incus.uninstall" || value == hostaction.ActionImagePrune || value == hostaction.ActionObserverApply || value == hostaction.ActionForwardingApply
}

func responseJobID(response map[string]any) string {
	job, _ := response["job"].(map[string]any)
	id, _ := job["id"].(string)
	return id
}

func responseJobStatus(response map[string]any) string {
	job, _ := response["job"].(map[string]any)
	status, _ := job["status"].(string)
	return status
}
