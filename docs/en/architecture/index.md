# Architecture

Chinese is the source language for the detailed design set. The pages below linked under `/en/` have English versions; the rest link to the Chinese originals, which remain normative. It covers:

- the normative [Core implementation standard](/en/architecture/core-implementation-standard);
- [modules, contracts, resources, and provider operations](/en/architecture/module-contract-resource-design);
- [module-specific commands, typed parameters, and shared CLI/API execution](/architecture/module-command-capability-design);
- [administrator account lifecycle](/architecture/admin-account-system);
- [IAM capability, protocol selection, and bidirectional logout registration](/en/architecture/iam-capability-design);
- [application catalog visibility and authorization](/architecture/app-catalog-design);
- [dynamic DNS capability selection](/architecture/dynamic-dns-capability-design);
- [object-storage capability binding and normalized S3 outputs](/en/architecture/object-storage-capability-design);
- [Forgejo Module identity, Actions authorization, and Incus VM runner design](/architecture/forgejo-module-design);
- [AI agent orchestration (Forgejo baseline)](/architecture/ai-agent-orchestration-design) — agents as Forgejo accounts with repository-scoped tokens, issue/label/comment events as the control surface, a standalone orchestrator packaged as a module, and one-job isolated execution. The design itself now lives with the component under `modules/ai_agent/`, ready to be split into its own project;
- [Incus host provisioning, ingress, and guest image baking (proposal)](/architecture/incus-host-provisioning) — selected design: dedicated control bridge with fixed-destination TLS pass-through; Traefik reaches managed guest addresses through restricted routing and firewall rules, without mandatory proxy devices or network forwards. HTTP is the first phase; TCP/UDP require separate authorization and listener planning. Image declarations use mutually exclusive catalog/name/revision or fingerprint objects, with no legacy string compatibility; deployment digests are frozen. Implementation and real-host verification remain pending. The Chinese source is normative;
- [runtime artifacts, releases, and persistent state](/architecture/runtime-release-state-design);
- [configuration and state lifecycle](/architecture/config-state-lifecycle).

The Chinese source documents remain normative while further English translations are prepared. Stable machine-facing behavior is separately defined by the [CLI contracts](/en/reference/contracts/).

Incus image declarations now use structured objects and freeze targets, catalogs and fingerprints in deployments. The shipped catalog is empty; baking/import and production ingress remain pending. A lab-only HTTP network artifact generator exists. Its real Linux namespace HTTP checks passed on 2026-09-11, but actual Docker/Incus guest acceptance has not been recorded.

The 2026-09-18 staging preflight adds explicit matching-source checks without invoking Docker:
`check-shared-build --source-root ... --staging-root ...` requires an absolute
`ANAS_SHARED_BUILD_CONTEXT` and compares present module build trees, shared input bytes and executable
bits. It does not validate a complete deployment manifest or prove an image build succeeds.
Offline image inspection reuses the shared `ArtifactRelease` representation through
`DescribeArtifactRelease`/`VerifyArtifactRelease` and `cmd/compute-image-artifact` (split files only
for the CLI). Verification requires an independently trusted fingerprint and target, rejects changed
parts and version/recipe bindings, and never treats the descriptor as its own trust source. It does not
bake, import, publish, validate catalog signatures or establish bootability. The new tests have not
been executed; M8b/M12/M13 acceptance and production ingress remain pending. See the
[Chinese design, section 6.2.2](/architecture/incus-host-provisioning).

`ArtifactArchive` and `cmd/incus-image-artifacts` add explicit local init/record/inspect/catalog
operations using that same representation. Private, locked archives publish content-addressed objects
before immutable revision metadata; identical original bytes can restore missing objects, while
conflicts, corruption and unknown metadata are preserved and rejected. Candidate catalogs require
explicit previous trusted history or an explicit first release. These commands emit metadata only and
do not rewrite the shipped catalog, run a builder, sign/distribute images, import into Incus or prune.
Recipe-file hashing alone does not prove build provenance. Archive/CLI regression sources are written
but uncompiled and unexecuted; M12/M13 remain unaccepted. See section 6.2.3 of the same design.

