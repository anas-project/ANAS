CREATE TABLE mail.company_directory (
  source text PRIMARY KEY CHECK (source='company'),
  fingerprint text NOT NULL CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
  snapshot jsonb NOT NULL,
  refreshed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
