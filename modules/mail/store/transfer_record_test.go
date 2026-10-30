// TEST_CASES: MAILMOD-T-015
package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/anas-project/ANAS/modules/mail/identity"
)

func TestProjectionLegacyBytesRemainStableWithoutTransferHistory(t *testing.T) {
	// Fixed v1 bytes from before TransferRecord existed; empty optional metadata
	// must not invalidate every old applied fingerprint on upgrade.
	legacy := []byte(`{"schema":"anas.mailbox-projection/v1","resource":{"ID":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","OwnerAnchor":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","Kind":"primary","DefaultAddress":"alice@example.test","State":"active","Version":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","OperationID":"dddddddd-dddd-4ddd-8ddd-dddddddddddd","OperationState":"complete","Addresses":[{"Address":"alice@example.test","Kind":"primary","State":"active","Version":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","OperationID":"dddddddd-dddd-4ddd-8ddd-dddddddddddd","OperationState":"complete"}]}}`)
	var snapshot projectionSnapshot
	if err := json.Unmarshal(legacy, &snapshot); err != nil {
		t.Fatal(err)
	}
	fingerprint, actual, err := ProjectionFingerprint(snapshot.Resource)
	sum := sha256.Sum256(legacy)
	if err != nil || !bytes.Equal(actual, legacy) || fingerprint != hex.EncodeToString(sum[:]) {
		t.Fatal("empty history changed a previously valid projection", err)
	}
}

func TestPostgreSQLTransferRecordAloneCannotReassignActiveOrRetainedAddress(t *testing.T) {
	s, ctx, old := projectionStore(t)
	alias := "transfer-" + old.ID + "@example.test"
	old.Addresses = append(old.Addresses, identity.AddressResource{Address: alias, Kind: "alias", State: "active", Version: old.Version, OperationID: old.OperationID, OperationState: "complete"})
	next := projectionResource(t, "primary")
	next.DefaultAddress = alias
	next.Addresses[0].Address = alias
	id, _ := mailboxID()
	proof, _ := mailboxID()
	var first Mailbox
	expected := ""
	for _, state := range []string{"active", "retained"} {
		old.Addresses[1].State = state
		old.Version, _ = mailboxID()
		p, err := s.StageMailbox(ctx, old, expected)
		if err != nil {
			t.Fatal(err)
		}
		expected = p.Revision
		if first.ID == "" {
			first = p.Mailbox
		}
		record := directorymail.TransferRecord{Schema: directorymail.TransferRecordSchema, OperationID: id, ProofID: proof, Actor: old.OwnerAnchor, Address: alias, FromKind: "alias", FromOwner: old.OwnerAnchor, FromMailbox: old.ID, FromClaimVersion: old.Addresses[1].Version, FromMailboxVersion: old.Version, RemainingDefault: old.DefaultAddress, ToOwner: next.OwnerAnchor, ToMailbox: next.ID, ToCreationOperation: next.OperationID}
		body, err := json.Marshal(record)
		if err != nil || !record.Valid() {
			t.Fatal("invalid synthetic record", err)
		}
		next.Addresses[0].TransferRecord = string(body)
		if _, _, err = ProjectionFingerprint(next); err != nil {
			t.Fatal("syntactic record rejected before ownership gate", err)
		}
		if _, err = s.StageMailbox(ctx, next, ""); !errors.Is(err, ErrAddressUnavailable) {
			t.Fatal("an AD receipt string replaced durable proof", err)
		}
		var mailbox, addressState string
		if err = s.DB.QueryRowContext(ctx, `SELECT mailbox_id,state FROM mail.address WHERE address=$1`, alias).Scan(&mailbox, &addressState); err != nil || mailbox != old.ID || addressState != state {
			t.Fatal("unproved record changed original address ownership", err)
		}
		var newRows int
		if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail.person WHERE anchor=$1`, next.OwnerAnchor).Scan(&newRows); err != nil || newRows != 0 {
			t.Fatal("failed transfer left a new person projection", err)
		}
		current, err := s.MailboxProjection(ctx, old.ID)
		if err != nil || current.Mailbox.ProtocolLogin != first.ProtocolLogin || current.Mailbox.StorageRef != first.StorageRef || current.Mailbox.RoundcubeKey != first.RoundcubeKey {
			t.Fatal("transfer attempt changed original mailbox identity", err)
		}
	}
}
