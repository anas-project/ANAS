// Package store owns mail resource identities and local address reservations.
// Session storage consumes verified IAM/directory decisions; it does not perform
// authentication or write directory/mail-server configuration.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/jackc/pgx/v5/pgconn"
)

//go:embed schema.sql
var schema string

//go:embed sessions.sql
var sessionSchema string

//go:embed login_flows.sql
var loginFlowSchema string

//go:embed iam_logout.sql
var iamLogoutSchema string

//go:embed webmail_handoffs.sql
var webmailHandoffSchema string

//go:embed mailbox_projection.sql
var mailboxProjectionSchema string

//go:embed protocol_credentials.sql
var protocolCredentialSchema string

//go:embed submissions.sql
var submissionSchema string

//go:embed revocation_tasks.sql
var revocationTaskSchema string

//go:embed submission_identity.sql
var submissionIdentitySchema string

//go:embed logout_flows.sql
var logoutFlowSchema string

//go:embed directory_cursor.sql
var directoryCursorSchema string

//go:embed company.sql
var companySchema string

//go:embed admission_groups.sql
var admissionGroupSchema string

//go:embed runtime_start.sql
var runtimeStartSchema string

//go:embed setup.sql
var setupSchema string

//go:embed ingress.sql
var ingressSchema string

//go:embed ingress_notifications.sql
var ingressNotificationSchema string

//go:embed ingress_routes.sql
var ingressRouteSchema string

//go:embed cloudflare_rules.sql
var cloudflareRuleSchema string

//go:embed ingress_reservations.sql
var ingressReservationSchema string

//go:embed address_transfer.sql
var addressTransferSchema string

//go:embed forward_destinations.sql
var forwardDestinationSchema string

//go:embed forward_rules.sql
var forwardRuleSchema string

//go:embed submission_request.sql
var submissionRequestSchema string

//go:embed address_audit.sql
var addressAuditSchema string

var migrations = []string{schema, sessionSchema, loginFlowSchema, iamLogoutSchema, webmailHandoffSchema, mailboxProjectionSchema, protocolCredentialSchema, submissionSchema, revocationTaskSchema, submissionIdentitySchema, logoutFlowSchema, directoryCursorSchema, companySchema, admissionGroupSchema, runtimeStartSchema, setupSchema, ingressSchema, ingressNotificationSchema, ingressRouteSchema, cloudflareRuleSchema, ingressReservationSchema, addressTransferSchema, forwardDestinationSchema, forwardRuleSchema, submissionRequestSchema, addressAuditSchema}

var (
	ErrAddressUnavailable    = errors.New("mail address is unavailable")
	ErrPrimaryAddressChanged = errors.New("primary address change requires a future managed operation")
	ErrSchemaMismatch        = errors.New("mail schema does not match this application version")
	anchorPattern            = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
)

type Store struct{ DB *sql.DB }
type Mailbox struct {
	ID             string `json:"id"`
	OwnerAnchor    string `json:"owner_anchor"`
	ProtocolLogin  string `json:"protocol_login"`
	StorageRef     string `json:"storage_ref"`
	RoundcubeKey   string `json:"roundcube_key"`
	State          string `json:"state"`
	DefaultAddress string `json:"default_address"`
}

