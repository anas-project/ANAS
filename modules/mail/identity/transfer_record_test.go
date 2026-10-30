// TEST_CASES: MAILMOD-T-015
package identity

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/anas-project/ANAS/internal/directorymail"
)

func TestMailboxTransferHistoryMustMatchAddressAndCurrentResource(t *testing.T) {
	d := Directory{config: directoryConfig()}
	box := MailboxResource{ID: resourceBoxID, OwnerAnchor: testAnchor, Kind: "primary", DefaultAddress: "alice@example.test"}
	r := directorymail.TransferRecord{Schema: directorymail.TransferRecordSchema, OperationID: resourceOperation, ProofID: resourceVersion, Actor: groupAnchor, Address: box.DefaultAddress, FromKind: "alias", FromOwner: groupAnchor, FromMailbox: resourceOperation, FromClaimVersion: resourceVersion, FromMailboxVersion: resourceVersion, RemainingDefault: "kept@example.test", ToOwner: testAnchor, ToMailbox: resourceBoxID, ToCreationOperation: resourceOperation}
	for _, mode := range []string{"legacy", "matching", "foreign-address", "foreign-owner", "foreign-box", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			claim := addressEntry(t, box.DefaultAddress, "primary")
			record := r
			switch mode {
			case "foreign-address":
				record.Address = "other@example.test"
			case "foreign-owner":
				record.ToOwner = resourceVersion
			case "foreign-box":
				record.ToMailbox = resourceVersion
			}
			body, _ := json.Marshal(record)
			if mode != "legacy" {
				values := []string{string(body)}
				if mode == "duplicate" {
					values = append(values, string(body))
				}
				changeAttribute(claim, "anasMailOwnershipHistory", values)
			}
			address, err := d.addressResource(claim, box)
			if mode == "legacy" || mode == "matching" {
				if err != nil || (address.TransferRecord != "") != (mode == "matching") {
					t.Fatal("legacy/matching history was not read consistently", err)
				}
			} else if !errors.Is(err, ErrMailResources) {
				t.Fatal("another ownership history entered this resource", err)
			}
		})
	}
}
