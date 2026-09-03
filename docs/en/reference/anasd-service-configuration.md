# `anasd` service configuration

`anasd` uses host-level configuration separate from every workspace `config.yml`. The default path is `/etc/anas/anasd.yml`; startup accepts only `--config` to select another absolute path. HTTP requests and process environment variables cannot override listener, workspace, store, or certificate settings.

The file must be owned by `root`, have permissions no wider than `0600`, and be a regular non-symlink file. Decoding is strict: unknown fields, a second YAML document, replacement or mutation while reading, and files larger than 1 MiB all make startup fail.

```yaml
api_version: anas.console-config/v1
mode: lan
port: 8080
allowed_dns_hosts:
  - anas.example.test
console_store: /var/lib/anas/console
workspaces:
  - id: main
    path: /srv/anas
backup_targets:
  - id: local_archive
    path: /srv/anas-backups
tls:
  lego:
    base_domain: example.test
    certificate: /var/lib/anas/lego/example.test.crt
    private_key: /var/lib/anas/lego/example.test.key
    issuer: /var/lib/anas/lego/example.test.issuer.crt
    trust_bundle: /var/lib/anas/lego/anas-trust-bundle.crt
    internal_ca: /var/lib/anas/lego/anas-internal-ca.crt
    issuer_marker: /var/lib/anas/lego/.issuer
  temporary:
    certificate: /var/lib/anas/console-tls/temp-console.crt
    private_key: /var/lib/anas/console-tls/temp-console.key
    dns_names:
      - bootstrap.example.test
    ip_addresses:
      - 192.0.2.10
trusted_proxy:
  bind_address: 0.0.0.0
  port: 8443
  public_url: https://anas.example.test:9000
  allowed_source_ips:
    - 172.18.0.5
  allowed_dns_hosts:
    - anas.example.test
  oidc_issuer: https://iam.example.test:9000
  platform_admin_group: Admins
  client_ca: /etc/anas/trusted-proxy/traefik-client-ca.crt
  client_spki_sha256:
    - 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
```

The paths are illustrative; lego fields must point at the deployment's actual published `ANAS_TLS_*` artifacts.

## Fields

| Field | Constraint and purpose |
| --- | --- |
| `api_version` | Must be `anas.console-config/v1`. |
| `mode` | `lan` (default) or `loopback`. This is static and never depends on administrators, certificates, IAM, Traefik, interfaces, or workspace state. |
| `port` | Fixed management port, default `8080`, range `1..65535`; state-gated HTTP and TLS share it. |
| `allowed_dns_hosts` | Exact additional ASCII DNS Hosts; IPs, ports, and wildcards are rejected. A numeric Host must still equal the local address actually reached by that connection. |
| `console_store` | Absolute audit, monotonic capability-state, authentication, and job-event state path. The directory is `0700`, private state files are `0600`, and it must be outside every workspace so snapshot/backup/restore cannot overwrite control-plane state. |
| `workspaces` | Server-owned `id -> absolute path` registrations. API clients submit IDs only. Each workspace must exist and contain `.anas/`. |
| `backup_targets` | Root-owned `id -> absolute path` backup-destination allowlist. Browsers and API clients select an ID and cannot submit or replace its host path. IDs and paths are unique, and no target may overlap the console store, a workspace, or another target. |
| `tls.lego` | Long-lived certificate, key, issuer, trust bundle, independent `anas-internal-ca.crt`, and `.issuer` marker published by lego. `anasd` consumes and validates them; it does not issue long-lived certificates. |
| `tls.temporary` | Optional temporary self-signed leaf paths and explicit SANs. At least one `dns_names` or concrete `ip_addresses` entry is required; no host, interface, or environment discovery occurs. Filenames must be the fixed names shown above for explicit CLI generation. |
| `trusted_proxy.bind_address` / `port` | Optional separate TLS-only listener. The address is an IP literal and the port differs from the direct port. It accepts no plaintext and never reuses the direct listener. |
| `trusted_proxy.public_url` / `allowed_dns_hosts` | Public Traefik/OIDC HTTPS origin and exact DNS Hosts accepted on this listener. The public URL Host must appear in the list. |
| `trusted_proxy.allowed_source_ips` | Exact Traefik source IPs allowed to connect. CIDRs, unspecified addresses, and broad RFC1918 trust are rejected. This is defense in depth, not a replacement for mTLS. |
| `trusted_proxy.oidc_issuer` / `platform_admin_group` | Fixed canonical HTTPS issuer and resolved directory administrator group. Only a stable issuer+subject in that group maps to `owner`. |
| `trusted_proxy.client_ca` / `client_spki_sha256` | Root-owned CA file for the Traefik client certificate and allowed leaf-public-key SPKI SHA-256 values (64 lowercase hex characters). Both CA validation and the SPKI allowlist must pass. |

