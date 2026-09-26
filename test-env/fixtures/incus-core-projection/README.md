# Native Core / Compose compute projection

This is separate from the installed-host-action and direct-backend fixtures.
It exercises actual `anas init`, `config import`, `render`, `apply` and `stop`,
the unchanged Incus calculate Hook and Provider Compose, the private Secret
Store, frozen deployment metadata, image supply and two synthetic consumers.
The fixture consumers do not stand in for full Forgejo or AI Agent workflows.

## Protected test environment

Use only a fresh disposable QEMU VM with an exact cloud-init ID of the form
`anas-incus-host-<six lowercase hex digits>`. The driver requires root inside
that VM and an empty dedicated Docker daemon with data root
`/var/lib/anas-host-provision-test`. Never use an existing physical-host Docker
socket, mount business volumes or bridge the VM into business networks.

Prepare the official-package Docker environment described by the
[installed approval fixture](../incus-host-action/README.md). This additional
fixture also needs an actual Docker Compose version supporting `gw_priority`,
`tar`/XZ and `mksquashfs`. It starts before any Incus provisioning state or
product service installation exists. The outer owner must retain a bounded
QEMU process, verify exact QMP identity, reap it and compare the physical host's
independent before/after baseline, including on test failure.

`/opt/anas-host-action-inputs` contains the source-bound versioned product
artifacts and existing approval driver. `/opt/anas-core-inputs` contains the
same `anas` bytes, `core.test` compiled from `internal/runner`, `test2json`, the
actual Incus Provider binary, the compiled `incus-core-consumer` helper, both
native driver scripts and a copy of the actual Incus Module and compute
Contract sources. Supply the actual Hook at its supported
`hook/bin/linux-amd64/anas-hook` location; do not replace it with a shell mock.

The Core manifest schema is `anas.native-core-inputs/v1`, with a `files` map of
relative paths to full SHA-256 values. It binds every delivered input, including
the test program and this driver. Protected root-owned directories, regular
single-link files and bounded sizes are mandatory. The product manifest records
the full dirty-worktree source identity and explicit native-test version; these
are not formal signed releases.

## Actual operation and evidence

```sh
sudo python3 /opt/anas-core-inputs/server-incus-core-projection-e2e.py \
  --vm-id anas-incus-host-abcdef
```

Use the actual VM ID rather than the example. The driver uses real HTTPS owner
enrollment and confirmed shared jobs to install/configure/enroll the host. The
Core CLI then creates and imports a workspace without any Incus connection,
architecture, storage pool or control-bridge parameters supplied by the test.
The real Hook derives those from the approved private host bundle.

Each synthetic consumer is non-root, read-only, capability-free and has
`no-new-privileges`. It receives only its own projected client credential,
connects on the projected external control network and retains a separate
business network with the higher gateway priority. Independent requests require
its project to remain accessible while the other/default project is refused.
Initial service readiness tests only its own project, since Core starts consumers
in dependency order. After apply, the driver independently confirms that both
projects exist, then executes each container's finite isolation probe. Its final
own-project positive control must still succeed. HTTP 500, a missing project or
an own-readiness report cannot substitute for the complete isolation gate.
The finite probe uses the named-project GET endpoint and validates the actual
name and restricted flag on its own successful response. Foreign instance-list
HTTP 500 responses observed in the native daemon are not counted as denials.
The separate readiness check still exercises the own-project instance list.

Two binary-only Docker transport fixtures avoid unrelated registry pulls; the
Provider binary, Hook and Compose are unchanged. A minimal split guest image is
bound by its actual byte fingerprints and imported through the real Core image
supply. **It is not bootable guest, distrobuilder or signed-catalog acceptance.**
Only the isolated copied module receives the explicitly named test catalog;
the repository production catalog is not populated by this test.

Repeated render/apply must preserve both independent client identities. The
already-active frozen deployment is first required to reject a repeated apply
with `deployment_not_ready` and no active-state/Secret Store changes. A new
rendered deployment is then actually applied and both live consumers are probed
again; the fixture never rewrites a consumed deployment back to `ready`.
The test only cleans exact owned and independently checked fixture resources.
After an approved host uninstall revokes the bundle, a new actual render must
fail with the specific calculation error and preserve the previous private
Secret Store. An unrelated command failure does not satisfy this negative gate.

Public evidence is under `/opt/anas-host-action-e2e/reports`; raw command
diagnostics are private under `/opt/anas-core-projection/private`. Do not archive
that private directory, console sessions, the host bundle or workspace secrets.
The required parent/subtest events are enumerated in the driver: missing,
duplicate, skipped, failed or substituted tests cannot count as acceptance.
Keep failed disk/effect evidence rather than rewriting it or resetting an
incomplete transaction. Record actual source digests, results and outer cleanup
in a dated review before changing milestone status.

Local checks, which do not establish native acceptance:

```sh
go test ./internal/runner ./modules/incus/hook ./modules/incus/provisioner \
  ./test-env/helpers/incus-core-consumer
python3 -m unittest discover -s test-env/scripts -p 'test_incus_core_projection_e2e.py'
```
