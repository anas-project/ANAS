---
doc_type: review
status: current
created: 2026-09-19
updated: 2026-09-19
---

# Incus host provisioning backend final review

## Result

`internal/incusprovision` is now a typed, fail-closed backend that can be wired
to compiled host actions after the host-action layer performs its own
one-time confirmation. It still must not be treated as real-host acceptance:
no sudo, apt, systemd, nft, Docker or Incus daemon was executed in this review.

The backend API remains:

- `NewLocalBackend() *Backend`
- `Inspect(ctx, Request) (InspectResult, error)`
- `Plan(ctx, Request) (Plan, error)`
- `Install(ctx, Request, Binding) (ApplyResult, error)`
- `Configure(ctx, Request, Binding) (ApplyResult, error)`
- `Enroll(ctx, Request, Binding) (ApplyResult, error)`
- `Uninstall(ctx, Request, Binding) (ApplyResult, error)`
- `ReadPrivateConnectionBundle(ctx) (ConnectionBundle, error)`

New helper API:

- `OfficialAPTConfigFiles(recipe incushost.Recipe) ([]APTConfigFile, error)`
- `WriteAPTConfigFiles(root string, recipe incushost.Recipe) ([]APTConfigFile, error)`

Host action integration can now write those exact files under `/` before
invoking `Install`; tests use a temporary root. Mutating calls still require a
fresh `Binding{schema, phase, plan_digest, destructive:true}` matching the
current plan digest. `Binding.Destructive` is only a bound receipt field here;
it is not user authorization and does not replace the host-action confirmation.

## Fixed in this pass

- Official apt configuration is deterministic and byte-for-byte verified. The
  backend no longer trusts arbitrary existing apt sources or broad substring
  checks. Static source and policy fixtures now live under `packaging/incus/apt`.
- Package install/remove continue to use fixed binaries and fixed argv only:
  `/usr/bin/apt-get -c /etc/anas/incus-apt/<recipe>.apt.conf ...`, with
  `APT_CONFIG` pinned to the same file.
- APT sourceparts/preferencesparts are recipe-scoped. Ubuntu includes
  `main restricted universe multiverse`, arm64 uses `ports.ubuntu.com`, Debian
  includes `debian-security`, and package pinning applies to all packages from
  the selected official origins so dependencies can resolve.
- Install now runs isolated `apt-get update` before install and uses dedicated
  ANAS list/cache directories.
- Firewall application now performs `nft -c -f -` before applying the generated
  ruleset.
- Firewall readback now uses `nft -j list table inet anas_incus_control` and
  validates exact table, chain priority, rule comments and JSON expression AST
  order. Comment-only, `!=`, wrong port, extra verdict and duplicate rules are
  rejected.
- Read-only `systemctl is-active` exit code 4 is treated as a missing/inactive
  unit; other unknown exits remain external effects.
- Successful install/configure/enroll effects now require readback before
  ownership and ok receipts are recorded. Unrecovered pending intents fail
  closed with an operator-recovery blocker instead of blindly taking over.
- `Enroll` blocks external daemons and missing daemon/network/storage/relay
  prerequisites before generating secrets or adding Incus trust.
- Relay removal returns config deletion errors, command output overflow fails,
  file locks use `O_NOFOLLOW` plus directory-chain checks, and unlock failures
  are joined into apply results.
- Docker control network removal now checks the persisted owner ID before
  deleting; storage pool volume checks remain in place.
- Regression tests cover apt fixture drift/write helpers, nft expression
  validation, sensitive redaction, readback-gated ownership, pending intent
  recovery, running guest uninstall blocking, ownership-scoped cleanup and plan
  drift rejection.

## Remaining risk

- The apt files are generated, writable through `WriteAPTConfigFiles`, and
  verified, but no real `apt-get update/install` was executed in this review.
- Package origin verification is still bounded to signed configured sources,
  source/policy file identity and isolated APT state. Real apt policy output
  and signed package origin checks still need clean-host acceptance.
- nft JSON parsing now validates the expected AST shape, but native nft
  acceptance, priority ordering, Docker/Incus interaction and IPv6 behavior
  still need Linux host validation.
- Docker, Incus, storage volume, service, relay reachability, mTLS pinning and
  uninstall behavior were not run against real daemons.
- `Enroll` intentionally reports `connection_ready` only; image import and
  consumer bridge reachability remain unsupported.

## Verification

Run with `GOCACHE=/private/tmp/anas-gocache`:

```text
go test ./internal/incusprovision ./internal/incushost
GOOS=linux GOARCH=amd64 go test -c -o /private/tmp/incusprovision-linux-amd64.test ./internal/incusprovision
GOOS=linux GOARCH=arm64 go test -c -o /private/tmp/incusprovision-linux-arm64.test ./internal/incusprovision
GOOS=linux GOARCH=amd64 go test -c -o /private/tmp/incushost-linux-amd64.test ./internal/incushost
GOOS=linux GOARCH=arm64 go test -c -o /private/tmp/incushost-linux-arm64.test ./internal/incushost
```

Also attempted:

```text
go test ./...
```

That full-repository run did not pass, with failures outside this scoped
change: bundled `incus` module `config.defaults.storage_pool` fails an existing
module-root pattern check in generated/config inventory tests; several
hostaction/jobexecutor/control-relay/provisioner tests cannot bind Unix/TCP
sockets in this sandbox; incusingresshost host commands timed out; and
modules/incus/hook expected a real host connection bundle. The scoped
`internal/incusprovision` and `internal/incushost` packages passed.