When both sources are configured, a valid lego certificate takes priority. Every handshake uses dynamic `GetCertificate` state. A new pair is published atomically only after its key match, validity, ServerAuth usage, SANs, chain, and issuer marker validate. A failed update keeps the last-known-good pair; it never downgrades to the temporary certificate or expands plaintext capabilities.

The service configuration is read only when `anasd` starts; changing its port, mode, Hosts, workspaces, store, trusted proxy, or TLS paths requires a restart. Direct serving-certificate artifacts at those paths are the exception: later TLS handshakes revalidate and hot-swap them without restarting the daemon.

At startup, `backup_targets` paths are cleaned and any resolvable symlink boundaries are checked. The daemon refuses to start when either the direct or resolved path contains, or is contained by, the control plane, a workspace, or another backup target. Capability, plan, list, and terminal-descriptor HTTP requests use public target IDs rather than root-configured paths; only a server-generated descriptor `argv` may include the registered canonical path required by the CLI invocation.

`allowed_dns_hosts` is not derived from `tls.lego.base_domain`. Full-state direct access uses `anas.<base_domain>`: administrators must add that name to `allowed_dns_hosts` and make it resolvable by client DNS. The lego leaf must cover both `<base_domain>` and `anas.<base_domain>`; the long-lived CA/ACME flow must not add an `IP:` SAN for the console in `ca.sh`. TLS by numeric address is limited to concrete addresses explicitly declared for the short-lived self-signed candidate under `tls.temporary`.

Every TLS artifact must be a root-owned, regular, non-symlink file. Private keys must not be accessible to group or others; certificates, issuers, trust bundles, internal CAs, and markers must not be writable by group or others; no artifact may be executable. The temporary cert and key generated by the CLI are both `0600`. The issuer marker accepts exactly `internal` or `acme`, with one optional trailing newline. The internal CA must be one currently valid self-signed CA contained in the trust bundle; it remains downloadable after the serving certificate moves to ACME.

Last-known-good state exists only in memory in the current `anasd` process. After a restart, at least one candidate must validate from disk. If none does, TLS handshakes fail, while plaintext HTTP remains constrained by the current console state and route allowlist and gains no additional capability.

## systemd privilege boundary

The release `anasd.service` explicitly uses `User=root` and `Group=root`, sets `UMask=0077`,
`NoNewPrivileges=true`, and `ProtectSystem=strict`, and limits its default writable paths to the
control-plane state and standard workspace/backup roots: `/var/lib/anas`, `/srv/anas`, and
`/srv/anas-backups`. If service configuration uses another workspace, backup target, or console
store, add those exact directories to `ReadWritePaths` in a systemd drop-in. Do not remove
`ProtectSystem` and make the whole host writable again.

Running as root is not a low-privilege sandbox. `anasd` reads root-only TLS private keys, modifies
workspace configuration, deployments, snapshots, and backups, and may connect to the Docker socket.
The Docker socket can create privileged containers and mount host paths, so its authority is
effectively root. A principal that can replace workspace content or a TLS private key can likewise
control service output or the management entry. Access to these paths, `/etc/anas/anasd.yml`, and
`/var/run/docker.sock` must therefore be limited to trusted administrators. The unit's writable-path
restriction protects against accidental writes; it is not a boundary against malicious root or a
Docker administrator.

The installer creates the first root-owned `0600` configuration, preserves it during upgrades, and
updates only binaries and the unit. Uninstall preserves configuration, workspaces, and console state
by default; `--purge` removes only service configuration and source preference, never state or user
data.

## Listener and bootstrap risk

`lan` binds `0.0.0.0` and, where supported, `[::]`. It means every interface, not “a detected LAN”: Wi-Fi, VPNs, container bridges, and public interfaces can all be included. Plaintext bootstrap HTTP has no confidentiality and does not resist an active attacker. Administrators own interface isolation and firewall policy. This is the accepted boundary that makes a new NAS immediately reachable from another device on its network.

The management UI is embedded in the `anasd` binary and needs no separate web service. In `bootstrap`, the root path serves the main UI over direct plaintext HTTP or TLS. In `enrollment`/`full`, the plaintext root only redirects to the canonical HTTPS origin, while the TLS root serves the main UI. The fixed `/emergency` page uses a small, Vue-independent bundle for minimal health checks when the main SPA is damaged; it follows the same state, transport, and direct-listener restrictions and is not an authentication bypass.

If that boundary is unacceptable, set `mode: loopback`; only `127.0.0.1`/`[::1]` are bound, for use through `ssh -L`. Alternatively, explicitly run from the current SSH session:

```bash
sudo anas console tls --self-signed
```

