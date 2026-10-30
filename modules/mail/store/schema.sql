CREATE SCHEMA IF NOT EXISTS mail;
CREATE TABLE mail.person (
  anchor text PRIMARY KEY CHECK (anchor ~ '^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$'),
  directory_state text NOT NULL DEFAULT 'unknown' CHECK (directory_state IN ('unknown','enabled','disabled','deleted')),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE mail.directory_group (
  anchor text PRIMARY KEY CHECK (anchor ~ '^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$'),
  directory_state text NOT NULL DEFAULT 'unknown' CHECK (directory_state IN ('unknown','enabled','deleted'))
);
CREATE TABLE mail.mailbox (
  id uuid PRIMARY KEY,
  owner_anchor text NOT NULL REFERENCES mail.person(anchor) ON DELETE RESTRICT,
  kind text NOT NULL CHECK (kind IN ('primary','additional')),
  protocol_login text NOT NULL UNIQUE CHECK (protocol_login ~ '^mbx_[a-f0-9]{32}$'),
  storage_ref text NOT NULL UNIQUE CHECK (storage_ref ~ '^store_[a-f0-9]{32}$'),
  roundcube_key text NOT NULL UNIQUE CHECK (roundcube_key ~ '^webmail_[a-f0-9]{32}$'),
  state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','active','disabled','retained')),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE UNIQUE INDEX mailbox_one_primary ON mail.mailbox(owner_anchor) WHERE kind='primary';
CREATE TABLE mail.address (
  address text PRIMARY KEY CHECK (address=lower(address) AND length(address) BETWEEN 3 AND 254 AND position('@' IN address)>1),
  mailbox_id uuid REFERENCES mail.mailbox(id) ON DELETE RESTRICT,
  kind text NOT NULL CHECK (kind IN ('primary','alias','independent','reserved')),
  is_default boolean NOT NULL DEFAULT false,
  state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','active','retained')),
  CHECK ((kind='reserved' AND mailbox_id IS NULL AND NOT is_default) OR (kind<>'reserved' AND mailbox_id IS NOT NULL)),
  CHECK (is_default = (kind IN ('primary','independent'))),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE UNIQUE INDEX address_one_default ON mail.address(mailbox_id) WHERE is_default;
