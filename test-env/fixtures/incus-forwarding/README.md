# Default Docker forwarding: controlled kernel experiment

## Kernel permissions and installed retirement

The separate Go native targets are `TestForwardingKernelNative` in
`internal/incusingresshost` and `TestNativeForwardingRetirement` in
`internal/incusprovision`. Run only frozen Linux test binaries in a new exact
QEMU/cloud-init VM with the default-DROP preparation; the retirement target also
requires the official `btrfs-progs`, `ipset` and `conntrack` packages. The native
selectors are `ANAS_REQUIRE_FORWARDING_PERMISSION_NATIVE=<vm-id>` plus a fresh
`ANAS_FORWARDING_NATIVE_ROUND`, and `ANAS_REQUIRE_FORWARDING_RETIREMENT_NATIVE=<vm-id>`.
These are test-binary guards, not production configuration switches.

The retirement target invokes the actual installed backend and kernel executor.
It checks rejection of a still-valid lease certificate, a stopped differently
named real Incus empty container and a real bridge port, then explicit retirement
and preservation of the ownership tombstone. The prior grant and stopped Core
metadata are prepared fixtures: this is not host approval transport, Core stop,
a booted guest or a Forgejo workflow. Production enable remains gated.

Preserve failure state, original intents and all failed disks. Archive only the
explicit public retirement JSON/JSONL files, never the host state, connection
bundle, keys or prepared workspace. Require ordered unique run/pass events,
package exit status, exact QMP normal shutdown, process/port absence and a fresh
physical-host Docker/network baseline comparison for each VM.

`server-incus-forwarding-e2e.py` separates a Linux forwarding problem from a
public registry, TLS, DNS or guest-image download failure. It requires a **fresh
disposable QEMU VM**, exact cloud-init identity, and an empty experimental Docker
daemon using `/var/lib/anas-forwarding-docker`. It must never run directly on the
SSH server or receive that server's Docker socket, data directories or devices.

Preparation is `prepare-incus-forwarding-native.sh <actual-vm-id>`, inside that
VM only. The initial IPv4 forwarding value must be zero. Official distro packages
are installed without starting their services; the new experimental Docker daemon
starts first with default configuration. The script requires Docker to enable
forwarding and install its default `FORWARD DROP`. It does not pre-enable routing,
change that policy to ACCEPT or change Docker's firewall configuration.
The official `dnsmasq-base` bridge dependency is explicitly installed and read
back. The local HTTP endpoint does not perform reverse DNS during bind; otherwise
a missing namespace resolver can interfere with a supposedly numeric-IP test.
All commands without an explicit input payload receive stdin EOF; only nft
transactions with an explicit payload receive a dedicated pipe. Inheriting an
open SSH input stream can make Incus create commands wait for optional YAML input
instead of sending the intended API operation.

## What is tested

The runner creates an actual Incus-managed NAT bridge. Two isolated network
namespaces provide deterministic source addresses on it; a third namespace serves
two local HTTP ports on a separate veth subnet. These are **namespace endpoints,
not booted guests, Forgejo jobs or production services**. No external registry,
name resolution, public IP or Internet response is required by the packet test.

The complete required sequence is:

1. Read back the empty experiment identity and default DROP, then prove both local
   endpoint ports respond from the VM itself.
2. An earlier nft chain matches and counts the source flow with ACCEPT, yet the
   request fails and Docker's later default-drop counter increases.
3. Two temporary exact `/32`, interface, endpoint and TCP-port permissions in
   `DOCKER-USER` make that same request succeed while the global policy remains
   DROP. The other source and port must remain denied.
4. An injected earlier exact DROP must still win. Removing it restores only the
   temporary permitted flow; removing both permits restores the original denial.
5. A precompiled Go native test calls the production **read-only** forwarding
   observer and verifies that the later DROP is exposed as a warning, never
   compute readiness. Original rule inventory, namespaces and Docker inventory
   must be restored after removing only this experiment's resources.

The two temporary rules are diagnostic interventions, **not a production fix**.
They do not prove anti-spoofing, lease ownership, timeout/TTL revocation, restart
reconciliation, IPv6, existing-flow revocation or permission to modify an existing
administrator chain. No automatic DOCKER-USER write has been added to `hostd` or
the Provider. A production adapter needs those independent ownership/lifecycle
requirements; a chain name or broad bridge prefix is not such authority.

## Inputs and evidence

Copy root-owned inputs into `/opt/anas-forwarding-inputs`: the compiled
`incusprovision.test`, `test2json`, runner/preparation scripts and a
`source-manifest.json` binding file hashes and the precise dirty-checkout inputs.
Run the Python entry with `--vm-id` set to the actual fresh VM identity.
`TestNativeForwardingDiagnostics` is enabled only by this isolated driver; the
production application has no new test or firewall-write option.

Public JSON snapshots, stage results and the native event stream are written
under `/opt/anas-forwarding-native/reports`. All required stages must pass once,
and the native event stream must include its exact package, ordered test run/pass
and final package result. Missing, duplicated or skipped evidence is not a pass.
The independent outer VM owner must archive these results, observe exact-QMP
normal shutdown and actual QEMU exit, check port absence, and compare the physical
server's original containers, networks, volumes, daemon/configuration, nft and
route baseline. Failed state and original reports are retained.
Command labels, exit codes and elapsed times are recorded without raw output;
bounded raw failure diagnostics stay in the separate `private` directory and
must not be added to the public archive. An endpoint or bridge-preparation failure
does not establish either network reachability or the cause of a blocked packet.

```sh
python3 -m unittest discover -s test-env/scripts -p test_incus_forwarding_e2e.py
GOPROXY=off go test ./internal/incusprovision -run TestForwarding
```

The offline checks do not establish native acceptance. Actual runs belong in
dated `dev-docs/reviews`; a passing packet experiment does not mean the default
production deployment or all Incus milestones have been completed.

The 2026-09-25 `incus-forwarding-r4` run on Ubuntu 26.04 amd64 / Docker 29.1.3
passed all nine native stages and the exact production collector test. Its
source-bound archive, normal VM exit and physical-host baseline were independently
verified. Earlier endpoint/preparation/command-input/transaction failures remain
separate records in
[`2026-09-25-incus-forwarding-observation.md`](../../../dev-docs/reviews/2026-09-25-incus-forwarding-observation.md).
The final strict-schema collector was recompiled and passed the same complete
sequence in a separate fresh `incus-forwarding-r5` VM. Its public archive,
current source binding and normal shutdown were independently verified; this
does not turn the experimental rules into a production authorization adapter.
