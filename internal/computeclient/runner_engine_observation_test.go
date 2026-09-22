package computeclient

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Test-only observations never change the lease, restart the service, inspect
// user workloads, or expose raw journal text. Labels cannot authorize a pass.
func runnerEngineReady(body []byte) bool {
	if len(body) > 4<<20 {
		return false
	}
	var value struct {
		Host struct {
			Security struct {
				Rootless *bool `json:"rootless"`
			} `json:"security"`
		} `json:"host"`
	}
	return json.Unmarshal(body, &value) == nil && value.Host.Security.Rootless != nil && *value.Host.Security.Rootless
}

func waitForRunnerEngine(ctx context.Context, probe func(context.Context) ([]byte, error), interval time.Duration) (int, error) {
	if ctx == nil || probe == nil || interval <= 0 {
		return 0, errors.New("invalid engine test probe")
	}
	attempts := 0
	for {
		if err := ctx.Err(); err != nil {
			return attempts, err
		}
		bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
		attempts++
		body, err := probe(bounded)
		callErr := bounded.Err()
		cancel()
		if ctx.Err() != nil {
			return attempts, ctx.Err()
		}
		if err == nil && callErr == nil && runnerEngineReady(body) {
			return attempts, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return attempts, ctx.Err()
		case <-timer.C:
		}
	}
}

func runnerEngineUnitSummary(body []byte) map[string]string {
	invalid := func() map[string]string { return map[string]string{"observation": "unavailable"} }
	if len(body) > 4096 {
		return invalid()
	}
	allowed := map[string]string{
		"ActiveState": "active reloading inactive failed activating deactivating maintenance",
		"SubState":    "running dead failed start-pre start start-post auto-restart auto-restart-queued exited stop stop-sigterm stop-sigkill stop-post final-sigterm final-sigkill",
		"Result":      "success resources timeout exit-code signal core-dump watchdog start-limit-hit protocol oom-kill",
	}
	numbers := map[string]int{"ExecMainStatus": 255, "ExecMainCode": 6, "NRestarts": 1000000}
	result := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || result[key] != "" {
			return invalid()
		}
		if values, known := allowed[key]; known {
			if !slices.Contains(strings.Fields(values), value) {
				return invalid()
			}
		} else if maximum, known := numbers[key]; known {
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 || n > maximum || strconv.Itoa(n) != value {
				return invalid()
			}
		} else {
			return invalid()
		}
		result[key] = value
	}
	if len(result) != len(allowed)+len(numbers) {
		return invalid()
	}
	return result
}

func runnerEngineJournalSummary(body []byte) []string {
	if len(body) > 64<<10 {
		return []string{"over_limit"}
	}
	text := strings.ToLower(string(body))
	var labels []string
	for _, item := range []struct{ match, label string }{
		{"226/namespace", "service_namespace_failure"},
		{"mount namespacing", "mount_namespace_observed"},
		{"newuidmap", "uid_mapping_observed"},
		{"newgidmap", "gid_mapping_observed"},
		{"cannot clone", "clone_failure_observed"},
		{"permission denied", "permission_denied_observed"},
		{"operation not permitted", "operation_denied_observed"},
		{"read-only file system", "readonly_filesystem_observed"},
		{"no such file or directory", "missing_path_observed"},
		{"no space left on device", "space_error_observed"},
		{"address already in use", "address_busy_observed"},
	} {
		if strings.Contains(text, item.match) {
			labels = append(labels, item.label)
		}
	}
	if len(labels) == 0 {
		labels = []string{"unclassified"}
	}
	return labels
}

// Keep only the fixed service/dependency job states, never arbitrary unit
// names or raw list-jobs lines. An inactive unit may be queued, not crashed.
func runnerEngineBootJobs(body []byte) map[string]string {
	invalid := func() map[string]string { return map[string]string{"observation": "unavailable"} }
	if len(body) > 8192 {
		return invalid()
	}
	allowed := []string{"anas-podman.service", "network-online.target", "systemd-networkd-wait-online.service", "multi-user.target",
		"network.target", "network-pre.target", "networking.service", "ifupdown-pre.service", "basic.target", "sysinit.target", "sockets.target",
		"systemd-networkd.service", "systemd-networkd.socket", "systemd-networkd-persistent-storage.service", "systemd-resolved.service",
		"systemd-udevd.service", "systemd-udev-trigger.service", "systemd-sysusers.service", "systemd-sysctl.service",
		"systemd-tmpfiles-setup.service", "systemd-journald.service", "systemd-journal-flush.service"}
	result := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 4 || !slices.Contains(allowed, fields[1]) {
			continue
		}
		id, err := strconv.ParseUint(fields[0], 10, 32)
		if err != nil || id == 0 || result[fields[1]] != "" || !slices.Contains([]string{"start", "stop", "restart"}, fields[2]) || !slices.Contains([]string{"waiting", "running"}, fields[3]) {
			return invalid()
		}
		result[fields[1]] = fields[2] + ":" + fields[3]
	}
	return result
}

func TestRunnerEngineBootJobsAreBoundedAndClosed(t *testing.T) {
	body := "23 anas-podman.service start waiting\n12 systemd-networkd-wait-online.service start running\n14 private-marker.service start waiting\n"
	got := runnerEngineBootJobs([]byte(body))
	if len(got) != 2 || got["anas-podman.service"] != "start:waiting" || got["systemd-networkd-wait-online.service"] != "start:running" {
		t.Fatal("fixed boot jobs not observed")
	}
	for _, invalid := range []string{body + "23 anas-podman.service start waiting\n", "23 anas-podman.service start private-marker\n", strings.Repeat("x", 8193)} {
		encoded, _ := json.Marshal(runnerEngineBootJobs([]byte(invalid)))
		if string(encoded) != `{"observation":"unavailable"}` {
			t.Fatal("untrusted job diagnostics escaped")
		}
	}
}

