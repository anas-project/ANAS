CREATE TABLE mail.revocation_task (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  session_id uuid NOT NULL REFERENCES mail.web_session(id) ON DELETE RESTRICT,
  mailbox_id uuid REFERENCES mail.mailbox(id) ON DELETE RESTRICT,
  revision bigint NOT NULL DEFAULT 1,
  attempts integer NOT NULL DEFAULT 0,
  next_attempt timestamptz NOT NULL DEFAULT clock_timestamp(),
  lease_until timestamptz,
  lease_token uuid,
  completed_at timestamptz,
  last_error text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE NULLS NOT DISTINCT (session_id,mailbox_id)
);
CREATE INDEX revocation_task_pending ON mail.revocation_task(next_attempt,id) WHERE completed_at IS NULL;

CREATE FUNCTION mail.enqueue_session_revocation() RETURNS trigger LANGUAGE plpgsql
SET search_path=pg_catalog,mail AS $$
BEGIN
  IF NEW.revoked_at IS NOT NULL AND OLD.revoked_at IS NULL THEN
    INSERT INTO mail.revocation_task(session_id) VALUES(NEW.id)
    ON CONFLICT(session_id,mailbox_id) DO UPDATE SET revision=mail.revocation_task.revision+1,
      completed_at=NULL,next_attempt=clock_timestamp(),lease_until=NULL,lease_token=NULL,last_error='';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER session_revocation AFTER UPDATE OF revoked_at ON mail.web_session
  FOR EACH ROW EXECUTE FUNCTION mail.enqueue_session_revocation();

CREATE FUNCTION mail.enqueue_mailbox_revocation() RETURNS trigger LANGUAGE plpgsql
SET search_path=pg_catalog,mail AS $$
BEGIN
  IF NEW.authorization_epoch<>OLD.authorization_epoch THEN
    INSERT INTO mail.revocation_task(session_id,mailbox_id)
      SELECT DISTINCT session_id,mailbox_id FROM mail.protocol_credential WHERE mailbox_id=NEW.id
    ON CONFLICT(session_id,mailbox_id) DO UPDATE SET revision=mail.revocation_task.revision+1,
      completed_at=NULL,next_attempt=clock_timestamp(),lease_until=NULL,lease_token=NULL,last_error='';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER mailbox_revocation AFTER UPDATE OF authorization_epoch ON mail.mailbox
  FOR EACH ROW EXECUTE FUNCTION mail.enqueue_mailbox_revocation();

-- Pending withdrawal survives upgrading an earlier running service.
INSERT INTO mail.revocation_task(session_id)
  SELECT id FROM mail.web_session WHERE revoked_at IS NOT NULL OR expires_at<=clock_timestamp()
  ON CONFLICT DO NOTHING;
