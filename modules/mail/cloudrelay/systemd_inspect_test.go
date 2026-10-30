// TEST_CASES: MAILMOD-T-064
package cloudrelay

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemdInspectionRequiresBoundMaterialsAndLock(t *testing.T) {
	layout, path, _, binary, unit := publishFixture(t)
	if err := publishSystemdFiles(layout, path, binary, unit); err != nil {
		t.Fatal(err)
	}
	count := 0
	verify := func() error {
		return withVerifiedPublication(layout, path, binary, unit, func(_ RuntimeConfig, _ int, _ []byte) error { count++; return nil })
	}
	if err := verify(); err != nil || count != 1 {
		t.Fatalf("verified materials: %v", err)
	}
	unitPath := filepath.Join(layout.units, "anas-mail-relay.service")
	if err := os.WriteFile(unitPath, []byte("modified"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verify(); !errors.Is(err, ErrPublishConflict) || count != 1 {
		t.Fatalf("modified unit: %v", err)
	}
}
func TestSystemdInspectionBlocksCompetingOperationAndDropIn(t *testing.T) {
	layout, path, _, binary, unit := publishFixture(t)
	if err := publishSystemdFiles(layout, path, binary, unit); err != nil {
		t.Fatal(err)
	}
	if err := withVerifiedPublication(layout, path, binary, unit, func(_ RuntimeConfig, _ int, _ []byte) error {
		return withVerifiedPublication(layout, path, binary, unit, func(_ RuntimeConfig, _ int, _ []byte) error { return nil })
	}); !errors.Is(err, ErrPublishConflict) {
		t.Fatalf("concurrent inspection: %v", err)
	}
	if err := os.Mkdir(filepath.Join(layout.units, "anas-mail-relay.service.d"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := withVerifiedPublication(layout, path, binary, unit, func(_ RuntimeConfig, _ int, _ []byte) error { t.Fatal("executed with drop-in"); return nil }); !errors.Is(err, ErrPublishConflict) {
		t.Fatal(err)
	}
}

func TestSystemdInspectionRejectsUnfinishedPublication(t *testing.T) {
	layout, path, _, binary, unit := publishFixture(t)
	if err := publishSystemdFiles(layout, path, binary, unit); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(layout.etc, "anas-mail-relay", "publish-unit.done")); err != nil {
		t.Fatal(err)
	}
	if err := withVerifiedPublication(layout, path, binary, unit, func(_ RuntimeConfig, _ int, _ []byte) error { t.Fatal("inspected incomplete publication"); return nil }); !errors.Is(err, ErrPublishUnknown) {
		t.Fatal(err)
	}
}
func TestSystemdInspectionParsesOnlyCompleteOwnedUnit(t *testing.T) {
	const clean = "MainPID=0\nDynamicUser=yes\nId=anas-mail-relay.service\nLoadState=loaded\nActiveState=inactive\nSubState=dead\nFragmentPath=/etc/systemd/system/anas-mail-relay.service\nDropInPaths=\nUnitFileState=disabled\nNeedDaemonReload=no\nTransient=no\n"
	status, err := parseSystemdObservation([]byte(clean))
	if err != nil || !status.owned("/etc/systemd/system/anas-mail-relay.service") {
		t.Fatal("valid status rejected", err)
	}
	for _, bad := range []string{clean + "ActiveState=active\n", strings.Replace(clean, "Transient=no\n", "", 1), clean + "Unexpected=x\n", strings.Repeat("x", 16385)} {
		if _, err := parseSystemdObservation([]byte(bad)); !errors.Is(err, ErrPublishUnknown) {
			t.Fatal("bad manager output accepted")
		}
	}
	for _, bad := range []string{strings.Replace(clean, "DynamicUser=yes", "DynamicUser=no", 1), strings.Replace(clean, "DropInPaths=", "DropInPaths=/foreign.conf", 1), strings.Replace(clean, "NeedDaemonReload=no", "NeedDaemonReload=yes", 1), strings.Replace(clean, "Transient=no", "Transient=yes", 1), strings.Replace(clean, "FragmentPath=/etc/systemd/system/anas-mail-relay.service", "FragmentPath=/tmp/foreign", 1)} {
		s, e := parseSystemdObservation([]byte(bad))
		if e != nil || s.owned("/etc/systemd/system/anas-mail-relay.service") {
			t.Fatal("unowned unit accepted")
		}
	}
}
