// TEST_CASES: MAILMOD-T-035
package installer

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeSchedule struct {
	fakeDeployment
	schedules []string
	writes    int
	lost      bool
}

func TestScheduleAPIRejectsIncompleteReadback(t *testing.T) {
	account, id, name := strings.Repeat("a", 32), strings.Repeat("b", 32), "anas-worker"
	body := `{"success":true,"result":{"schedules":[]}}`
	c := cloudFixture(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/workers/workers/"+id) {
			return responseWith(200, `{"success":true,"result":{"id":"`+id+`","name":"`+name+`"}}`), nil
		}
		if !strings.HasSuffix(r.URL.Path, "/workers/scripts/"+name+"/schedules") || r.Method != http.MethodGet {
			t.Fatal("unexpected API call")
		}
		return responseWith(200, body), nil
	})
	if got, err := c.readSchedules(t.Context(), account, id, name); err != nil || len(got) != 0 {
		t.Fatal("empty list rejected", err)
	}
	body = `{"success":true,"result":{}}`
	if _, err := c.readSchedules(t.Context(), account, id, name); !errors.Is(err, ErrUnknown) {
		t.Fatal("missing list accepted", err)
	}
}

func (f *fakeSchedule) readSchedules(context.Context, string, string, string) ([]string, error) {
	return append([]string{}, f.schedules...), nil
}
func (f *fakeSchedule) writeMaintenanceSchedule(context.Context, string, string, string) error {
	f.writes++
	f.schedules = []string{maintenanceCron}
	if f.lost {
		return errors.New("lost acknowledgement")
	}
	return nil
}

func TestScheduleInstallAndRecovery(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "lost"}[lost], func(t *testing.T) {
			path, spec := fixture(t)
			j, err := OpenJournal(path, spec)
			if err != nil {
				t.Fatal(err)
			}
			cloud := &fakeSchedule{fakeDeployment: fakeDeployment{fakeCloud: fakeCloud{workerOK: true, bucketOK: true}, vacant: true, matches: true, active: true}, lost: lost}
			if err := j.ReserveResources(t.Context(), cloud); err != nil {
				t.Fatal(err)
			}
			cfg := versionFixture()
			if err := j.deployVersion(t.Context(), cloud, cfg); err != nil {
				t.Fatal(err)
			}
			err = j.installSchedule(t.Context(), cloud, cfg)
			if lost && !errors.Is(err, ErrUnknown) || !lost && err != nil {
				t.Fatal(err)
			}
			if err := j.Close(); err != nil {
				t.Fatal(err)
			}
			j, err = OpenJournal(path, spec)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			err = j.installSchedule(t.Context(), cloud, cfg)
			if lost && !errors.Is(err, ErrUnknown) || !lost && err != nil || cloud.writes != 1 {
				t.Fatal("unsafe recovery", err, cloud.writes)
			}
		})
	}
}

func TestScheduleRefusesExistingOrDriftedSchedules(t *testing.T) {
	j, path, _ := secretFixture(t)
	defer j.Close()
	cloud := &fakeSchedule{fakeDeployment: fakeDeployment{fakeCloud: fakeCloud{workerOK: true, bucketOK: true}, vacant: true, matches: true, active: true}, schedules: []string{"0 * * * *"}}
	cfg := versionFixture()
	if err := j.deployVersion(t.Context(), cloud, cfg); err != nil {
		t.Fatal(err)
	}
	if err := j.installSchedule(t.Context(), cloud, cfg); !errors.Is(err, ErrConflict) || cloud.writes != 0 {
		t.Fatal("overwrote existing schedule", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "source-secrets.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
