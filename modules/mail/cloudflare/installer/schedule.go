package installer

import (
	"context"
	"encoding/json"
	"net/http"
)

const maintenanceCron = "* * * * *"

type scheduleClient interface {
	versionClient
	readSchedules(context.Context, string, string, string) ([]string, error)
	writeMaintenanceSchedule(context.Context, string, string, string) error
}

// installSchedule writes only on a freshly verified empty schedule list. A
// lost acknowledgement is not retried, since the API replaces the whole list.
func (j *Journal) installSchedule(ctx context.Context, client scheduleClient, cfg versionConfig) error {
	if j.file == nil || j.failed || client == nil || !cfg.valid() || cfg.Bucket != j.state.Spec.Bucket {
		return ErrJournal
	}
	r := j.state
	if r.Stage == "schedule-issued" || r.Stage == "endpoint-issued" {
		return ErrUnknown
	}
	if r.Stage != "version-verified" && r.Stage != "schedule-verified" && r.Stage != "endpoint-configured" && r.Stage != "endpoint-verified" {
		return ErrJournal
	}
	digest, err := versionPlanDigest(cfg)
	if err != nil {
		return err
	}
	if r.OperationID != cfg.OperationID || r.VersionDigest != digest {
		return ErrConflict
	}
	if err := verifyReserved(ctx, client, r); err != nil {
		return err
	}
	match, err := client.versionMatches(ctx, r.Spec.AccountID, r.WorkerID, r.VersionID, cfg)
	if err != nil {
		return ErrUnknown
	}
	if !match {
		return ErrConflict
	}
	active, err := client.activeVersion(ctx, r.Spec.AccountID, r.WorkerID, r.Spec.Worker, r.VersionID)
	if err != nil {
		return ErrUnknown
	}
	if !active {
		return ErrConflict
	}
	schedules, err := client.readSchedules(ctx, r.Spec.AccountID, r.WorkerID, r.Spec.Worker)
	if err != nil {
		return err
	}
	if r.Stage == "version-verified" {
		if len(schedules) != 0 {
			return ErrConflict
		}
		r.Stage = "schedule-issued"
		if err := j.append(r); err != nil {
			return err
		}
		if err := client.writeMaintenanceSchedule(ctx, r.Spec.AccountID, r.WorkerID, r.Spec.Worker); err != nil {
			return ErrUnknown
		}
		schedules, err = client.readSchedules(ctx, r.Spec.AccountID, r.WorkerID, r.Spec.Worker)
		if err != nil {
			return ErrUnknown
		}
	}
	if len(schedules) != 1 || schedules[0] != maintenanceCron {
		return ErrConflict
	}
	if err := verifyReserved(ctx, client, r); err != nil {
		return err
	}
	match, err = client.versionMatches(ctx, r.Spec.AccountID, r.WorkerID, r.VersionID, cfg)
	if err != nil {
		return ErrUnknown
	}
	if !match {
		return ErrConflict
	}
	active, err = client.activeVersion(ctx, r.Spec.AccountID, r.WorkerID, r.Spec.Worker, r.VersionID)
	if err != nil {
		return ErrUnknown
	}
	if !active {
		return ErrConflict
	}
	if r.Stage == "schedule-issued" {
		r.Stage = "schedule-verified"
		return j.append(r)
	}
	return nil
}

func (c *Cloudflare) readSchedules(ctx context.Context, account, workerID, name string) ([]string, error) {
	match, err := c.WorkerMatches(ctx, account, workerID, name)
	if err != nil {
		return nil, ErrUnknown
	}
	if !match {
		return nil, ErrConflict
	}
	status, response, err := c.request(ctx, http.MethodGet, account+"/workers/scripts/"+name+"/schedules", nil)
	if err != nil || status != http.StatusOK || !response.Success {
		return nil, ErrUnknown
	}
	var result struct {
		Schedules []struct {
			Cron string `json:"cron"`
		} `json:"schedules"`
	}
	if json.Unmarshal(response.Result, &result) != nil || result.Schedules == nil || len(result.Schedules) > 100 {
		return nil, ErrUnknown
	}
	crons := make([]string, 0, len(result.Schedules))
	for _, item := range result.Schedules {
		if item.Cron == "" {
			return nil, ErrUnknown
		}
		crons = append(crons, item.Cron)
	}
	match, err = c.WorkerMatches(ctx, account, workerID, name)
	if err != nil {
		return nil, ErrUnknown
	}
	if !match {
		return nil, ErrConflict
	}
	return crons, nil
}

func (c *Cloudflare) writeMaintenanceSchedule(ctx context.Context, account, workerID, name string) error {
	match, err := c.WorkerMatches(ctx, account, workerID, name)
	if err != nil {
		return ErrUnknown
	}
	if !match {
		return ErrConflict
	}
	status, result, err := c.request(ctx, http.MethodPut, account+"/workers/scripts/"+name+"/schedules", []map[string]string{{"cron": maintenanceCron}})
	if err != nil || status < 200 || status >= 300 || !result.Success {
		return ErrUnknown
	}
	return nil
}
