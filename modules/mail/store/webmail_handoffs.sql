-- A local authorization generation prevents old handoffs from becoming usable
-- when a withheld mailbox projection is later confirmed active again.
ALTER TABLE mail.mailbox ADD COLUMN authorization_epoch bigint NOT NULL DEFAULT 1 CHECK (authorization_epoch>0);

CREATE TABLE mail.webmail_handoff (
  code_hash bytea PRIMARY KEY CHECK (octet_length(code_hash)=32),
  browser_hash bytea NOT NULL CHECK (octet_length(browser_hash)=32),
  session_id uuid NOT NULL REFERENCES mail.web_session(id) ON DELETE RESTRICT,
  mailbox_id uuid NOT NULL REFERENCES mail.mailbox(id) ON DELETE RESTRICT,
  authorization_epoch bigint NOT NULL CHECK (authorization_epoch>0),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  expires_at timestamptz NOT NULL CHECK (expires_at>created_at)
);
CREATE INDEX webmail_handoff_expiry ON mail.webmail_handoff(expires_at);
