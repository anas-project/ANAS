package config

// TEST_CASES: TEMP-T-001
// REQUIREMENTS: TEMP-R-002 TEMP-R-037

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestTemporaryPathMustBeAbsoluteAndIsNormalized(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yml")
	for _, test := range []struct {
		path, want string
		valid      bool
	}{
		{"/srv/temp/../managed", "/srv/managed", true},
		{"relative/temp", "", false},
		{"/srv/temp\x00", "", false},
		{"/srv/temp\r", "", false},
		{"/srv/temp\n", "", false},
	} {
		if err := os.WriteFile(file, []byte("modules: {lego: {}}\nglobal:\n  temp_path: "+strconv.Quote(test.path)+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(file)
		if !test.valid {
			if err == nil {
				t.Fatal("relative temporary root accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Global.TempPath != test.want || cfg.BaseEnv()["TEMP_PATH"] != test.want {
			t.Fatalf("temporary path did not normalize: %+v", cfg.Global)
		}
	}
}
