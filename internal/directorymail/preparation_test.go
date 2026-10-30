// TEST_CASES: MAILMOD-T-015
package directorymail

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestTransferPreparationKeepsExistingAddressAndAllocatesNoPerson(t *testing.T) {
	for _, mode := range []string{"normal", "lost-add-reply", "lost-readback"} {
		t.Run(mode, func(t *testing.T) {
			memory := newResourceMemory()
			claimDN, _, _ := AddressClaimDN(primaryTestBase, "new@example.test")
			memory.rows[claimDN] = map[string][]string{"objectClass": {"top", AddressClaimClass}, "anasMailAddress": {"new@example.test"}, OwnerAnchorAttribute: {primaryTestGUID}, MailboxIDAttribute: {primaryTestGUID}, "anasMailAddressKind": {"alias"}, "anasMailState": {"active"}, ConfigVersionAttribute: {primaryTestGUID}, "anasMailOperationID": {primaryTestGUID}, "anasMailOperationState": {"complete"}}
			before, _ := json.Marshal(memory.rows[claimDN])
			if mode == "lost-add-reply" {
				memory.loseAdd = 1
			} else if mode == "lost-readback" {
				memory.readFailureAfterWrite = true
			}
			directory, err := NewPrimaryDirectory(memory, primaryTestBase)
			if err != nil {
				t.Fatal(err)
			}
			first, err := directory.PrepareAccount(primaryTestOwner, "NEW@EXAMPLE.TEST", primaryIntent())
			if mode == "lost-readback" && !errors.Is(err, ErrUnknown) {
				t.Fatal("missing readback pretended preparation succeeded", err)
			} else if mode != "lost-readback" && err != nil {
				t.Fatal(err)
			}
			directory, _ = NewPrimaryDirectory(memory, primaryTestBase)
			prepared, err := directory.PrepareAccount(primaryTestOwner, "new@example.test", primaryIntent())
			if err != nil || prepared.Address != "new@example.test" || prepared.Stage != "reserved" || prepared.ObjectGUID != "" || first.MailboxID != "" && first.MailboxID != prepared.MailboxID {
				t.Fatal("retry changed target or created a login identity", err)
			}
			read, intent, err := directory.PreparedAccount(primaryTestOwner)
			if err != nil || read.MailboxID != prepared.MailboxID || read.OperationID != prepared.OperationID || intent != primaryIntent() {
				t.Fatal("read-only prepared creation did not retain exact intent", err)
			}
			if _, err = directory.Reserve(primaryTestOwner, "new@example.test", primaryIntent()); !errors.Is(err, ErrConflict) {
				t.Fatal("preparation acquired another person's address", err)
			}
			if _, err = directory.StartCreation(prepared); !errors.Is(err, ErrConflict) {
				t.Fatal("creation started before the address was handed over", err)
			}
			after, _ := json.Marshal(memory.rows[claimDN])
			if string(after) != string(before) || len(memory.rows) != 2 || memory.adds != 1 || memory.modifies != 0 || memory.rows[primaryIntent().PersonDN] != nil {
				t.Fatal("preparation modified old ownership or created a person")
			}
		})
	}
}

func TestPreparedAccountRejectsStartedCreationOrChangedIntent(t *testing.T) {
	memory, directory, registration := primaryFixture(t)
	changed := primaryIntent()
	changed.ProfileHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := directory.PrepareAccount(primaryTestOwner, registration.Address, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("preparation adopted another creation intent", err)
	}
	if _, err := directory.StartCreation(registration); err != nil {
		t.Fatal(err)
	}
	before := memory.modifies
	if _, _, err := directory.PreparedAccount(primaryTestOwner); !errors.Is(err, ErrConflict) {
		t.Fatal("already-started account reported as a fresh prepared target", err)
	}
	if memory.modifies != before {
		t.Fatal("prepared-account read advanced creation")
	}
}
