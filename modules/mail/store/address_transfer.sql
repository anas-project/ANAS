CREATE TABLE mail.address_transfer_proof (
 id uuid PRIMARY KEY,
 operation_id uuid NOT NULL UNIQUE,
 address text NOT NULL,
 from_owner text NOT NULL REFERENCES mail.person(anchor) ON DELETE RESTRICT,
 from_mailbox uuid NOT NULL REFERENCES mail.mailbox(id) ON DELETE RESTRICT,
 from_claim_version uuid NOT NULL,
 to_owner text NOT NULL,
 to_mailbox uuid NOT NULL,
 record jsonb NOT NULL,
 authorization_epoch bigint NOT NULL CHECK(authorization_epoch>0),
 projection_fingerprint text NOT NULL CHECK(projection_fingerprint ~ '^[a-f0-9]{64}$'),
 source_versions jsonb NOT NULL,
 applied_to uuid REFERENCES mail.mailbox(id) ON DELETE RESTRICT,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 consumed_at timestamptz,
 UNIQUE(address,from_claim_version),
 UNIQUE(id,applied_to),
 CHECK(from_owner<>to_owner AND from_mailbox<>to_mailbox),
 CHECK((applied_to IS NULL)=(consumed_at IS NULL)),
 CHECK(applied_to IS NULL OR applied_to=to_mailbox)
);
ALTER TABLE mail.address ADD COLUMN transfer_proof_id uuid;
ALTER TABLE mail.address ADD CONSTRAINT address_transfer_target
 FOREIGN KEY(transfer_proof_id,mailbox_id)
 REFERENCES mail.address_transfer_proof(id,applied_to) ON DELETE RESTRICT;
