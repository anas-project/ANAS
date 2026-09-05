# Migrate the identity-anchor OID to PEN 66678

This runbook migrates the **single** Samba AD DC that uses the legacy GUID-derived OID to IANA-assigned PEN `66678`. The final attribute is:

| Item | Before migration (permanently retired) | After migration (official) |
| --- | --- | --- |
| `attributeID` | `1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401.2.1` | `1.3.6.1.4.1.66678.1.2.1` |
| `schemaIDGUID` | `7108c5a7-2290-45e0-9eba-eef087be58e3` | `db3786ae-3261-4d44-a2a1-588bfe3e41c5` |
| Status | Defunct legacy object; never reuse | Current `anasIdentityAnchor` |

This procedure changes the AD schema and requires a maintenance window. It applies only to a deployment with one writable DC that owns the Schema Master FSMO role. A multi-DC or replicated topology needs a separately designed migration.

> Internal OID sub-assignments do not need individual IANA registration. See the [OID registry](../governance/oid-registry.md) for the allocation rules and retirement record.

## Migration rules

- Use the exact `ghcr.io/anas-project/anas-samba-dc:4.23.6-r11` image; never use `latest`. A private registry must resolve to the same r11 image digest.
- `--check` is read-only; only `--execute` changes the schema and object values.
- Create the snapshot only after every writer is stopped, and successfully validate it with `anas snapshot verify`. The script's `--snapshot-id` is only an evidence label; it does not validate the snapshot for you.
- `--backup-dir` must be a protected, pre-existing host path mounted outside the Samba data volume. Use a new, nonexistent child directory for every execution.
- If any step after `--execute` fails, restore the **entire Samba data-volume snapshot**. Never restore only `sam.ldb`, delete the migration marker, or hand-edit the database to continue.

## 1. Prepare the r11 deployment

Pull the image and render the candidate deployment before the outage to shorten the maintenance window. The examples use `/srv/anas`; replace it with the real absolute workspace path.

```bash
docker pull ghcr.io/anas-project/anas-samba-dc:4.23.6-r11
docker pull ghcr.io/anas-project/anas-samba-dc-anchor:4.23.6-r11
anas module update samba_dc -w /srv/anas
anas render -w /srv/anas
```

Record the candidate deployment ID returned by `anas render`, then inspect the deployment and the images resolved by Compose:

```bash
anas deployments inspect REPLACE_WITH_R11_DEPLOYMENT_ID -w /srv/anas
ANAS_R11_MODULE_DIR=/srv/anas/.anas/deployments/REPLACE_WITH_R11_DEPLOYMENT_ID/modules/samba_dc
docker compose --project-directory "${ANAS_R11_MODULE_DIR}" \
  --env-file "${ANAS_R11_MODULE_DIR}/.env" \
  -f "${ANAS_R11_MODULE_DIR}/docker-compose.yml" config --images
docker image inspect --format '{{.Id}} {{json .RepoDigests}}' \
  ghcr.io/anas-project/anas-samba-dc:4.23.6-r11 \
  ghcr.io/anas-project/anas-samba-dc-anchor:4.23.6-r11
```

Confirm that the deployment manifest resolves Samba DC r11 and that `config --images` shows the exact
`4.23.6-r11` tags for the DC and anchor worker. Record the local image IDs/RepoDigests in the controlled
execution evidence, and compare them if your organization maintains approved digests. `deployments inspect`
does not itself report runtime image digests. **Do not start the candidate yet**: normal r11 initialization
rejects an unmigrated legacy schema, while any pre-r11 legacy image must not be restarted after the data has
been migrated. With a private registry, replace the two public image references above with the actual values
from `config --images`.

Also complete these preparations:

1. Record every enabled Consumer and, for each Consumer, the account mapping and current anchor for one known user for comparison after migration.
2. Confirm that workspace data is at `<workspace>/data/samba_dc/var` and that this is the only writable DC.
3. Pre-create an evidence parent on persistent storage outside the Samba data volume and restrict it to administrators:

   ```bash
   sudo install -d -m 0700 /mnt/anas-migration-evidence
   ```

