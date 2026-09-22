package computeclient

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Fixed test measurements only: never return arbitrary guest output or paths.
func runnerOCILimitObservation(body []byte) map[string]string {
	invalid := func() map[string]string { return map[string]string{"observation": "unavailable"} }
	if len(body) > 1024 {
		return invalid()
	}
	allowed := map[string]bool{"memory": true, "pids": true, "cpu": true, "nnp": true, "token_visible": true}
	valuePattern := regexp.MustCompile(`^(?:(?:[0-9]+|max)(?: [0-9]+)?|unavailable|true|false)$`)
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		line = strings.TrimSuffix(line, "\r")
		key, value, ok := strings.Cut(line, "=")
		if !ok || !allowed[key] || values[key] != "" || !valuePattern.MatchString(value) {
			return invalid()
		}
		values[key] = value
	}
	if len(values) != len(allowed) {
		return invalid()
	}
	return values
}

func runnerOCILimitsEnforced(values map[string]string) bool {
	if len(values) != 5 || values["memory"] != "134217728" || values["pids"] != "32" || values["nnp"] != "1" || values["token_visible"] != "false" {
		return false
	}
	cpu := strings.Fields(values["cpu"])
	if len(cpu) != 2 {
		return false
	}
	period, err := strconv.ParseUint(cpu[1], 10, 64)
	quota, quotaErr := strconv.ParseUint(cpu[0], 10, 64)
	return err == nil && quotaErr == nil && period > 0 && period%2 == 0 && quota == period/2
}

func TestOCIExecMeasurementsRequireEffectiveLimits(t *testing.T) {
	const valid = "memory=134217728\npids=32\ncpu=50000 100000\nnnp=1\ntoken_visible=false\n"
	if !runnerOCILimitsEnforced(runnerOCILimitObservation([]byte(valid))) {
		t.Fatal("positive measurement rejected")
	}
	if runnerOCILimitObservation([]byte(strings.Replace(valid, "50000 100000", "max 100000", 1)))["cpu"] != "max 100000" {
		t.Fatal("unlimited CPU should be observable but never accepted")
	}
	for _, body := range []string{
		strings.Replace(valid, "134217728", "max", 1), strings.Replace(valid, "pids=32", "pids=max", 1),
		strings.Replace(valid, "50000 100000", "max 100000", 1), strings.Replace(valid, "50000 100000", "0 0", 1),
		strings.Replace(valid, "50000 100000", "100000 100000", 1),
		strings.Replace(valid, "nnp=1", "nnp=0", 1), strings.Replace(valid, "token_visible=false", "token_visible=true", 1),
		valid + "pids=32\n", valid + "path=private-diagnostic\n", "memory=unavailable\n", strings.Repeat("x", 1025),
	} {
		if runnerOCILimitsEnforced(runnerOCILimitObservation([]byte(body))) {
			t.Fatal("missing, ambiguous or unenforced controls accepted")
		}
	}
}
