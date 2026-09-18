//go:build linux

package jobexecutor

import (
	"strings"
	"testing"
)

func TestModuleActionProcMountVisibilityBinding(t *testing.T) {
	visible := "25 1 0:5 / /proc rw,nosuid,nodev,noexec,relatime - proc proc rw\n"
	for _, body := range []string{visible, strings.Replace(visible, "proc proc rw", "proc proc rw,hidepid=0", 1), strings.Replace(visible, " - ", " shared:2 - ", 1)} {
		if !moduleActionProcMountVisible([]byte(body)) {
			t.Fatal("complete unrestricted procfs mount rejected")
		}
	}
	for name, body := range map[string]string{
		"missing":       "",
		"duplicate":     visible + visible,
		"hidden PID":    strings.Replace(visible, "proc proc rw", "proc proc rw,hidepid=2", 1),
		"restricted":    strings.Replace(visible, "proc proc rw", "proc proc rw,hidepid=1", 1),
		"named hiding":  strings.Replace(visible, "proc proc rw", "proc proc rw,hidepid=invisible", 1),
		"wrong FS":      strings.Replace(visible, " - proc ", " - tmpfs ", 1),
		"subtree mount": strings.Replace(visible, "0:5 / /proc", "0:5 /123 /proc", 1),
		"wrong path":    strings.Replace(visible, " /proc ", " /other/proc ", 1),
		"truncated":     "25 1 0:5 / /proc rw - proc\n",
	} {
		t.Run(name, func(t *testing.T) {
			if moduleActionProcMountVisible([]byte(body)) {
				t.Fatal("an incomplete or restricted procfs view was treated as complete")
			}
		})
	}
}

func TestModuleActionProcessStatDelimiterBinding(t *testing.T) {
	for _, body := range []string{
		"101 (worker) Z 100 101 99 0 0\n",
		"101 (worker ) (name\n)) Z 100 101 99 0 0\n",
	} {
		state, group, err := parseModuleActionProcessState([]byte(body), 101)
		if err != nil || state != 'Z' || group != 101 {
			t.Fatal("process identity or group was not parsed after the complete comm field")
		}
	}
	for _, body := range []string{
		"102 (worker) Z 100 101\n",
		"101 (worker) Z 100 -1\n",
		"101 (worker) ? 100 101\n",
		"101 worker Z 100 101\n",
		"101 (worker) Z 100\n",
	} {
		if _, _, err := parseModuleActionProcessState([]byte(body), 101); err == nil {
			t.Fatal("invalid process/group binding accepted")
		}
	}
}
