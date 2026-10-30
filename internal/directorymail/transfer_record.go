package directorymail

import (
	"encoding/json"
	"io"
	"strings"
)

const TransferRecordSchema = "anas.mail-address-transfer/v1"

// TransferRecord is one non-secret value of anasMailOwnershipHistory. It binds
// the old address and retained mailbox to one new-account operation. A record
// alone grants nothing: Go must recognize its proof in the durable ledger.
type TransferRecord struct {
	Schema              string `json:"schema"`
	OperationID         string `json:"operation_id"`
	ProofID             string `json:"proof_id"`
	Actor               string `json:"actor"`
	Address             string `json:"address"`
	FromKind            string `json:"from_kind"`
	FromOwner           string `json:"from_owner"`
	FromMailbox         string `json:"from_mailbox"`
	FromClaimVersion    string `json:"from_claim_version"`
	FromMailboxVersion  string `json:"from_mailbox_version"`
	RemainingDefault    string `json:"remaining_default"`
	ToOwner             string `json:"to_owner"`
	ToMailbox           string `json:"to_mailbox"`
	ToCreationOperation string `json:"to_creation_operation"`
}

func (r TransferRecord) Valid() bool {
	if r.Schema != TransferRecordSchema || r.FromKind != "alias" && r.FromKind != "additional" || r.FromOwner == r.ToOwner || r.FromMailbox == r.ToMailbox || r.Address == r.RemainingDefault {
		return false
	}
	for _, value := range []string{r.OperationID, r.ProofID, r.Actor, r.FromOwner, r.FromMailbox, r.FromClaimVersion, r.FromMailboxVersion, r.ToOwner, r.ToMailbox, r.ToCreationOperation} {
		if !resourceUUID.MatchString(value) {
			return false
		}
	}
	for _, value := range []string{r.Address, r.RemainingDefault} {
		local, domain, ok := strings.Cut(value, "@")
		canonical, err := Address(local, domain)
		if !ok || err != nil || canonical != value {
			return false
		}
	}
	return true
}

func ParseTransferRecord(raw string) (TransferRecord, error) {
	var r TransferRecord
	if len(raw) > 4096 || strictJSON(raw, &r) != nil || !r.Valid() {
		return TransferRecord{}, ErrConflict
	}
	// Fields are flat strings. Reject duplicates rather than trusting JSON's
	// last-value-wins behavior for an ownership or proof identifier.
	d := json.NewDecoder(strings.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return TransferRecord{}, ErrConflict
	}
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		var value string
		if err != nil || !ok || seen[name] || d.Decode(&value) != nil {
			return TransferRecord{}, ErrConflict
		}
		switch name {
		case "schema", "operation_id", "proof_id", "actor", "address", "from_kind", "from_owner", "from_mailbox", "from_claim_version", "from_mailbox_version", "remaining_default", "to_owner", "to_mailbox", "to_creation_operation":
		default:
			return TransferRecord{}, ErrConflict
		}
		seen[name] = true
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') || d.Decode(new(any)) != io.EOF {
		return TransferRecord{}, ErrConflict
	}
	return r, nil
}

// CurrentTransferRecord does not depend on AD multivalue ordering. Historical
// records remain in AD; only the unique record ending at the current owner/box
// enters the mailbox projection. Unknown/ambiguous history never authorizes it.
func CurrentTransferRecord(values []string, address, owner, box string) (string, error) {
	if len(values) == 0 {
		return "", nil
	}
	if len(values) > 64 {
		return "", ErrConflict
	}
	bytes, selected := 0, ""
	seen := map[string]bool{}
	for _, raw := range values {
		bytes += len(raw)
		if bytes > 65536 {
			return "", ErrConflict
		}
		r, err := ParseTransferRecord(raw)
		if err != nil || r.Address != address || seen[r.ProofID] {
			return "", ErrConflict
		}
		seen[r.ProofID] = true
		if r.ToOwner == owner && r.ToMailbox == box {
			if selected != "" {
				return "", ErrConflict
			}
			canonical, _ := json.Marshal(r)
			selected = string(canonical)
		}
	}
	if selected == "" {
		return "", ErrConflict
	}
	return selected, nil
}
