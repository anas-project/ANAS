package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// schema is applied in order at every start. Each statement is idempotent, so
// a restart, a rollback to an older orchestrator, or a hand-repaired database
// all converge on the same shape without a migration tool the control plane
// would then have to keep in step with its own image tag.
var schema = []string{
	`CREATE TABLE IF NOT EXISTS inbox_event (
		delivery_id TEXT PRIMARY KEY,
		event TEXT NOT NULL,
		repo TEXT NOT NULL,
		sender TEXT NOT NULL,
		payload BYTEA NOT NULL,
		received_at TIMESTAMPTZ NOT NULL,
		processed BOOLEAN NOT NULL DEFAULT FALSE,
		source TEXT NOT NULL DEFAULT 'webhook'
	)`,
	`CREATE INDEX IF NOT EXISTS inbox_event_pending ON inbox_event (received_at) WHERE NOT processed`,
	`CREATE TABLE IF NOT EXISTS outbox_write (
		key TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		kind TEXT NOT NULL,
		target TEXT NOT NULL,
		completed BOOLEAN NOT NULL DEFAULT FALSE,
		created_at TIMESTAMPTZ NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS outbox_write_run ON outbox_write (run_id)`,
	`CREATE TABLE IF NOT EXISTS agent_identity (
		runtime TEXT NOT NULL,
		scope TEXT NOT NULL DEFAULT '',
		account TEXT NOT NULL,
		forgejo_user_id BIGINT NOT NULL,
		token_id BIGINT NOT NULL,
		token_fingerprint TEXT NOT NULL,
		ssh_key_id BIGINT NOT NULL DEFAULT 0,
		generation INTEGER NOT NULL DEFAULT 1,
		rotated_at TIMESTAMPTZ NOT NULL,
		PRIMARY KEY (runtime, scope)
	)`,
	`CREATE TABLE IF NOT EXISTS webhook_registration (
		id INTEGER PRIMARY KEY DEFAULT 1,
		hook_id BIGINT NOT NULL,
		url TEXT NOT NULL,
		secret_fingerprint TEXT NOT NULL,
		events TEXT[] NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL,
		CONSTRAINT webhook_registration_singleton CHECK (id = 1)
	)`,
	`CREATE TABLE IF NOT EXISTS reconcile_cursor (
		repo TEXT PRIMARY KEY,
		last_seen TIMESTAMPTZ NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS audit_record (
		id BIGSERIAL PRIMARY KEY,
		at TIMESTAMPTZ NOT NULL,
		subject TEXT NOT NULL,
		source TEXT NOT NULL,
		action TEXT NOT NULL,
		decision TEXT NOT NULL,
		reason TEXT NOT NULL,
		repo TEXT NOT NULL DEFAULT '',
		issue INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS audit_record_at ON audit_record (at DESC)`,
}

// PostgresStore is the authoritative orchestration state.
type PostgresStore struct {
	pool     *pgxpool.Pool
	redactor *Redactor
}

// OpenPostgres connects with the credential the relational_database Resource
// published. The redactor is passed in so a connection error cannot print the
// password back out through the error path.
func OpenPostgres(ctx context.Context, dsn string, redactor *Redactor) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect to the orchestration database: %s", redactor.Error(err))
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("reach the orchestration database: %s", redactor.Error(err))
	}
	return &PostgresStore{pool: pool, redactor: redactor}, nil
}

func (s *PostgresStore) Close() { s.pool.Close() }

func (s *PostgresStore) Migrate(ctx context.Context) error {
	for _, statement := range schema {
		if _, err := s.pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("apply orchestration schema: %s", s.redactor.Error(err))
		}
	}
	return nil
}

