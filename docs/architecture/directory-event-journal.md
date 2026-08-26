# Directory event journal

> Status: **partially implemented**. The Samba audit follower plus the
> `AUTHENTIK_DIRWATCH_*` and `CASDOOR_DIRWATCH_*` subscribers are implemented.
> Casdoor's create/profile/debounce/cursor E2E passed on 2026-08-26;
> `ANCHOR_EVENT_MAX_BYTES` and deletion/deactivation acceptance remain open.

A change in Active Directory reaches most consumers immediately, because they
query LDAP live. Consumers that keep a synchronized copy do not: they see the
change only at their next scheduled sync.

authentik and Casdoor both keep synchronized directory copies. authentik's
login path binds to LDAP to verify the password but never re-reads the account,
so group membership and attributes come from the last sync. Casdoor likewise
imports LDAP users into local shadow records. A Samba change can therefore be
authoritative and still remain invisible to either IAM until its next schedule.
That is exactly how an authentik-backed Nextcloud login failed on 2026-08-08:
the membership was written to AD at 12:46:12, the scheduled sync ran at
12:54:20, and every attempt in between was refused with the group already
correct in the directory.

The journal closes that window without polling harder.

## Shape

```text
samba_dc ──► /var/log/samba-audit/dsdb.json      (Samba's own format, private)
                    │ followed by the anchor worker, one reader, one cursor
                    ▼
             events.jsonl                        (ANAS_DIRECTORY_EVENTS_DIR)
                    │ tailed by each subscriber, each with its own cursor
                    ▼
             ├─ authentik dirwatch ──► Schedule.send()
             └─ Casdoor dirwatch ────► local LDAP sync API
```

Publishing is a side effect of work the anchor worker already does. It follows
the dsdb audit log to stamp identity anchors on new objects; the same parsed
record now also feeds the journal, so the log keeps exactly one reader and one
cursor.

## Why a journal and not a callback

Subscribers pull. Nothing registers an endpoint, and the producer never calls
out.

- **No credential.** A callback into authentik would need an API token, which
  means a component sitting next to the domain controller holding a key that
  operates the identity provider. That widens the blast radius in the wrong
  direction.
- **No stable endpoint to call.** authentik's trigger is
  `/api/v3/tasks/schedules/{uuid}/send/`, and the uuid is generated at runtime.
  There is nothing a hook could render into a registration at config time.
- **No cross-module network.** samba_dc runs on the host network and declares no
  shared network with consumers.
- **Delivery survives downtime.** A subscriber that was stopped for an hour
  resumes from its cursor. A fire-and-forget POST would simply have been lost.

## Record format

```json
{"seq": 1, "ts": "2026-08-08T12:46:12.072033+0800", "op": "Modify",
 "dn": "CN=APP_nextcloud,OU=Apps,OU=Groups,DC=example,DC=com",
 "attributes": ["member"]}
```

`seq` is monotonic and is what a subscriber persists. It resumes from the tail
of the existing journal across producer restarts, and from the rotated file
when the current one is fresh — otherwise a restart just after a rotation would
restart the sequence at zero, leaving every stored cursor ahead of the stream
and silently swallowing events.

`attributes` carries names only, never values. The journal therefore exposes
nothing that LDAP does not already expose to the same consumers, which is why
the directory is world-readable and subscribers mount it read-only.

## Filtering happens twice

The audit stream is overwhelmingly noise: on 2026-08-08 it held 3977 records,
3958 of them `lastLogon`/`logonCount` churn from one machine account. Both
stages matter.

| Stage | Setting | Question it answers |
| --- | --- | --- |
| Producer | `SAMBA_DC_ANCHOR_EVENT_ATTRIBUTES` | Is this worth telling anyone about? |
| Subscriber | `AUTHENTIK_DIRWATCH_ATTRIBUTES` / `CASDOOR_DIRWATCH_ATTRIBUTES` | Is this worth a full source sync? |

The producer publishes `Add` and `Delete` for any in-scope object, and `Modify`
only when it touched a watched attribute. Each subscriber narrows further:
authentik ignores `displayName` and `mail`, which are worth syncing but not
worth syncing *now*, and acts on membership and account-state changes. Casdoor
does import those profile fields, so its filter also treats them as immediate.

## Debounce

Neither integration has a safe per-user refresh at this boundary. authentik's
entry point is a scheduled full source sync; Casdoor's subscriber fetches the
configured LDAP result set and posts it to the official sync API. Both
subscribers therefore coalesce:

| Setting | Default | Purpose |
| --- | --- | --- |
| `AUTHENTIK_DIRWATCH_DEBOUNCE_SECONDS` / `CASDOOR_DIRWATCH_DEBOUNCE_SECONDS` | 5 | Collapse a burst into one run |
| `AUTHENTIK_DIRWATCH_MIN_INTERVAL_SECONDS` / `CASDOOR_DIRWATCH_MIN_INTERVAL_SECONDS` | 60 | Floor between consecutive runs |

Adding five members to a group is one sync, not five.

The cursor is committed only after a trigger fires. A crash between reading an
event and acting on it replays it rather than losing it; events that could
never trigger anything advance the cursor immediately. Casdoor's subscriber
uses the Module-owned Application credential against the private service
network; the Samba producer never receives an IAM credential.

## Retention

Both files in the chain are capped, by different mechanisms, for different
reasons.

| File | Cap | Rotated by |
| --- | --- | --- |
| `dsdb.json` | `SAMBA_DC_MAX_LOG_SIZE` (KB), one `.old` generation | Samba |
| `events.jsonl` | `ANCHOR_EVENT_MAX_BYTES`, one `.1` generation | the anchor worker |

Samba's raw log is rotated by Samba. `check_log_size()` runs on every debug
write and walks each class holding its own `@PATH` target, so `max log size`
bounds the audit file and not just `log file`: past the limit Samba renames it
to `dsdb.json.old` and reopens the original name.

Leaving that to Samba is deliberate. Samba is multi-process, and only one
process performs the rename; the rest keep writing through descriptors they
already hold until their own check notices the path's inode has changed. That
inode comparison lives behind the same size check, so `max log size = 0` does
not merely uncap the file — it strips the mechanism that reunites the writers
after any rotation, whoever performed it. An external rotator would depend on
that check anyway, and would add a second actor racing Samba for the same
rename.

The cap is a bound, not an archive. Nothing reads `dsdb.json.old`; the anchor
worker republishes what matters into the journal, and one rotated generation
exists only so the reader can finish draining a file that has just been
renamed out from under it. `AuditFollower` handles that crossing by inode
rather than by size, which is why the rotation must be a rename —
`copytruncate` keeps the inode and would silently skip records once the
refilled file grew past the follower's offset.

The transaction audit is not written at all. Enabling
`dsdb_transaction_json_audit` produced a second file of begin/prepare/commit
records that nothing ever opened, describing the framing of changes this
deployment already observes individually.

## What this is not

It is an accelerator, not a source of truth. Each IAM's scheduled sync stays
enabled, and the anchor worker keeps its own periodic reconciliation. If a
watcher is stopped, the deployment falls back to its previous behaviour:
slower, not broken.

It does not make syncing cheaper. Latency drops from up to two hours to a few
seconds, but each trigger is still a full source sync. What it does avoid is
the alternative of raising the schedule frequency, which would run that sync
288 times a day whether or not anything changed — and would multiply the
exposure of the source's `delete_not_found_objects` sweep by the same factor.

Samba's dsdb audit records only writes performed on the local DC. A second
domain controller needs its own producer.
