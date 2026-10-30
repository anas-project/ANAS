ALTER TABLE mail.login_flow ADD COLUMN purpose text NOT NULL DEFAULT 'login'
  CHECK (purpose IN ('login','logout'));
