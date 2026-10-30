-- The live PostgreSQL process start is not restored by an offline database
-- recovery. It fences pre-recovery sessions at Go startup without another
-- service, secret or a second copy of mailbox ownership.
CREATE TABLE mail.runtime_start (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  postgres_started_at timestamptz NOT NULL
);
