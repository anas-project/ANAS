# Architecture

## Directory events and Casdoor sessions

The existing [directory event journal](/architecture/directory-event-journal) observes local Samba AD
writes and feeds the Casdoor watcher. Revision r10 implements per-application OIDC session/token
revocation, a durable pending-work file and bounded delivery retry in the existing watcher. Independent
OIDC session identifiers preserve later grants when old notifications are retried. Source and helper
tests pass; deployment acceptance is in progress. SAML SLO remains unavailable.

Lease egress is being redesigned as a static per-lease policy (decided 2026-09-28, not yet implemented). A
consumer Module declares one of four tiers for its compute lease: `internet` (the default), `internet_lan`,
`internet_lan_host`, or `modules_only`. It can also enable two switches, `module_access` and `intra_lease`,
both off by default. The Incus Provider enforces the tier through the lease bridge's network ACL and address
sets. On the host there is a single static forwarding rule. The LAN is defined positively as the default-route
interface's connected subnets plus configured extras. Traefik's current address is refreshed from Docker, because
the ACL sees post-DNAT destinations. This replaces the per-instance 30-second forwarding permits described below;
no renewal or runtime owner is needed for egress. See
[section 5.4 of the Chinese host design](/architecture/incus-host-provisioning).

Lease ingress was redesigned on 2026-09-28 and finalized on 2026-09-29 (not yet implemented). A lease declares one
of two ingress tiers: `none` (the default) or `published`. Lease bridges deny inbound traffic by default. There are
two kinds of publication, declared separately. HTTP publication goes through Traefik, which terminates HTTPS and
forwards HTTP to the guest; an unprivileged mediator inside anasd writes the route files. Port bindings forward raw
TCP or UDP the way Docker publishes ports: an ANAS-owned rule chain matches only traffic addressed to the host itself
(`fib daddr type local`) and rewrites it to a slot, a fixed address the Provider reserves for a named instance. hostd
synchronizes the port table at apply time within a port range the operator approved once, and a systemd socket holds
each bound port. Incus network forwards and proxy devices are not used. Instances with random names (one-off
instances) take no slot and use HTTP publication when they need ingress. See
[section 5.1.8 of the Chinese host design](/architecture/incus-host-provisioning).

Decided on 2026-09-30 (not yet implemented): the Traefik address list that lease ACLs refer to by name is a
host-wide address set written only by a hostd bounded synchronization action. The operator approves it once in
`incus.configure`; afterwards anasd triggers it after apply starts Traefik, after the Traefik container restarts and
when anasd starts, and hostd reads the address from Docker itself. anasd never runs the Provider for this, so the Incus
administrator certificate stays out of unattended runtime paths. A service installation must install both
`anas-helper` and `anas-hostd` and fails without either one.

Implemented on 2026-09-30: Incus serves its HTTPS API directly on the control bridge gateway (port 8443),
and a drop-in orders `incus.service` after `docker.service`, because Incus 7.0.1 retries a failed listener
bind only once, after 30 seconds. The non-root control relay, its unit, account and configuration are
removed, and the connection bundle is now `anas.incus-connection-bundle/v2`. Paragraphs below that describe
the relay are history. A lab probe on the same day also verified the lease ACL, address set, default-deny
ingress, port isolation, slot addresses and the Docker-style port table; see section 3.9 of the Chinese host
design and the [lease network probe](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-30-incus-lease-network-probe.md).

Incus host install plans now freeze the managed workspace's effective `CHINESE_SPEEDUP` in their
confirmation parameters. Enabled plans use fixed Aliyun distribution mirrors while preserving archive
signatures and the pinned Zabbly `lts-7.0` Incus source. Guest release baking separately accepts
`CHINESE_BUILD_SPEEDUP`, freezing bootstrap and pre-package APT mirror selection into the recipe.
Neither setting rebakes an existing revision; native mirror installation/baking remains unverified.
See the [Chinese host design](/architecture/incus-host-provisioning) and
[image supply](/en/architecture/incus-image-supply).

Lease-forwarding confirmation is now wired through the existing host action catalog, job/approval
boundary and host `state.json` receipts. The production enable operation remains blocked by
`forwarding_lifecycle_integration_unavailable`; this is not a released automatic Docker fix. Candidate
30-second permissions bind actual leases, instance incarnations, physical interfaces and explicit
IPv4/TCP destinations. Disable retains deny rules and distinguishes packet closure from verified NAT
connection cleanup. Unretired records block host dependency changes. Automatic renewal, full lifecycle
ownership, restart recovery and real guest/Forgejo acceptance under Docker's default DROP remain open.
See the Chinese [host provisioning design](../../architecture/incus-host-provisioning.md) and
`dev-docs/reviews/2026-09-25-incus-forwarding-permission-continuation.md` for scoped evidence.

The enable failure path now attempts one independently bounded withdrawal under the original host-state
lock, including errors after an accepted refresh, final persistence and session closure. It does not retry
installation or renewal, erase failed intent, or turn successful compensation into a successful enable.
Explicit `retire` uses the same confirmed action and requires a stopped deployment, revoked authority,
empty whole-project instance/operation inventories and an empty original bridge before removing its
receipt-owned kernel objects. Retained successful Incus operations are not treated as absent: the fixture
waits for natural expiry before an independent positive control. Production enable and full lifecycle
acceptance remain gated; see the [Chinese design](../../architecture/incus-host-provisioning.md).

