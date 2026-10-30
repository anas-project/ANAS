CREATE TABLE mail.address_audit (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  actor_anchor text NOT NULL,
  owner_anchor text NOT NULL,
  operation_id text NOT NULL,
  request jsonb NOT NULL,
  outcome text NOT NULL DEFAULT 'unknown',
  started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  finished_at timestamptz
);
