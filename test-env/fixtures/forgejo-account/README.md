# Native managed Actions account lifecycle

`test-env/scripts/server-forgejo-account-e2e.py` invokes the actual Forgejo
entrypoint helper and fixed Forgejo 15.0.7 application, rather than mocked HTTP
responses. It is deliberately separate from the full Core/Compose deployment
and Incus one-job fixtures. Its database is an isolated SQLite fixture; this is
not PostgreSQL, IAM, browser-session or running-guest cleanup acceptance.

## Environment and inputs

Run only as root **inside a fresh disposable QEMU VM** with an exact cloud-init
ID `anas-incus-host-<six lowercase hexadecimal characters>`. A Docker socket or
`/var/lib/docker` is forbidden. The service itself runs as UID/GID 1000, using
the existing `anas-test` account, and binds only `127.0.0.1:3000`. This fixture
must not be run on the physical SSH host or against existing application data.

The outer owner provides Git and CA certificates, verifies the base image,
retains a bounded QEMU lifetime, and compares the physical host's Docker and
network baseline before/after. All changed files, users, accounts and service
processes belong exclusively to the disposable VM. Failed experiments keep
their original disk and reports rather than rewriting receipts to continue.

Protected root-owned `/opt/anas-forgejo-account-inputs` contains:

```text
forgejo
anas-forgejo-entrypoint
anas-forgejo-actions-controller
server-forgejo-account-e2e.py
source-manifest.json
```

The manifest schema is `anas.forgejo-account-native-inputs/v1`. Its `files` map
contains exactly the four delivered executables/script with SHA-256 hashes;
the upstream Forgejo digest must also match the runner's fixed digest. Record
the complete relevant repository inputs, dirty-checkout identity and compiler
separately. The runner validates root ownership, non-writable ancestors,
single-link regular files, sizes and file identity. It exclusively creates
`/var/lib/gitea` and `/opt/anas-forgejo-account-native`; existing directories are
not reused. Public executables are explicitly 0755 despite the private umask;
credential/config/receipt files remain private.

```sh
sudo python3 /opt/anas-forgejo-account-inputs/server-forgejo-account-e2e.py \
  --vm-id anas-incus-host-abcdef
```

Use the actual VM identity, not the example. No product API, account, path or
password is selected by command-line arguments; helper credentials use stdin.

## Required results and limits

The script's complete `REQUIRED` set is authoritative. It covers a distinct
recovery owner; fresh disabled behavior; rejection of an unowned, same-name,
same-email, same-role user; creation and idempotence; disabling the managed
password; re-enabling the same numeric identity; restart persistence; refusal
to adopt a replacement numeric ID; and independent process exit. Read-only
SQLite equality checks prove repeated reconciliation did not needlessly change
password hashes, without exporting those hashes or salts.

Additional checks run the real controller in disabled mode. A genuinely empty
state exits successfully, an explicit pending-work fixture without a compute
lease fails without changing it, and a dangling state link cannot be accepted
as missing state. Those negative fixtures do not establish actual guest or
registration reclamation; that requires the separate real-workflow harness.

The account keeps its existing site-administrator role. The managed password
is invalidated; the account is not deleted by the product, and separately
created access tokens, SSH keys or browser sessions are not claimed revoked.
The helper requires stable, proven account ownership and a separate recovery
administrator. Fixture-created users may be deliberately removed to test ID
replacement, but only after independently checking their exact fixture ID.

Only `/opt/anas-forgejo-account-native/reports` is public evidence. Do not
archive `private`, the database, application logs, config or receipt store.
The outer owner must independently verify normal VM shutdown, actual QEMU
exit, listener removal and unchanged physical-host baseline, on failure too.
Neither these local guards nor the existence of the runner proves acceptance:

```sh
python3 -m unittest discover -s test-env/scripts -p 'test_forgejo_account_e2e.py'
```