The command uses only SANs declared under `tls.temporary`, prints SHA-256 certificate and SPKI fingerprints, and issues a bootstrap token with a default 20-minute lifetime. `sudo anas console token --ttl 20m` issues only a token; the accepted range is 15–30 minutes. Raw tokens are shown once and only SHA-256 digests are stored. Reissuing revokes the previous token and bootstrap session. In `enrollment`, the command can issue only a same-transaction recovery token for system/CA, job/events, and handoff routes; it does not reopen config, plan, or apply. Token issuance is rejected permanently in `full`.

`anasd` now persists the monotonic `bootstrap → enrollment → full` state. A validated lego `internal` or `acme` certificate atomically advances `bootstrap` to `enrollment`; a temporary self-signed certificate never changes capability state. At the old origin, the browser uses its restricted bootstrap session to issue a one-use handoff, then submits it as a top-level form POST to `https://anas.<base_domain>:<port>` for a Secure enrollment session. The server binds that handoff to the source and target origins and to the certificate SPKI actually selected for that TLS connection's handshake. Direct management TLS disables session resumption so every connection selects a certificate and records that SPKI. On success, the exchange sets an HttpOnly session cookie plus a separate same-origin SPA-readable CSRF cookie and returns a `303` to the target origin root; it exposes no CSRF value in a URL or JSON. Initial-owner creation requires that CSRF cookie to exactly match `X-CSRF-Token` and also validates the server-side session digest. Its success response deletes the enrollment session, CSRF, and bootstrap cookies. Initial local-owner creation, revocation of all enrollment credentials, and publication of `full` use a recoverable transaction. Once its WAL is durable, browser disconnection cannot interrupt convergence; commit or rollback arbitration continues under an independent bounded context. The flow does not enable CORS. Enrollment plaintext/TLS can download the validated public `anas-internal-ca.crt`; the enrollment/full plaintext root only redirects to the configured canonical HTTPS origin and never uses the request Host, query, Cookie, Authorization, or body. Initial config/plan/apply and full-state direct local step-up HTTP routes are available. In full state, the mTLS trusted-proxy listener combines an exact source IP, fixed identity headers, and an OIDC session to expose the same capabilities. Local login, bootstrap, enrollment, and the emergency UI always return `404` on the trusted-proxy origin.

## Traefik / OIDC trusted entry

When `oauth2_proxy.console_proxy_enabled` is enabled, the Module publishes the `anas.<base_domain>` route, the existing `ANAS_FORWARD_AUTH_*` middleware, and a Traefik `serversTransport` named `ANAS_CONSOLE_MTLS`. oauth2-proxy remains the OIDC client; `anasd` performs no OIDC discovery or code exchange and never accepts an IdP password. It consumes only seven fixed identity fields generated after ForwardAuth verification on its separate trusted listener, rejecting duplicates, comma ambiguity, or missing/mismatched issuer and subject. The direct listener unconditionally strips those headers.

The Traefik Hook creates a stable, dedicated CA and client certificate for each named transport in the Secret Store. It projects the public CA, client cert/key, and SPKI digest under Traefik-only runtime state at `dynamic/client-identities/ANAS_CONSOLE_MTLS/`; the CA private key is never projected. Configure `anasd`'s `client_ca` and `client_spki_sha256` from this identity, copying the public CA to a stable root-owned `0600` path before starting the service. M1.5 installer automation does not yet perform this copy; a lego server CA, ordinary route TLS, or source-IP allowlist alone is not proxy identity.

`allowed_source_ips` must contain the one exact address Traefik currently uses to reach the host; inspect the `anas_traefik` container network before configuring it. If a Docker recreation changes that address, update the root-managed configuration and restart `anasd`. Requests cannot reach the HTTP handler until mTLS succeeds. Proxy high-risk actions accept only an OIDC `auth_time` no older than five minutes. The issued StepUpProof is additionally bound to issuer, subject, action/plan, one use, and a short TTL; stale authentication sends the user back to the IdP and never falls back to a local or IdP password field.

Record both entry points at any time without relying on the console page:

```bash
sudo anas console status
```

`Direct recovery (local owner)` is the IAM-independent recovery entry; `Traefik / OIDC` is the normal proxy entry. The access page also shows both, but a direct recovery origin is plain non-clickable text on the proxy origin and never a local-account fallback link.

