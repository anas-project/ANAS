package runner

import (
	"context"
	"os"
	"testing"
)

func TestWorkspaceChineseSpeedupUsesManagedConfigPrecedence(t *testing.T) {
	t.Setenv("CHINESE_SPEEDUP", "true")
	for _, tc := range []struct {
		body string
		want bool
	}{
		{"", false},
		{"global:\n  chinese_speedup: true\n", true},
		{"global:\n  chinese_speedup: false\n", false},
		{"global:\n  chinese_build_speedup: true\n", false},
		{"env:\n  CHINESE_SPEEDUP: ' True '\n", true},
		{"global:\n  chinese_speedup: true\nenv:\n  CHINESE_SPEEDUP: false\n", false},
		{"module_source: official-cn\n", true},
		{"module_source: official-cn\nglobal:\n  chinese_speedup: false\n", false},
	} {
		_, workspace := newConfigApplicationTestService(t, "modules:\n  demo: {}\n"+tc.body, false)
		got, err := WorkspaceChineseSpeedup(context.Background(), workspace)
		if err != nil || got != tc.want {
			t.Fatalf("%q: got %v, %v", tc.body, got, err)
		}
		if err := os.WriteFile(workspaceConfigPath(workspace), []byte("global:\n  chinese_speedup: true\n# tampered\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := WorkspaceChineseSpeedup(context.Background(), workspace); err == nil {
			t.Fatal("accepted unmanaged configuration drift")
		}
	}
	workspace := t.TempDir()
	if err := ensureRuntimeLayout(stateDir(workspace)); err != nil {
		t.Fatal(err)
	}
	if got, err := WorkspaceChineseSpeedup(context.Background(), workspace); err != nil || got {
		t.Fatalf("fresh workspace should use upstream: %v, %v", got, err)
	}
}
