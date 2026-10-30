CREATE TABLE mail.protocol_credential (
  id uuid PRIMARY KEY,
  family_id uuid NOT NULL,
  session_id uuid NOT NULL REFERENCES mail.web_session(id) ON DELETE RESTRICT,
  mailbox_id uuid NOT NULL REFERENCES mail.mailbox(id) ON DELETE RESTRICT,
  authorization_epoch bigint NOT NULL CHECK (authorization_epoch>0),
  access_hash bytea NOT NULL UNIQUE CHECK (octet_length(access_hash)=32),
  smtp_hash bytea NOT NULL UNIQUE CHECK (octet_length(smtp_hash)=32),
  refresh_hash bytea NOT NULL UNIQUE CHECK (octet_length(refresh_hash)=32),
  smtp_login text NOT NULL UNIQUE CHECK (smtp_login ~ '^sub_[a-f0-9]{32}$'),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  expires_at timestamptz NOT NULL,
  refresh_expires_at timestamptz NOT NULL,
  rotated_at timestamptz,
  revoked_at timestamptz,
  CHECK (expires_at>created_at AND refresh_expires_at>=expires_at)
);
CREATE INDEX protocol_credential_session ON mail.protocol_credential(session_id)
  WHERE revoked_at IS NULL AND rotated_at IS NULL;
CREATE INDEX protocol_credential_box ON mail.protocol_credential(mailbox_id)
  WHERE revoked_at IS NULL AND rotated_at IS NULL;