Core now also persists an independent 32-byte naming key per compute lease, projects it privately to the consumer,
and preserves it across applies and file backup/restore. It is excluded from credential rotation. The dedicated naming-key rotation command and production HTTP ingress remain pending.


The HTTP lab prototype now includes explicit-socket, read-only observation capture and ordered
publish/replace/withdrawal plans. Withdrawal retains deny filters until the lab endpoints stop; changed
topology requires a new lab session. These additions were not compiled or tested on 2026-09-12 at the
operator's request. They do not implement production authorization, IP reservation or a host executor.

Core now freezes optional HTTP authorization in `compute_ingress` and blocks startup of ingress-bearing
consumers until runtime mediation and host acceptance are ready. Fixed/named/random names and namespace
checks are implemented; random uses 128 bits of HMAC-SHA256. The lab `--mediation` mode connects strict
Linux request-directory reads, authorization and in-memory name reservations, retaining frozen ForwardAuth
middleware. Default `auth: none` is not access control and must not expose sensitive or writable services.
New paths remain untested; production read-only identity, directory provisioning, host actions/IP holds and
reconciliation remain pending. See the [Chinese design](/architecture/incus-host-provisioning).

The lab workspace adapter now reads active Core authority under the shared runtime lock and can register
fresh per-lease request directories. Epochs bind workspace, activation and manifest digests; workspace mode
rejects manual authority/key-source overrides and rechecks state after capture. Registration contains no
keys, and no mounts/network actions run. The administrator-only key reader is not production key delivery.
These additions are untested; production startup and real-host acceptance remain blocked.

The 2026-09-13 continuation adds a local exclusive session lock, a serialized periodic controller and a
workspace request source. Startup retires persisted targets before accepting freshly validated work;
failed authority reads trigger withdrawal and cancellation gets a separate bounded cleanup deadline.
Retirement records prevent reuse of the same reservation, without TTL pruning. Workspaces revalidate
Core grants, request bindings and instance UUID/IP/MAC through an independent fact reader. Actual route
inventory, restricted Incus identity, host actions, probes, orphan recovery and narrow key delivery still
need adapters. The file renderer now reuses the existing Traefik environment-field template in a private
staging directory; file writes do not prove Traefik consumption. These changes remain untested and
production ingress is still blocked. See the [Chinese design](/architecture/incus-host-provisioning).

