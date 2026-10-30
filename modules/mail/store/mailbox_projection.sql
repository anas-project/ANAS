-- AD owns mailbox IDs and address membership. Local protocol/storage/webmail
-- identities remain independent and never change with a directory address.
CREATE TABLE mail.mailbox_projection (
  mailbox_id uuid PRIMARY KEY REFERENCES mail.mailbox(id) ON DELETE RESTRICT,
  revision uuid NOT NULL,
  fingerprint text NOT NULL CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
  directory_version uuid NOT NULL,
  authorization_epoch bigint NOT NULL CHECK (authorization_epoch>0),
  snapshot jsonb NOT NULL,
  applied_revision uuid,
  last_error text NOT NULL DEFAULT '',
  observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  applied_at timestamptz
);
CREATE INDEX mailbox_projection_pending ON mail.mailbox_projection(observed_at)
  WHERE applied_revision IS DISTINCT FROM revision;
