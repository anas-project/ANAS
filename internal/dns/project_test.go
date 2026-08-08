package dns

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks up from the package directory so the test locates cask
// bundles without depending on where `go test` was invoked from.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// The generated tables are copies of the registry that live in distributable
// bundles, so nothing but this test prevents them from drifting once someone
// edits providers.yml and forgets to regenerate.
func TestProjectionsMatchCommittedFiles(t *testing.T) {
	reg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	projections, err := reg.Projections()
	if err != nil {
		t.Fatal(err)
	}
	if len(projections) != len(knownEngines) {
		t.Fatalf("projected %d engines, want %d", len(projections), len(knownEngines))
	}
	root := repoRoot(t)
	for _, projection := range projections {
		committed, err := os.ReadFile(filepath.Join(root, projection.Path))
		if err != nil {
			t.Errorf("%s: %v\nrun: go run ./cmd/gen-dns-registry .", projection.Path, err)
			continue
		}
		if !bytes.Equal(committed, projection.Source) {
			t.Errorf("%s is out of date with internal/dns/providers.yml\nrun: go run ./cmd/gen-dns-registry .", projection.Path)
		}
	}
}

// A projection must only carry the platforms its own engine can address.
// Shipping lego's 200-odd vendors to ddns-go would make a hook accept a
// platform it cannot actually update.
func TestProjectionCarriesOnlyItsOwnEngine(t *testing.T) {
	reg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	projections, err := reg.Projections()
	if err != nil {
		t.Fatal(err)
	}
	for _, projection := range projections {
		source := string(projection.Source)
		for _, p := range reg.Platforms {
			// gofmt aligns map values, so match the value rather than the key.
			marker := `{Name: "` + p.Name + `",`
			present := strings.Contains(source, marker)
			if want := p.Supports(projection.Engine); present != want {
				t.Errorf("%s: platform %s present=%v, supported=%v", projection.Engine, p.Name, present, want)
			}
		}
	}
}

// lego must not be handed the legacy DNSPod token, and ddns_updater must not
// be handed Tencent Cloud keys: both would be a credential the engine cannot
// use, surfacing as an authentication failure rather than a config error.
func TestProjectionExcludesUnsupportedPairs(t *testing.T) {
	reg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	projections, err := reg.Projections()
	if err != nil {
		t.Fatal(err)
	}
	byEngine := map[string]string{}
	for _, projection := range projections {
		byEngine[projection.Engine] = string(projection.Source)
	}
	if strings.Contains(byEngine[EngineLego], `"dnspod":`) {
		t.Error("lego projection contains the legacy dnspod platform")
	}
	if strings.Contains(byEngine[EngineDDNSUpdater], `"tencentcloud":`) {
		t.Error("ddns_updater projection contains tencentcloud, which it cannot address")
	}
	if !strings.Contains(byEngine[EngineDDNSGo], `"tencentcloud":`) {
		t.Error("ddns_go projection is missing tencentcloud")
	}
}
