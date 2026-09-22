# Baked Runner image acceptance

The immutable-image gate also requires `rootless-oci-exec-limits`. It runs a digest-pinned BusyBox
container as the actual agent through the rootless socket, exercises create and exec, and reads the
effective CPU, memory, PID and no-new-privileges controls inside that container. It never patches the
guest service. The `cgroup-control` replay is explicitly diagnostic and cannot replace this gate.
The independent one-job matrix must still confirm payload execution and resource reclamation.

The image gate also requires `runner-config-readable`, executed as `runner-agent`. Root-only binary
and API probes did not catch the original 0700 public configuration parent. Older binaries missing
this required subtest cannot satisfy the current gate.
It now also requires `rootless-user-session`, which queries the real engine user's systemd manager
after the API is ready. This covers the user bus needed by rootless container DNS helpers; it does not
replace the separate real-workflow/container-start matrix.

`server-forgejo-onejob-e2e.py` is the separate real-workflow matrix. Run it as root only inside the
identified disposable VM, with verified Forgejo 15.0.7, Git and the actual controller test executable.
Its report must be a new child of `/home/anas-test/verification`. It exercises normal completion,
intentional failure, controller SIGTERM cancellation after the payload starts, actual SIGKILL and preserved-state
controller restart, and a queued unapproved repository. Every case checks state, instance, root-disk
and registration inventories. It does not prove recovery after loss of the controller state volume.
It does not exercise Forgejo's separate web/UI cancellation. The pinned Forgejo API uses `commit_sha`
and has no run/jobs/steps endpoint; payload progress is observed through read-only, non-secret columns
of this fixture's SQLite database. No task status is written by the harness. The job image is a
verified amd64 BusyBox manifest pinned by digest at the reachable ECR public registry, not a mutable
Docker Hub tag. External registry reachability is separate from Incus isolation and image acceptance.
The fixture installs a public test CA only after guest start, with a readable public copy, and uses
guest `/run` for the CA tool's temporary files to avoid boot-time `/tmp` cleanup. TLS verification is
not disabled; the private source credential paths and Incus fences are not changed.

These are explicit administrator tests inside a **disposable QEMU VM** whose
cloud-init instance ID matches `anas-runner-bake-<six lowercase letters/digits>`.
They reject the physical business host, another instance ID and any VM with a
Docker socket/data root. Do not use existing business Docker as the execution
environment. Record its physical-host baseline separately before and after the VM.

## Real copy-generator regression

Generate the actual default recipe with the repository release tool, then run
the following only inside that identified VM with distribution-provided
distrobuilder and squashfs-tools already installed:

```sh
sudo python3 test-env/scripts/test-distrobuilder-copy-native.py \
  --vm-id anas-runner-bake-abc123 \
  --recipe /absolute/generated-runner-recipe.yml \
  --report-root /absolute/new-copy-report
```

This uses a tiny synthetic rootfs to isolate the copy-path bug. The old bare
`forgejo-runner` source must fail, and the generated `sources/forgejo-runner`
source must produce exactly the frozen input bytes in a real squashfs image.
Both controls are required. This is **not** a complete Runner image, Debian
bootstrap, signature validation or one-job result.

## Real resolver-hook regression

The generated recipe must not replace the build chroot's resolver. With the
same disposable-VM guard and a new report directory, run:

```sh
sudo python3 test-env/scripts/test-distrobuilder-resolver-native.py \
  --vm-id anas-runner-bake-abc123 \
  --recipe /absolute/generated-runner-recipe.yml \
  --report-root /absolute/new-resolver-report
```

The old post-files `ln -sf` must fail, while the new guest tmpfiles rule must
pack successfully and create the expected resolver symlink when applied to the
extracted test root. This synthetic rootfs does not establish guest DNS or a
complete Runner image.

## Actual baked-image smoke

Before creating runtime directories or changing the daemon, the harness checks that `/run` has space for
both measured image parts plus 16 MiB of headroom. Retaining previous runtime copies can otherwise exhaust
the VM's tmpfs even when its disk has ample free space. This check does not reserve capacity against other
processes. Staging files and private inputs still require owner-controlled teardown; the final
`owned_baked_image_daemon_resources_removed` event refers only to the Incus objects, not those files or the VM.

The failure diagnostic also filters `systemctl list-jobs` to the fixed Podman/network-online boot dependency
names. This distinguishes a queued service from a completed launch; it does not prove the cause of a failed
engine or turn the failed smoke test into success.

First complete a real `incus-image-artifacts build` and `export` into a fresh
private directory. Keep the independently recorded build fingerprint, tool and
Runner input digests, original recipe and immutable archive. A cancelled attempt
is not an image and must not be silently retried with the same revision.

