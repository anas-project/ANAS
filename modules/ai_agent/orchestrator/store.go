package main

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// ErrNotFound is returned by lookups that legitimately find nothing, so callers
// can tell "no row yet" from "the store is broken".
var ErrNotFound = errors.New("not found")

// InboxEvent is one Forgejo delivery, exactly as it arrived. It is written
// before any handler runs and never rewritten, so a redelivery can be
// recognised and a lost delivery can be replayed (AGENT-R-011, AGENT-R-012).
type InboxEvent struct {
	DeliveryID string
	Event      string
	Repo       Repo
	Sender     string
	Payload    []byte
	ReceivedAt time.Time
	Processed  bool
	// Source records how the event reached the inbox: a signed webhook, or the
	// reconciliation sweep. Both take the same processing path; the difference
	// only matters when explaining what happened.
	Source string
}

// OutboxWrite is a write the orchestrator intends to make in Forgejo. It is
// reserved under an idempotency key before the call and only marked done after
// it succeeds, so a retry, a redelivery or two operators clicking approve at
// once produce one comment, one branch and one pull request (AGENT-R-044).
type OutboxWrite struct {
	Key       string
	RunID     string
	Kind      string
	Target    string
	Completed bool
	CreatedAt time.Time
}

// AgentIdentity is one agent's Forgejo account and the credentials issued to
// it. Scope is empty when the deployment's Forgejo can limit a token to
// specific repositories, and holds the repository when it cannot and the
// degraded one-account-per-repository path is in use (AGENT-R-006).
type AgentIdentity struct {
	Runtime          string
	Account          string
	Scope            string
	ForgejoUserID    int64
	TokenID          int64
	TokenFingerprint string
	SSHKeyID         int64
	Generation       int
	RotatedAt        time.Time
}

// WebhookRegistration records the system webhook this deployment owns, so a
// secret rotation can re-register the same hook rather than leaving a second
// one behind signing with a key nobody accepts (AGENT-R-009).
type WebhookRegistration struct {
	HookID            int64
	URL               string
	SecretFingerprint string
	Events            []string
	UpdatedAt         time.Time
}

// AuditRecord is one authorization decision, including refusals. A record that
// only kept the approvals would be useless for the question audits actually
// ask, which is who was told no and why (AGENT-R-034).
type AuditRecord struct {
	At       time.Time
	Subject  string
	Source   string
	Action   string
	Decision string
	Reason   string
	Repo     string
	Issue    int
}

// Store is the durable orchestration state. Nothing authoritative lives in
// process memory: the control plane must be able to restart and carry on from
// the database and the session volume alone (AGENT-R-003).
type Store interface {
	Migrate(ctx context.Context) error

	// RecordDelivery persists an event and reports whether it is new. A
	// repeated delivery id returns false and writes nothing.
	RecordDelivery(ctx context.Context, event InboxEvent) (bool, error)
	PendingEvents(ctx context.Context, limit int) ([]InboxEvent, error)
	MarkProcessed(ctx context.Context, deliveryID string) error

	// ReserveWrite claims an idempotency key. It returns false when the key is
	// already held, which is the signal to skip the external call entirely.
	ReserveWrite(ctx context.Context, write OutboxWrite) (bool, error)
	CompleteWrite(ctx context.Context, key string) error
	// WriteByRunID reports whether a run id belongs to this orchestrator's own
	// outbox. It is the second self-trigger guard behind the sender check.
	WriteByRunID(ctx context.Context, runID string) (bool, error)

	SaveIdentity(ctx context.Context, identity AgentIdentity) error
	Identities(ctx context.Context) ([]AgentIdentity, error)

	SaveWebhook(ctx context.Context, registration WebhookRegistration) error
	Webhook(ctx context.Context) (WebhookRegistration, error)

	Cursor(ctx context.Context, repo Repo) (time.Time, error)
	SetCursor(ctx context.Context, repo Repo, at time.Time) error

	AppendAudit(ctx context.Context, record AuditRecord) error
	Audit(ctx context.Context, limit int) ([]AuditRecord, error)

	Close()
}

