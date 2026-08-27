# Casdoor IAM Operations Runbook

This runbook covers the ANAS-managed Casdoor provider. Examples use `/srv/anas`.
Before a write, confirm that `anas status -w /srv/anas` names the intended
deployment and keep a working `admin_casdoor` credential available throughout
the maintenance window.

## Routine checks and IAM recovery

Check ANAS state and the health of Casdoor, PostgreSQL, Samba DC, and the
directory subscriber before checking OIDC discovery or SAML metadata. Samba AD
is authoritative for business users and groups; do not repair their profile,
group, disabled state, or password directly in Casdoor.

If federated login is unavailable, use the local recovery surface exported as
`CASDOOR_LOCAL_RECOVERY_URL` (normally `/login` on the Casdoor HTTPS origin) and
the `admin_casdoor` account. Read its password only in a controlled terminal:

```bash
anas admin local credential casdoor break_glass -w /srv/anas
anas admin local rotate casdoor break_glass -w /srv/anas
```

After a successful rotation, verify that the new password works and the old
one is rejected. A failed command restores the old bcrypt value and leaves the
Secret Store unchanged. Verify the old password before investigating the Hook,
container, and PostgreSQL; do not retry rotations blindly.

## Signing-key and client-secret rotation

Inspect the inventory and run a read-only plan first:

```bash
anas credential list -w /srv/anas
anas credential rotate casdoor.signing_key -w /srv/anas --dry-run --json
anas credential rotate casdoor.portal_client_secret -w /srv/anas --dry-run --json
```

When the plan contains only the expected modules, execute it in a maintenance
window:

```bash
anas credential rotate casdoor.signing_key -w /srv/anas -y --json
anas credential rotate casdoor.portal_client_secret -w /srv/anas -y --json
```

- `casdoor.signing_key` creates an immutable candidate with a new RSA/X.509
  keypair. Certificate rows use fingerprinted names. The previous certificate
  remains in JWKS for a one-hour trust overlap: new tokens use the new `kid`,
  while unexpired old tokens remain verifiable during that interval. Private
  keys never enter the deployment manifest.
- `casdoor.portal_client_secret` atomically updates the built-in Portal
  Application. The Secret Store is committed only after the new value passes
  verification; the old value has no overlap and must fail immediately.
- On failure, ANAS stops the candidate, restores the previous deployment and
  application-side value, and retains the old Secret Store generation. If
  `anas credential list` reports `recovery_required`, preserve the transaction
  journal and deployment artifacts and diagnose recovery before another write.

After rotation, verify a real OIDC login, JWKS and SAML assertion signatures,
Portal login, and a directory user's permanent anchor. Health or discovery
alone is not acceptance evidence.

## Consistent backup and empty-workspace restore

A recovery point must include PostgreSQL, signing material, consumer secrets,
the `${DATA_PATH}/casdoor/dirwatch` cursor, `.anas/secrets.yml`,
`.anas/local-admins.yml`, the active deployment, and its metadata. A database-
only backup is incomplete.

```bash
anas backup create -w /srv/anas --to /backup/anas --mode snapshot -y --json
anas backup verify --to /backup/anas --backup-id BACKUP_ID --json
anas init /srv/anas-restore -y
anas backup restore --from /backup/anas -w /srv/anas-restore \
  --backup-id BACKUP_ID --dry-run --json
anas backup restore --from /backup/anas -w /srv/anas-restore \
  --backup-id BACKUP_ID -y --json
```

Two workspaces with the same container prefix must not run concurrently. Stop
the source before starting the restored target, and reverse that order when
switching back. Confirm the unchanged issuer, OIDC/SAML clients and signatures,
cursor continuity, `admin_casdoor` login, and a real login that retains the
pre-backup Samba anchor and Casdoor `sub`.

## Fixed-version upgrade, restart, and artifact rollback

Create and verify a consistent backup before using `anas lock`, `anas plan`,
`anas apply`, and `anas status` for a pinned-revision upgrade. Do not use the
test-only `--no-snapshot` option in a production maintenance window. After an
upgrade or restart, verify real login, the permanent identity anchor, consumer
clients, JWKS/SAML signatures, and the directory cursor. Prefer ANAS for routine
restarts; direct Docker operations are reserved for validated recovery steps.

Select a verified deployment with `anas deployments -w /srv/anas`, then run:

```bash
anas rollback DEPLOYMENT_ID -w /srv/anas -y
```

A safe artifact rollback requires `data_touched=false`. It switches runtime
artifacts only: it does not rewind the database, secrets, cursor, or any other
persistent data, and it does not automatically lower the desired module
revision. Do not force a lower lock revision as a substitute for data recovery.
If persistent data must be rewound, use the explicit snapshot-restore procedure
above and repeat the restore acceptance checks.

## Provider switch and deprecation

A provider switch is not an account or session migration. Keep the Samba
permanent anchor as the consumer-side linking key, issue independent clients
for the new provider, verify redirect/logout URIs, `ALLOW_GROUPS`, administrator
demotion, and real login, then move consumers one at a time. Old sessions,
refresh tokens, and client credentials do not migrate; never copy Casdoor local
user IDs, sessions, or passwords.

After all consumers pass verification and the rollback observation window,
revoke Casdoor clients, stop the module, and apply the chosen database and
backup retention policy. Removing a declaration is not a substitute for secret
revocation or a retention decision.

## Explicitly unsupported capabilities

- Pinned Casdoor `3.143.0` does not publish SAML SLO; SAML consumers log out
  locally only.
- LDAP/AD password write-back is disabled, and Casdoor is not directory
  authority.
- Database type or name cannot change silently; PostgreSQL is the only
  interface and migration must be explicit.
- Old tokens, cookies, or passkeys are not guaranteed across issuer/domain
  changes; this restore procedure requires the same issuer.
- Casdoor local user IDs are not Samba permanent anchors, and provider sessions
  are not migrated automatically.