Compile `internal/computeclient` tests for native Linux amd64, the actual Incus
Provider and `cmd/test2json`. The gate requires an empty Incus daemon in that
disposable VM, with `dnsmasq-base` and a btrfs-capable kernel. Run:

```sh
sudo python3 test-env/scripts/server-incus-runner-image-e2e.py \
  --vm-id anas-runner-bake-abc123 \
  --provider /absolute/provider \
  --tests /absolute/computeclient.test \
  --test2json /absolute/test2json \
  --exported-image /absolute/exported-image \
  --expected-fingerprint '<independently recorded 64-hex bake fingerprint>' \
  --report-root /absolute/new-runner-report
```

It checks real split-file hashes, supplies them at the Provider's fixed private
runtime paths, and requires actual import, repeated ensure and inspect. The
product shared client then creates and starts the baked image under a restricted
container lease. The real Runner binary, one-job flags and access by
`runner-agent` to a rootless Podman API must all work. A parent/subtest skip,
missing pass event or failed package is not acceptance. Test code does not override the Provider's
declared profile or grant devices. The current container profile explicitly permits inner OCI namespaces;
the native `provider-namespace-fence` subtest requires that policy and also rejects privileged/raw/host
disk/character-device requests through the restricted client. This is a declared capability change, not
a claim that all kernel permissions stayed identical to the earlier blocked-nesting profile.

This uses the supported explicit-fingerprint configuration, not a forged signed
catalog. It neither creates a real Forgejo job nor replaces the full one-job,
cancel/crash/registration cleanup matrix. Cleanup refuses unknown instances or
images and removes only the fixed test objects; private runtime credentials and
the disposable VM still need owner-controlled teardown after reports are saved.
The September 22 recovery completed the actual `lab-r4` bake, archive, export
and repeat-build reuse. Its Provider import and restricted-container boot
passed, as did the real Runner 13.2.0 and one-job CLI checks. The rootless engine
API check exited 125, so the overall baked-image smoke **failed**. The recorded
archive is retained for byte-identical restoration; do not rebuild that revision
or relax project/device restrictions to mask the unresolved failure. See the
[recovery review](../../../dev-docs/reviews/2026-09-22-incus-runner-build-recovery.md).

## Real scoped Forgejo API, independently of image boot

`server-forgejo-runner-api-e2e.py` uses the same exact disposable QEMU identity
but must run as the ordinary `anas-test` user, not root. It starts a fresh
loopback-only Forgejo 15.0.7 with SQLite, generates a temporary credential outside
argv, and runs the production client's repository/organization API tests:

```sh
python3 test-env/scripts/server-forgejo-runner-api-e2e.py \
  --vm-id anas-runner-bake-abc123 \
  --forgejo /absolute/verified-forgejo-15.0.7 \
  --tests /absolute/actions-controller.test \
  --test2json /absolute/test2json \
  --report-root /absolute/new-forgejo-api-report
```

Port 13000 must be free. The script refuses pre-existing report/state paths and
checks that registrations are absent after the test. It stops its own process
and removes temporary credential/configuration files; the disposable database
and logs remain private until the VM owner collects non-secret reports and
tears down the VM. This API gate passed with the final nullable-array-compatible
client. It is not a real workflow, the production database matrix, a controller
crash test or a signed release.

## Engine-readiness diagnostics and token-admission tests

The native image gate now retries actual rootless API reads within 35 seconds,
with a three-second deadline per command, rather than assuming service readiness
from the guest entrypoint's existence. On failure it makes two fixed read-only
observations: selected `anas-podman.service` properties and up to 40 journal
lines. Only bounded enums, integers and fixed diagnostic labels are logged;
raw journal text is never printed. Labels describe observations, not proven
causes, and cannot override a failed or missing native pass event. This gate
can run against restored immutable `lab-r4` without altering its image or policy.

Run the separate, unprivileged starter admission checks with:

```sh
python3 -m unittest discover -s test-env/scripts -p 'test_forgejo_runner_start.py'
```

The test executes the actual starter file, intercepts the first filesystem
effect, and uses the installed coreutils timeout with mocked service commands.
It verifies unread token input, failed/rootful/malformed results, delayed
readiness, a real timed-out probe, and the existing active-job behavior. No
Docker, Incus, root privileges, network or real /run writes are required. The
test is included in CI. Passing it does not establish real engine/one-job
operation or rebuild the archived candidate. This continuation's native rerun
remains pending because SSH to the selected physical host did not complete.
