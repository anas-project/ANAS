// TEST_CASES: MAILMOD-T-042
package installer

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProvisionRejectsInvalidInputsBeforeCloudWrites(t *testing.T) {
	for _, scenario := range []string{"domain", "capacity", "notification", "proxy"} {
		t.Run(scenario, func(t *testing.T) {
			path, spec := fixture(t)
			j, err := OpenJournal(path, spec)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			cloud := &fakeEndpoint{fakeSchedule: fakeSchedule{fakeDeployment: fakeDeployment{fakeCloud: fakeCloud{workerOK: true, bucketOK: true}, vacant: true, matches: true, active: true}}, label: "account-name"}
			inputs := sourceInputs{Domains: []string{"example.test"}, CapacityBytes: 2 << 30, DeliveredDays: 7}
			proxy := ""
			switch scenario {
			case "domain":
				inputs.Domains = []string{"BAD DOMAIN"}
			case "capacity":
				inputs.CapacityBytes = -1
			case "notification":
				inputs.NotifyOrigin = "http://notify.example.test"
			case "proxy":
				proxy = "file:///invalid"
			}
			if _, err := j.provision(t.Context(), cloud, inputs, proxy); !errors.Is(err, ErrJournal) {
				t.Fatal("expected preflight rejection", err)
			}
			if cloud.workerCreates != 0 || cloud.bucketCreates != 0 || cloud.creates != 0 || cloud.writes != 0 || j.state.Stage != "prepared" {
				t.Fatal("invalid inputs caused a cloud write")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), "source-secrets.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid inputs created a credential file", err)
			}
		})
	}
}

func TestProvisionRunsFixedStagesAndResumesWithoutRecreating(t *testing.T) {
	for _, scenario := range []string{"success", "lost-endpoint", "probe-failure"} {
		t.Run(scenario, func(t *testing.T) {
			path, spec := fixture(t)
			j, err := OpenJournal(path, spec)
			if err != nil {
				t.Fatal(err)
			}
			cloud := &fakeEndpoint{fakeSchedule: fakeSchedule{fakeDeployment: fakeDeployment{fakeCloud: fakeCloud{workerOK: true, bucketOK: true}, vacant: true, matches: true, active: true}}, label: "account-name", lost: scenario == "lost-endpoint", probeFailure: scenario == "probe-failure"}
			inputs := sourceInputs{Domains: []string{"example.test"}, CapacityBytes: 2 << 30, DeliveredDays: 7, NotifyOrigin: "https://notify.example.test"}
			first, err := j.provision(t.Context(), cloud, inputs, "")
			if scenario == "success" && err != nil || scenario != "success" && !errors.Is(err, ErrUnknown) {
				t.Fatal("unexpected preparation outcome", err)
			}
			if scenario == "success" {
				cfg, e := j.sourceConfig(inputs)
				if e != nil {
					t.Fatal(e)
				}
				encoded, _ := json.Marshal(first)
				if first.SourceID != cfg.SourceID || first.Generation != cfg.Generation || !validWorkerOrigin(spec.Worker, first.Origin) || strings.Contains(string(encoded), cfg.Reader) || strings.Contains(string(encoded), cfg.Control) || strings.Contains(string(encoded), cfg.NotifyToken) {
					t.Fatal("invalid public result")
				}
			}
			if err = j.Close(); err != nil {
				t.Fatal(err)
			}
			j, err = OpenJournal(path, spec)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			cloud.probeFailure = false
			_, err = j.provision(t.Context(), cloud, inputs, "")
			if scenario == "lost-endpoint" && !errors.Is(err, ErrUnknown) || scenario != "lost-endpoint" && err != nil {
				t.Fatal("unsafe preparation recovery", err)
			}
			if cloud.workerCreates != 1 || cloud.bucketCreates != 1 || cloud.creates != 1 || cloud.writes != 1 {
				t.Fatal("recovery repeated cloud writes")
			}
		})
	}
}
