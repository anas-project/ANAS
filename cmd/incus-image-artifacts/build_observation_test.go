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
