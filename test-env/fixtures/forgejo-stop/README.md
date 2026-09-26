# Forgejo running-workload stop integration

`test-env/scripts/server-forgejo-stop-e2e.py` tests the production Core stop
method, frozen Forgejo Hook, Actions controller, account helper and Incus
Provider against actual Docker Compose, Forgejo and an executing guest job.
The runner image is the separately baked immutable `policy-loader-r1` export; this test
does not modify guest trust, re-bake on apply or accept an unpinned image.
This revision passed all ten image run/pass pairs and the real five-case Forgejo
workflow matrix on Ubuntu's mediated user-namespace kernel. The original `trust-r2`
Debian acceptance does not substitute for that platform-specific gate. Source,
export and independent checks are recorded in
[`2026-09-25-forgejo-fixed-policy-loader.md`](../../../dev-docs/reviews/2026-09-25-forgejo-fixed-policy-loader.md).

## Scope and isolation

Use a **fresh disposable QEMU VM**, with the exact cloud-init identity matching
`anas-incus-host-[a-f0-9]{6}`. The driver rejects non-root/non-QEMU execution and
requires an empty experimental Docker daemon at
`/var/lib/anas-host-provision-test`. The physical server's existing Docker socket,
networks, raw disks and application volumes must not be mounted into the VM.

Inputs are root-owned, immutable source-bound binaries under
`/opt/anas-forgejo-stop-inputs`. The source manifest records their digests and
binds the separate installed-host-action manifest under
`/opt/anas-host-action-inputs`. The latter runs actual confirmed host installation,
configuration and management enrollment before the test provisions a lease.

The fixture uses a measured binary/OS-library transport image, an isolated SQLite
database, a local TLS reverse proxy and a prepared lifecycle workspace. It is
**not** the production Forgejo Dockerfile, a complete PostgreSQL/IAM deployment,
or a public CLI init/import/render/activation acceptance. The running workload
and the Hook/controller/Provider implementations are real; these distinctions
must remain explicit when reporting results.

Networking is a separate prerequisite, not established by the account or
control-bridge tests. The driver records the actual IPv4 `FORWARD` base policy
and forwarding switch before host setup without changing either. Such a snapshot
does not prove connectivity or effective authorization across all chains. A
fixture with IPv4 forwarding already enabled before Docker startup must be
reported as an explicitly routable profile; passing this profile does not
demonstrate compatibility with Docker's default-DROP initialization. Do not edit
the physical host's forwarding policy or existing Docker service to make this
experiment pass. Retain registry/download failures as failures rather than
inferring that a payload ran from a queued workflow or a created guest.

The original deployment's network and management API remain live while its
controller receives SIGTERM and retires jobs. Only a successful cleanup followed
by independent terminal reads allows Core's Compose removal. A subsequent
disabled deployment must invalidate the managed account password, and reenabling
must preserve numeric account identity and execute a real new workflow.

## Execution and evidence

Inside the correctly prepared VM, with its actual identity:

```sh
sudo python3 /opt/anas-forgejo-stop-inputs/server-forgejo-stop-e2e.py \
  --vm-id anas-incus-host-abcdef
```

The fixed `REQUIRED` set is authoritative: every unique required stage must pass,
including running-job cleanup, repeated stop, password invalidation, reenabling
and a negative test in which uncertain controller state blocks both removal and
revocation. Missing, duplicate or skipped stages never count as acceptance.
Each Core subprocess also requires the expected package and test identity, one
ordered run/pass pair, a single final package pass, and an actual zero process
exit. A success-looking test label on its own is insufficient; the same pure
validator can independently recheck archived JSONL without rewriting it.

The pinned Forgejo release retains soft-deleted Runner rows. After application
removal, the read-only fixture database check therefore validates bounded
`id`/`deleted` records and counts only `deleted=0`; malformed or unknown records
are not absence evidence. It does not delete tombstones or inspect credential
columns. While the reenabled application is live, its actual scoped Runner API
must independently return an empty active inventory before that stage can pass.

Public reports are confined to `/opt/anas-forgejo-stop-native/reports`. Do not
archive its `private` directory, account receipts, secret-bearing environment
files, SQLite database or host connection bundle. The fixture owner explicitly
retires test-only containers after recording the expected rejected-stop case;
this is not a product retry and does not rewrite the failed controller state.

While the first payload is pending, bounded diagnostic samples may read only
fixed guest service state, unit journals and socket metadata. Selection requires
this fixture's exact managed instance prefix, container type, immutable image
fingerprint and instance UUID. The observer does not query Podman's API, start
services, consume stdin credentials or pause the controller's compensation.
All sampled output stays under `private`, and no observation counts as a passing
stage. The unchanged running-payload and completed-cleanup checks remain required.

An independent outer owner must always archive available public reports, verify
the exact QMP identity, shut down the VM, observe QEMU exit and listener absence,
and compare the physical host's preexisting Docker/container/network/volume,
service identity/configuration, nft and IPv4/IPv6 route baselines. Retain failed
experiment disks and reports instead of clearing intents to manufacture a pass.

Offline harness guards:

```sh
python3 -m unittest discover -s test-env/scripts -p 'test_forgejo_stop_e2e.py'
```

Actual native outcomes belong in dated `dev-docs/reviews` records. This entry's
existence and its local guard tests do not themselves establish native success.