Host provisioning plans now include read-only IPv4 forwarding observations: the fixed routing switch,
DROP policies in relevant nft filter base chains, and the presence of Docker's user chain. Packet counters
and raw rules are not projected or included in approval digests. An early ACCEPT, absence of a detected DROP,
or successful control-plane mTLS does not establish guest egress. Failed reads remain unverified warnings,
with no automatic Docker policy changes. A separate fresh-VM experiment uses real Docker/Incus bridges and
fixed local namespace endpoints to isolate forwarding behavior from registry, DNS and guest-image failures;
its staged native results and production-adapter limitations are recorded in
`dev-docs/reviews/2026-09-25-incus-forwarding-observation.md`.

That controlled Ubuntu 26.04 amd64 / Docker 29.1.3 run subsequently passed all nine required stages.
Packets on an actual Incus bridge hit the earlier nft ACCEPT but still incremented Docker's later default
DROP counter. Exact temporary permissions admitted only the chosen source/endpoint/port; other traffic and
an earlier explicit rejection remained denied, and withdrawing the permissions restored denial. The real
read-only observer, public archive, unchanged source inputs, normal VM exit and unchanged physical-host
baseline were independently checked. These are namespace endpoints, not booted guest workloads. No
production DOCKER-USER writer, automatic lease grant or source-spoofing/lifecycle guarantee is implied.

A fresh, explicitly routable Ubuntu 26.04 amd64 fixture passed all ten combined Forgejo stop stages and
five actual Core cases on 2026-09-25. A running workload was reclaimed before Compose removal; disabling
invalidated the managed password, reenabling preserved account identity and ran a new workflow, and an
uncertain cleanup preserved its containers, state and credential. The public archive, normal VM exit and
unchanged physical-host Docker/network baselines were independently checked. This SQLite/prepared-workspace
fixture does not establish default-DROP Docker coexistence, full CLI business deployment or production
ingress. Earlier network-timeout and inventory-check failures remain separate records in
`dev-docs/reviews/2026-09-25-forgejo-stop-forwarding-continuation.md`.

The new immutable `policy-loader-r1` passed a fresh Ubuntu 26.04 amd64 build/export/reuse, ten native
image and rootless-OCI gates, and five real Forgejo workflow scenarios. Its guest loader admits only
the fixed Podman policy rather than distribution-wide userns fallback profiles. Ordinary userns
creation remained denied, and host restrictions and outer Incus confinement stayed in place.
Original events, source-bound artifacts, split-image digests, normal shutdown and the physical-host
baseline were independently verified. This is experimental-image admission, not signed publication,
full business deployment, other-platform acceptance or the separate Core stop-lifecycle result.

The default Debian/debootstrap guest-image build requires the build distribution's official Debian
archive keyring before reserving a revision. HTTPS and downloaded package hashes do not substitute
for Release-signature verification. The fixed regular keyring or the official package's exact
same-directory `.gpg` to `debian-archive-keyring.pgp` alias is accepted after filesystem identity checks;
arbitrary links are not. An observed skipped-verification warning remains a failure even if packing
later exits successfully. These release-build prerequisites do not establish native image acceptance.

The subsequent Forgejo continuation closes a previously fixture-only internal-CA path. The controller
now validates a fixed public read-only CA input and appends it to the existing stdin token using bounded
length/digest framing; only a temporary one-job `SSL_CERT_FILE` changes. A new immutable `trust-r2` bake,
repeat reuse, nine image/engine gates and five real workflow scenarios passed on Debian 13 amd64 without
manual guest trust installation. Normal VM exit and unchanged physical-host Docker/network evidence were
independently rechecked. Full business Compose, trust inside arbitrary workflow OCI images, other platforms
and signed publication remain separate. See the
[Chinese host/image architecture](/architecture/incus-host-provisioning) and the source review
`dev-docs/reviews/2026-09-24-forgejo-runner-trust-projection.md`.

On 2026-09-24, a fresh Ubuntu 26.04 amd64 VM completed the real Core/Compose automatic projection cycle:
installed host approval, CLI init/import/render/apply, the production Hook/Provider, two non-root synthetic
consumers, existing-project isolation, activation of a second frozen deployment with stable credentials,
cleanup and rejection after host-connection revocation. Five outer stages, nine main native events and one
revocation test passed, as did eight host jobs. Normal shutdown and the physical host's unchanged original
Docker/network state were independently verified. This does not establish real Forgejo/AI Agent deployments,
bootable or signed guest images, ARM64/VM, full failure recovery or production ingress.

The compiled host recipe table is distinct from native acceptance. The three primary amd64 distributions
have independent 25-gate installed-service results, but those do not establish ARM64, complete failure
recovery or full Core business deployment. Current preflight uses the installed root/root anasd and
independently verified hostd through the shared CLI/HTTP job path; migrating TLS/state to a non-root daemon
is not a prerequisite. The `host_actions` switch remains off by default, and a preflight result alone cannot
enable compute or ingress. Older implementation snapshots below should not override these current boundaries.

Core now prepares each consumer's resources after its dependency-ordered Provider calculation and before
the consumer Hook. The target architecture from an approved host bundle therefore exists before image
resolution is frozen; a shared per-calculation conflict table and validation-before-secret generation are
preserved. Core does not gain an Incus-specific connection-file reader. The two fixed read-only image mounts
contain only verified non-secret copies with readable files/directories for the non-root Provider. Private
staging parents, original archives and secret permissions remain unchanged. Actual CLI/Compose acceptance is
tracked separately from local regressions and does not imply full business-flow or signed-image acceptance.

