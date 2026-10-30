-- Provider destination IDs are public routing configuration, not mailbox identity.
-- An issued create with no recorded response stays unknown across restarts.
CREATE TABLE mail.forward_source (
 source_id uuid PRIMARY KEY REFERENCES mail.ingress_route_state(source_id) ON DELETE RESTRICT,
 generation uuid NOT NULL,
 account_id text NOT NULL CHECK(account_id ~ '^[a-f0-9]{32}$'),
 zone_id text NOT NULL CHECK(zone_id ~ '^[a-f0-9]{32}$'),
 delivery_domain text NOT NULL
);
CREATE TABLE mail.forward_destination (
 binding_id text PRIMARY KEY REFERENCES mail.ingress_binding(id) ON DELETE RESTRICT,
 source_id uuid NOT NULL REFERENCES mail.forward_source(source_id) ON DELETE RESTRICT,
 target text NOT NULL UNIQUE,
 state text NOT NULL DEFAULT 'prepared' CHECK(state IN ('prepared','issued','created')),
 destination_id text CHECK(destination_id ~ '^[a-f0-9]{32}$'),
 issued_at timestamptz,
 created_at timestamptz,
 CHECK((state='prepared')=(issued_at IS NULL)),
 CHECK((state='created')=(destination_id IS NOT NULL)),
 CHECK((state='created')=(created_at IS NOT NULL))
);
CREATE UNIQUE INDEX forward_destination_provider_id ON mail.forward_destination(destination_id) WHERE destination_id IS NOT NULL;
