# Incus HTTP network prototype

Status: lab artifact generator; full Incus host acceptance is incomplete and no production ingress integration.

Run `go run ./cmd/incus-network-prototype --input test-env/fixtures/incus-network-prototype/observation.json --out /tmp/anas-incus-http-lab` with a new output directory. This writes `firewall.nft`, existing `ANAS_TRAEFIK_ROUTE__*` fields in `traefik.env`, and typed route/revocation/conntrack arguments in `operations.json`. It executes no host commands. The fixture is synthetic and must never be treated as a live allocation.

Use only a disposable Linux Docker/Incus host. Record daemon/kernel/Docker/nft versions, the exact image fingerprint, architecture, tier and code revision. Save existing firewall and route state, and ensure the two `anas_incus_lab*` tables do not already exist. Prepare a running guest with HTTP on its NIC and reserve its address for the entire permit lifetime. Capture project ownership, instance UUID/state, NIC/MAC/allocation, Docker bridge and Traefik host veth from trusted host tools. Check all subnet conflicts, including LAN and VPN. The generator checks consistency of observations, not their authenticity.

Check the generated rules with `nft --check --file <output>/firewall.nft`. An early nft accept cannot override a later Docker/Incus drop; do not insert a global accept or disable existing firewalls. Record any ordering blocker. Apply reviewed lab rules, execute `AddRoute` inside Traefik's network namespace and verify the selected source/interface with `ip route get`. Verify the backend first, then use the existing Traefik entrypoint renderer in an isolated lab instance. The prototype allows only HTTP and `.example.test` hosts.

Permits expire after 30 seconds. Re-observe identity and state before repeating; never renew from stale consumer requests. Test both container and VM tiers, permitted HTTP, denied ports, other Docker/LAN sources, same-bridge source-IP/MAC spoofing, reverse guest traffic and IPv6 bypass. Record exit codes, counters and packet traces.

Revoke the Traefik route first, remove the permit, then delete matching conntrack entries. Verify an established stream stops before releasing the guest address. Only then reassign the address and check that the old route cannot reach its new occupant. Keep both deny tables after withdrawal. Stop/disconnect all lab endpoints before removing the owned tables and other experiment resources; never flush the host ruleset. On partial failure stop probes and perform the same cleanup.

The new `--mediation` mode reads a lab request directory and applies frozen authorization. Host actions, production directory registration/read-only identity and lifecycle reconciliation/watchers remain unimplemented. TCP as HTTP transport is not general TCP publishing; TLS TCP, raw TCP, UDP and LAN remain unimplemented. Public HTTPS/IPv6 is not validated by this fixture. The local development machine has no usable Docker daemon or Incus. On 2026-09-11, the operator-selected Ubuntu 26.04 host passed the isolated namespace checks below, including real nft loading and HTTP packets. It lacks the Incus/KVM setup needed for managed-guest acceptance. Record real evidence in the Incus plan, never infer it from unit tests.

