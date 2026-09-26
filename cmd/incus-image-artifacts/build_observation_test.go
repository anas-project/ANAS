package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/computeimage"
)

func TestBuildObservationReportsOnlyFixedLastStage(t *testing.T) {
	w := &buildObservation{}
	for _, chunk := range []string{`time="private" level=info msg="Managing pack`, "ages\"\n", "private-token-and-url\n", "msg=\"Running generator\" source=private-path\n", "partial-private"} {
		if n, err := w.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatal(n, err)
		}
	}
	err := w.failure()
	if !errors.Is(err, computeimage.ErrArtifactBuildIncomplete) || !strings.HasSuffix(err.Error(), "stage: files") || strings.Contains(err.Error(), "private") {
		t.Fatal(err)
	}
	if w.line != nil {
		t.Fatal("raw diagnostic fragment retained")
	}
}

func TestBuildObservationBoundsLinesAndRejectsPartialPhaseRecords(t *testing.T) {
	w := &buildObservation{}
	w.Write([]byte(strings.Repeat("x", maxBuildObservationLine+1) + `msg="Running generator"` + "\n"))
	w.Write([]byte(`msg="Managing packages"`))
	if cap(w.line) > maxBuildObservationLine*2 || !strings.HasSuffix(w.failure().Error(), "stage: unknown") {
		t.Fatal("unbounded/partial log admitted")
	}
}

func TestUnsignedBootstrapObservationCannotBecomeSuccessfulPacking(t *testing.T) {
	w := &buildObservation{}
	for _, chunk := range []string{"W: Cannot check Release sig", "nature; keyring file not available /private-keyring-path\n", "msg=\"Packing image\"\n"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if !w.sourceWasUnverified() {
		t.Fatal("packing hid the missing bootstrap signature verification")
	}
	err := w.failure()
	if !errors.Is(err, computeimage.ErrArtifactBuildIncomplete) || !strings.Contains(err.Error(), "signatures were not verified") || strings.Contains(err.Error(), "private") {
		t.Fatal("missing verification did not retain a closed diagnostic", err)
	}
	if w.line != nil {
		t.Fatal("raw output retained")
	}
}
