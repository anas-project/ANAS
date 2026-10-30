CREATE TABLE mail.ingress_route_state (
  source_id uuid PRIMARY KEY,
  generation uuid NOT NULL,
  revision bigint NOT NULL DEFAULT 0 CHECK (revision BETWEEN 0 AND 9007199254740991),
  fingerprint text NOT NULL CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
  sequence bigint NOT NULL DEFAULT 0 CHECK (sequence BETWEEN 0 AND 9007199254740991),
  observed_at timestamptz
);
CREATE TABLE mail.ingress_route_operation (
  id uuid PRIMARY KEY,
  source_id uuid NOT NULL REFERENCES mail.ingress_route_state(source_id) ON DELETE RESTRICT,
  generation uuid NOT NULL,
  expected_revision bigint NOT NULL CHECK (expected_revision BETWEEN 0 AND 9007199254740990),
  fingerprint text NOT NULL CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
  routes jsonb NOT NULL,
  state text NOT NULL DEFAULT 'prepared' CHECK (state IN ('prepared','issued','superseded','confirmed','cancelled')),
  last_error text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  confirmed_at timestamptz
);
CREATE UNIQUE INDEX ingress_route_operation_pending ON mail.ingress_route_operation(source_id)
  WHERE state IN ('prepared','issued');
