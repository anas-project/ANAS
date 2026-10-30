// TEST_CASES: MAILMOD-T-015
package directorymail

import (
	"encoding/json"
	"strings"
	"testing"
)

func transferRecordFixture() TransferRecord {
	return TransferRecord{Schema: TransferRecordSchema, OperationID: "11111111-1111-4111-8111-111111111111", ProofID: "22222222-2222-4222-8222-222222222222", Actor: "33333333-3333-4333-8333-333333333333", Address: "new@example.test", FromKind: "alias", FromOwner: "44444444-4444-4444-8444-444444444444", FromMailbox: "55555555-5555-4555-8555-555555555555", FromClaimVersion: "66666666-6666-4666-8666-666666666666", FromMailboxVersion: "77777777-7777-4777-8777-777777777777", RemainingDefault: "kept@example.test", ToOwner: "88888888-8888-4888-8888-888888888888", ToMailbox: "99999999-9999-4999-8999-999999999999", ToCreationOperation: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}
}

func TestTransferRecordRejectsMalformedOrAmbiguousOwnership(t *testing.T) {
	r := transferRecordFixture()
	raw, _ := json.Marshal(r)
	got, err := ParseTransferRecord(string(raw))
	if err != nil || got != r {
		t.Fatal("valid transfer record did not round-trip", err)
	}
	for _, mode := range []string{"primary", "same-owner", "same-box", "lost-default", "noncanonical", "proof", "unknown", "duplicate", "case-duplicate", "null", "extra-json", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			bad := r
			switch mode {
			case "primary":
				bad.FromKind = "primary"
			case "same-owner":
				bad.ToOwner = bad.FromOwner
			case "same-box":
				bad.ToMailbox = bad.FromMailbox
			case "lost-default":
				bad.RemainingDefault = bad.Address
			case "noncanonical":
				bad.Address = "NEW@EXAMPLE.TEST"
			case "proof":
				bad.ProofID = "unverified"
			}
			encoded, _ := json.Marshal(bad)
			input := string(encoded)
			switch mode {
			case "unknown":
				input = strings.TrimSuffix(input, "}") + `,"password":"must-never-be-recorded"}`
			case "duplicate":
				input = strings.TrimSuffix(input, "}") + `,"proof_id":"` + r.ProofID + `"}`
			case "case-duplicate":
				input = strings.TrimSuffix(input, "}") + `,"PROOF_ID":"` + r.ProofID + `"}`
			case "null":
				input = "null"
			case "extra-json":
				input += "{}"
			case "oversize":
				input += strings.Repeat(" ", 4097)
			}
			if _, err := ParseTransferRecord(input); err == nil {
				t.Fatal("invalid ownership record accepted", mode)
			}
		})
	}
}

func TestCurrentTransferRecordIgnoresADOrderingButNeverAmbiguity(t *testing.T) {
	r := transferRecordFixture()
	raw, _ := json.MarshalIndent(r, "", " ")
	canonical, _ := json.Marshal(r)
	past := r
	past.ProofID, past.OperationID = past.FromClaimVersion, past.FromMailboxVersion
	past.ToOwner, past.ToMailbox = past.Actor, past.OperationID
	history, _ := json.Marshal(past)
	for _, values := range [][]string{{string(raw), string(history)}, {string(history), string(raw)}} {
		got, err := CurrentTransferRecord(values, r.Address, r.ToOwner, r.ToMailbox)
		if err != nil || got != string(canonical) {
			t.Fatal("AD order changed the selected current record", err)
		}
	}
	for _, mode := range []string{"missing-current", "other-address", "duplicate-proof", "two-current", "oversize-count"} {
		t.Run(mode, func(t *testing.T) {
			values := []string{string(raw)}
			switch mode {
			case "missing-current":
				values = []string{string(history)}
			case "other-address":
				bad := r
				bad.Address = "other@example.test"
				body, _ := json.Marshal(bad)
				values = []string{string(body)}
			case "duplicate-proof":
				values = append(values, string(canonical))
			case "two-current":
				bad := r
				bad.ProofID = bad.Actor
				body, _ := json.Marshal(bad)
				values = append(values, string(body))
			case "oversize-count":
				values = make([]string, 65)
			}
			if _, err := CurrentTransferRecord(values, r.Address, r.ToOwner, r.ToMailbox); err == nil {
				t.Fatal("unrecognized history adopted", mode)
			}
		})
	}
}
