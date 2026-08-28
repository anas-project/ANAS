# Remote test target setup

M2 provides a read-only remote preflight. It validates target authorization, SSH host-key binding, source identity,
least privilege, run-scoped allocation, capacity, networking, target capabilities, and the existing Docker/Compose
isolation guards. It does not yet transfer or deploy the source bundle; `run/status/collect/cleanup` belong to M3.

## Local target profile

Copy `targets.example.yml` to `targets.local.yml`, set mode `0600`, and keep it Git ignored. The strict schema permits
only an SSH config alias, remote test root, capability labels, and concurrency. Authentication stays in the operator's
SSH agent/config. `remote-test` resolves the alias with `ssh -G`, forces `BatchMode=yes` and
`StrictHostKeyChecking=yes`, and requires the resolved host (including a non-default port) to exist in a configured
known-hosts file.

An unregistered target can be used only when the same invocation contains both `--ssh-target <alias>` and
`--authorize-target <alias>` with identical values, plus its root, capabilities, and concurrency. This permits an
explicit production target without treating it as permanently trusted; all isolation and preflight checks remain
mandatory.

## Server helper

Build the helper, review `anas-test-helper.example.yml`, then install it on the target as an administrator:

```bash
go build -o /tmp/anas-test-helper ./cmd/anas-test-helper
sudo ./test-env/scripts/install-remote-test-helper.sh \
  /tmp/anas-test-helper test-env/remote/anas-test-helper.example.yml
```

The installer copies the binary, config, and the existing
`server-require-isolated-docker.sh` into root-owned fixed paths and grants the test user passwordless sudo only to the
fixed helper. The helper currently exposes only the read-only `preflight` verb and rejects positional commands,
caller-provided paths, unknown flags, root SSH accounts, privileged groups, arbitrary passwordless sudo, writable
helper files, and a failed Docker isolation guard. The installer also creates the configured remote work root and
lease directory as root-owned, non-group-writable directories; review that path before running the installer.

Run those commands only from a reviewed checkout already present on the target; M3 does not yet automate helper
bootstrap. The server-side config is root-owned and records capacity/allocation policy; it is not the local target profile. The
same remote root, capabilities, and concurrency must be present on both sides or preflight fails closed.

## Read-only preflight

```bash
go run ./cmd/remote-test preflight \
  --target dedicated-test \
  --requirements test-env/remote/preflight-vikunja.example.yml \
  --source worktree
```

On success the command writes a mode-`0600` source bundle under `test-env/.remote-test/packages/` and prints a JSON
plan containing the commit, worktree-patch identity, bundle digest, `run-id`, workspace, port block, network/container
prefixes, Docker/containerd paths, authorization source, and `remote_mutated: false`. It does not upload that bundle.
