//go:build linux

package jobexecutor

import "testing"

func TestModuleActionProcStatSeparatesCommFromFields(t *testing.T) {
	for _, test := range []struct {
		name, body string
		pid, group int
		state      byte
		valid      bool
	}{
		{"normal", "321 (executor) Z 7 321 7 0 0", 321, 321, 'Z', true},
		{"embedded-delimiters", "321 (a ) b\n c)) S 7 321 7 0 0", 321, 321, 'S', true},
		{"missing-close", "321 (executor S 7 321", 0, 0, 0, false},
		{"missing-fields", "321 (executor) S 7", 0, 0, 0, false},
		{"invalid-pid", "-1 (executor) S 7 321", 0, 0, 0, false},
		{"invalid-group", "321 (executor) S 7 -2", 0, 0, 0, false},
		{"invalid-state-width", "321 (executor) ZZ 7 321", 0, 0, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, group, err := parseModuleActionProcessState([]byte(test.body), 321)
			if test.valid {
				if err != nil || group != test.group || state != test.state {
					t.Fatalf("unexpected stat parsing result: state=%q group=%d error=%v", state, group, err)
				}
			} else if err == nil {
				t.Fatal("malformed process identity accepted")
			}
		})
	}
}