// RecordDelivery relies on the primary key rather than a read-then-write. Two
// concurrent deliveries of the same event reach the same row, and exactly one
// of them is told it is new -- which is what makes the once-only side effect a
// property of the database rather than of the request timing.
func (s *PostgresStore) RecordDelivery(ctx context.Context, event InboxEvent) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO inbox_event (delivery_id, event, repo, sender, payload, received_at, source)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (delivery_id) DO NOTHING`,
		event.DeliveryID, event.Event, event.Repo.String(), event.Sender,
		event.Payload, event.ReceivedAt.UTC(), valueOr(event.Source, "webhook"))
	if err != nil {
		return false, fmt.Errorf("record delivery: %s", s.redactor.Error(err))
	}
	return tag.RowsAffected() == 1, nil
}

func (s *PostgresStore) PendingEvents(ctx context.Context, limit int) ([]InboxEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT delivery_id, event, repo, sender, payload, received_at, source
		FROM inbox_event WHERE NOT processed ORDER BY received_at LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("read pending events: %s", s.redactor.Error(err))
	}
	defer rows.Close()
	var events []InboxEvent
	for rows.Next() {
		var event InboxEvent
		var repo string
		if err := rows.Scan(&event.DeliveryID, &event.Event, &repo, &event.Sender,
			&event.Payload, &event.ReceivedAt, &event.Source); err != nil {
			return nil, fmt.Errorf("read pending events: %s", s.redactor.Error(err))
		}
		if event.Repo, err = ParseRepo(repo); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *PostgresStore) MarkProcessed(ctx context.Context, deliveryID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE inbox_event SET processed = TRUE WHERE delivery_id = $1`, deliveryID)
	if err != nil {
		return fmt.Errorf("mark event processed: %s", s.redactor.Error(err))
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) ReserveWrite(ctx context.Context, write OutboxWrite) (bool, error) {
	createdAt := write.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO outbox_write (key, run_id, kind, target, created_at)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT (key) DO NOTHING`,
		write.Key, write.RunID, write.Kind, write.Target, createdAt.UTC())
	if err != nil {
		return false, fmt.Errorf("reserve outbox write: %s", s.redactor.Error(err))
	}
	return tag.RowsAffected() == 1, nil
}

func (s *PostgresStore) CompleteWrite(ctx context.Context, key string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE outbox_write SET completed = TRUE WHERE key = $1`, key)
	if err != nil {
		return fmt.Errorf("complete outbox write: %s", s.redactor.Error(err))
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) WriteByRunID(ctx context.Context, runID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM outbox_write WHERE run_id = $1)`, runID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("look up outbox run: %s", s.redactor.Error(err))
	}
	return exists, nil
}

func (s *PostgresStore) SaveIdentity(ctx context.Context, identity AgentIdentity) error {
	rotatedAt := identity.RotatedAt
	if rotatedAt.IsZero() {
		rotatedAt = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO agent_identity
			(runtime, scope, account, forgejo_user_id, token_id, token_fingerprint, ssh_key_id, generation, rotated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (runtime, scope) DO UPDATE SET
			account = EXCLUDED.account, forgejo_user_id = EXCLUDED.forgejo_user_id,
			token_id = EXCLUDED.token_id, token_fingerprint = EXCLUDED.token_fingerprint,
			ssh_key_id = EXCLUDED.ssh_key_id, generation = EXCLUDED.generation,
			rotated_at = EXCLUDED.rotated_at`,
		identity.Runtime, identity.Scope, identity.Account, identity.ForgejoUserID,
		identity.TokenID, identity.TokenFingerprint, identity.SSHKeyID, identity.Generation, rotatedAt.UTC())
	if err != nil {
		return fmt.Errorf("save agent identity: %s", s.redactor.Error(err))
	}
	return nil
}

