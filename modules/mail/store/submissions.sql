CREATE TABLE mail.smtp_submission (
  queue_id text PRIMARY KEY CHECK (queue_id ~ '^[A-Za-z0-9]{5,64}$'),
  credential_id uuid NOT NULL REFERENCES mail.protocol_credential(id) ON DELETE RESTRICT,
  mailbox_id uuid NOT NULL REFERENCES mail.mailbox(id) ON DELETE RESTRICT,
  session_id uuid NOT NULL REFERENCES mail.web_session(id) ON DELETE RESTRICT,
  envelope text NOT NULL,
  visible_from text NOT NULL,
  accepted_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX smtp_submission_rate ON mail.smtp_submission(mailbox_id,accepted_at);
