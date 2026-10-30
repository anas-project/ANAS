CREATE TABLE mail.login_flow (
  state_hash bytea PRIMARY KEY CHECK (octet_length(state_hash)=32),
  browser_hash bytea NOT NULL CHECK (octet_length(browser_hash)=32),
  nonce text NOT NULL CHECK (nonce ~ '^[A-Za-z0-9_-]{43}$'),
  verifier text NOT NULL CHECK (verifier ~ '^[A-Za-z0-9_-]{43}$'),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  expires_at timestamptz NOT NULL CHECK (expires_at>created_at)
);
CREATE INDEX login_flow_expiry ON mail.login_flow(expires_at);
