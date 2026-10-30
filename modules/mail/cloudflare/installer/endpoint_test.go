// TEST_CASES: MAILMOD-T-040
package installer

import (
	"context"
	"errors"
	"testing"
)

type fakeEndpoint struct {
	fakeSchedule
	label                                     string
	routingErr                                error
	enabled, previews                         bool
	writes, probes                            int
	routingReads                              int
	lost, probeFailure, driftOnProbe, delayed bool
}

func (f *fakeEndpoint) preflightRouting(context.Context, string, string, string) error {
	f.routingReads++
	return f.routingErr
}

func (f *fakeEndpoint) accountSubdomain(context.Context, string) (string, error) { return f.label, nil }
func (f *fakeEndpoint) readEndpoint(context.Context, string, string, string) (bool, bool, error) {
	return f.enabled, f.previews, nil
}
func (f *fakeEndpoint) writeEndpoint(context.Context, string, string, string) error {
	f.writes++
	if !f.delayed {
		f.enabled = true
	}
	if f.lost {
		return errors.New("lost response")
	}
	return nil
}
func (f *fakeEndpoint) probeEndpoint(context.Context, string, versionConfig, string) error {
	f.probes++
	if f.driftOnProbe {
		f.label = "changed"
	}
	if f.probeFailure {
		return ErrUnknown
	}
	return nil
}

func TestEndpointPublicationRecoveryAndDrift(t *testing.T) {
	for _, scenario := range []string{"success", "lost", "probe-failure", "existing", "mid-probe-drift", "delayed"} {
		t.Run(scenario, func(t *testing.T) {
			path, spec := fixture(t)
			j, err := OpenJournal(path, spec)
			if err != nil {
				t.Fatal(err)
			}
			cloud := &fakeEndpoint{fakeSchedule: fakeSchedule{fakeDeployment: fakeDeployment{fakeCloud: fakeCloud{workerOK: true, bucketOK: true}, vacant: true, matches: true, active: true}}, label: "account-name", lost: scenario == "lost", probeFailure: scenario == "probe-failure", enabled: scenario == "existing", driftOnProbe: scenario == "mid-probe-drift", delayed: scenario == "delayed"}
			if err = j.ReserveResources(t.Context(), cloud); err != nil {
				t.Fatal(err)
			}
			cfg := versionFixture()
			if err = j.deployVersion(t.Context(), cloud, cfg); err != nil {
				t.Fatal(err)
			}
			if err = j.installSchedule(t.Context(), cloud, cfg); err != nil {
				t.Fatal(err)
			}
			err = j.enableEndpoint(t.Context(), cloud, cfg, "")
			if scenario == "success" && err != nil || (scenario == "lost" || scenario == "probe-failure" || scenario == "delayed") && !errors.Is(err, ErrUnknown) || (scenario == "existing" || scenario == "mid-probe-drift") && !errors.Is(err, ErrConflict) {
				t.Fatal("unexpected endpoint result", err)
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
			if scenario == "delayed" {
				cloud.enabled = true
			}
			err = j.enableEndpoint(t.Context(), cloud, cfg, "")
			switch scenario {
			case "success", "probe-failure", "delayed":
				if err != nil || cloud.writes != 1 || j.state.Stage != "endpoint-verified" {
					t.Fatal("unsafe endpoint recovery", err, cloud.writes)
				}
			case "lost":
				if !errors.Is(err, ErrUnknown) || cloud.writes != 1 {
					t.Fatal("lost reply retried", err, cloud.writes)
				}
			case "existing":
				if !errors.Is(err, ErrConflict) || cloud.writes != 0 {
					t.Fatal("existing endpoint adopted", err, cloud.writes)
				}
			case "mid-probe-drift":
				if !errors.Is(err, ErrConflict) || cloud.writes != 1 {
					t.Fatal("mid-probe drift adopted", err, cloud.writes)
				}
			}
			if scenario == "success" {
				cloud.label = "changed"
				if !errors.Is(j.enableEndpoint(t.Context(), cloud, cfg, ""), ErrConflict) {
					t.Fatal("account subdomain drift accepted")
				}
			}
		})
	}
}

func TestWorkerOriginIsStrictlyAccountWorkersDev(t *testing.T) {
	if !validWorkerOrigin("anas-worker", "https://anas-worker.account-name.workers.dev") {
		t.Fatal("valid origin rejected")
	}
	for _, origin := range []string{"http://anas-worker.account-name.workers.dev", "https://anas-worker.other.example", "https://anas-worker.account-name.workers.dev.evil.test", "https://anas-worker.account-name.workers.dev/path", "https://anas-worker..workers.dev"} {
		if validWorkerOrigin("anas-worker", origin) {
			t.Fatal("invalid origin accepted", origin)
		}
	}
}

func TestAnonymousProbeClassifiesOnlySuccessfulBypassAsConflict(t *testing.T) {
	if checkAnonymousStatus(401) != nil {
		t.Fatal("expected rejection not accepted")
	}
	if !errors.Is(checkAnonymousStatus(200), ErrConflict) {
		t.Fatal("anonymous access accepted")
	}
	for _, status := range []int{403, 404, 429, 500, 503} {
		if !errors.Is(checkAnonymousStatus(status), ErrUnknown) {
			t.Fatal("unavailable endpoint misclassified", status)
		}
	}
}