func (s Store) Migrate(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The lock precedes all DDL so concurrent fresh deployments cannot race.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('anas-mail-schema',0))`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS mail; CREATE TABLE IF NOT EXISTS mail.schema_migration(version integer PRIMARY KEY, checksum text NOT NULL)`); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT version,checksum FROM mail.schema_migration ORDER BY version`)
	if err != nil {
		return err
	}
	var applied int
	for rows.Next() {
		var version int
		var checksum string
		if err = rows.Scan(&version, &checksum); err != nil {
			rows.Close()
			return err
		}
		if version != applied+1 || version > len(migrations) || checksum != migrationChecksum(migrations[applied]) {
			rows.Close()
			return ErrSchemaMismatch
		}
		applied++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for i := applied; i < len(migrations); i++ {
		if _, err = tx.ExecContext(ctx, migrations[i]); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO mail.schema_migration(version,checksum) VALUES($1,$2)`, i+1, migrationChecksum(migrations[i])); err != nil {
			return err
		}
	}
	// ANAS stops every writer, including Go and PostgreSQL, before replacing
	// the recovery set. PostgreSQL's live start time is outside the restored
	// database contents. A Go-only restart keeps it unchanged. Run this under
	// the migration lock and before any listener or credential worker starts.
	result, err := tx.ExecContext(ctx, `INSERT INTO mail.runtime_start(singleton,postgres_started_at)
 VALUES(true,pg_postmaster_start_time()) ON CONFLICT(singleton) DO UPDATE
 SET postgres_started_at=EXCLUDED.postgres_started_at
 WHERE mail.runtime_start.postgres_started_at IS DISTINCT FROM EXCLUDED.postgres_started_at`)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 0 {
		// Existing triggers durably enqueue the actual DMS withdrawals. All
		// handoffs, protocol access and refresh grants already check this row.
		if _, err = tx.ExecContext(ctx, `UPDATE mail.web_session SET revoked_at=clock_timestamp() WHERE revoked_at IS NULL;
 DELETE FROM mail.webmail_handoff; DELETE FROM mail.login_flow;`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func migrationChecksum(sql string) string {
	sum := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(sum[:])
}

func token(prefix string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}
func mailboxID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
func primaryAddress(value string) (string, error) {
	parts := strings.Split(value, "@")
	if len(parts) != 2 {
		return "", errors.New("invalid directory primary address")
	}
	return directorymail.Address(parts[0], parts[1])
}

// EnsurePrimary consumes an already verified directory identity and its derived
// mail value. New resources stay pending; this function grants no mail access.
func (s Store) EnsurePrimary(ctx context.Context, anchor, address string) (Mailbox, error) {
	var m Mailbox
	if !anchorPattern.MatchString(anchor) {
		return m, errors.New("canonical directory anchor required")
	}
	address, err := primaryAddress(address)
	if err != nil {
		return m, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return m, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "anas-mail-owner:"+anchor); err != nil {
		return m, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO mail.person(anchor) VALUES($1) ON CONFLICT DO NOTHING`, anchor); err != nil {
		return m, err
	}
	err = tx.QueryRowContext(ctx, `SELECT m.id,m.owner_anchor,m.protocol_login,m.storage_ref,m.roundcube_key,m.state,COALESCE(a.address,'') FROM mail.mailbox m LEFT JOIN mail.address a ON a.mailbox_id=m.id AND a.is_default WHERE m.owner_anchor=$1 AND m.kind='primary'`, anchor).Scan(&m.ID, &m.OwnerAnchor, &m.ProtocolLogin, &m.StorageRef, &m.RoundcubeKey, &m.State, &m.DefaultAddress)
	if err == nil {
		if m.DefaultAddress != address {
			return Mailbox{}, ErrPrimaryAddressChanged
		}
		return m, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return m, err
	}
	m.OwnerAnchor = anchor
	m.DefaultAddress = address
	m.State = "pending"
	if m.ID, err = mailboxID(); err != nil {
		return m, err
	}
	if m.ProtocolLogin, err = token("mbx_"); err != nil {
		return m, err
	}
	if m.StorageRef, err = token("store_"); err != nil {
		return m, err
	}
	if m.RoundcubeKey, err = token("webmail_"); err != nil {
		return m, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO mail.mailbox(id,owner_anchor,kind,protocol_login,storage_ref,roundcube_key) VALUES($1,$2,'primary',$3,$4,$5)`, m.ID, anchor, m.ProtocolLogin, m.StorageRef, m.RoundcubeKey)
	if err != nil {
		return Mailbox{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO mail.address(address,mailbox_id,kind,is_default) VALUES($1,$2,'primary',true)`, address, m.ID)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return Mailbox{}, ErrAddressUnavailable
		}
		return Mailbox{}, err
	}
	if err = tx.Commit(); err != nil {
		return Mailbox{}, err
	}
	return m, nil
}
