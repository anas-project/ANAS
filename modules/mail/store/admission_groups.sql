CREATE TABLE mail.admission_group (
  role text PRIMARY KEY CHECK (role IN ('application','all','administrator')),
  anchor text NOT NULL UNIQUE CHECK (anchor ~ '^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$'),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