func runnerEngineProcesses(body []byte) []string {
	if len(body) > 16<<10 {
		return []string{"unavailable"}
	}
	allowed := []string{"systemd", "systemd-journal", "systemd-network", "systemd-resolve", "systemd-udevd", "udevadm", "systemd-tmpfile", "systemd-sysuser"}
	var rows []string
	for _, line := range strings.Split(string(body), "\n") {
		f := strings.Fields(line)
		if len(f) != 4 || !slices.Contains(allowed, f[1]) || !slices.Contains([]string{"S", "D", "R", "I", "T", "Z"}, f[2]) {
			continue
		}
		pid, err := strconv.ParseUint(f[0], 10, 32)
		if err != nil || pid == 0 || strconv.FormatUint(pid, 10) != f[0] {
			continue
		}
		wait := "other"
		for _, name := range []string{"seccomp", "epoll", "ep_poll", "futex", "do_wait", "pipe", "unix_stream", "nanosleep", "wait_woken"} {
			if strings.Contains(f[3], name) {
				wait = name
				break
			}
		}
		rows = append(rows, f[0]+":"+f[1]+":"+f[2]+":"+wait)
	}
	return rows
}

func TestRunnerEngineProcessesDoNotEchoUntrustedNamesOrWaitSymbols(t *testing.T) {
	rows := runnerEngineProcesses([]byte("1 systemd S do_epoll_wait\n23 systemd-network S seccomp_do_user_notification\n24 private-secret S private-symbol\n25 systemd-network S private-symbol\n"))
	if len(rows) != 3 || rows[1] != "23:systemd-network:S:seccomp" || strings.Contains(strings.Join(rows, " "), "private") {
		t.Fatal("unsafe or missing process observation")
	}
}

func TestRunnerEngineReadinessRequiresSuccessfulRootlessResponse(t *testing.T) {
	if !runnerEngineReady([]byte(`{"host":{"security":{"rootless":true}}}`)) {
		t.Fatal("positive control")
	}
	for _, body := range []string{"null", `{}`, `{"host":{"security":{"rootless":false}}}`, `{"host":{"security":{"rootless":"true"}}}`, `{"host":{"security":{"rootless":null}}}`, `{"host":{"security":{"rootless":true}}} {}`, strings.Repeat(" ", 4<<20) + `{}`} {
		if runnerEngineReady([]byte(body)) {
			t.Fatal("invalid engine readiness accepted")
		}
	}
}

func TestRunnerEngineProbeWaitsForActualReadiness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	calls := 0
	n, err := waitForRunnerEngine(ctx, func(call context.Context) ([]byte, error) {
		deadline, ok := call.Deadline()
		if !ok || time.Until(deadline) > 3*time.Second {
			t.Fatal("unbounded native probe")
		}
		calls++
		if calls == 1 {
			return nil, errors.New("not ready")
		}
		if calls == 2 {
			return []byte(`{"host":{"security":{"rootless":false}}}`), nil
		}
		return []byte(`{"host":{"security":{"rootless":true}}}`), nil
	}, time.Millisecond)
	if err != nil || n != 3 || calls != 3 {
		t.Fatal(n, calls, err)
	}
}

func TestRunnerEngineProbeCannotPassOnErrorOrCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	_, err := waitForRunnerEngine(ctx, func(context.Context) ([]byte, error) {
		return []byte(`{"host":{"security":{"rootless":true}}}`), errors.New("command failed")
	}, time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	stop()
	n, err := waitForRunnerEngine(ctx, func(context.Context) ([]byte, error) { t.Fatal("probe ran after cancellation"); return nil, nil }, time.Millisecond)
	if n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal(n, err)
	}
}

func TestRunnerEngineObservationsNeverReturnJournalOrUnknownFields(t *testing.T) {
	valid := "ActiveState=failed\nSubState=failed\nResult=exit-code\nExecMainStatus=226\nExecMainCode=1\nNRestarts=3\n"
	if runnerEngineUnitSummary([]byte(valid))["ExecMainStatus"] != "226" {
		t.Fatal("numeric positive control")
	}
	for _, body := range []string{valid + "Environment=private-marker\n", strings.Replace(valid, "failed", "private-marker", 1), strings.Replace(valid, "failed", "failed activating", 1), valid + "NRestarts=3\n", strings.Repeat("x", 4097)} {
		encoded, _ := json.Marshal(runnerEngineUnitSummary([]byte(body)))
		if string(encoded) != `{"observation":"unavailable"}` {
			t.Fatal("untrusted unit diagnostic escaped")
		}
	}
	labels := runnerEngineJournalSummary([]byte("private-marker: status=226/NAMESPACE; newuidmap: Operation not permitted"))
	encoded, _ := json.Marshal(labels)
	if strings.Contains(string(encoded), "private-marker") || len(labels) != 3 {
		t.Fatal("journal text escaped")
	}
	if runnerEngineJournalSummary([]byte(strings.Repeat("x", (64<<10)+1)))[0] != "over_limit" {
		t.Fatal("unbounded observation")
	}
}
