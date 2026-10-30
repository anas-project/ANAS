CREATE TABLE mail.setup_draft (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  revision bigint NOT NULL CHECK (revision > 0),
  draft jsonb NOT NULL CHECK (jsonb_typeof(draft)='object'),
  editor_anchor text NOT NULL REFERENCES mail.person(anchor),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
