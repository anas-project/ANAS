ALTER TABLE mail.ingress_route_state
  ADD COLUMN reserved_capacity_bytes bigint NOT NULL DEFAULT 0
    CHECK (reserved_capacity_bytes BETWEEN 0 AND 9007199254740991),
  ADD COLUMN source_identity_fingerprint text
    CHECK (source_identity_fingerprint ~ '^[a-f0-9]{64}$');
