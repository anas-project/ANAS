package directorymail

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/go-ldap/ldap/v3"
)

// The fake models only single-object atomic operations. Cross-object recovery
// is exercised by discarding the client and keeping the directory after faults.
type resourceMemory struct {
	mu                    sync.Mutex
	rows                  map[string]map[string][]string
	adds, modifies        int
	failAdd, failModify   int
	loseAdd, loseModify   int
	failNextRead          bool
	readFailureAfterWrite bool
}

func newResourceMemory() *resourceMemory {
	return &resourceMemory{rows: map[string]map[string][]string{}}
}

func (m *resourceMemory) Search(q *ldap.SearchRequest) (*ldap.SearchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failNextRead {
		m.failNextRead = false
		return nil, errors.New("injected read failure")
	}
	row, ok := m.rows[q.BaseDN]
	if !ok {
		return nil, ldap.NewError(ldap.LDAPResultNoSuchObject, errors.New("not found"))
	}
	return &ldap.SearchResult{Entries: []*ldap.Entry{ldap.NewEntry(q.BaseDN, row)}}, nil
}

func (m *resourceMemory) Add(q *ldap.AddRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.adds++
	if m.adds == m.failAdd {
		return errors.New("injected add failure")
	}
	if _, ok := m.rows[q.DN]; ok {
		return ldap.NewError(ldap.LDAPResultEntryAlreadyExists, errors.New("already exists"))
	}
	row := map[string][]string{}
	for _, a := range q.Attributes {
		row[a.Type] = append([]string(nil), a.Vals...)
	}
	m.rows[q.DN] = row
	if m.readFailureAfterWrite {
		m.readFailureAfterWrite = false
		m.failNextRead = true
	}
	if m.adds == m.loseAdd {
		return errors.New("injected lost response after add")
	}
	return nil
}

func (m *resourceMemory) Modify(q *ldap.ModifyRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modifies++
	if m.modifies == m.failModify {
		return errors.New("injected modify failure")
	}
	row := m.rows[q.DN]
	if row == nil {
		return ldap.NewError(ldap.LDAPResultNoSuchObject, errors.New("not found"))
	}
	copy := map[string][]string{}
	for k, v := range row {
		copy[k] = append([]string(nil), v...)
	}
	for _, c := range q.Changes {
		switch c.Operation {
		case ldap.DeleteAttribute:
			if fmt.Sprint(copy[c.Modification.Type]) != fmt.Sprint(c.Modification.Vals) {
				return ldap.NewError(ldap.LDAPResultNoSuchAttribute, errors.New("stale version"))
			}
			delete(copy, c.Modification.Type)
		case ldap.AddAttribute:
			if len(copy[c.Modification.Type]) > 0 {
				return errors.New("attribute exists")
			}
			copy[c.Modification.Type] = append([]string(nil), c.Modification.Vals...)
		case ldap.ReplaceAttribute:
			copy[c.Modification.Type] = append([]string(nil), c.Modification.Vals...)
		default:
			return errors.New("unexpected modify operation")
		}
	}
	m.rows[q.DN] = copy
	if m.readFailureAfterWrite {
		m.readFailureAfterWrite = false
		m.failNextRead = true
	}
	if m.modifies == m.loseModify {
		return errors.New("injected lost response after modify")
	}
	return nil
}

const primaryTestBase = "DC=example,DC=test"
const primaryTestOwner = "8a107f8e-b0b6-4e68-803a-a8da14ea11e0"
const primaryTestGUID = "12418911-8739-4c5a-8ce7-04603828c41d"

