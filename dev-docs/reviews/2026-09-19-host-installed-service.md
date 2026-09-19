---
doc_type: review
status: current
created: 2026-09-19
updated: 2026-09-19
---

# Host Action Installed Service Slice

## Result

This slice corrects the earlier implementation assumption that `anasd` must be
migrated to a non-root service before host actions can run. `CONSOLE-R-154`,
`CONSOLE-R-157`, and `CONSOLE-R-163` remain authoritative: the installed
`anasd` service stays `root:root`, its service configuration, TLS material,
console store, job state, and authentication files stay root-managed, and this
work does not relax TLS file ownership or move state into a non-root account.

The installed host-action policy schema was revised to unreleased v2:

```json
{
  "schema": "anas.host-action-installation/v2",
  "release": {
    "version": "<same release semver>",
    "commit": "<same release commit>"
  },
  "service_mode": "systemd-root-service",
  "service_unit": "anasd.service",
  "socket_gid": 0
}
```

`service_mode` is required. UID 0 is not a default policy value and no
`service_uid`/`service_gid` fields remain in the installed schema. The runtime
admits a root peer only when kernel `SO_PEERCRED` identifies a live local peer
and PID 1 independently reports that peer as the configured root/root systemd
unit from `/etc/systemd/system/<service_unit>`. A root process outside that unit
is not a valid caller for this policy.

## Installed Contract

- `anasd.service` remains `User=root` and `Group=root`.
- `anas-hostd.socket` is root-owned and root-only: `/run/anas/hostd.sock`,
  `SocketUser=root`, `SocketGroup=root`, `SocketMode=0600`, `Accept=yes`.
- `anas-hostd@.service` runs one request as `root:root` and keeps the compiled
  action registry tied to the same release version/commit.
- `/etc/anas/hostd.json` is root-owned `0600`, version/commit-bound, and
  replaced on install/upgrade. It contains no paths, commands, argv, handler
  directories, environment overrides, secrets, or request-selected policy.
- `/run/anas-job-broker/socket` is created by the installed `anasd` owner inside
  the root-only broker runtime directory. The root executor must still match the
  original peer PID/UID/GID pinned from the activation connection; JSON never
  supplies PID authority.
- Job authorization, execution lease retention, store ownership, broker grant
  handling, and final recorder completion remain shared with the existing job
  store. Root never receives a console-store path or opens a second job log.
- Missing installation, missing socket, or a policy that does not authorize the
  current daemon is recorded as no privileged execution submitted. Only cases
  where bytes may have reached the executor, exit evidence is missing, or cleanup
  is unconfirmed enter the containment barrier.

## Installer Coverage

`install.sh` now installs and upgrades the packaged `anas-hostd` binary, hostd
socket unit, hostd service template, and v2 policy when system service
installation is enabled. It verifies the release archive checksum as before,
then checks `release.json` and `anas-hostd --version` so the installed policy
uses the same version and commit as the archive.

Normal uninstall removes installed binaries, hostd units, and hostd policy while
preserving workspace data, console state, and the administrator's `anasd.yml`.
`--purge` still removes the service config. The installer does not recursively
`chown` data directories. Broker runtime creation is primarily owned by
`RuntimeDirectory=anas-job-broker`; `install.sh` only pre-creates `/run` content
when `/run` is writable in the current environment.

## Limits

Only the read-only `incus.status` preflight remains compiled. Privileged Incus
install/configure/enroll/uninstall/image-prune actions are still not registered:
they require paired execution, real rollback/cleanup evidence, and two-step
confirmation before admission.

Preflight success is still not compute readiness. Reports continue to distinguish
`compute_ready: false`, `runtime_verified: false`, and the remaining blockers.

This slice does not update requirement, plan, or architecture indexes because
the current worker scope excludes those shared documents. Parent integration
should update the host-action and Incus plan text to remove the stale non-root
anasd migration language and replace it with the root/root installed-service
contract above.

## Verification

- `TMPDIR=/private/tmp bash scripts/ci/install-test.sh` passed. The harness uses
  fixture releases and fake `systemctl`, `curl`, `uname`, and `id`; it does not
  call the real service manager.
- `TMPDIR=/private/tmp GOCACHE=/private/tmp/anas-go-build-cache go test -run '^$'
  ./internal/hostaction ./internal/jobexecutor ./cmd/anasd ./cmd/anas-hostd`
  passed for compilation.
- Focused non-socket tests passed:
  `internal/hostaction` installation/registry/execution tests,
  `internal/jobexecutor` host binding tests, and full `cmd/anasd` /
  `cmd/anas-hostd` tests.

The full `internal/hostaction` and `internal/jobexecutor` package tests did not
complete in this sandbox because Unix-domain socket bind returned
`operation not permitted`, even with `TMPDIR=/private/tmp`. No real systemd,
root service start, package install, Incus daemon, or KVM validation was run.
