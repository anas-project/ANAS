-- One public forwarding rule is tied to one immutable destination claim.
CREATE TABLE mail.forward_rule (
 binding_id text PRIMARY KEY REFERENCES mail.forward_destination(binding_id) ON DELETE RESTRICT,
 source_id uuid NOT NULL REFERENCES mail.forward_source(source_id) ON DELETE RESTRICT,
 address text NOT NULL,
 state text NOT NULL DEFAULT 'prepared' CHECK(state IN ('prepared','create_issued','create_responded','created','delete_issued','removed','cancelled')),
 rule_id text CHECK(rule_id ~ '^[a-f0-9]{32}$'),
 fingerprint text CHECK(fingerprint ~ '^[a-f0-9]{64}$'),
 issued_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK((rule_id IS NULL)=(fingerprint IS NULL)),
 CHECK((state IN ('prepared','create_issued','cancelled'))=(rule_id IS NULL)),
 CHECK((state IN ('prepared','cancelled'))=(issued_at IS NULL))
);
CREATE UNIQUE INDEX forward_rule_active_address ON mail.forward_rule(source_id,address) WHERE state NOT IN ('removed','cancelled');
CREATE UNIQUE INDEX forward_rule_provider_id ON mail.forward_rule(rule_id) WHERE rule_id IS NOT NULL;