// MemoryStore is an in-process Store used by tests and by the healthcheck path.
// It is never used to serve a deployment: every production path takes the
// PostgreSQL implementation, because process memory does not survive the
// restart AGENT-R-003 requires the orchestrator to survive.
type MemoryStore struct {
	mu         sync.Mutex
	events     map[string]InboxEvent
	order      []string
	writes     map[string]OutboxWrite
	identities map[string]AgentIdentity
	hook       *WebhookRegistration
	cursors    map[string]time.Time
	audit      []AuditRecord
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		events: map[string]InboxEvent{}, writes: map[string]OutboxWrite{},
		identities: map[string]AgentIdentity{}, cursors: map[string]time.Time{},
	}
}

func (s *MemoryStore) Migrate(context.Context) error { return nil }
func (s *MemoryStore) Close()                        {}

func (s *MemoryStore) RecordDelivery(_ context.Context, event InboxEvent) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.events[event.DeliveryID]; exists {
		return false, nil
	}
	s.events[event.DeliveryID] = event
	s.order = append(s.order, event.DeliveryID)
	return true, nil
}

func (s *MemoryStore) PendingEvents(_ context.Context, limit int) ([]InboxEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var pending []InboxEvent
	for _, id := range s.order {
		event := s.events[id]
		if event.Processed {
			continue
		}
		pending = append(pending, event)
		if len(pending) == limit {
			break
		}
	}
	return pending, nil
}

func (s *MemoryStore) MarkProcessed(_ context.Context, deliveryID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	event, ok := s.events[deliveryID]
	if !ok {
		return ErrNotFound
	}
	event.Processed = true
	s.events[deliveryID] = event
	return nil
}

func (s *MemoryStore) ReserveWrite(_ context.Context, write OutboxWrite) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.writes[write.Key]; exists {
		return false, nil
	}
	if write.CreatedAt.IsZero() {
		write.CreatedAt = time.Now().UTC()
	}
	s.writes[write.Key] = write
	return true, nil
}

func (s *MemoryStore) CompleteWrite(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	write, ok := s.writes[key]
	if !ok {
		return ErrNotFound
	}
	write.Completed = true
	s.writes[key] = write
	return nil
}

func (s *MemoryStore) WriteByRunID(_ context.Context, runID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, write := range s.writes {
		if write.RunID == runID {
			return true, nil
		}
	}
	return false, nil
}

func (s *MemoryStore) SaveIdentity(_ context.Context, identity AgentIdentity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.identities[identity.Runtime+"|"+identity.Scope] = identity
	return nil
}

func (s *MemoryStore) Identities(context.Context) ([]AgentIdentity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AgentIdentity, 0, len(s.identities))
	for _, identity := range s.identities {
		out = append(out, identity)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Runtime != out[j].Runtime {
			return out[i].Runtime < out[j].Runtime
		}
		return out[i].Scope < out[j].Scope
	})
	return out, nil
}

func (s *MemoryStore) SaveWebhook(_ context.Context, registration WebhookRegistration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	registration.UpdatedAt = time.Now().UTC()
	s.hook = &registration
	return nil
}

func (s *MemoryStore) Webhook(context.Context) (WebhookRegistration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hook == nil {
		return WebhookRegistration{}, ErrNotFound
	}
	return *s.hook, nil
}

func (s *MemoryStore) Cursor(_ context.Context, repo Repo) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cursors[repo.String()], nil
}

func (s *MemoryStore) SetCursor(_ context.Context, repo Repo, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cursors[repo.String()] = at.UTC()
	return nil
}

func (s *MemoryStore) AppendAudit(_ context.Context, record AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record.At.IsZero() {
		record.At = time.Now().UTC()
	}
	s.audit = append(s.audit, record)
	return nil
}

func (s *MemoryStore) Audit(_ context.Context, limit int) ([]AuditRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > len(s.audit) {
		limit = len(s.audit)
	}
	out := make([]AuditRecord, limit)
	copy(out, s.audit[len(s.audit)-limit:])
	return out, nil
}