4. Confirm that the Btrfs snapshot store has enough space and prepare the full restore command.

## 2. Stop host-level writers

Stop the entire workspace:

```bash
anas stop -w /srv/anas
```

On the host, confirm that all of the following are stopped—not just the Samba container in Compose:

- `samba_dc`, the anchor worker, the event initializer, and every Consumer;
- temporary containers, scheduled jobs, and interactive sessions that use LDAP, `samba-tool`, or offline LDB access;
- backup, synchronization, or maintenance processes that read or write `<workspace>/data/samba_dc/var`.

The migration script can inspect only its own container and cannot prove that the host is fully quiesced. After confirming quiescence, keep every service stopped and continue to the read-only preflight.

## 3. Set maintenance variables

```bash
ANAS_WORKSPACE_PATH=/srv/anas
ANAS_SAMBA_DATA_PATH="${ANAS_WORKSPACE_PATH}/data/samba_dc/var"
ANAS_EVIDENCE_PARENT=/mnt/anas-migration-evidence
ANAS_EVIDENCE_NAME=pen66678-REPLACE_WITH_UNIQUE_RUN_ID
ANAS_R11_DEPLOYMENT_ID=REPLACE_WITH_R11_DEPLOYMENT_ID
ANAS_MIGRATION_IMAGE=ghcr.io/anas-project/anas-samba-dc:4.23.6-r11
ANAS_R11_MODULE_DIR="${ANAS_WORKSPACE_PATH}/.anas/deployments/${ANAS_R11_DEPLOYMENT_ID}/modules/samba_dc"
ANAS_CONTAINER_PREFIX=$(sed -n 's/^CONTAINER_PREFIX=//p' "${ANAS_R11_MODULE_DIR}/.env" | head -n 1)
ANAS_COMPOSE_PROJECT="${ANAS_CONTAINER_PREFIX}samba_dc"
ANAS_DC_CONTAINER="${ANAS_CONTAINER_PREFIX}samba_dc"
ANAS_ANCHOR_CONTAINER="${ANAS_CONTAINER_PREFIX}samba_dc_anchor"
```

The child named by `ANAS_EVIDENCE_NAME` must not already exist. Evidence contains directory DNs and stable identifiers; protect it as sensitive directory data and never copy it to public tickets or logging services.

## 4. Run the read-only preflight

Use a one-shot r11 container with no network and mount the stopped Samba data volume:

```bash
docker run --rm --network none --user 0 \
  --entrypoint /usr/local/bin/migrate-identity-anchor-oid.sh \
  --mount type=bind,src="${ANAS_SAMBA_DATA_PATH}",dst=/var/lib/samba \
  "${ANAS_MIGRATION_IMAGE}" --check
```

Continue only when the output explicitly identifies the **complete legacy state** supported by the script. Stop for any of these results:

- complete final state: there is nothing to execute;
- partial migration, mixed OIDs, unknown GUIDs, duplicate values, inconsistent class links, or an unreadable database: do not patch automatically; preserve the state and investigate;
- a running Samba process or writer: return to the host-level quiescence check.

## 5. Create a real cold snapshot and execute the migration once

After preflight succeeds, and while the host remains fully quiesced, create the real cold snapshot:

```bash
anas snapshot create --label "before PEN 66678 identity-anchor OID migration" -w "${ANAS_WORKSPACE_PATH}"
ANAS_SNAPSHOT_ID=REPLACE_WITH_VERIFIED_SNAPSHOT_ID
anas snapshot show "${ANAS_SNAPSHOT_ID}" -w "${ANAS_WORKSPACE_PATH}"
anas snapshot verify "${ANAS_SNAPSHOT_ID}" -w "${ANAS_WORKSPACE_PATH}"
anas snapshot pin "${ANAS_SNAPSHOT_ID}" -w "${ANAS_WORKSPACE_PATH}"
```

Continue only after `snapshot verify` succeeds. Record the real snapshot ID returned by the tool; do not invent an ID or substitute a label. No writer may start while the snapshot is being created or verified.

Mount the external evidence parent at the same host and container path, and explicitly pass a new child directory:

```bash
docker run --rm --network none --user 0 \
  --entrypoint /usr/local/bin/migrate-identity-anchor-oid.sh \
  --mount type=bind,src="${ANAS_SAMBA_DATA_PATH}",dst=/var/lib/samba \
  --mount type=bind,src="${ANAS_EVIDENCE_PARENT}",dst="${ANAS_EVIDENCE_PARENT}" \
  "${ANAS_MIGRATION_IMAGE}" --execute \
  --snapshot-id "${ANAS_SNAPSHOT_ID}" \
  --backup-dir "${ANAS_EVIDENCE_PARENT}/${ANAS_EVIDENCE_NAME}"
```

The script exports and validates legacy values before it removes the old class links, marks and renames the legacy schema object, creates the new OID/GUID object, restores values, and rebuilds the User/Group class links. Each phase has an auditable checkpoint, but an in-place retry after failure is **not supported**.

After completion, keep every production service stopped and run the same read-only check again:

```bash
docker run --rm --network none --user 0 \
  --entrypoint /usr/local/bin/migrate-identity-anchor-oid.sh \
  --mount type=bind,src="${ANAS_SAMBA_DATA_PATH}",dst=/var/lib/samba \
  "${ANAS_MIGRATION_IMAGE}" --check

sha256sum -c "${ANAS_EVIDENCE_PARENT}/${ANAS_EVIDENCE_NAME}/SHA256SUMS"
```

For a complete final state, `--check` validates:

- the OIDs, GUIDs, immutable fields, and defunct status of the final and legacy schema objects;
- the User and Group class links to `anasIdentityAnchor`;
- exactly one `objectGUID` and one `mS-DS-ConsistencyGuid` for every object with a text anchor;
- equality between the text UUID and the Windows `bytes_le` representation of the binary GUID, plus global text-anchor uniqueness.

## 6. Start r11 and validate write permission

First start only the DC and initializer from the candidate deployment. Consumers remain stopped:

```bash
docker compose --project-name "${ANAS_COMPOSE_PROJECT}" \
  --project-directory "${ANAS_R11_MODULE_DIR}" \
  --env-file "${ANAS_R11_MODULE_DIR}/.env" \
  -f "${ANAS_R11_MODULE_DIR}/docker-compose.yml" \
  up -d anas_samba_dc_events_init anas_samba_dc
```

Confirm that the DC is healthy, initialization succeeded, and the schema ready marker exists:

```bash
docker exec "${ANAS_DC_CONTAINER}" test -f /run/anas-identity-schema.ready
for _ in $(seq 1 90); do
  ANAS_DC_HEALTH=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "${ANAS_DC_CONTAINER}")
  [ "${ANAS_DC_HEALTH}" = healthy ] && break
  sleep 2
done
test "${ANAS_DC_HEALTH}" = healthy
docker logs --tail 100 "${ANAS_DC_CONTAINER}"
```

Check the ACLs on `OU=People` and `OU=Groups`. Both must contain the new schemaIDGUID and no longer contain the legacy GUID.

```bash
ANAS_USERS_DN=$(docker exec "${ANAS_DC_CONTAINER}" printenv SAMBA_DC_BASE_USERS_DN)
ANAS_GROUPS_DN=$(docker exec "${ANAS_DC_CONTAINER}" printenv SAMBA_DC_BASE_GROUPS_DN)

docker exec "${ANAS_DC_CONTAINER}" samba-tool dsacl get --objectdn="${ANAS_USERS_DN}" | grep -F 'db3786ae-3261-4d44-a2a1-588bfe3e41c5'
docker exec "${ANAS_DC_CONTAINER}" samba-tool dsacl get --objectdn="${ANAS_GROUPS_DN}" | grep -F 'db3786ae-3261-4d44-a2a1-588bfe3e41c5'
```

Manually confirm that neither ACL output contains `7108c5a7-2290-45e0-9eba-eef087be58e3`. Then start the anchor worker:

