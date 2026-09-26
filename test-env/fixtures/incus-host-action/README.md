# Installed Incus host-action acceptance

`test-env/scripts/server-incus-host-action-e2e.py` exercises the installed product
path, separately from the direct-backend host provisioning runner. It uses real
`anas`/`anasd`/`anas-hostd` binaries, packaged systemd units, actual HTTPS owner
enrollment, local-owner session envelopes on CLI stdin, shared durable jobs,
one-use confirmations and independently supervised executor exit. It is not a
production installer, mocked service, release signing operation or browser-UI test.

## Isolation and prerequisites

Run **inside a fresh disposable VM only**. The runner independently requires
Linux, root, QEMU DMI vendor, an exact cloud-init ID matching
`anas-incus-host-[a-z0-9]{6}`, and an empty experimental Docker daemon whose data
root is `/var/lib/anas-host-provision-test`. An existing host Docker daemon is
never an accepted substitute. Do not bind host folders, container sockets, raw
disks or business bridges into this VM.

Prepare the same official-package Docker fixture used by
[native host provisioning](../incus-host-provision/README.md), without running its
Incus lifecycle first. The root-action fixture must start without an anasd
installation, console store, hostd policy/socket or Incus provisioning state.
The server source config can use the packaged root-owned 0755 `/etc/anas` directory;
private leaf files stay 0600 and TLS secrets remain below a root-private directory.
Do not relax filesystem checks or erase a failed operation to make the test pass.

Copy trusted inputs to root-owned `/opt/anas-host-action-inputs` with no writable
ancestors, symlinks or hardlinks. The source manifest must have the exact eight
artifacts `anas`, `anasd`, `anas-hostd`, `anas-incus-control-relay`, `anasd.service`,
`anas-hostd.socket`, `anas-hostd@.service`, `anas-incus-control-relay.service`, plus
`runner_sha256` binding the runner itself. Each artifact has a SHA-256 value.
The current network-extended entry also requires the separate `test_helpers`
map with exactly `incus-control-probe` and its SHA-256. Build this static test-only
helper for the VM architecture; it is not a ninth product artifact or a release
component:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOPROXY=off \
  go build -o /tmp/incus-control-probe ./test-env/helpers/incus-control-probe
```

Copy it alongside the other inputs and include its Go sources and byte digest in
the same source manifest. Do not substitute an arbitrary container image.
The manifest records `release.version`/`release.commit`; all three product binaries
must be compiled from the same recorded inputs and release identity. Only an
explicit `0.0.0-native.<numeric-components>` experiment version is accepted here,
with a 40-lowercase-hex source commit and a separate dirty-source manifest where
applicable. This must not be described as a signed or published production release.

The relay and its unit may already be present from fixture preparation **only if
their actual bytes match**. Other existing product installations are rejected.
The runner creates a private short-lived internal CA, two empty test workspaces,
a root-managed service config, and the fixed installation policy. Only this VM's
test hostnames are added to its `/etc/hosts`. The packaged service units are not
replaced by permissive test units, and the application has no test authentication
or alternate backend switch.

## Run and evidence

From inside the identified VM:

```sh
sudo python3 /opt/anas-host-action-inputs/server-incus-host-action-e2e.py \
  --vm-id anas-incus-host-abcdef