The Incus fact reader now has a scoped HTTPS GET implementation with exact certificate/version checks,
bounded responses and two matching samples of the project, managed NIC and address allocation. Executor
targets bind an incarnation digest of UUID, generation and last start time, so a rapid restart cannot renew
an old token merely by retaining its address and MAC. This code has not been run. Server-enforced
read-only identity still needs provisioning: a project-restricted Incus TLS certificate retains write rights.
No global authorization policy was changed, and Traefik inventory/confirmation and host adapters remain
pending. API fields follow [Incus v7.3.0](https://github.com/lxc/incus/blob/v7.3.0/shared/api/instance_state.go);
version pinning does not establish compatibility or real-host acceptance.

Traefik inventory/confirmation code now reads two complete snapshots through the existing protected API,
pins version 3.7.10 and the certificate, and checks its live API router. Ownership exclusion requires a
held-journal receipt, the complete owned file and matching loaded router/service/auth. HTTPS rules with
unknown or unbounded Host scope are rejected. Direct ForwardAuth definitions require trusted installation
pins, checked before file publication and after loading; live API data cannot establish those pins.
Withdrawal confirms file/router/service disappearance. Traefik's default UP flag only means a backend
was constructed, so independent probes and real-host tests remain required. Credential/pin delivery and
service assembly are pending; this code is untested and production ingress remains blocked. See the
[runtime API source](https://github.com/traefik/traefik/blob/v3.7.10/pkg/api/handler.go).

The 2026-09-15 continuation adds private reader-credential delivery and reader assembly. Core exports
only active random leases' naming keys alongside installer-supplied reader identities and exact auth pins
to a new private 0400, single-link, bounded file. All naming modes bind the complete active snapshot;
each API GET rechecks file identity and content. Old credentials must stay valid until old routes are
withdrawn. This does not provision server-enforced read-only rights or install a service.

An independent `.example.test` fixture probe now checks a pre-registered target-specific response and
fresh authority/Incus facts. A trusted launcher must provide Traefik process/namespace identity, socket
cookie and ingress source IPv4, with the probe already in that namespace. Linux checks procfs/nsfs and
the actual socket's namespace cookie; bounded credential-free HTTP exchanges cannot follow redirects
or proxies. This remains dependent on host address holds and is not proof of public TLS/auth or
Traefik's own source selection. Launch provisioning, fixture registration, host actions, production
application probes, orphan recovery and E2E remain pending. No tests or server operations were run;
production ingress remains blocked. Details and source references are in the
[Chinese design](/architecture/incus-host-provisioning).

The experimental CLI now includes `--capture-probe` and `--prepare-fixtures` (unrun). The former checks
selected Docker IDs/allocation and samples process/namespace identity on a locked thread already in the
target namespace, without setns. The latter prepares distinct response files and a private registry from
live WorkspaceReaders under the state lock, refusing outstanding publications. Persistent fixture records
exclude reservation tokens but bind all other target fields; fresh authorization/instance facts are still
required when a new token is used after mediator restart. Guest restarts require new registration. No
host action, guest service installation or probe is performed by preparation. Production launch delivery,
the privileged action channel and real-host acceptance remain pending; tests remain deferred.

Recovery now has a mandatory external-artifact inventory boundary. `Executor.Recover` retires outstanding
journal intents under the held lock, then requires a clear installed scope before the controller resumes.
Opening/renewal and address release are guarded as well; unknown artifacts retain addresses and retiring
receipts while known routes and permits can close. Retired tokens are never cleanup candidates.

The concrete file/API checks require a dedicated directory (4096 entries, 30 seconds), exact rendered
bytes, stable file identities around API reads and complete loaded HTTP router/service/auth matches.
Other providers, protocols, dynamic sections and service references cannot claim the reserved prefix;
only verified ForwardAuth usage edges are excluded. Canonical temporary files can be removed only with
an outstanding receipt and complete matching bytes. Corrupt/missing receipts, partial files and unknown
ownership need independent recovery evidence; there is no automatic import or prefix-wide deletion.
Actual host inventory and administrator recovery actions still depend on the planned host channel.
These observations are not an atomic file/API/kernel snapshot, and the code remains unrun. See the
[Chinese design](/architecture/incus-host-provisioning) for the remaining implementation and E2E limits.

On 2026-09-16, the shared action ABI prerequisite gained unverified framing and stream-validation code
in `internal/actionabi`, using only the standard library. Requests/events are bounded strict JSONL with
job/invocation binding. Executors cannot assign journal sequences or claim truncation; replay gaps need
an explicit trusted marker. Exact progress counters may exceed estimates without clamping. Completion
combines a terminal frame, actual EOF and process-exit evidence; forced termination or incomplete output
remains unknown. These primitives do not dispatch actions or create a second job store. Existing Module
Command/CLI/HTTP behavior is unchanged, and host inventory still needs execution integration and the
privileged channel. See the [Chinese action ABI design](/architecture/action-abi) for wire details and
the pending migration. No runtime compilation, tests, gates or server operations were performed.

On 2026-09-17, the existing `consolejobs` journal gained internal action bindings, per-job ABI sequences,
and atomic records combining public events, truncation and terminal state. Global console event IDs
remain separate. At capacity (default 1024 events), or when an append finds expired history, a persisted
cumulative marker replaces the prior prefix before the next event. The terminal and bounded tail remain
available; final job expiry and a global disk quota are still pending. Replay and compacted snapshots
check the action suffix, cursors and outcome. The existing execution lease and audit observer protect
action starts, lifecycle commits and unknown recovery after restart. Legacy workers skip action jobs.

`jobexecutor.ActionRecorder` requires a trusted action-specific public projection before persistence.
Terminal frames remain provisional until actual EOF and process-exit evidence agree; malformed output,
forced termination or unconfirmed completion produces unknown. This is an internal adapter, not a
dispatcher or process launcher. Registries/permissions, cooperative cancellation, coalescing, action-key
idempotency/one-hour retention, confirmations and client migration remain pending. Journal schema v1 has
new typed fields/records that old readers reject; there is no downgrade conversion. These changes remain
uncompiled and untested and do not enable the host channel or production ingress.

On 2026-09-18, `consolejobs` gained a durable start barrier for unknown actions caused by lost process
containment or daemon restart. All start paths check the existing journal receipt before allowing new
execution, including read-only jobs and other workspaces sharing the store. Compaction, reopening,
registry recreation and ordinary compensation acknowledgement do not clear the barrier. Reads, replay
and queued cancellation remain available. Independent process/writer recovery and service wiring are
still pending; the barrier is not proof of cleanup. Regression sources were added but not executed.
See [the Chinese action ABI design](/architecture/action-abi) and
[the host-provisioning design](/architecture/incus-host-provisioning).

The 2026-09-18 control-transport component in `modules/incus/control-relay` is a Linux non-root,
fixed-destination TCP relay, not an installed service or a new privileged entry point. It binds only an
installation-approved private IPv4/interface/high port and connects only to `127.0.0.1:8443`; it does
not load TLS credentials, terminate TLS, resolve upstream names, or accept CONNECT/SOCKS targets.
Configuration and ancestors must be root-owned and non-writable by group/other users, with no symlink
traversal. Dedicated UID/GID, capabilities, strict JSON, source CIDR, connection capacity, idle deadlines,
half-close, shutdown and interface-drift checks are implemented but uncompiled and untested.

The source check does not establish an ingress-interface authorization. Host installation must first
establish owned network and INPUT/FORWARD restrictions, then start the service, verify mTLS/pinning,
project separation and denial from LAN/other Docker networks/guests, and only then publish an endpoint.
Interface recreation requires a freshly verified installation binding. Packaging, service units, this
host coordination and symmetric uninstall are still missing. Local test sources do not replace real
Linux acceptance; no service was started and production ingress remains blocked. See the
[Chinese host-provisioning design](/architecture/incus-host-provisioning) section 3.8.

The host-installation design now follows the Provider's current Created btrfs/zfs admission rule instead
of proposing a default dir pool that the Provider would reject. No installer or automatic storage
conversion was introduced. Incus's bilingual technical sources also explain the staging build override:
`ANAS_SHARED_BUILD_CONTEXT` must identify the absolute root of matching complete source, not the staging
root. Real checkout/staging builds, storage quota checks and host acceptance remain pending.

The opt-in `jobexecutor.ModuleActionWorker` now consumes registered workspaces from the same journal
under the existing execution lease; construction does not start it. It uses daemon lifetime rather than
an invoke/attach connection, refreshes durable state after each invocation, and refuses unreconciled
running actions. Running cancellation persists actor/time evidence in `Action.Cancellation`, outside
the bounded event tail. A pre-commit permission recheck and actor-bound audit precede the private
notification; duplicates preserve the original evidence and do not notify twice. Queued
cancellation competes atomically with start. Neither the intent nor a forced kill means cancelled.
Unconfirmed cleanup stops admission and retains the application lock and execution lease. The outer
owner must still reconcile external processes and halt other execution paths. These are trusted,
unprivileged Module adapters, not a hostile-code sandbox or the host-root registry. The main daemon,
existing Module Command/CLI/HTTP entry points and production ingress have not been switched to this
worker. Queue, cancellation, authorization and terminal-evidence regression sources were added, not run.
See the [Chinese action ABI design](/architecture/action-abi) for the implementation boundary.

On 2026-09-18, queued action preflight rejection gained an atomic journal boundary. A matching
invocation and successful pre-commit audit are required; the failed job and its fixed
`action_not_started` terminal event are committed together. Start and rejection race under the same
job lock. A job which never started does not require business compensation, but a running executor
cannot claim this reserved preflight code to erase its execution history. Replay and compacted
snapshots also distinguish these cases using `StartedAt`. Registry preflight calls use this boundary;
uncertain start commits and unconfirmed cleanup must not be converted into harmless rejection.
Regression source covers audit failure, invocation mismatch, start/rejection races, replay and
compaction/reopen. These tests remain unrun; client migration, host actions and real-host acceptance
are not established by this change. Details are in the [Chinese action ABI design](/architecture/action-abi).

The current worker's cancellation entrypoint persists the first canceling actor and timestamp in the
same job before signaling its executor. Duplicate requests preserve that record; event-tail truncation
and compaction cannot erase it. Ordinary executor warnings cannot create this control evidence, and
running jobs need both durable intent and the supervisor's independent acknowledgement/EOF/exit checks
to finish as cancelled. An accepted request is not a promised outcome. Additional regression source
covers audit denial, invocation mismatch, duplicate requests, defensive copies and persistence through
truncation/compaction/reopen. It remains unrun. These Module-action prerequisites do not supply Incus
host networking, root-action authorization or production ingress installation.

The opt-in `jobexecutor.ModuleActionDispatcher` provides shared Invoke/Get/List/Attach/Cancel operations
for future CLI/HTTP adapters. It delegates execution and durable cancellation to `ModuleActionWorker`,
not a second queue or journal. Request disconnects never cancel execution. Creation/start commits and
queued preflight recheck current entrypoint permissions; subscriptions reauthorize each page and preserve
actual sequence/truncation markers. Authorized administrators can read each other's jobs, including
historical jobs whose action is no longer registered. Module views and queue consumption exclude host
actions. Cancellation audit remains bound to the cancelling actor and precedes durable intent and
notification. Invocation preserves optional caller keys; the shared store now owns action-key expiry
and coalescing as described below. Confirmation tokens remain pending. The main daemon and existing
clients are not switched to this facade. Regression sources were added for visibility, disconnect/revocation, retries, truncation,
cancellation, unrecovered jobs and host-action isolation; none were run. See the
[Chinese action ABI design](/architecture/action-abi) for the callback and integration boundaries.

The internal Module action adapter also canonicalizes normalized parameter objects before computing
request digests, so nested key order and whitespace do not cause false retry conflicts. `UseNumber`
preserves integer precision, output is detached from callback-owned memory, and size limits are checked
again after encoding. Frozen deployment/descriptor and parameter checks still reject new defaults on
queued requests. Canonicalization alone is not the retry policy; the store implementation below provides
that separate boundary. New protocol, recorder and
parameter-boundary test sources cover strict framing, truncation, terminal evidence, public projection,
large integers and canonicalization; none were executed in this work. The Incus bilingual technical
notes now include the absolute source-root override required for local staging builds, with actual
source/staging build acceptance explicitly pending.

The internal registry/dispatcher now uses `CreateActionWithPolicyObserved` for `(action, key)` identity
within the shared store, independent of actor and transport. Workspace, frozen parameters and concurrency
policy belong to the request digest and cannot change under the same key. Coalescing adds new key aliases
atomically after auditing the actual joining actor; reject reports an active job, while queue creates a
separate job and serializes equivalent requests, including read-only actions. Unspecified internal
registry policy defaults to reject. Keyless coalescing creates no aliases, and the 64-alias per-job limit
fails explicitly rather than evicting live keys. Returning an existing job or identifying conflict also
requires the caller's current read permission.

Pending/running keys do not expire. A terminal key can be rebound after one hour; persisted assignment
timestamps, not job creation order, determine its latest owner. Journal recovery and sealed snapshots
reject overlapping ownership and prevent clock rollback from resurrecting superseded owners. This is
logical expiry, not job deletion or a global disk quota. The original single journal and one creation
receipt per job remain; new policy state and the atomic `action_key_bound` record hold logical aliases.
New regression sources cover retries, concurrency, expiry/clock rollback, audit failures, snapshots,
public projection, exact integers and permissions. They have not been compiled or run. Product clients,
root actions, destructive confirmation and real-host acceptance remain pending; no Incus acceptance
milestone or production ingress setting changes as a result.

The Linux Module adapter delivers cooperative cancellation on a dedicated inherited pipe
(`ANAS_ACTION_CANCEL_FD=4`). `actionabi.ReadModuleCancellation` accepts only EOF on that pipe as a
request; stdin EOF, subscriber disconnects, host sockets, read errors and unexpected bytes are not
cancellation acknowledgements. Executors retain responsibility for safe-point cleanup and an explicit
cancelled terminal followed by a clean exit. The helper neither opens descriptors nor starts a goroutine.

Additional unrun regression sources exercise request/stream boundaries, recorder projection and memory
isolation, and a native ELF fixture for working-directory/environment binding, explicit cancellation,
ignored cancellation, a success frame followed by a hang, missing terminals, malformed trailing bytes
and excessive stderr. The native fixture requires unprivileged, capability-free Linux with pidfd;
skipped cases are not acceptance evidence. It performs no Incus or server/network operations and does
not replace host-channel, CLI/HTTP, process-recovery or real guest/network acceptance. See the
[Chinese action ABI design](/architecture/action-abi) for the interface and source-file inventory.

The consumer-side HTTP request path now includes `computeingress.RequestWriter` and explicit shared
client publication methods. Installation must already provide a private, registered lease directory;
no authorization, registry, mount or production ingress is created by the client. On supported local
Linux filesystems, directory flock coordinates cooperating writers. Complete bounded requests are
published by synchronized temporary files and rename, with 256-entry and 256-receipt limits. Identical
requests reuse their file; different workloads cannot overwrite an occupied instance/port slot.

Opaque receipts retain an inode descriptor. Withdrawal matches both file identity and request content
before removing intent, so a stale receipt cannot remove a replacement using the same filename.
`Resume` only adopts an existing request matching persisted expectations; a missing request is never
republished. Closing local handles leaves durable intent intact. Synchronization/identity failures are
uncertain results, not success. Predicted URLs and file receipts do not prove route readiness,
authentication, connection cleanup or address release. The independent mediator still enforces all
active-authorization and host-network checks; the consumer lock is not an adversarial security boundary.

`Client.OpenHTTPPublisher` configures `HTTPPublisher.PublishPort/UnpublishPort` from a minimal
projection of lease scope, public policy, base domain, private directory and a separately delivered
random-mode naming key. The consumer never receives a full frozen grant, middleware, entrypoint or
global Store reference; the key is omitted from requests and default JSON/formatted output. Shared
`Policy.Host` only predicts names, while `Authorization.Host` continues validating the mediator's
complete grant. The client checks the exact Running managed instance and its workload metadata;
`Inspect` rejects duplicate exact identities and no longer accepts the first fuzzy name match.
Withdrawal uses the original `HTTPPublication` receipt rather than a reusable instance/port name.

The three shared-client image builds and CI catalogs include `internal/computeingress`. Filesystem
regression source covers retries, recovery, stale receipts, in-place mutation, links/FIFOs, directory
replacement, bounds, cancellation and concurrent writers. Client regression sources additionally
cover minimal projection versus full authority, exact identity, local Host collisions, idempotent
submission, stale-receipt replacement, cancellation and uncertain commits. These tests and actual image builds remain
unrun. Application recovery wiring, service identities, UID/mounts and the privileged host path are
still pending, with production ingress disabled. See the
[Chinese host-provisioning design](/architecture/incus-host-provisioning) for the implementation limits.
