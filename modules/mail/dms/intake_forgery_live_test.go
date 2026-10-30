// TEST_CASES: MAILMOD-T-007
package dms

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// This is a counterexample, not a passing security acceptance: it records
// whether the current observer accepts a forged mailbox trace. The caller is
// the explicit disposable fixed-DMS experiment and supplies only a synthetic
// message identifier; the observer itself performs no mail delivery here.
func TestNativeStoredTraceForgeryObservation(t *testing.T) {
	if os.Getenv("MAIL_DMS_FORGERY_OBSERVE") != "1" || os.Getenv("MAIL_DMS_NATIVE_CONFIG") != "/input/native.json" {
		t.Skip("explicit isolated DMS forgery fixture required")
	}
	label := os.Getenv("MAIL_DMS_FORGERY_LABEL")
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(label) {
		t.Fatal("invalid synthetic message identifier")
	}
	m := IntakeMetadata{SourceID: "11111111-1111-4111-8111-111111111111", MessageID: label, MailboxID: "22222222-2222-4222-8222-222222222222"}
	user := "mbx_" + strings.Repeat("a", 32) + "@" + ProtocolRealm
	if !nativeIntakeProtected(t.Context()) {
		t.Fatal("native protections unavailable")
	}
	if state, _ := nativeIntakeObserve(t.Context(), intakeRecord{Metadata: m, State: "queued"}, user); state != "stored" {
		t.Fatal("forged mailbox trace was not accepted by the current observer", state)
	}
	t.Log("OBSERVATION: current observer accepted forged mailbox trace as stored")
}
