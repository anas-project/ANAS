-- Email Routing rule IDs are provider configuration, never mailbox identity.
CREATE TABLE mail.cloudflare_rule (
 source_id uuid NOT NULL REFERENCES mail.ingress_route_state(source_id) ON DELETE RESTRICT,
 generation uuid NOT NULL,
 address text NOT NULL,
 worker text NOT NULL,
 rule_id text CHECK(rule_id ~ '^[a-f0-9]{32}$'),
 fingerprint text CHECK(fingerprint ~ '^[a-f0-9]{64}$'),
 observed_at timestamptz,
 PRIMARY KEY(source_id,address),
 CHECK((rule_id IS NULL)=(fingerprint IS NULL))
);
CREATE TABLE mail.cloudflare_rule_operation (
 id uuid PRIMARY KEY,
 source_id uuid NOT NULL,
 address text NOT NULL,
 route_revision bigint NOT NULL CHECK(route_revision BETWEEN 1 AND 9007199254740991),
 action text NOT NULL CHECK(action IN ('create','delete')),
 expected_id text CHECK(expected_id ~ '^[a-f0-9]{32}$'),
 expected_fingerprint text CHECK(expected_fingerprint ~ '^[a-f0-9]{64}$'),
 result_id text CHECK(result_id ~ '^[a-f0-9]{32}$'),
 result_fingerprint text CHECK(result_fingerprint ~ '^[a-f0-9]{64}$'),
 state text NOT NULL DEFAULT 'prepared' CHECK(state IN ('prepared','issued','superseded','cancelled','confirmed')),
 lease_token uuid,
 lease_until timestamptz,
 attempts integer NOT NULL DEFAULT 0,
 next_attempt timestamptz NOT NULL DEFAULT clock_timestamp(),
 last_error text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 issued_at timestamptz,
 confirmed_at timestamptz,
 FOREIGN KEY(source_id,address) REFERENCES mail.cloudflare_rule(source_id,address) ON DELETE RESTRICT,
 CHECK((expected_id IS NULL)=(expected_fingerprint IS NULL)),
 CHECK((result_id IS NULL)=(result_fingerprint IS NULL)),
 CHECK((action='create')=(expected_id IS NULL)),
 CHECK(state NOT IN ('issued','superseded','confirmed') OR issued_at IS NOT NULL)
);
CREATE UNIQUE INDEX cloudflare_rule_operation_pending ON mail.cloudflare_rule_operation(source_id,address)
 WHERE state IN ('prepared','issued');
CREATE INDEX cloudflare_rule_operation_work ON mail.cloudflare_rule_operation(source_id,next_attempt,created_at)
 WHERE state IN ('prepared','issued','superseded');
