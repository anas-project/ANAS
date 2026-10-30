-- Old authorization records have no trustworthy scan identifier. Preserve
-- them for rate accounting without inventing an identity for those records.
ALTER TABLE mail.smtp_submission DROP CONSTRAINT smtp_submission_pkey;
ALTER TABLE mail.smtp_submission ADD COLUMN request_id text
  CHECK (request_id IS NULL OR request_id ~ '^[a-f0-9]{32}$');
CREATE UNIQUE INDEX smtp_submission_request_id ON mail.smtp_submission(request_id)
  WHERE request_id IS NOT NULL;
