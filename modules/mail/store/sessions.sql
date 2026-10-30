CREATE TABLE mail.web_session (
  id uuid PRIMARY KEY,
  token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash)=32),
  owner_anchor text NOT NULL REFERENCES mail.person(anchor) ON DELETE RESTRICT,
  iam_issuer text NOT NULL,
  iam_sid text NOT NULL,
  authenticated_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  CHECK (expires_at>authenticated_at AND expires_at>created_at)
);
CREATE INDEX web_session_owner ON mail.web_session(owner_anchor) WHERE revoked_at IS NULL;
CREATE INDEX web_session_iam ON mail.web_session(iam_issuer,iam_sid) WHERE revoked_at IS NULL;
