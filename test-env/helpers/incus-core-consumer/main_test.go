package main

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestServiceReadinessDoesNotDependOnLaterConsumers(t *testing.T) {
	for _, own := range []string{"anas-core-one", "anas-core-two"} {
		var requested []string
		err := verifyProjectAccess(own, false, func(project string) (int, error) {
			requested = append(requested, project)
			if project == own {
				return 200, nil
			}
			return 500, nil // Another project's creation has not run yet.
		})
		if err != nil || !reflect.DeepEqual(requested, []string{own}) {
			t.Fatal("startup readiness depended on a not-yet-created foreign project", err, requested)
		}
	}
}

func TestFiniteIsolationProbeRequiresBothKnownProjectsAndFinalPositiveControl(t *testing.T) {
	var requested []string
	err := verifyProjectAccess("anas-core-one", true, func(project string) (int, error) {
		requested = append(requested, project)
		if project == "anas-core-one" {
			return 200, nil
		}
		return 403, nil
	})
	if err != nil || !reflect.DeepEqual(requested, []string{"anas-core-one", "anas-core-two", "default", "anas-core-one"}) {
		t.Fatal("finite isolation probe omitted cross-project checks or its final positive control", err, requested)
	}
	for _, status := range []int{200, 401, 429, 500, 503} {
		err := verifyProjectAccess("anas-core-one", true, func(project string) (int, error) {
			if project == "anas-core-one" {
				return 200, nil
			}
			return status, nil
		})
		var failure probeFailure
		if !errors.As(err, &failure) || failure.stage != "foreign_project_status" || failure.status != status {
			t.Errorf("foreign response %d was mistaken for verified rejection: %v", status, err)
		}
	}
}

func TestReadinessAndIsolationNeverAcceptLostOwnProject(t *testing.T) {
	for _, isolation := range []bool{false, true} {
		err := verifyProjectAccess("anas-core-one", isolation, func(string) (int, error) { return 404, nil })
		var failure probeFailure
		if !errors.As(err, &failure) || failure.stage != "own_project_status" {
			t.Fatal("missing own project was accepted", err)
		}
	}
	positives := 0
	err := verifyProjectAccess("anas-core-one", true, func(project string) (int, error) {
		if project != "anas-core-one" {
			return 404, nil
		}
		positives++
		if positives == 1 {
			return 200, nil
		}
		return 0, probeFailure{stage: "request_failed"}
	})
	if err == nil || positives != 2 {
		t.Fatal("a failed final positive control was treated as cross-project isolation")
	}
}

func TestProjectProbeUsesExactProjectEvidenceRatherThanForeignEnumerationErrors(t *testing.T) {
	project := "anas-core-one"
	if probePath(project, false) != "/1.0/instances?recursion=1&project=anas-core-one" ||
		probePath(project, true) != "/1.0/projects/anas-core-one" {
		t.Fatal("readiness and project visibility do not use their fixed read-only endpoints")
	}
	if !validProbeMetadata([]byte(`{"type":"sync","status_code":200,"metadata":[]}`), project, false) ||
		!validProbeMetadata([]byte(`{"type":"sync","status_code":200,"metadata":{"name":"anas-core-one","config":{"restricted":"true"}}}`), project, true) {
		t.Fatal("complete independent readiness/project evidence rejected")
	}
	for _, body := range []string{
		`{"type":"error","error_code":500,"error":"permission denied"}`,
		`{"type":"sync","status_code":200,"metadata":null}`,
		`{"type":"sync","status_code":200,"metadata":[]}`,
		`{"type":"sync","status_code":200,"metadata":{"name":"anas-core-two","config":{}}}`,
		`{"type":"sync","status_code":200,"metadata":{"name":"anas-core-one"}}`,
		`{"type":"sync","status_code":200,"metadata":{"name":"anas-core-one","config":{"restricted":"false"}}}`,
	} {
		if validProbeMetadata([]byte(body), project, true) {
			t.Fatal("unproven project identity or error response became positive evidence")
		}
	}
}

func TestProbeDiagnosticsContainOnlyClosedStageAndHTTPStatus(t *testing.T) {
	for _, err := range []error{errors.New("PRIVATE KEY secret-response"), probeFailure{stage: "secret-response", status: 123}, probeFailure{stage: "peer_pin", status: 99999}} {
		body, e := json.Marshal(reportProbe("core_one", err))
		if e != nil || strings.Contains(string(body), "secret-response") || strings.Contains(string(body), "PRIVATE KEY") || strings.Contains(string(body), "99999") {
			t.Fatal("private diagnostic leaked")
		}
	}
	result := reportProbe("core_two", probeFailure{stage: "foreign_project_status", status: 401})
	if result["passed"] != false || result["failure_code"] != "foreign_project_status" || result["http_status"] != 401 {
		t.Fatal("fixed public failure facts missing")
	}
}

func TestConsumerProbeHasOnlyTheFixedFiniteOrServiceModes(t *testing.T) {
	for _, name := range []string{"core_one", "core_two"} {
		for _, once := range []bool{false, true} {
			args := []string{name}
			if once {
				args = append(args, "--once")
			}
			module, finite, ok := parseInvocation(args)
			if !ok || module != name || finite != once {
				t.Fatal("fixed consumer mode rejected")
			}
		}
	}
	for _, args := range [][]string{nil, {}, {"foreign"}, {"core_one", "--url"}, {"core_one", "--once", "extra"}, {"core_one\n"}} {
		if _, _, ok := parseInvocation(args); ok {
			t.Fatal("arbitrary probe command admitted")
		}
	}
}

func TestConsumerNeverAcceptsProviderOrSiblingProjection(t *testing.T) {
	for _, foreign := range []string{"INCUS_ADMIN_KEY_B64=secret", "INCUS_HOST_CONNECTION_SOURCE=host-bundle:v1",
		"ANAS_COMPUTE_RESOURCE__CORE_TWO__WORKERS__CLIENT_KEY=secret", "ANAS_COMPUTE_RESOURCE__CORE_ONE__OTHER__CLIENT_KEY=secret"} {
		if isolatedEnvironment([]string{foreign}, "core_one") {
			t.Fatal("foreign projection accepted")
		}
	}
	if !isolatedEnvironment([]string{"PATH=/usr/bin", "INCUS_STORAGE_POOL=anas-btrfs", "ANAS_COMPUTE_RESOURCE__CORE_ONE__WORKERS__CLIENT_KEY=private"}, "core_one") {
		t.Fatal("own resource projection rejected")
	}
}

func TestConsumerRequiresAllUnprivilegedProcessFacts(t *testing.T) {
	status := "CapEff:\t0000000000000000\nCapPrm:\t0000000000000000\nCapInh:\t0000000000000000\nCapBnd:\t0000000000000000\nCapAmb:\t0000000000000000\nNoNewPrivs:\t1\n"
	if !isolatedIdentity(status) || isolatedIdentity("") || isolatedIdentity(status+"CapEff:\t0000000000000001\n") {
		// Duplicate evidence is rejected separately below, not assumed valid.
		t.Fatal("invalid process identity")
	}
}
