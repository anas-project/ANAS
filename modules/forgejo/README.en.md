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
and deploy keys remain application-owned. The Module configures no LDAP or SAML source and performs no directory
user/group synchronization or directory password write-back.

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
| `forgejo.actions_runner_image` | string | `pattern: ^(?:[0-9a-f]{64})?$` | `""` | `static` | `FORGEJO_ACTIONS_RUNNER_IMAGE` | no | no | no | yes | `container_recreate` | Pinned SHA-256 fingerprint of the approved Runner VM image |
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
policy, not a second switch, and global Runners are rejected. Enabling requires an independent Incus/KVM endpoint,
restricted-project TLS credential, constrained profile, and pinned Runner image fingerprint; otherwise rendering
fails before a server-only state can be deployed.

The controller creates no registration or VM for an empty queue. Each approved waiting job gets one ephemeral
registration and one VM, with `forgejo-runner one-job` selected by job handle. The token travels through Incus exec
stdin to guest tmpfs. Separate `runner-agent` and `runner-engine` users use rootless Podman inside the disposable VM.
Neither Forgejo nor the VM receives an ANAS host Docker socket or ANAS/Forgejo data mount.

Custom Git hooks and local-path imports are independently configurable and disabled by default. Hooks execute
server-side code as the Forgejo user. Local imports are limited to paths already visible inside the container;
enabling the setting does not add an arbitrary host mount. Changing either setting recreates the container.

`${DATA_PATH}/forgejo` contains the complete `/var/lib/gitea` tree, including repositories, LFS, packages,
attachments, SSH state, and application configuration. Consistent backup/restore must include this tree, the
database Resource, `.anas/secrets.yml`, and deployment metadata.

The Module is `developing`. Database/architecture matrices, browser OIDC, restore, and upgrade/rollback E2E remain
release gates. SMTP, object storage, and external search are not automatically configured. The Actions controller
and Runner-image assets are wired, but independent Incus/KVM, egress, and real one-job E2E are still pending.
`forgejo.oidc_client_secret` uses manual `migrate` rotation because Forgejo cannot participate in the unified
transactional rotation contract.

See the [technical documentation](docs/technical.en.md) for current implementation details. Design decisions and
remaining work are tracked in the [Forgejo Module design](../../docs/architecture/forgejo-module-design.md) and
[implementation plan](../../dev-docs/plans/forgejo-module.md).