func primaryIntent() AccountIntent {
	return AccountIntent{Username: "new", UPN: "new@example.test", PersonDN: "CN=new,OU=People," + primaryTestBase, ProfileHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
}

func primaryFixture(t *testing.T) (*resourceMemory, *PrimaryDirectory, PrimaryRegistration) {
	t.Helper()
	m := newResourceMemory()
	d, err := NewPrimaryDirectory(m, primaryTestBase)
	if err != nil {
		t.Fatal(err)
	}
	r, err := d.Reserve(primaryTestOwner, "NEW@EXAMPLE.TEST", primaryIntent())
	if err != nil {
		t.Fatal(err)
	}
	return m, d, r
}

func TestPrimaryDirectoryReservationIdentityAndConflict(t *testing.T) {
	m, d, r := primaryFixture(t)
	if r.Owner == r.MailboxID || r.Owner == r.OperationID || r.MailboxID == r.OperationID || r.Address != "new@example.test" || r.Stage != "reserved" {
		t.Fatal("resource identities or pending state collapsed")
	}
	again, err := d.Reserve(r.Owner, r.Address, primaryIntent())
	if err != nil || again != r || m.adds != 2 {
		t.Fatal("same reservation was replaced")
	}
	intent := primaryIntent()
	intent.ForceChange = !intent.ForceChange
	if _, err := d.Reserve(r.Owner, r.Address, intent); !errors.Is(err, ErrConflict) {
		t.Fatal("changed intent was adopted")
	}
	other, _ := newResourceUUID()
	if _, err := d.Reserve(other, r.Address, primaryIntent()); !errors.Is(err, ErrConflict) {
		t.Fatal("another owner acquired existing address")
	}
	claimDN, _, _ := AddressClaimDN(primaryTestBase, r.Address)
	if m.rows[claimDN][OwnerAnchorAttribute][0] != r.Owner {
		t.Fatal("conflicting reservation changed ownership")
	}
}

func TestPrimaryDirectoryInterruptedReservationResumes(t *testing.T) {
	for _, mode := range []string{"mailbox_add", "claim_add", "mailbox_readback", "add_response_lost"} {
		t.Run(mode, func(t *testing.T) {
			m := newResourceMemory()
			switch mode {
			case "mailbox_add":
				m.failAdd = 1
			case "claim_add":
				m.failAdd = 2
			case "mailbox_readback":
				m.readFailureAfterWrite = true
			case "add_response_lost":
				m.loseAdd = 2
			}
			d, _ := NewPrimaryDirectory(m, primaryTestBase)
			_, first := d.Reserve(primaryTestOwner, "new@example.test", primaryIntent())
			if mode != "add_response_lost" && first == nil || mode == "add_response_lost" && first != nil {
				t.Fatal("incorrect initial write/readback outcome")
			}
			boxDN, _ := PrimaryMailboxDN(primaryTestBase, primaryTestOwner)
			boxID := ""
			if row := m.rows[boxDN]; row != nil {
				boxID = row[MailboxIDAttribute][0]
			}
			d, _ = NewPrimaryDirectory(m, primaryTestBase)
			r, err := d.Reserve(primaryTestOwner, "new@example.test", primaryIntent())
			if err != nil || boxID != "" && r.MailboxID != boxID || len(m.rows) != 2 || r.Stage != "reserved" {
				t.Fatal("interrupted reservation did not resume with same identity")
			}
		})
	}
}

func TestPrimaryDirectoryConcurrentCreatorOnlyOneWinner(t *testing.T) {
	_, d, r := primaryFixture(t)
	results := make(chan error, 12)
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() { _, err := d.StartCreation(r); results <- err })
	}
	workers.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatal("incorrect creator election", winners)
	}
}

func TestPrimaryDirectoryUnknownCreationNeverGrantsAnotherAdd(t *testing.T) {
	m, d, r := primaryFixture(t)
	m.readFailureAfterWrite = true
	if _, err := d.StartCreation(r); err == nil {
		t.Fatal("failed readback claimed success")
	}
	d, _ = NewPrimaryDirectory(m, primaryTestBase)
	r, err := d.Reserve(r.Owner, r.Address, primaryIntent())
	if err != nil || r.Stage != "creating" {
		t.Fatal("unknown write lost its durable intent")
	}
	if _, err := d.StartCreation(r); !errors.Is(err, ErrUnknown) {
		t.Fatal("creating stage allowed User Add again")
	}
}

func TestPrimaryDirectoryCompletionRecoveryAndObjectBinding(t *testing.T) {
	for _, failure := range []int{0, 1, 2, 3} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			m, d, r := primaryFixture(t)
			r, err := d.StartCreation(r)
			if err != nil {
				t.Fatal(err)
			}
			if failure != 0 {
				m.failModify = m.modifies + failure
			}
			r, err = d.ConfirmAccount(r, primaryTestGUID)
			if err == nil {
				_, err = d.Complete(r)
			}
			if failure != 0 && err == nil {
				t.Fatal("injected failure claimed completion")
			}
			m.failModify = 0
			d, _ = NewPrimaryDirectory(m, primaryTestBase)
			r, err = d.Reserve(primaryTestOwner, "new@example.test", primaryIntent())
			if err == nil {
				r, err = d.ConfirmAccount(r, primaryTestGUID)
			}
			if err == nil {
				r, err = d.Complete(r)
			}
			if err != nil || r.Stage != "complete" {
				t.Fatal("same-person completion could not resume", err)
			}
			if _, err := d.ConfirmAccount(r, primaryTestOwner); !errors.Is(err, ErrConflict) {
				t.Fatal("recreated AD person inherited bound resource")
			}
			claimDN, _, _ := AddressClaimDN(primaryTestBase, r.Address)
			delete(m.rows, claimDN)
			if _, err := d.Reserve(r.Owner, r.Address, primaryIntent()); !errors.Is(err, ErrConflict) {
				t.Fatal("deleted completed address claim was silently recreated")
			}
		})
	}
}

func TestPrimaryDirectoryRejectsMalformedResourceAndScope(t *testing.T) {
	for _, attribute := range []string{MailboxIDAttribute, OwnerAnchorAttribute, ConfigVersionAttribute, "anasMailboxKind", "anasMailDefaultAddress", "anasMailOperationID", "anasMailOperationData", "anasMailOperationState", "anasMailState", "objectClass"} {
		t.Run(attribute, func(t *testing.T) {
			m, d, r := primaryFixture(t)
			dn, _ := PrimaryMailboxDN(primaryTestBase, r.Owner)
			m.rows[dn][attribute] = []string{"malformed"}
			if _, err := d.Reserve(r.Owner, r.Address, primaryIntent()); !errors.Is(err, ErrConflict) {
				t.Fatal("malformed resource was adopted")
			}
		})
	}
	if _, err := NewPrimaryDirectory(newResourceMemory(), "OU=Mail,DC=example,DC=test"); err == nil {
		t.Fatal("caller-selected resource root accepted")
	}
}
