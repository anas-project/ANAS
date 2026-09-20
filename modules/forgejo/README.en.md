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

### Directory attribute changes

**Matching key**: the OIDC `sub`, which Forgejo stores verbatim in its internal `login_name` field.
The username comes separately from `preferred_username`, is written once at account creation, and
takes no part in identifying anyone. **Whether this key survives a directory rename depends on which
IAM Provider the deployment selected** — `authentik` and `casdoor` both mint a subject identifier
that is not a directory label, so `sub` does not change on rename; `llng` derives its subject
identifier from the login name by default, so after a rename `sub` changes, Forgejo creates a second
account, and the existing repositories stay behind on the old one (see the matching-key column in
[Module IAM / OIDC support](/en/reference/module-iam-support)).

| Directory change | What Forgejo does | Evidence |
| --- | --- | --- |
| `sAMAccountName` changes | Same account, nothing created; the Forgejo username stays frozen at the old value, repository paths remain `/old/...`, and the identifier in URLs does not follow. This does not hold when the Provider's `sub` is the login name (`llng`), where a second account appears | `inferred` |
| `mail` changes | Not refreshed; the account keeps the address captured at creation. Email takes no part in account binding, but Forgejo emails are globally unique, so while the old address still belongs to the old account a new account using it fails to be created | `inferred` |
| `displayName` and other profile attributes | Written once at account creation and never refreshed afterwards | `inferred` |
| Direct or recursive group membership changes | Reach teams and the site-administrator mapping through the groups claim at the user's **next OIDC login**; without a login nothing converges, and there is no sync or real-time path | `inferred` |
| Account disabled | **No automatic path.** Existing Forgejo sessions, access tokens, and SSH/deploy keys all keep working; the last two never pass through a login, so "it converges at the next sign-in" does not apply to them | `inferred` |
| Account deleted | As above, and the Forgejo account keeps its repositories, issues, and packages untouched; ownership of assets is never transferred automatically | `inferred` |
| Identifier recycled and reassigned | Recycled username: the newcomer's first login collides with the old account's `preferred_username` and account creation fails (fail-closed). A recycled email collides the same way. But if the Provider's `sub` is itself the recycled label (`llng`), the newcomer lands directly on the old account (**fail-open**) | `inferred` |

**Fallback path** — what operations must do for every "no automatic path" row above:

1. When a person is disabled or deleted in the directory, a Forgejo site administrator must **disable
   that account and explicitly revoke every access token and SSH/deploy key it owns**. Whether the
   pinned version's `prohibit_login` also closes tokens and Git over SSH has not been re-checked, so
   do both steps; disabling the account alone is not enough;
2. Before deleting a directory account, transfer the repositories it owns to a successor or an
   organization in Forgejo, then delete the Forgejo account;
3. When group revocation has to take effect immediately, terminate the user's existing Forgejo
   sessions in addition to step 1; waiting for the next login is not enough;
4. **Directory-side process constraints**: usernames and mail aliases must never be recycled, and
   renames must go through the formal process. The Module cannot enforce either.

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