An empty runtime-service selection remains empty after Contract operation services and disabled services
are filtered out. Startup never passes that empty selection to Compose as an implicit “all services”.
One-shot Provider operations retain their existing `compose_run` path, and module resource/credential/ready
barriers still run. This startup rule does not change image-building selection.

The current same-artifact amd64 matrix passed on all three primary distributions: Debian 13, Ubuntu 26.04
and Ubuntu 24.04 each completed **25 installed-service gates and 18 jobs with independently observed exits**.
The tests include actual CLI/HTTPS approval, control-bridge authentication and source restrictions, natural
five-minute confirmation expiry, exact owned-package removal and repeated uninstall. The respective runs
removed 6/4/3 owned packages while preserving 327/682/667 original packages and unowned dependencies.
All archives, artifact identities, normal VM shutdowns and physical-host baselines were independently checked;
no test QEMU processes or experiment listeners remain. This does not close full business Core/Compose
deployment, unsupported-system degradation, ARM64/VM native, failure recovery, production ingress or formal
signed-image publication. The earlier 23-gate and Debian failure records below remain historical evidence.

The latest Debian 13 amd64 continuation passed all **25 installed-service gates**, including exact owned
package removal and repeated uninstall, with 18 successful jobs and independently observed systemd exits.
Both legacy null storage-pool inventories must agree and the named managed pool must be absent before
removal is authorized. The owned daemon is explicitly stopped and read back before deleting its packages;
the Debian maintainer script is not treated as service-exit evidence. Six owned packages were removed while
327 original packages and unowned dependencies were preserved. Normal VM shutdown and the physical-host
baseline passed. Other architectures, complete business Compose deployment, failure recovery and production
ingress or signed-image publication remain separate; earlier Debian preparation failures below are historical.

The Ubuntu 24.04 amd64 continuation independently passed the same complete 23 installed-service gates
and 14 successful jobs after the management-certificate POST was aligned with the base64-DER API form.
The archive, normal VM shutdown and unchanged physical-host baseline were rechecked. Debian's earlier
attempts stopped while preparing the experimental Docker package indexes, before product approval ran;
they are not evidence of either successful Debian acceptance or another Incus enrollment failure.

Initial browser session recovery now preserves a maintenance deep link while the actual HttpOnly owner
session is being restored. A system response revealing workspaces is no longer treated as final anonymous
access denial. Once recovery settles, inaccessible sections still redirect to the overview, including on
authentication failure or later access loss. Component/API authorization is unchanged. Reactive navigation
regressions and the full frontend build pass. The real embedded UI then passed eight browser gates twice:
after more than 302 seconds a new plan replaced the expired one without inheriting its checked consent,
and only a fresh explicit selection caused one confirmation and one successful apply. The second run also
passed actual QEMU reaping, normal shutdown, listener release and the unchanged physical-host baseline.
Tunnel/port-check failures can no longer bypass VM cleanup; unknown or forced exit remains a failed gate.

Latest 2026-09-23 Incus acceptance: a fresh Ubuntu 26.04 amd64 VM passed all **23 installed-service
gates**, covering real CLI/HTTPS owner, shared jobs, systemd hostd identity/exit, confirmed provisioning
and teardown, workspace/replay rejection, real five-minute expiry and execution after a fresh plan.
Non-root Docker test containers additionally verified control-bridge pinned mTLS, wrong-pin and anonymous
restrictions, and rejection from another bridge. All 14 jobs matched observed activations; experiment
resources returned to baseline and the VM shut down normally with the physical host's original Docker,
firewall and routes unchanged. This does not establish complete Core/Compose projection, browser
re-confirmation, other distributions, ARM64/VM, IPv6 or signed production publication. The module remains
developing and production ingress stays gated. Earlier passages below retain their original narrower scope.

The 2026-09-23 Incus continuation makes `restricted.devices.proxy=block` explicit for both isolation
tiers, preventing a previous `allow` from surviving project merges. Ensure tightens and reads it back;
read-only inspect rejects drift without deleting instances or repairing configuration. Local and designated
Linux Provider regressions passed. The disposable-VM lifecycle gate now also requires a direct proxy
rejection and overlapping/revoked management credentials with unchanged running guest lifetimes; these
new native cases passed in the earlier 2026-09-23 isolated Ubuntu 26.04 amd64 / Incus 6.0.5 run.
The 2026-09-22 default-container/btrfs/real one-job evidence remains valid
only for its recorded scope, not VM/ARM64, signed releases or production ingress. See
[the current Chinese host design](/architecture/incus-host-provisioning).