func (s *PostgresStore) Identities(ctx context.Context) ([]AgentIdentity, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT runtime, scope, account, forgejo_user_id, token_id, token_fingerprint, ssh_key_id, generation, rotated_at
		FROM agent_identity ORDER BY runtime, scope`)
	if err != nil {
		return nil, fmt.Errorf("read agent identities: %s", s.redactor.Error(err))
	}
	defer rows.Close()
	var identities []AgentIdentity
	for rows.Next() {
		var identity AgentIdentity
		if err := rows.Scan(&identity.Runtime, &identity.Scope, &identity.Account, &identity.ForgejoUserID,
			&identity.TokenID, &identity.TokenFingerprint, &identity.SSHKeyID,
			&identity.Generation, &identity.RotatedAt); err != nil {
			return nil, fmt.Errorf("read agent identities: %s", s.redactor.Error(err))
		}
		identities = append(identities, identity)
	}
	return identities, rows.Err()
}

func (s *PostgresStore) SaveWebhook(ctx context.Context, registration WebhookRegistration) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO webhook_registration (id, hook_id, url, secret_fingerprint, events, updated_at)
		VALUES (1, $1, $2, $3, $4, $5)
		ON CONFLICT (id) DO UPDATE SET
			hook_id = EXCLUDED.hook_id, url = EXCLUDED.url,
			secret_fingerprint = EXCLUDED.secret_fingerprint,
			events = EXCLUDED.events, updated_at = EXCLUDED.updated_at`,
		registration.HookID, registration.URL, registration.SecretFingerprint,
		registration.Events, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("save webhook registration: %s", s.redactor.Error(err))
	}
	return nil
}

func (s *PostgresStore) Webhook(ctx context.Context) (WebhookRegistration, error) {
	var registration WebhookRegistration
	err := s.pool.QueryRow(ctx, `
		SELECT hook_id, url, secret_fingerprint, events, updated_at FROM webhook_registration WHERE id = 1`).
		Scan(&registration.HookID, &registration.URL, &registration.SecretFingerprint,
			&registration.Events, &registration.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return WebhookRegistration{}, ErrNotFound
	}
	if err != nil {
		return WebhookRegistration{}, fmt.Errorf("read webhook registration: %s", s.redactor.Error(err))
	}
	return registration, nil
}

func (s *PostgresStore) Cursor(ctx context.Context, repo Repo) (time.Time, error) {
	var at time.Time
	err := s.pool.QueryRow(ctx, `SELECT last_seen FROM reconcile_cursor WHERE repo = $1`, repo.String()).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("read reconciliation cursor: %s", s.redactor.Error(err))
	}
	return at, nil
}

func (s *PostgresStore) SetCursor(ctx context.Context, repo Repo, at time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO reconcile_cursor (repo, last_seen) VALUES ($1, $2)
		ON CONFLICT (repo) DO UPDATE SET last_seen = EXCLUDED.last_seen`, repo.String(), at.UTC())
	if err != nil {
		return fmt.Errorf("save reconciliation cursor: %s", s.redactor.Error(err))
	}
	return nil
}

// AppendAudit scrubs the free-text reason before it is stored. The reason is
// the one audit field built from upstream strings, so it is the one field that
// could carry a credential into a long-lived table.
func (s *PostgresStore) AppendAudit(ctx context.Context, record AuditRecord) error {
	at := record.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_record (at, subject, source, action, decision, reason, repo, issue)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		at.UTC(), record.Subject, record.Source, record.Action, record.Decision,
		s.redactor.String(record.Reason), record.Repo, record.Issue)
	if err != nil {
		return fmt.Errorf("append audit record: %s", s.redactor.Error(err))
	}
	return nil
}

func (s *PostgresStore) Audit(ctx context.Context, limit int) ([]AuditRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT at, subject, source, action, decision, reason, repo, issue
		FROM audit_record ORDER BY at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("read audit records: %s", s.redactor.Error(err))
	}
	defer rows.Close()
	var records []AuditRecord
	for rows.Next() {
		var record AuditRecord
		if err := rows.Scan(&record.At, &record.Subject, &record.Source, &record.Action,
			&record.Decision, &record.Reason, &record.Repo, &record.Issue); err != nil {
			return nil, fmt.Errorf("read audit records: %s", s.redactor.Error(err))
		}
		records = append(records, record)
	}
	return records, rows.Err()
}