```bash
docker compose --project-name "${ANAS_COMPOSE_PROJECT}" \
  --project-directory "${ANAS_R11_MODULE_DIR}" \
  --env-file "${ANAS_R11_MODULE_DIR}/.env" \
  -f "${ANAS_R11_MODULE_DIR}/docker-compose.yml" \
  up -d anas_samba_dc_anchor

for _ in $(seq 1 90); do
  ANAS_ANCHOR_HEALTH=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "${ANAS_ANCHOR_CONTAINER}")
  [ "${ANAS_ANCHOR_HEALTH}" = healthy ] && break
  sleep 2
done
test "${ANAS_ANCHOR_HEALTH}" = healthy
```

While Consumers are still stopped, use an approved disposable user that is disabled and belongs to no `APP_*` groups as a real permission probe:

1. Create the disposable user in `OU=People` through the normal management interface.
2. Wait for the worker to write `anasIdentityAnchor` and `mS-DS-ConsistencyGuid`.
3. Query both values, confirm the text UUID matches the binary GUID, and confirm that the worker remains `healthy`.
4. Delete the disposable user and confirm that the directory object is gone. The event journal is append-only;
   do not delete events. After Consumers start, confirm that they consume the user's Add/Delete events and retain
   no test account.

This probe validates the actual `svc_anchor` write permission for the new GUID; reading the ACL alone is not a substitute.

## 7. Activate the deployment and validate Consumers

After the DC, ACL, and worker checks pass, activate the same rendered r11 candidate:

```bash
anas apply --deployment "${ANAS_R11_DEPLOYMENT_ID}" -w "${ANAS_WORKSPACE_PATH}"
anas status -w "${ANAS_WORKSPACE_PATH}"
```

Confirm the DC ready marker and anchor-worker health again after activation. Then validate every Consumer recorded before migration:

- the known user still maps to the original account, the anchor is unchanged, and no duplicate account appears;
- sign-in, group authorization, and sign-out work normally;
- Consumer synchronization/authentication logs contain no schema, ACL, duplicate-anchor, or unknown-user errors;
- directory events from the maintenance period are consumed without a persistent retry or backlog.

End the maintenance window only after all checks pass. Keep the pinned snapshot and external evidence directory until the organization's observation and retention periods expire.

## Failure and full-volume recovery

If any step fails after `--execute` begins, stop immediately and do not run `--execute` again. Keep Consumers stopped. If the candidate Compose project has been started, shut it down first, then restore the validated real snapshot:

```bash
docker compose --project-name "${ANAS_COMPOSE_PROJECT}" \
  --project-directory "${ANAS_R11_MODULE_DIR}" \
  --env-file "${ANAS_R11_MODULE_DIR}/.env" \
  -f "${ANAS_R11_MODULE_DIR}/docker-compose.yml" down

anas stop -w "${ANAS_WORKSPACE_PATH}"
anas snapshot verify "${ANAS_SNAPSHOT_ID}" -w "${ANAS_WORKSPACE_PATH}"
anas snapshot restore "${ANAS_SNAPSHOT_ID}" --dry-run -w "${ANAS_WORKSPACE_PATH}"
anas snapshot restore "${ANAS_SNAPSHOT_ID}" -w "${ANAS_WORKSPACE_PATH}" -y
```

Skip the first `docker compose ... down` command if the candidate Compose project was never started.

`snapshot restore` returns workspace data, the active artifact, configuration, lock, and secrets to one recovery point; this is required to keep the directory and credentials consistent. After restore, run an offline check with the deployment version that matches the restored state before deciding to start it. Snapshot restore leaves services stopped; start the original legacy deployment only after confirming that the entire Samba data volume is consistent. The external evidence directory is not rolled back with the data volume and must be preserved unchanged for investigation.

The migration implementation is [`modules/samba_dc/samba_dc/root/usr/local/bin/migrate-identity-anchor-oid.sh`](https://github.com/anas-project/ANAS/blob/master/modules/samba_dc/samba_dc/root/usr/local/bin/migrate-identity-anchor-oid.sh). See the [Samba module technical documentation](https://github.com/anas-project/ANAS/blob/master/modules/samba_dc/docs/technical.en.md) for the schema contract and limitations.
