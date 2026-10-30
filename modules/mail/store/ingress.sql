-- A route binding is an immutable, confirmed cloud acceptance permission.
-- Closing it preserves its sequence boundary for mail accepted before removal.
CREATE TABLE mail.ingress_binding (
  id text PRIMARY KEY CHECK (id ~ '^[a-f0-9]{64}$'),
  source_id uuid NOT NULL,
  generation uuid NOT NULL,
  address text NOT NULL,
  claim_version uuid NOT NULL,
  mailbox_id uuid NOT NULL REFERENCES mail.mailbox(id) ON DELETE RESTRICT,
  owner_anchor text NOT NULL REFERENCES mail.person(anchor) ON DELETE RESTRICT,
  first_sequence bigint NOT NULL CHECK (first_sequence BETWEEN 1 AND 9007199254740991),
  closed_sequence bigint CHECK (closed_sequence BETWEEN 0 AND 9007199254740991),
  confirmed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  CHECK (closed_sequence IS NULL OR closed_sequence>=first_sequence-1),
  UNIQUE(source_id,generation,address,claim_version,mailbox_id,owner_anchor)
);
CREATE UNIQUE INDEX ingress_binding_current_address
  ON mail.ingress_binding(source_id,generation,address) WHERE closed_sequence IS NULL;

CREATE TABLE mail.ingress_message (
  id text PRIMARY KEY CHECK (id ~ '^[a-f0-9]{64}$'),
  binding_id text NOT NULL REFERENCES mail.ingress_binding(id) ON DELETE RESTRICT,
  source_id uuid NOT NULL,
  generation uuid NOT NULL,
  message_id text NOT NULL CHECK (message_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'),
  mailbox_id uuid NOT NULL REFERENCES mail.mailbox(id) ON DELETE RESTRICT,
  observation jsonb NOT NULL,
  expected_sha256 text CHECK (expected_sha256 ~ '^[a-f0-9]{64}$'),
  sha256 text CHECK (sha256 ~ '^[a-f0-9]{64}$'),
  phase text NOT NULL DEFAULT 'download' CHECK (phase IN ('download','native','ack','cleanup','complete')),
  native_state text NOT NULL DEFAULT '' CHECK (native_state IN ('','prepared','submitting','queued','unknown','retryable','rejected','stored','processed')),
  attempts integer NOT NULL DEFAULT 0,
  next_attempt timestamptz NOT NULL DEFAULT clock_timestamp(),
  lease_until timestamptz,
  lease_token uuid,
  last_error text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  acknowledged_at timestamptz,
  completed_at timestamptz,
  UNIQUE(source_id,message_id,mailbox_id),
  CHECK ((phase='download' AND sha256 IS NULL) OR (phase<>'download' AND sha256 IS NOT NULL)),
  CHECK (phase NOT IN ('ack','cleanup','complete') OR native_state IN ('stored','processed')),
  CHECK ((phase='complete')=(completed_at IS NOT NULL)),
  CHECK ((phase IN ('cleanup','complete'))=(acknowledged_at IS NOT NULL))
);
CREATE INDEX ingress_message_pending ON mail.ingress_message(source_id,next_attempt,id) WHERE phase<>'complete';

CREATE TABLE mail.ingress_poll (
  source_id uuid PRIMARY KEY,
  generation uuid NOT NULL,
  cursor text NOT NULL DEFAULT '' CHECK (octet_length(cursor)<=2048),
  route_revision bigint NOT NULL DEFAULT 0 CHECK (route_revision BETWEEN 0 AND 9007199254740991),
  lease_token uuid,
  lease_until timestamptz,
  last_success timestamptz,
  last_error text NOT NULL DEFAULT '',
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
