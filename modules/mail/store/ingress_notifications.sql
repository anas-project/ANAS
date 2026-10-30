-- Notifications are authenticated hints, not mailbox access or delivery grants.
-- Unknown local mailbox UUIDs may wait for the independent route projection.
CREATE TABLE mail.ingress_notice (
  source_id uuid NOT NULL,
  generation uuid NOT NULL,
  notification_id uuid NOT NULL,
  message_id text NOT NULL CHECK (message_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'),
  mailbox_id uuid NOT NULL,
  body_sha256 text NOT NULL CHECK (body_sha256 ~ '^[a-f0-9]{64}$'),
  state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','accepted','expired')),
  attempts integer NOT NULL DEFAULT 0,
  next_attempt timestamptz NOT NULL DEFAULT clock_timestamp(),
  lease_token uuid,
  lease_until timestamptz,
  last_error text NOT NULL DEFAULT '',
  received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  completed_at timestamptz,
  PRIMARY KEY(source_id,generation,notification_id),
  CHECK ((state='pending')=(completed_at IS NULL))
);
CREATE INDEX ingress_notice_pending ON mail.ingress_notice(source_id,next_attempt) WHERE state='pending';
