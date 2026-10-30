-- Postfix queue IDs can be reused. Scope retry identity to the short-lived
-- SMTP credential; a later credential must consume its own rate allowance.
ALTER TABLE mail.smtp_submission DROP CONSTRAINT smtp_submission_pkey;
ALTER TABLE mail.smtp_submission ADD PRIMARY KEY (credential_id,queue_id);
ALTER TABLE mail.smtp_submission ADD CONSTRAINT smtp_submission_queue_present
  CHECK (lower(queue_id) NOT IN ('noqueue','undef','unknown'));
