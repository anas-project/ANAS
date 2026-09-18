//go:build linux

package jobexecutor

import "testing"

func TestModuleActionProcessStatBoundary(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		pid   int
		state byte
		group int
		valid bool
	}{
		{"exited leader", "123 (worker) Z 1 123 123 0 0 0\n", 123, 'Z', 123, true},
		{"live descendant", "456 (worker child) S 123 123 123 0 0 0\n", 456, 'S', 123, true},
		{"comm delimiter", "456 (child ) with (brackets)\nand newline) S 123 123 123 0 0 0\n", 456, 'S', 123, true},
		{"kernel group zero", "8 (kworker) I 2 0 0 0 0 0\n", 8, 'I', 0, true},
		{"wrong pid", "456 (worker) Z 1 456 456\n", 123, 0, 0, false},
		{"missing delimiter", "123 worker Z 1 123", 123, 0, 0, false},
		{"missing group", "123 (worker) Z 1", 123, 0, 0, false},
		{"negative group", "123 (worker) Z 1 -123", 123, 0, 0, false},
		{"overflow group", "123 (worker) Z 1 999999999999999999999999999999", 123, 0, 0, false},
		{"invalid state", "123 (worker) Q 1 123", 123, 0, 0, false},
		{"long state", "123 (worker) ZZ 1 123", 123, 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, group, err := parseModuleActionProcessState([]byte(tc.text), tc.pid)
			if !tc.valid {
				if err == nil {
					t.Fatal("invalid process identity accepted")
				}
				return
			}
			if err != nil || state != tc.state || group != tc.group {
				t.Fatalf("state=%q group=%d error=%v", state, group, err)
			}
		})
	}
}

func TestModuleActionProcMountVisibilityBoundary(t *testing.T) {
	const visible = "36 25 0:32 / /proc rw,nosuid,nodev,noexec,relatime - proc proc rw\n"
	cases := map[string]struct {
		body    string
		visible bool
	}{
		"normal":                   {visible, true},
		"explicit full visibility": {"36 25 0:32 / /proc rw - proc proc rw,hidepid=0\n", true},
		"hidepid option":           {"36 25 0:32 / /proc rw,hidepid=2 - proc proc rw\n", false},
		"hidepid super option":     {"36 25 0:32 / /proc rw - proc proc rw,hidepid=invisible\n", false},
		"duplicate mount":          {visible + visible, false},
		"subtree mount":            {"36 25 0:32 /123 /proc rw - proc proc rw\n", false},
		"different filesystem":     {"36 25 0:32 / /proc rw - tmpfs tmpfs rw\n", false},
		"different path":           {"36 25 0:32 / /other rw - proc proc rw\n", false},
		"missing separator":        {"36 25 0:32 / /proc rw proc proc rw\n", false},
		"missing mount":            {"", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := moduleActionProcMountVisible([]byte(tc.body)); got != tc.visible {
				t.Fatalf("visible=%v, want %v", got, tc.visible)
			}
		})
	}
}
