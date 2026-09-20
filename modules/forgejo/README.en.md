# Forgejo

Self-hosted Git collaboration with HTTP/SSH Git, code review, issues, wiki, Git LFS, packages, and ANAS OIDC authentication.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `forgejo` |
| Version / revision | `15.0.7-r1` |
| Status | `developing` |
| Category | `app` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

## Dependencies and minimal configuration

Forgejo requires Traefik, the OIDC IAM capability, and relational database Contract `>=1.0.0 <2.0.0`.
PostgreSQL is the default; MariaDB is also supported.

```yaml
modules:
  forgejo: {}

identity:
  iam:
    provider: llng
```

The default Web URL is `https://git.<BASE_DOMAIN>:<TRAEFIK_BASE_PORT>`. SSH is published on host port 2222;
set `forgejo.ssh_port` before deployment when that port is unavailable and update the firewall accordingly.

## Identity, groups, and recovery

The Module registers confidential OIDC client `forgejo` with callback
`<FORGEJO_DOMAIN_FULL>/user/oauth2/anas/callback` and scopes `openid profile email groups`. Forgejo creates users
just in time. With Samba application filtering enabled, IAM admits `APP_forgejo`, `APP_all`, and administrators;
the administrator group claim grants Forgejo site administration. Organizations, teams, repository permissions,
and deploy keys remain application-owned. The Module never configures a SAML source or writes passwords back to
the directory.

**OIDC is the only login path.** The Module configures no LDAP source, synchronizes neither users nor groups,
and neither publishes nor consumes `anasIdentityAnchor`: the pinned version has no interface that binds an OIDC
identity to a pre-provisioned LDAP account by an immutable id, and falling back to username or email lets a
recycled identifier reach somebody else's old account. The cost is that **revocation never reaches Forgejo on
its own**: once a person is disabled in the directory, their Forgejo account, established session, access tokens,
and SSH keys all keep working, and the last two never pass through a login at all, so nothing "converges at the
next sign-in". An administrator has to disable the account in Forgejo; that step cannot be skipped.

**A rename does not produce a second account.** Forgejo identifies people by the OIDC `sub`, which it keeps in
the internal `login_name` field; the username comes from `preferred_username` and is written only when the
account is created. After a directory rename it is still the same account, with the Forgejo username frozen at
the old value and repository paths still under `/old/...`; the email is not refreshed either. Mail aliases and
usernames must still never be recycled: Forgejo's emails are globally unique, so while the old address still
belongs to the old account a newcomer's first login fails to create one. (Inferred from upstream behavior, not
yet confirmed by a probe.)

Forgejo `/user/logout` clears the application session only. The pinned version exposes neither a stable
RP-Initiated Logout integration for this Module nor an IAM-initiated front/back-channel receiver.

Retrieve the managed local recovery account when IAM is unavailable:

```bash
anas admin local credential forgejo break_glass -w /srv/anas
```

The default username is `admin_forgejo`; use `<FORGEJO_DOMAIN_FULL>/user/login`. This revision provides idempotent
apply only. Forgejo 15 cannot offer a verified transactional password change with rollback through its CLI, so
`anas admin local rotate forgejo break_glass` is intentionally unavailable.

## All configuration parameters

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `forgejo.actions_allowed_scopes` | string | — | `""` | `static` | `FORGEJO_ACTIONS_ALLOWED_SCOPES` | no | no | no | yes | `container_recreate` | Comma-separated organizations or repositories authorized to consume ANAS Runner compute |
| `forgejo.actions_enabled` | bool | — | `false` | `static` | `FORGEJO_ACTIONS_ENABLED` | no | no | no | yes | `container_recreate` | The only shared switch for the Actions server and one-job Runner controller |
| `forgejo.actions_isolation` | enum (`auto`, `incus_vm`, `incus_container`) | — | `auto` | `static` | `FORGEJO_ACTIONS_ISOLATION` | no | no | no | yes | `container_recreate` | Isolation tier requested from the compute provider: a VM has its own guest kernel, a system container shares the host's |
| `forgejo.actions_runner_image` | string | `format: json_object` | `""` | `static` | `FORGEJO_ACTIONS_RUNNER_IMAGE` | no | no | no | yes | `container_recreate` | Structured image reference; Core freezes the fingerprint before rendering |
| `forgejo.custom_git_hooks_enabled` | bool | — | `false` | `static` | `FORGEJO_CUSTOM_GIT_HOOKS_ENABLED` | no | no | no | yes | `container_recreate` | Allow repository custom Git hooks to execute server-side code as the Forgejo user |
| `forgejo.db_name` | string | — | `forgejo` | `static` | `FORGEJO_DB_NAME` | no | no | no | no: `migrate-forgejo-database` | `data_migrate` | Application database name |
| `forgejo.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `FORGEJO_DB_TYPE` | no | no | no | no: `migrate-forgejo-database` | `data_migrate` | Relational database type or automatic selection |
| `forgejo.domain_prefix` | string | `length: 1..63`; `pattern: ^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$` | `git` | `static` | `FORGEJO_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | Service domain prefix |
| `forgejo.iam_protocol` | enum (`auto`, `oidc`) | — | `auto` | `static` | `FORGEJO_IAM_PROTOCOL` | no | no | no | yes | `container_recreate` | IAM login protocol; OIDC only |
| `forgejo.language` | string | — | — | `inherited` | `FORGEJO_LANGUAGE` | no | yes | no | yes | `reconcile` | Default UI language; browser and saved preferences take precedence |
| `forgejo.local_path_import_enabled` | bool | — | `false` | `static` | `FORGEJO_LOCAL_PATH_IMPORT_ENABLED` | no | no | no | yes | `container_recreate` | Allow imports from paths already visible inside the Forgejo container without adding a host mount |
| `forgejo.ssh_port` | int | `1..65535` | `2222` | `static` | `FORGEJO_SSH_PORT` | no | no | no | yes | `container_recreate` | Public SSH Git port |