The source guard uses bridge `input`: `ibrname` is limited to bridge input/forward hooks, per the [official nftables troubleshooting reference](https://wiki.nftables.org/wiki-nftables/index.php/Troubleshooting). Actual loading in an isolated namespace has passed; coexistence with Docker/Incus rules remains untested. This lab filter drops other inbound traffic to the guest bridge; it is not a general guest egress firewall.

## Linux namespace data-plane checks

`test-env/scripts/server-incus-http-netns.py` is a manual remote lab entry point. It requires Linux, root,
Python 3, `unshare`, `nsenter`, `ip`, `sysctl` and `nft`. It never calls Docker or Incus. Explicitly select
the test host for the current run, copy the script and generated fixture artifacts into a new run-specific
temporary directory, and run there:

```bash
sudo python3 /tmp/<run-directory>/server-incus-http-netns.py --artifacts /tmp/<run-directory>
```

The script creates a fresh network namespace and rejects pre-existing links, routes or rules. Every
bridge/veth, listening port, HTTP probe and nft rule remains inside that namespace or its children;
no globally named netns is created. Child processes are cleaned up and have a 90-second lifetime limit.
The outer process also compares stateless host nft digests and fails on a change. This manual script
does not claim to pass the separate M2 helper/least-privilege remote preflight.

Six positive controls establish connectivity before loading the actual generated rules. The checks then
cover permitted HTTP, denied ports, other-bridge and same-bridge peers, IP/MAC spoofing, reverse guest
initiation, and IPv6 backend denial. A real streaming HTTP connection must stop after tuple revocation;
new connections must fail. A fresh fixed-fixture permit must expire after 30 seconds. These tests do not
implement TCP/UDP publishing or validate public Traefik, managed IP reassignment or conntrack deletion.

On 2026-09-11 all 20 namespace checks and the unchanged-host-rules check passed; see the
[experiment record](../../../dev-docs/reviews/2026-09-11-incus-http-netns-validation.md). Real Incus guests,
identity checks, cross-lease fences and full revocation still require separate acceptance. Core's independent
`lease_secret` is implemented, but this experiment never reads or generates a real lease key.

## Observation capture and lifecycle plans

The optional `--capture capture.json` mode reads explicitly configured, isolated Linux Incus/Docker Unix
sockets with GET and queries host/Traefik network links. It checks project/network ownership, instance
state, NIC/MAC/lease allocation, Docker endpoint and reciprocal veth indices, then compares two samples.
Known default sockets/default Docker bridges are rejected. The administrator must still provide dedicated
daemons; the path check alone does not prove isolation. Complete Docker environments and credentials are
not persisted. This is a privileged lab observer, not a production read-only daemon identity.

A new output directory is mandatory (0700, files 0600, exclusive creation). Administrator inputs are
bounded at 64 KiB and reject final symlinks/devices, duplicate/unknown fields, case aliases and trailing
JSON. Outputs include observation.json, optional capture-evidence.json, publication.json and lifecycle.json.
A publication records a proposal, not proof of application; reobserve_after is advisory, not an executor.

`--previous` requires the record of the one lab route actually applied. Same-topology updates retire it
first, then replace both owned tables in one nft transaction using firewall-replace.nft. Changed lease,
Docker endpoint or network topology generates withdrawal only and requires ending the old lab session.
`--withdraw --previous` produces cleanup without capture. A failed capture or unusable current observation
with a valid previous record also produces withdrawal only. Malformed inputs abort. Exit 0 means a plan was
written; inspect lifecycle.json.action before any operator action.

Withdrawal removes the route, permit, exact conntrack entries and /32, then confirms retirement while
retaining both deny filters. Hold the guest IP until all cleanup is confirmed. Only stop/disconnect of all
lab endpoints permits final filter teardown. HTTP probes, renderer calls, real IP holds and cleanup readbacks
remain operator steps. Tests for these additions are deferred; earlier namespace results do not cover them.

## Authorization and request mediation (new, testing deferred)

`--mediation <administrator-file>` combines with `--input` or `--capture`. For a revoke request, combine
it with `--withdraw --previous`. It only generates plans; capture still performs its documented read-only
queries. Consumer requests contain only action, instance_id, workload_id, guest_port and optional label.
They cannot choose a lease, hostname, IP/URL, auth, middleware or entrypoint.

mediation.example.json is synthetic administrator input. Its authorization matches the shape of deployment
resource compute_ingress; active_deployment is the operator's assertion, and request_directory/request_file
bind one directory to that lease. This fixture is not proof of an active deployment. Obtain matching frozen
authorization from the actual test deployment. Keep the parent/mount administrator-owned and expose only
that lease's request directory to its consumer; authorization, naming-key files, other leases and Traefik
output must remain outside consumer write access. The workspace mode below adds Core reads/registration; automatic mounts remain unimplemented.

The example uses named mode with prefix incus and label http, deriving incus-http.example.test. Its default
`auth: none` provides no access control: SNI, Referer and logs can disclose URLs; do not publish sensitive
or writable services. request.example.json is a complete request to install by atomic rename. On Linux
amd64/arm64 the reader pins a flat filename with O_PATH, rejects symlinks/hard links/devices/FIFOs, then reads
the same regular inode through trusted /proc/self/fd. It caps requests at 4 KiB and rejects changing content,
duplicate/unknown/case-aliased/null fields. This mode explicitly refuses macOS.

```bash
# Generation only; prepare administrator-owned inputs first. Not run this turn.
incus-network-prototype --input /var/tmp/anas-lab/observation.json \
  --mediation /var/tmp/anas-lab/mediation.json --out /var/tmp/anas-lab/mediated
# Set action to revoke and ensure previous represents the currently applied lab route:
incus-network-prototype --withdraw --previous /var/tmp/anas-lab/mediated/publication.json \
  --mediation /var/tmp/anas-lab/revoke.json --out /var/tmp/anas-lab/revoked
```

Fixed/named modes need no naming-key file. Random mode requires lease_secret_file in the administrator
configuration: a JSON string containing canonical base64 from this grant's independent naming-key record.
Keep it outside the request directory and readable only by the operator. The tool neither generates keys
nor persists their paths/values. This is not a production Secret Store adapter. Random uses 128 bits of
HMAC-SHA256; the client certificate bundle cannot substitute for the key.

Observation identity must match the grant. Host, allowed ports and auth come from authority; guest_port
comes from the authorized request. publication.json.mediation records proposed UUID/workload/host and a
reservation token, never the key. ForwardAuth preserves the frozen middleware and adds operator steps to
check provider ownership and unauthenticated denial before route installation; the tool does not perform
those checks. Output uses the existing ANAS_TRAEFIK_ROUTE__INCUS_LAB__* renderer ABI. The lab entrypoint
remains HTTP with TLS=false, providing no claim about production HTTPS authentication.

The planner reserves names in memory for one process. This CLI provides no durable global lock, background
reconcile or production route registry. Previous cleanup always precedes the new publication; generating a
reservation does not prove a real host/IP hold. Rejected observations with a same-lease previous record
produce only withdrawal, whereas invalid requests/grants fail before capture. Mediated revoke matches the
recorded instance/workload/port/label without requiring a Running guest. Administrators can still use plain
--withdraw for retired grants or missing naming keys.

Build gates, unit/file-attack tests and server acceptance are deferred; the Chinese [pending test list](e2e-plan.md)
is the development source. No new-path success is inferred from the earlier namespace experiment.

## Registration and reads from active Core state (new, untested)

Workspace mode replaces offline active_deployment/authorization/request_directory/key-file input.
mediation-workspace.example.json shows the shape. Workspace and registry root are administrator-selected,
never consumer request fields. The reader validates active state and frozen grants under Core's shared
runtime lock. Directory registration binds the resolved workspace path, deployment, activation time and
manifest digest. Switching/moving requires a new registration root; old directories do not renew authority.

```bash
# Creates only a new private registry root, empty lease directories and registry.json.
# The normal ingress startup guard remains. Positive cases use separate metadata fixtures;
# do not edit actual active deployment state to bypass the guard.
incus-network-prototype --register-requests /var/tmp/anas-lab/workspace \
  --out /var/tmp/anas-lab/request-registry
# After the operator places an atomically written http.json in the registered lease directory:
incus-network-prototype --input /var/tmp/anas-lab/observation.json \
  --mediation /var/tmp/anas-lab/mediation-workspace.json --out /var/tmp/anas-lab/core-mediated
```

Keep root/registry.json outside consumer access; later mount only the selected lease directory. Root and
lease directories use 0700, registry.json uses 0400 and contains no Secret. Existing targets are refused,
and recorded epoch/directory mappings must exactly match fresh Core authority. Directory device/inode
identities must match registration; same-named replacements or swaps are refused. Only example.test grants
are accepted, so registration cannot enable production ingress.

Random mode uses the Core adapter to return this lease's original independent naming key, rejecting file
source overrides. It remains an administrator process with workspace access, not permission to mount the
Store into a production mediator. Fixed/named modes do not read naming keys. The authority reader never
reads the Store; only random-key resolution invokes the existing Store parser. Authority is rechecked after
capture and losing it produces only previous same-lease withdrawal. If inactive/unreadable Core state or
stale registration prevents loading, administrators retain plain --withdraw for the recorded lab route.

These commands, real registration, real Secret Store reads and tests have not been run. The pending list
covers permissions, locks, registration failures, switching/restoration, replay and actual mount isolation.
There is no background consumer, global route lock or lifecycle watcher. Revalidate authority and observations
before real execution; old disk requests must not automatically restore access.

## Probe identity and fixture preparation (new, unrun)

`--capture-probe` and `--prepare-fixtures` are mutually exclusive administrator lab modes. Both require
a new output directory and cannot be combined with other modes. The code has not been compiled/tested;
the examples below describe later work and were not executed. Production ingress protection stays in
place; positive cases use isolated metadata fixtures, never edits to actual active state to bypass it.

The container/network IDs in `capture-probe.example.json` are placeholders. Replace them with full
64-character IDs selected by the administrator. The collector must already share Traefik's netns and
have a procfs view of the PID returned by Docker. It does not enter namespaces or launch processes.
It reads only the independent lab Docker socket, still rejects default system sockets, checks the
selected bridge allocation, and samples actual procfs/nsfs and an unbound socket on one locked OS
thread. Selected Docker mappings and kernel identity are compared before and after capture.

```bash
# After placement in the selected lab Traefik netns; not executed this turn.
incus-network-prototype --capture-probe /var/tmp/anas-lab/capture-probe.json \
  --out /var/tmp/anas-lab/probe-capture
```

`probe-identity.json` contains capture time, container/network/endpoint IDs and `identity`: PID, start
ticks, boot ID, namespace device/inode/cookie and source IPv4. No socket paths, certificates or keys are
included. This is an administrator observation, not a credential or production grant. Runtime probes
still check the process and each socket. Production launch assembly remains pending; do not give the
mediator the lab Docker administration socket to use this path.

`prepare-fixtures.example.json` selects an existing active lab workspace, request registry, private
Core-delivered reader configuration, shared executor state directory and trusted renderer. The reader
configuration contains sensitive values and must come from trusted preparation, never consumer input.
Prepare a private state directory owned by the runtime user, and retire outstanding publications first.

```bash
# Reads, holds the state lock and prepares files; no Probe, HostActions or route publication.
incus-network-prototype --prepare-fixtures /var/tmp/anas-lab/prepare-fixtures.json \
  --out /var/tmp/anas-lab/http-fixtures
```

The full command has a 90-second deadline; registration also has a 30-second deadline. `response-*.bin`
files contain distinct target responses. `fixture-plan.json` maps them to their targets and fixed GET
path `/anas-incus-http-fixture`, with status `prepared-only`. `fixture-registry.json` is published last:
canonical JSON, at most 1 MiB, 0400 and one hard link. It contains no naming keys, API credentials or
replayable tokens. Response/plan files are 0600 under a 0700 directory. Failed final registration may
leave private preparation files for inspection and returns an error; they authorize no publication.
Retries must not overwrite an old output directory.

An administrator later installs each response into its selected guest and serves its exact bytes on
the specified port/path. This tool does not connect to or modify guests, install HTTP services, or learn
expectations from the first backend response. Give each guest only its own response, never another
target's response or the registry root. Actual Probe/E2E must still verify this preparation.

Runtime assembly can load the registry with `NewRegisteredFixtureHTTPProbe`. Records omit the transient
reservation but fix every other deployment/request/Host/auth and UUID/incarnation/MAC/IP/port field.
Only the current Planner produces a new token, with Core, request, independent instance facts and the
registration file checked around each probe. Mediator restart does not replay an old token; guest
restart or target changes require new preparation. The file cannot replace receipts, address holds or
current authority.

Neither mode has been run. Real read-only identity, host actions, production launch delivery, in-guest
service installation, orphan recovery and server acceptance remain pending. Once testing resumes, use
the [pending matrix](e2e-plan.md); prepared files are not evidence of a working network.

## External artifact inventory and recovery wiring (unrun code)

The runtime adds `Executor.Recover` and mandatory `HTTPArtifactInventory` checks. Under the same state
lock, the controller retires outstanding receipts and then requires the complete file/API/host scope to
be clear. Opening/renewal and address release are also guarded. No new CLI mode is provided; preparation
commands do not invoke recovery, modify networking or prove that the external installation is clear.

The installer must point `route_directory` at a dedicated trusted directory without unrelated auth
configuration and ensure that the actual Traefik file provider watches it. Inventory allows 4096 entries
within 30 seconds, requires complete journal/rendered-byte matches, and checks file/directory identities
around API reads. Only complete loaded HTTP matches and verified ForwardAuth usage edges are excluded.
Unknown files, orphan loaded objects and other services/providers/protocols referencing `anas-compute-`
block progress. Unrelated strings using that reserved prefix may also fail; do not use it for other
object names or unrelated configuration values.

Withdrawal additionally removes only canonical temporary files whose complete bytes match an outstanding
receipt. Partial writes, changed content/templates, unknown files and retired tokens cannot establish
deletion rights or authority from names alone. Unknown artifacts retain addresses and retiring receipts
while verified routes/permits/connections can close. Preserve missing/corrupt-state incidents for independent
backup/host reconciliation; do not delete the state/lock and rerun or reconstruct permits from owner comments.

Actual host inventory must verify address holds, /32 routes, HTTP permits, connections, installed namespace/
interface identities and the deny baseline. That adapter and restricted administrator recovery actions are
not implemented; empty adapters are not a substitute. Code has not been compiled or run. File/API checks
do not prove network cleanup; follow-up fault injection and host acceptance are in the [E2E plan](e2e-plan.md).