```

Use the **actual** cloud-init ID, not the example ID. An outer experiment owner
must bound the process/VM lifetime and observe final shutdown independently.
The script's `REQUIRED` set is authoritative: a zero-exit command, skipped gate,
subset or duplicated pass event cannot replace complete acceptance. The positive
path includes confirmed skip/install/configure/enroll/uninstall; negative paths
cover anonymous access, unsupported request fields, cross-workspace confirmation,
and consumed confirmation replay before and after a real anasd restart.

The current entry requires **23 distinct passed stages**. The original approval
gates include use of an otherwise
valid token in the wrong workspace before using that same token in its correct
workspace. Workspace mismatch must return the product's `400 invalid_json`, while
replay must return `409 confirmation_consumed`. An expired token, authentication
error or unavailable service is not substitute rejection evidence. Initial owner
creation requires the actual `201 Created` response.

Four additional gates run after enrollment from actual temporary Docker containers:
trusted mTLS through the managed control bridge, rejection of a wrong server pin,
an anonymous client receiving only `untrusted`, and a client on the unrelated default
bridge unable to establish TCP even with the correct certificate. A TLS failure is
not accepted as a network-denial result. The test builds a scratch image from the
verified static probe without pulling a base image. Containers use UID/GID 65534,
read-only root filesystems, no capabilities, no-new-privileges and no host mounts or
host network/PID. Credentials enter only via stdin, not environment, argv or image
layers. Exact container/image identity and ownership are checked before cleanup;
containers, images, volumes and networks must return to their original inventory.

These are transport/source-boundary gates using the private management bundle,
not a replacement for restricted-project/consumer-credential tests or proof that
Core/Compose automatically projects the network to every consumer. Default routes,
IPv6, interface recovery, guest/VM isolation and production ingress remain separate.
The earlier 17-gate success cannot replace these four new required outcomes.

The expiry gates issue a separate unused approval before the main lifecycle. Its
server expiry must be exactly five minutes after the plan's creation, not five
minutes after the test decides to consume it. Other gates run while that real
deadline elapses; the runner then waits against monotonic time without changing
the VM clock or the production ledger. Both using the expired approval and issuing
another token for the old plan must return `409 confirmation_expired`. A genuinely
new plan and confirmation must subsequently execute. Consumed-token rejection,
TLS/authentication errors or unavailable service are not expiry proof. This tests
the real API/CLI contract, not the browser's visual re-confirmation behavior.

The consumed-token restart check runs immediately after confirmed skip and before
long package downloads, with the plan's actual five-minute validity checked both
before and after restart/replay. Once a binding has expired, the product correctly
checks expiry before its consumption ledger, so accepting an expired response is
not proof of consumption persistence. The separate unused-token expiry gates stay
independent and keep their exact error-code assertions. No clock, TTL or ledger
is changed to make these tests complete sooner.

The current complete runner requires **25** distinct passed stages. Following
the new plan after expiry, it explicitly confirms removal of individually owned
packages and repeats that uninstall. Independent complete dpkg inventories must
be healthy: unfinished configuration/triggers, reinstreq, ambiguous names and
incomplete output are rejected. Every original package and unowned dependency
must remain; exactly the recorded managed set may disappear, and the daemon must
be inactive. Do not use autoremove or force removal to satisfy these checks.
Historical 23-stage reports are valid for their original scope, not these added
package-removal gates. Debian 13 and Ubuntu 26.04 both explicitly track the actual
`incus-base` daemon; a missing meta-package alone cannot prove daemon removal.

PID1 identity and exit queries use the independently kernel-authenticated private
connection. The fixed system-bus connection supplies only `Unit.Ref/Unref` retention,
because those operations require a bus-client identity; no authorization or exit
facts are accepted from it. Authentication-queue sequencing and missing-manager
negative tests do not replace this installed-service acceptance gate.

Public results are under `/opt/anas-host-action-e2e/reports`; private enrollment
material and failure diagnostics remain under the separate `private` directory.
Do not archive that private directory, TLS material, console auth store or root
Incus connection state. Public reports contain fixed gate results, job IDs/status
and independent service activation evidence, not session or confirmation tokens.
The runner checks response leakage and the experimental Docker before/after
identity. The **outer** owner must independently compare the physical host's
preexisting containers, networks, volumes, daemon identity/config, nft and routes,
then verify exact QMP identity before shutting down only this disposable VM.

The outer owner's cleanup must not stop after a tunnel or port-check error.
`test-env/scripts/incus_vm_cleanup.py` provides independent bounded steps for
stopping the owned tunnel, requesting identity-verified QMP shutdown, waiting for
the actual child exit, checking resource absence and comparing the physical-host
baseline. Forced quit, unknown exit and errors remain separate evidence; a zero
QEMU exit after forced quit is not a graceful pass. Persist the resulting report
before returning a failing status. Do not invent a wait status after the original
process owner has already exited.

Check for an actual listening socket instead of attempting to rebind the former
SSH-forward port. A connection-refused result proves no listener; TIME_WAIT can
prevent rebinding after a correctly closed tunnel and must not prevent QEMU
cleanup. Timeout/unreachable/error is unknown, not proof of absence. These helpers
accept only callbacks from the trusted experiment owner, never paths, QMP names
or shell commands supplied by a guest.

Keep unsuccessful experiment state and reports. This runner does not clear pending
host effects, rewrite receipts or bypass plan drift to resume a failed lifecycle.
Actual completed runs and remaining gates belong in dated `dev-docs/reviews`;
neither the existence of this entry nor its local unit tests establishes a pass.

```sh
python3 -m unittest discover -s test-env/scripts -p 'test_incus_host_action_e2e.py'
python3 -m unittest discover -s test-env/scripts -p 'test_incus_vm_cleanup.py'
```
