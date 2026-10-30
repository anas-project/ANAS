CREATE TABLE mail.directory_cursor (
  consumer text PRIMARY KEY CHECK (consumer='samba-mail-v1'),
  sequence bigint NOT NULL CHECK (sequence>=0),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