Before opening any listener, `anasd` also validates or creates `console_store/jobs.jsonl` and exclusively holds `jobs.execution.lock` for the process lifetime. Only the daemon holding that lease performs startup recovery and marks abandoned `running` jobs `interrupted`; a second daemon fails before recovery and listening without changing the first daemon's active jobs. Unchanged steady-state reads reuse validated state. Appends from cooperating Store instances in the same process validate only the new tail when a bounded receipt chain is complete; cross-process or otherwise unknown growth conservatively triggers full recovery. Every supported job/event writer must use `consolejobs.Store`/`jobs.lock`. Caller-driven record or transaction oversize is rejected as invalid input without poisoning the Store. The job journal measures the prospective state's reclaimable space when its projected size crosses an internal 64 MiB boundary. At the savings threshold it writes a chunked checkpoint with a generation, counts, and SHA-256 seal; only after temp fsync, atomic rename, and directory fsync does it truncate the old inode. An already-open Store that observes a canonical inode replacement must fully validate under `jobs.lock`, accept only a fully sealed checkpoint with a higher generation and no rollback relative to its validated state, and then switch descriptors. Receipts never cross inodes.

Audit independently uses `audit.jsonl`, a fixed `audit.lock` that is never rotated, and the reserved temporary path `audit.jsonl.compacting`. Initial creation first persists a checksummed pristine lock slot that pins StoreID, carries the current policy, and has zero generation/sequence/prune watermarks and no commit time, then fsyncs the matching journal header; the first nonzero policy is pinned thereafter. A blank lock may accompany an existing empty journal or, for legacy compatibility, a sole incomplete record that is identifiable as the old Event encoding and has no complete record. A torn first slot with no valid revision may accompany only an exactly empty journal and is retried as revision 1; the sole exception is a journal that fully validates as legacy Event-only, whose existing watermarks and commit time are used to retry its initial metadata migration. With a valid pristine slot, Open may complete only an empty journal or a canonical partial header provably belonging to that slot's StoreID while retaining its policy. A complete envelope header without valid metadata is rejected; a nonempty torn first slot paired with any partial journal is likewise rejected without truncation. Lock metadata occupies two fixed 512-byte slots; each has an increasing revision and a SHA-256 over its revision and metadata. Updates alternate slots with `WriteAt` and never truncate the lock inode. Recovery selects the highest complete valid revision, falls back when the newest slot is torn, and fails closed if an initialized store has no valid slot. A torn new slot while migrating legacy single-line metadata likewise falls back to the intact old prefix. Metadata later records generation, `last_sequence`, `pruned_through`, and `last_recorded_at`; every newly acknowledged append/compaction updates it before unlock and success. A durable journal with older metadata is an allowed later crash window: Open fully validates a same-lineage advance and writes a new slot to catch up. A missing or empty journal, journal rollback behind metadata, or mismatch after policy pinning fails closed. `audit.Writer` prunes a contiguous sequence prefix by Writer commit time; caller-supplied occurrence timestamps never control retention. A legacy Event-only journal uses inode mtime as legacy `recorded_at` and forces migration on its first append; other automatic replacement requires obsolete history and measured savings. Checkpoints seal lineage, watermarks, event count, retained `recorded_at`, and SHA-256, reject backward time, and require exactly matching snapshot begin/end times; replacement commits only after temp fsync, rename, and directory fsync, then truncates the old inode. An old Writer accepts a replacement under the lock only when it has the same lineage, a higher generation, and no retained-event or watermark rollback. Cancellation is non-poisoning only before any rename attempt when temp cleanup fully succeeds; ambiguity or persistence failure fails closed. A safe pre-rename temp residue is removed by the next locked Open/Compact, while a symlink, hard link, overly permissive file, or non-regular path is not deleted and fails closed. Complete lines are bounded at 2 MiB, caller oversize does not poison the Writer, and ENOSPC retains its cause and fails closed. A local root-equivalent process that ignores the lock and modifies a root-owned `0600` journal remains outside this threat model.

`anasd` currently calls `audit.Open`, whose non-destructive retention defaults use `MaxEvents=0` and `Retention=0` to disable the corresponding pruning dimension; the service configuration has no override fields yet. The crash-safe retention/compaction mechanism is therefore available, but production retention values, periodic `Compact` maintenance, and `GET /api/v1/audit-events` are not wired. When they are wired, every cooperating daemon and CLI Writer must receive the same Options. Audit history is not silently deleted before those product values are explicitly selected.

`GET /api/v1/jobs`, `GET /api/v1/jobs/{id}`, and `GET /api/v1/jobs/{id}/events` expose redacted, read-only job history and SSE replay. Recovery credentials in bootstrap and enrollment states can read only jobs created by the same transaction; a local owner in full state can read only jobs belonging to registered workspaces. SSE uses strict `Last-Event-ID` parsing, machine-readable `410` gaps, heartbeats, per-write deadlines, and a process-wide connection limit. At every replay-batch, poll, and heartbeat boundary it rechecks the current state, session, and object authorization without extending idle TTL; losing access silently closes the stream. A terminal job drains its final events before closing, and a reconnect with a caught-up `Last-Event-ID` receives `204` to stop browser `EventSource`. Job creation and execution routes are not exposed yet.