The next host-uninstall slice adds bounded read-only dependency checks before connection revocation:
stopped/frozen guests, pool references and volumes, attached Docker endpoints and later external daemon
objects can all block teardown. Incomplete evidence and cancellation create no effect intent. Individual
deletions still recheck ownership and absence; this is not an atomic reservation across APIs. The root
Docker client now rejects connect/disconnect/prune and all container operations. A new mandatory native
case retains a custom volume and verifies that refused uninstall preserves the working management
connection. This fixture does not establish the full distribution matrix or production approval channel.
See [section 3.4 of the Chinese host design](/architecture/incus-host-provisioning#_3-4-卸载).

The first native host-install run exposed a test-supervisor defect: an inherited 32 MiB `RLIMIT_FSIZE`
also truncated APT indexes, not just logs. The supervisor now bounds stdout/stderr through separate
pipes while retaining process deadlines and group cleanup. Large data files, log overflow and early
stream closure passed regression tests on macOS and the isolated Linux VM. The failed installation
state is retained; a clean VM must rerun the full 11-event gate. Separately, all 16 non-root broker
socket/pidfd and job-owner cases passed on the VM's 7.0 kernel; the physical 5.15 kernel correctly failed
the required `SO_PEERPIDFD` gate. Neither result is installed root/systemd approval-channel acceptance.

The subsequent host run finished package installation but hit the 30-second `systemctl` command limit;
the daemon became active later, and the failed effect was correctly retained. Service queries now keep
their 30-second budget while the fixed Incus start allows 11 minutes and relay transitions allow two.
The confirmed install action has a compiled 45-minute total budget, with matching broker waiting and a
2730-second packaged outer watchdog. Shorter caller cancellation, other action budgets and the five-minute
one-use approval are unchanged. Regression coverage includes late activation after a serialized-state
reopen: it cannot clear the failure, claim service ownership or continue configuration/enrollment.
This timeout repair does not itself establish the full native lifecycle or distribution matrix.

A later fresh Ubuntu 26.04 amd64 VM passed the complete 11-gate native backend lifecycle, including actual
package installation, configuration, pinned mTLS enrollment, idempotence, retained-volume protection and
both uninstall modes. The experiment restored its Docker baseline and the physical host's independent
container/network/service/configuration/firewall/route comparison was unchanged. The installed CLI/HTTPS
shared-job approval channel, other distributions and consumer connectivity remain separate acceptance gates.

The installed approval path now separates direct PID1 identity/exit reads from auxiliary unit-object
retention. Actual UNIX send-queue drain bounds the transition from text authentication to binary requests;
it is cancellable and does not retry methods. The auxiliary system-bus connection only holds a unit reference,
never authority or exit facts. Native socket regressions and an installed hostd preflight passed; full
CLI/HTTPS approval is checked separately with exact cross-workspace and consumed-token rejection codes.

The next isolated native run passed official package installation, then failed relay readback. The source
configuration was below root-private `/etc/anas`, inaccessible to the non-root service. Packaging now projects
only that public file read-only into the service mount namespace, preserving the private directory and empty
capability set. Route-netlink is allowed solely for interface readback; the exact gateway source supports the
separately firewalled, pinned mTLS host probe. The Ubuntu 26.04 recipe also records the split `incus-base`
daemon package explicitly, and helper installation cannot adopt an external daemon. These repairs need a
fresh full native run; neither the retained failed state nor production publication gates are cleared.

The isolated Incus daemon continuation fixes a real synchronous-create response mismatch: POST may return
HTTP 201 with a successful synchronous 200 envelope; reads and other methods do not inherit that allowance.
Independent resource readback and ownership checks remain required. A new explicit lab harness runs the
actual Unix client and Provider rejection/pinning checks with an extracted distribution daemon in private
mount/network/PID namespaces and ephemeral state. It does not install host packages or launch guests, and
does not prove supported-volume quotas, 7.3.0 compatibility or production deployment. See
[the Chinese host design, section 7.16](/architecture/incus-host-provisioning).

The 2026-09-21 native Incus readback repair uses symbolic nft protocol output and rejects ambiguous numeric
EtherTypes. A read-only kernel GETRULE observation supplies the original interface index, bound to complete
JSON by an unchanged GETGEN generation, table, chain and rule handle. Device names never supply replacement
indices. Exact redundant protocol dependencies and split IPv4 policy prefixes are handled without widening
the remaining ordered checks. Namespace fixtures restore their creator thread and model cross-namespace
veth peers. Eight mandatory native cases, three shuffled repetitions and the host-network package passed on
the designated Ubuntu host; these do not establish production guest/Traefik/Docker acceptance. Publication
remains disabled. See [the Chinese design, section 7.15](/architecture/incus-host-provisioning).

The 2026-09-21 workspace-launch continuation joins host-only readers, the existing coordinator and the
cross-process workspace fence. The running host-action service supplies the owner context and observation
invoker; a private delivery cannot select a different scope. Startup checks separate request, credential,
journal and route directories, pin directory/file identities and the renderer bytes, and recheck them before
writing a lifetime marker. Lost consumer inputs revoke publication without invalidating the independent
retirement reader. Local tests include pinned HTTPS and an empty-request lifecycle, not real UID/mount,
Incus or Traefik acceptance. Production installation and publication remain disabled. See
[the Chinese host design, section 7.14](/architecture/incus-host-provisioning).

The 2026-09-21 Incus mediator slice wires a credential-free host observer into
private reader delivery and the existing WorkspaceSource/Traefik assembly. Host
and direct Incus modes are exclusive and never fall back to each other. A managed
ControllerService retains its exact journal/flock and old readers after failed
drain; explicit retries only retire old targets. Readers close only after confirmed
cleanup. This is an internal lifecycle owner, not an installed production daemon.
Configuration-change coordination, host network actions, health, UID/mounts and
real-host acceptance remain outstanding. See the normative
[Chinese design, section 7.10](/architecture/incus-host-provisioning).

The 2026-09-21 observer-configuration slice adds `incus.ingress.observer.plan` and
`incus.ingress.observer` through the existing shared confirmation/job/audit path.
The host derives a container-only scope from its enrolled connection and active workspace;
callers select refresh or disable, not credentials, paths, versions or authorization snapshots.
Pending/committed/disabled records live in the existing host state, with readback, bounded
recovery and tombstones. CLI/HTTP use the observer phase. This does not start the production
mediator, drain network rules or establish native acceptance. See the normative
[Chinese host design, section 7.9](/architecture/incus-host-provisioning).

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
- [Incus host provisioning, ingress, and guest image baking](/architecture/incus-host-provisioning) — dedicated control bridge with fixed-destination TLS pass-through; ingress uses the lease bridge ACL with two tiers, HTTP publication through Traefik, and Docker-style port bindings; no proxy devices or network forwards. Host actions and frozen image supply are wired in code, but complete production ingress and real-host acceptance remain pending. Image declarations use mutually exclusive catalog/name/revision or fingerprint objects; deployment digests are frozen. The Chinese source is normative;
- [runtime artifacts, releases, and persistent state](/architecture/runtime-release-state-design);
- [configuration and state lifecycle](/architecture/config-state-lifecycle).

The Chinese source documents remain normative while further English translations are prepared. Stable machine-facing behavior is separately defined by the [CLI contracts](/en/reference/contracts/).

**Update 2026-09-30.** The host action channel no longer proves which root process is at the other end
of its root-only socket: anasd runs as root, so that proof was not a boundary. The fixed private broker,
the PID 1 private-bus unit checks and the systemd exit observation are removed, together with the
`godbus` dependency. hostd now admits only a root/root peer, binds its installation policy (v3) to the
release alone, and keeps its own invocation record: an invocation id can begin only once, and the
terminal is written before the terminal frame is sent. When a stream ends early, anasd reads that record
through the read-only `host.invocation.status` action. The Incus forwarding-permission and ingress
observer actions were deleted on the same day. Linux/systemd acceptance has not been rerun on the new
implementation. The rest of this section is the historical record.

The [host action channel design](/architecture/host-action-channel) now has internal read-only
primitives (2026-09-19), not an installed root service. `internal/hostaction` recognizes only the
installation-preflight subset of `incus.status`. An already accepted Linux Unix stream is checked
using kernel peer credentials and a fixed installation UID/GID policy before strict action-ABI input
is read. There is no caller-supplied command, path, identity, plugin registry or write-action handler.
Only actual primary GID membership is currently recognized; supplementary-group admission still
needs a reviewed integration. Execution reuses the existing audit writer, auditing before observation
and before returning a provisional result; audit completion failure yields unknown. The shared job
store/recorder must still own identity, sequencing, process completion and recovery. No listener,
root binary, activation unit, production CLI/HTTP execution path or destructive confirmation was installed.

The next 2026-09-19 slice adds fixed-path, root-owned installation policy and accepted-fd validation
for per-connection systemd activation. Version/commit, exact fd markers, socket family/type/address,
file ownership, modes, links and held directory identities are checked independently of request data.
`Activation.Serve` requires a job binding and audits rejected peer/input without trusting payload ids.
`jobexecutor.HostJobBinding` retains the existing store's execution lease, matches a running read-only
job and frozen release, and reauthorizes its persisted actor before one synchronous invocation. It
does not write completion or treat socket EOF as successful process exit. The root-side authenticated
broker transport and private listener/routing are implemented as described below. The latest slice adds
independent exit observation and recorder integration; production service migration and native acceptance
are still missing. Root must not open user-writable job storage.
Descriptor cleanup failures also return an error after any provisional frame; the launcher must not
report a clean exit merely because a candidate result was written.

`anas host actions [--json]` is now available as a **local compiled-client inventory**, explicitly
reporting `installation_verified: false`. It does not connect to the socket or enable pending write
actions. The existing `anasd.service` runs as root/root, incompatible with the non-root peer policy;
the service account and permission migration remain an explicit deployment blocker, not a reason to
weaken peer checks. See [the detailed source, section 8](/architecture/host-action-channel) and
[the CLI contract](/en/reference/contracts/commands#host-actions).

The broker slice connects `Activation.ServeBrokered` to `HostJobBinding.ServeBroker` through one fixed
private Unix endpoint. The root executor checks the broker's exact PID/UID/GID against the original
request peer; the non-root owner admits only a kernel-authenticated root peer. Socket-bound
`SO_PEERPIDFD` handles pin both processes without a numeric-PID lookup fallback. Unsupported kernels
fail closed. Root checks the private socket directory at connection time and never reads the owner's
job files, scripts or executables.

The activated executor also pins the original request socket's process through the complete exchange.
It checks that handle before and after the callback; a recycled numeric PID on the broker connection
cannot substitute for an original caller that has exited.

The bounded canonical handshake carries the existing action request, frozen release, original peer
and a per-session nonce. It neither creates a job store nor substitutes for destructive confirmation.
The owner checks the running job and current actor rights before granting execution and again before
validating completion. After any possible grant, `Close` retains the execution lease until the pinned
executor process has terminated, even if the handshake succeeded or the connection disappeared.
That observation provides no exit code, child-process-tree evidence or successful job outcome. Only
the current childless, read-only preflight is supported; write actions remain unavailable.

`test-host-job-broker-native.sh` provides a non-root Linux socket/subprocess regression gate that
rejects missing or skipped key tests. Native execution, actual root peers, systemd activation, service
identity migration, service assembly and independent exit-status integration remain separate release
gates. See [the Chinese design, section 9](/architecture/host-action-channel).

`OpenJobBrokerListener` now creates only the fixed socket in a preinstalled private directory, holding
an exclusive directory lock and checking identity while idle and during cleanup. It neither takes over
stale entries nor removes replacements. `HostJobBroker` routes to pre-registered running jobs in the
same Store/ExecutionLease, with at most 32 bindings and 8 concurrent connections. Socket input cannot
register a job; retiring one needs durable terminal state plus remote cleanup. Post-grant uncertainty
stops admission without releasing the remote process pin. Owner shutdown waits for connection workers;
request/subscription cancellation does not own this lifetime. The native regression script now checks
listener and owner-routing cases and is wired into Go CI; this is not a recorded CI or native pass.
The current installed-service and plan/approval/apply integration supersedes those earlier gaps.
The existing root/root anasd identity is retained; no non-root service migration is required.
See [section 13 of the Chinese design](/architecture/host-action-channel) for the current boundary.

The latest slice pins PID 1's unique D-Bus identity, the exact host unit, its invocation and live process
before any broker grant. A unit reference retains exit evidence through service completion. After the
socket-bound process terminates, the observer requires a matching terminal unit, actual main-process exit
status and an empty unit process inventory, then rechecks identity. Missing properties, manager loss,
restart, signals or cleanup uncertainty cannot validate a successful executor frame. The fixed systemd private D-Bus
transport uses `github.com/godbus/dbus/v5 v5.2.2`, not caller-supplied addresses or systemctl subprocesses.
Its exact bus/permission model still requires native verification on each supported distribution.

`HostJobBroker.ExecutePreflight` connects prior running-job registration, the fixed activation request,
broker completion, real output EOF and independent exit evidence to the existing `ActionRecorder` and
single job journal. Final role and control-state checks precede commit. Unconfirmed process cleanup
persists the existing containment barrier and retains execution ownership; reopening a broker is not
recovery. Only canonical, recomputable preflight data and fixed error messages may enter the recorder.

`cmd/anas-hostd` now implements one-request activation, fixed root audit storage and compiled inventory.
It, fixed socket/service units and the non-root control relay are built and installed with the same
release identity. The registry now includes Incus plan/confirmation/apply operations. Package and account
operations need full root and explicit system-tree writes; this is not a DAC-only sandbox. Fixed peer/
unit identity, one-use approval, parameter validation, audit and independent exit supervision remain
mandatory. The installer checks that host actions have drained before replacing binaries. Native
systemd, actual-root and Incus acceptance is still outstanding.

`HostActionService` now connects the existing broker to an optional daemon-owned queue and
`POST /api/v1/workspaces/{ws}/host/actions/incus.status`. It shares the existing job store, execution
lease, authentication and audit. The request accepts only `{}`, requires full/TLS/owner and normal
session/Origin/CSRF checks, and returns queue admission rather than completion. In-flight requests
coalesce; disconnect does not cancel execution; only queued preflights can be explicitly cancelled.
Actor checks use current local owner/proxy state and never renew credentials or claim real-time IdP
revocation. Recovery continues to record a daemon-restarted barrier even when the option is disabled.

`host_actions` defaults to false. The installed caller remains root/root anasd and its existing
root-only TLS and state policies are unchanged. CLI and Web use the same HTTPS job queue. The UI shows
server-generated plans with typed inputs and explicit consent; expired plans refresh without applying
and require renewed consent. No token/parameter-JSON paste controls or new root-password path are used.
The five-minute approval ledger stores digests, survives daemon restarts within the same boot and is
independently claimed at execution. Complete native installation/network acceptance remains pending.
See [section 13](/architecture/host-action-channel) and
[service configuration](/en/reference/anasd-service-configuration).

`internal/incushost` and the unprivileged `cmd/incus-host-preflight` provide the corresponding
compiled Debian 13 / Ubuntu 24.04 / Ubuntu 26.04 recipe table and fixed-path OS inspection. They do
not source shell, invoke package managers or connect to a daemon that a socket could activate.
Exact release matching does not use `ID_LIKE`; canonical os-release fallback/link handling is bounded
and verifies root-owned non-writable ancestors. Default container isolation never changes implicitly;
explicit VM selection is preserved when KVM is absent. `--skip` requires no OS-file observation.
Recognized packages do not establish runtime compatibility: every report keeps `compute_ready` and
`runtime_verified` false. Package-origin verification, daemon compatibility, installation/ownership,
networking and real-host acceptance remain outstanding. Local unit/race tests passed, while Linux
native peer/filesystem execution is tracked separately. See the [Chinese source, section 2.1](/architecture/incus-host-provisioning).

Incus image declarations use structured objects and freeze targets, catalogs and fingerprints in deployments.
The shipped catalog is empty. An explicit release-side bake command now exists, but real bakes, automatic
signed release distribution and production ingress remain pending. Frozen local artifact supply and
Provider multipart import are implemented but not validated against a real Incus guest. A lab-only HTTP network artifact generator
exists. Its Linux namespace HTTP checks passed on 2026-09-11, but actual Docker/Incus guest acceptance has
not been recorded.

The 2026-09-18 staging preflight adds explicit matching-source checks without invoking Docker:
`check-shared-build --source-root ... --staging-root ...` requires an absolute
`ANAS_SHARED_BUILD_CONTEXT` and compares present module build trees, shared input bytes and executable
bits. It does not validate a complete deployment manifest or prove an image build succeeds.
Offline image inspection reuses the shared `ArtifactRelease` representation through
`DescribeArtifactRelease`/`VerifyArtifactRelease` and `cmd/compute-image-artifact` (split files only
for the CLI). Verification requires an independently trusted fingerprint and target, rejects changed
parts and version/recipe bindings, and never treats the descriptor as its own trust source. It does not
bake, import, publish, validate catalog signatures or establish bootability. Local macOS arm64 regressions
now pass; M8b/M12/M13 native acceptance and production ingress remain pending. See the
[Chinese design, section 6.2.2](/architecture/incus-host-provisioning).

`ArtifactArchive` and `cmd/incus-image-artifacts` add explicit local init/record/inspect/catalog
operations using that same representation. Private, locked archives publish content-addressed objects
before immutable revision metadata; identical original bytes can restore missing objects, while
conflicts, corruption and unknown metadata are preserved and rejected. Candidate catalogs require
explicit previous trusted history or an explicit first release. These commands emit metadata only and
do not rewrite the shipped catalog, run a builder, sign/distribute images, import into Incus or prune.
Recipe-file hashing alone does not prove build provenance. Archive/CLI regressions pass locally;
M12/M13 remain unaccepted. See section 6.2.3 of the same design.

The separate `incus-image-artifacts build` command now integrates `ArtifactArchive.BuildOnce` with a
digest-pinned distrobuilder ELF on an isolated, disposable native Linux root builder. Preflight precedes
revision reservation; execution uses a sealed memfd, explicit environment and fixed split-output flags.
The archive lock covers admission, build and commit. A durable recipe/builder attempt blocks silent
retry after interruption, while a committed revision is verified and reused without rebaking missing
bytes. Recovery uses verified original outputs, not a changed same-name build. Recipes remain root-capable
code; process-group cancellation does not prove mount/external-resource cleanup. The CLI emits metadata
only and is not registered as an apply, Provider or host action. Portable orchestration tests pass, but
default Forgejo runner recipes and Provider supply/import code are now present, while actual bakes,
native sealed-ELF execution, signing/distribution, guest acceptance and destructive pruning remain pending.
The automatic host bundle carries the observed target architecture and owned pool; per-resource control
network projections connect the Provider and compute consumers without replacing their business gateway.
See section 6.2.4 of the [Chinese design](/architecture/incus-host-provisioning).

The same verification fixed canonical certificate configuration-to-wire projection, endpoint sensitivity,
consumer connection-value redaction and administrative redirect/error handling. New HTTP policy/planner
and executor tests cover frozen authentication, namespace conflicts, failed-step compensation, retained
address holds, orphan inventory and stale reservation rejection. These are local unit/transaction tests,
not native network acceptance; production ingress remains disabled.

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

Incus host provisioning update (2026-09-20): confirmed image prune is wired in code;
ingress has v2 ownership receipts and an opened-netns executor. Allocator/health identity,
production wiring, signed images and real-host acceptance remain incomplete. See the
[Chinese design](/architecture/incus-host-provisioning) and [image supply](/en/architecture/incus-image-supply).


The 2026-09-20 device-bound address-routing candidate adds a host-only policy table, permanent neighbors,
terminal denial, explicit allocation receipts and normal failure withdrawal. It is not DHCP reservation or
VM/TAP support, and does not yet prove reverse-connection safety or real Docker/Incus packet flow. Native
FIB tests are registered but not executed here; production ingress remains disabled. See
[the Chinese design, section 7.5](/architecture/incus-host-provisioning#_7-5-设备绑定的地址路由保护-候选实现-生产关闭).


The reply-side candidate now adds an original-ifindex/MAC/port bridge gate, atomic inet/bridge permission
updates and exact bidirectional conntrack cleanup after verified revocation. Native packet and kernel-record
tests are registered, not executed here. Full lifetime/ifindex-reuse guarantees, real Incus identity supply,
VM/TAP, health, service wiring and production acceptance remain outstanding; publication stays disabled.
See [the Chinese design, section 7.6](/architecture/incus-host-provisioning#_7-6-回复物理来源与双向连接清理-候选实现-生产关闭).

The 2026-09-21 observation/lifecycle slice binds the entire installed grant and the instance's own
managed/workload labels, rejects selected-field JSON case aliases, and revalidates identity after route
consumption. The internal v2 host projection binds each call to a fresh observation ID and rejects stale
responses and cancellation races. Local tests combine pinned mutual TLS, the actual controller and durable
file journal with synthetic daemon data and explicit host/renderer/probe adapters. They verify retirement
on pause/stop, fresh reservations on recovery, and retained cleanup ownership on failure. These are not
server-enforced read-only identity provisioning, a registered root observation handler, native networking,
VM/TAP or production service acceptance. No global authorization routing was changed and publication stays
disabled. The [Chinese design, section 7.7](/architecture/incus-host-provisioning) is the normative account.

The 2026-09-21 narrow-host-observation slice registers `incus.ingress.observe_http` in the existing
compiled host-action channel. A protected installed scope, active Core snapshot, root-only bundle,
loopback mTLS and native veth checks feed a selected v3 projection. The mediator receives no Incus
credential. Shared-job admission/recovery/execution bind scope to the authorized workspace, and
each observation uses a new job/nonce rather than cached results. Local boundary tests are not
native acceptance. Automatic scope delivery/rotation, mediator installation, VM/TAP, health and
continuous network-lifetime safeguards remain outstanding; production publication stays disabled.
See [the Chinese design, section 7.8](/architecture/incus-host-provisioning).

The 2026-09-21 image-supply update adds complete historical bundle export through pinned output
directories, per-reference metadata validation before physical deduplication, and cancellation-aware
staging. The release script uses the same bundle entrypoint and validates history before baking.
Provider readiness now checks the complete managed project fence and live network/profile/certificate/image
dependencies; revoked trust cannot remain ready. These local fixture checks do not establish actual
guest boot, daemon enforcement, signed distribution or production ingress. See
[image supply](/en/architecture/incus-image-supply) and the
[Chinese host-provisioning design](/architecture/incus-host-provisioning).

The 2026-09-21 configuration/drain continuation wires the shared host-action queue to a per-workspace
ControllerCoordinator. Confirmed configuration jobs wait without occupying the root executor, keeping
readonly cleanup dependencies runnable. Launches remain fenced through drain and confirmed job completion;
failed drains retain their original owner and unknown execution does not release the fence. Normal service
shutdown keeps the broker and execution lease alive until drain succeeds; retry is explicit and never
reopens publishing. Local tests use the real controller, file journal, queue and confirmation ledger, but
synthetic network/root adapters. This is not production launcher installation, cross-process crash recovery
or native acceptance; publication remains disabled. See
[the Chinese design, section 7.11](/architecture/incus-host-provisioning).

The workspace-backed controller now uses the existing runtime lock for cross-process exclusion. A durable
nonempty fence pins the original HTTP journal directory until confirmed drain; process death releasing flock
does not authorize standalone credential rotation. Runner writers check the actual descriptor before recovery
or credential effects; readonly observers remain available. Missing/corrupt journals and substituted directories
cannot be treated as empty installations. This is a cooperating-writer protocol, not a new RPC or an automatic
CLI drain request. Real subprocess tests exercise file locking and crash evidence with synthetic network
adapters. Production launch, UID/mount setup, health, VM/TAP and native network acceptance remain outstanding.
See [the Chinese design, section 7.13](/architecture/incus-host-provisioning).

The workspace-mutation continuation shares one coordinator between anasd's legacy deployment/maintenance
workers and the host-action queue. Mutation jobs remain queued while old controllers drain, leaving slots
and workspace locks available to cleanup dependencies. Audit and durable actor/request bindings are checked
before drain and execution. Unstarted failures commit an atomic rejection; missing terminal commits or pending
compensation retain the fence. Bootstrap/enrollment jobs remain restricted to their original apply transaction.
The new queued-to-failed journal transition can be rejected by older readers; unrestricted downgrade is not
claimed. Standalone CLI credential rotation, other processes and production startup remain outside this
integration. Publication stays disabled. See
[the Chinese design, section 7.12](/architecture/incus-host-provisioning).

The September 22 client continuation freezes private CLI configuration alongside TLS files and replaces
repeated `remote add` calls with read-only project listings. The initialization lock separates exclusive
creation from opening an existing entry; disappearance does not authorize lock recreation. Forgejo saves
uncertain-create and retirement intent before effects and uses independent bounded cancellation cleanup.
The three shared builds now include `securefs`, with transitive-import checks and reduced-input offline
compilation. These checks do not establish guest lifecycle, one-job execution or Docker build acceptance.
See [the Chinese design, section 7.17](/architecture/incus-host-provisioning).

The lifecycle continuation explicitly adds `dnsmasq-base` to the host package recipes: the real
Ubuntu 26.04 bridge creation failed when `--no-install-recommends` omitted this helper. A disposable
QEMU lab now exercises the actual Provider, restricted two-lease container lifecycle, btrfs disk
limits, stdin and independent cleanup after cancellation without using the physical host's Docker.
The tiny measured fixture is not a released distrobuilder/Runner image. ZFS, product VM workloads,
one-job execution and production ingress are not inferred from these results. See
[the Chinese design, section 7.18](/architecture/incus-host-provisioning).

The default Runner recipe now copies the frozen `sources/forgejo-runner` input. Actual distrobuilder 3.2
reproduces the old-path failure and confirms the corrected packed bytes. Cancelling a real bake leaves its
revision blocked against silent retry. The complete Debian/Podman bake and baked-image boot/engine gate
have not passed; the native copy fixture and Runner CLI help are not a real one-job or signed release.
See [the Chinese design, section 7.19](/architecture/incus-host-provisioning).

Subsequent Runner work completed immutable container-image bakes and real engine/user-session admission.
The container Provider now permits the inner namespaces required by OCI runtimes in its fixed profile,
while preserving project-level unprivileged, raw-config, host-device and managed-network fences. This is
distinct from nested virtualization and changes the permitted shared-kernel operation set; untrusted or
cross-trust workloads still require explicit VMs. Native positive and rejection controls, actual workflow
execution and recovery are separate acceptance stages. See [the Chinese Forgejo design](/architecture/forgejo-module-design),
section 4.3, and the dated acceptance review for exact executed scope rather than the earlier build snapshots.
