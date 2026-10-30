// TEST_CASES: MAILMOD-T-009
package ingress

import (
	"errors"
	"os"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/store"
)

func TestUnsafeLocalStagingIsRejectedBeforeDownloadAndReportedAsLocal(t *testing.T) {
	path := t.TempDir()
	if e := os.Chmod(path, 0755); e != nil {
		t.Fatal(e)
	}
	// A nil source makes any attempted download fail the test. This reproduces
	// the real fixture failure without a network or native delivery dependency.
	c := Consumer{Directory: path}
	if _, e := c.stageRaw(t.Context(), store.IngressTask{}); !errors.Is(e, ErrStaging) || sourceErrorCode(e) != "local-staging-unavailable" {
		t.Fatal("unsafe staging was accepted or blamed on the source", e)
	}
}
