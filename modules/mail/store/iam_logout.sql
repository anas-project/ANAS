-- An IAM logout can arrive before its login callback. Keep the verified
-- revocation intent so that a delayed callback cannot recreate that session.
CREATE TABLE mail.iam_logout (
  issuer text NOT NULL,
  jti text NOT NULL,
  subject text NOT NULL,
  sid text NOT NULL,
  issued_at timestamptz NOT NULL,
  received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (issuer,jti),
  CHECK (subject<>'' OR sid<>'')
);
CREATE INDEX iam_logout_binding ON mail.iam_logout(issuer,sid,subject);