Database name/type changes do not migrate data. Back up the database and persistent volume before an explicit migration.

## Actions, storage, and limitations

Actions defaults off and exposes exactly one feature switch, `forgejo.actions_enabled`, for both the Forgejo server
and the one-job Runner controller. There is no `runner.enabled`. Repository/organization scopes are authorization
policy, not a second switch, and global Runners are rejected. Enabling also requires an independent Incus host --
the `incus` Module supplies its endpoint, restricted certificate, and profile through the compute contract -- plus a
pinned Runner image fingerprint. Two places check the prerequisites: the hook validates scopes, the control-plane
account password, and the fingerprint at render time; a one-shot preflight connects to Incus and validates the
project, quotas, and profile before Forgejo starts. Either failure stops Forgejo, so a server-only state cannot be
deployed.

### The two isolation tiers

`forgejo.actions_isolation` decides what a job runs inside. It defaults to `auto`:

| Tier | Instance | Kernel | Host requirement |
| --- | --- | --- | --- |
| `incus_container` (what `auto` resolves to) | Unprivileged Incus system container | **Shares the host kernel** | No KVM needed |
| `incus_vm` | QEMU/KVM virtual machine | Own guest kernel | Host must provide KVM |

The container tier is the default because NAS boxes and small hosts do not reliably provide KVM, and a default that
needs it would make Actions uninstallable on that hardware. Quotas, one-shot instances, absent host mounts and
sockets, and the egress allowlist are identical in both tiers -- **the only difference is the kernel boundary**. The
container tier's isolation rests on the host kernel, so a kernel privilege-escalation bug reaches the host. Set
`incus_vm` explicitly, on a KVM-capable host, for **scopes whose writers span trust domains or that execute
untrusted input** (typically a public repository taking outside pull requests). Tier selection never happens on its
own: a host without KVM is not silently downgraded, and a host with KVM is not silently upgraded.

### The Forgejo account the controller uses

Enabling Actions reconciles a fixed account, `anas_actions_controller`, whose password is held in the ANAS Secret
Store and which the controller uses to call the Actions Runner API. It is not a human sign-in path; the human
recovery path is the `break_glass` account below.

**That account is currently created as a site administrator, and this is a recorded privilege deviation.** The
controller only calls three `actions/runners` endpoints inside approved scopes; site administrator is merely the
consequence of there being no reconciliation path that grants organization-owner or repository-admin rights per
scope. Two consequences to be aware of: compromising the controller is equivalent to compromising a Forgejo site
administrator, and **turning `actions_enabled` off does not revoke this account** -- disable or delete it in Forgejo
by hand. M3 of the implementation plan tracks the convergence.

The controller creates no registration or instance for an empty queue. Each approved waiting job gets one ephemeral
registration and one instance, with `forgejo-runner one-job` selected by job handle. The token travels through Incus
exec stdin to guest tmpfs. Separate `runner-agent` and `runner-engine` users use rootless Podman inside the
disposable instance. Neither Forgejo nor the instance receives an ANAS host Docker socket or ANAS/Forgejo data
mount.

Custom Git hooks and local-path imports are independently configurable and disabled by default. Hooks execute
server-side code as the Forgejo user. Local imports are limited to paths already visible inside the container;
enabling the setting does not add an arbitrary host mount. Changing either setting recreates the container.

`${DATA_PATH}/forgejo` contains the complete `/var/lib/gitea` tree, including repositories, LFS, packages,
attachments, SSH state, and application configuration. Consistent backup/restore must include this tree, the
database Resource, `.anas/secrets.yml`, and deployment metadata.

Actions controller state lives in the Docker named volume `forgejo_actions_state`. It is outside that consistency
point but **is not freely discardable today**: losing it leaves orphaned Runner instances uncollected until the
switch is turned off, and leaves the occasional orphaned Runner registration in Forgejo permanently. Keep the volume
when rebuilding a deployment.

The Module is `developing`. Database/architecture matrices, browser OIDC, restore, and upgrade/rollback E2E remain
release gates. SMTP, object storage, and external search are not automatically configured. The Actions controller
and Runner-image assets are wired, but an independent Incus host, egress, and real one-job E2E (once per tier) are
still pending. Runner images are signed off per amd64/arm64 and per container/VM tier, while the configuration holds
a single fingerprint, so a wrong tier or architecture only surfaces when instance creation fails.
`forgejo.oidc_client_secret` uses manual `migrate` rotation because Forgejo cannot participate in the unified
transactional rotation contract.

See the [technical documentation](docs/technical.en.md) for current implementation details. Design decisions and
remaining work are tracked in the [Forgejo Module design](../../docs/architecture/forgejo-module-design.md) and
[implementation plan](dev-docs/plans/forgejo-module.md).

## Structured compute images

```yaml
modules:
  incus:
    config:
      image_architecture: amd64
  forgejo:
    config:
      actions_runner_image: {fingerprint: "<64hex>"}
```

Use the fingerprint of an existing image in the lease project. Old bare strings are refused.
Named references need a trusted release catalog; the shipped catalog is currently empty.
