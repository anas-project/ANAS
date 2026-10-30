// TEST_CASES: MAILMOD-T-034
package installer

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func secretFixture(t *testing.T) (*Journal, string, Spec) {
	t.Helper()
	path, spec := fixture(t)
	j, err := OpenJournal(path, spec)
	if err != nil {
		t.Fatal(err)
	}
	cloud := &fakeCloud{workerOK: true, bucketOK: true}
	if err := j.ReserveResources(t.Context(), cloud); err != nil {
		t.Fatal(err)
	}
	return j, path, spec
}

func TestSourceSecretsSurviveRestartAndExcludeJournal(t *testing.T) {
	j, path, spec := secretFixture(t)
	inputs := sourceInputs{Domains: []string{"example.test"}, CapacityBytes: 2 << 30, DeliveredDays: 7, NotifyOrigin: "https://notify.example.test:10000"}
	cfg, err := j.sourceConfig(inputs)
	if err != nil || !cfg.valid() || cfg.NotifyToken == cfg.Reader || cfg.NotifyToken == cfg.Control {
		t.Fatal("source creation failed", err)
	}
	secretPath := filepath.Join(filepath.Dir(path), "source-secrets.json")
	info, err := os.Stat(secretPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("invalid secret permissions", err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = OpenJournal(path, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	recovered, err := j.sourceConfig(inputs)
	if err != nil || !reflect.DeepEqual(cfg, recovered) {
		t.Fatal("secret recovery mismatch", err)
	}
	inputs.DeliveredDays++
	if _, err := j.sourceConfig(inputs); !errors.Is(err, ErrConflict) {
		t.Fatal("changed input accepted", err)
	}
	journal, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(journal), cfg.Reader) || strings.Contains(string(journal), cfg.Control) || strings.Contains(string(journal), cfg.NotifyToken) {
		t.Fatal("credential leaked to journal")
	}
}

func TestSourceSecretsFailClosed(t *testing.T) {
	inputs := sourceInputs{Domains: []string{"example.test"}, CapacityBytes: 2 << 30, DeliveredDays: 7}
	t.Run("invalid-before-create", func(t *testing.T) {
		j, path, _ := secretFixture(t)
		defer j.Close()
		if _, err := j.sourceConfig(sourceInputs{}); !errors.Is(err, ErrJournal) {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(filepath.Dir(path), "source-secrets.json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("invalid input created file", err)
		}
	})
	t.Run("partial", func(t *testing.T) {
		j, path, _ := secretFixture(t)
		defer j.Close()
		if err := os.WriteFile(filepath.Join(filepath.Dir(path), "source-secrets.json"), []byte("{"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := j.sourceConfig(inputs); !errors.Is(err, ErrUnknown) {
			t.Fatal("partial source accepted", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		j, path, _ := secretFixture(t)
		defer j.Close()
		if err := os.Symlink(path, filepath.Join(filepath.Dir(path), "source-secrets.json")); err != nil {
			t.Fatal(err)
		}
		if _, err := j.sourceConfig(inputs); !errors.Is(err, ErrJournal) {
			t.Fatal("unsafe source accepted", err)
		}
	})
	t.Run("lost-after-issue", func(t *testing.T) {
		j, path, _ := secretFixture(t)
		defer j.Close()
		cfg, err := j.sourceConfig(inputs)
		if err != nil {
			t.Fatal(err)
		}
		cloud := &fakeDeployment{fakeCloud: fakeCloud{workerOK: true, bucketOK: true}, vacant: true, createErr: errors.New("lost")}
		if err := j.deployVersion(t.Context(), cloud, cfg); !errors.Is(err, ErrUnknown) {
			t.Fatal(err)
		}
		secretPath := filepath.Join(filepath.Dir(path), "source-secrets.json")
		if err := os.Remove(secretPath); err != nil {
			t.Fatal(err)
		}
		if _, err := j.sourceConfig(inputs); !errors.Is(err, ErrUnknown) {
			t.Fatal("missing credential regenerated", err)
		}
		if _, err := os.Lstat(secretPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("missing credential recreated", err)
		}
	})
}
