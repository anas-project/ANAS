package computeimage

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestUnverifiedSourceFailureSurvivesOnlyAsClosedDiagnostic(t *testing.T) {
	wrapped := fmt.Errorf("private-builder-path: %w", BuildUnverifiedSourceFailure())
	err := sanitizeBuildFailure(wrapped)
	if !errors.Is(err, ErrArtifactBuildIncomplete) || !strings.Contains(err.Error(), "signatures were not verified") || strings.Contains(err.Error(), "private") {
		t.Fatal("source-verification failure was lost or exposed raw context", err)
	}
}
