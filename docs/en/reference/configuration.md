# Structured `config.yml` and environment-variable inventory

This reference distinguishes settings with a structured `config.yml` entry from overrides that currently require top-level `env:`. Counts reflect the 2026-08-15 working tree and are not a fixed ABI.

## Summary

- `anas config list --json` currently declares **128** configurable parameters: 14 global and 114 module parameters.
- 110 module parameters live at `modules.<module>.config.<parameter>`. Four declared `samba_fs` parameters use `env.<KEY>` because they intentionally export bare environment names.
- Control fields under `modules`, `administration`, `identity`, `dynamic_dns`, `rollback`, and `secrets` are structured but are not parameter-to-environment mappings, so they are not included in the 128 count.
- Top-level `env:` is an intentionally open escape hatch. The raw-only inventory below covers keys explicitly consumed by this repository, not every possible environment key.

“Structured” has two layers:

1. native fields parsed and validated by the Go YAML schema;
2. manifest-declared parameters whose names, defaults, types, sensitivity, and change policies are visible to `config list/set/explain/plan` even though their final transport is an environment variable.

## Native structured fields

| YAML path | Meaning | Status |
| --- | --- | --- |
| `module_source` | Module distribution profile: `official`, `official-cn`, or the `cn` shorthand | used for catalog, download, cache, lock, and CN defaults |
| `modules.<module>` | Requested modules and their settings | used |
| `global.*` | 14 deployment-wide parameters | used |
| `administration.bootstrap.username` | Bootstrap administrator username | used |
| `administration.local_accounts.username_template` | Global local-administrator username template | invalid; usernames are ANAS-managed |
| `administration.local_accounts.password_length` | Generated length, minimum 16 | used |
| `identity.directory.provider` | Directory provider; currently `samba_dc` | used |
| `identity.iam.provider` | IAM provider | used |
| `identity.iam.default_protocol` | Default IAM protocol; omitted means `oidc`, with per-module manifest fallback | used |
| `dynamic_dns.provider` | Module maintaining deployment DNS records, or `auto` | used |
| `dynamic_dns.dns_provider` | DNS vendor | used |
| `rollback.snapshot.backend` | Snapshot backend | used |
| `rollback.snapshot.source` | Snapshot source | used |
| `rollback.snapshot.root` | Snapshot root | used |
| `rollback.snapshot.keep_auto` | Automatic snapshot retention | used |
| `secrets.<name>` | User secret distributed to declared consumers | used; dynamic keys |
| `modules.<module>.enabled` | Enable or disable a service | used |
| `modules.<module>.version` | Exact Module release such as `34.0.2-r4` | `module update` resolves it to an immutable OCI digest |
| `modules.<module>.depends_on[]` | User-added dependencies | used |
| `modules.<module>.identity.login_protocol` | `auto`, `oidc`, or `saml` | used |
| `modules.<module>.administration.local_accounts.<id>.username` | Module-local username override | invalid; usernames are ANAS-managed |
| `modules.<module>.config.<parameter>` | Manifest-declared module parameter | used |
| `env.<KEY>` | Raw environment escape hatch | open map |

`config.Load` uses `KnownFields(true)`. Misspelled structured fields fail instead of being silently ignored, except inside the intentionally open `secrets`, `modules`, and `env` maps. Although `modules.<module>.config` is a map at YAML decode time, `config import` and deployment resolution now validate every key and value type against the manifest; hand-written YAML cannot bypass the declaration checks used by `config set`.

`module_source` defaults to `official`. The `cn` shorthand is normalized to `official-cn` in managed
configuration. When `official-cn` / `cn` is selected and `global.chinese_speedup` is not explicitly
set, importing persists `global.chinese_speedup: true`; an explicit `false` is never overridden.

`global.timezone`, `global.default_language`, and `global.default_locale` are optional and explicit values are validated and normalized. When `default_locale` is absent, the runner uses an explicit region-bearing `default_language`, then host locale, CLDR likely-subtag inference, and finally `en-US`. A language directly becomes the locale only when `language.Region()` returns `Exact`: `en-GB` qualifies, while `en` and `zh-Hans` do not replace an available host locale. `config list` and rendered environments show the final BCP 47 value.

Language controls the UI-text fallback and locale controls regional formatting. Derivation supplies a default; it does not merge these concepts. See the [module timezone and language matrix](/en/reference/module-localization) for actual consumers.

## The 128 declared parameters

Every entry below appears in `anas config list`. Ordinary editable parameters can be addressed by `anas config set`; `credential_rotate`, `data_migrate`, and `immutable` entries are inventory/explain-only and require their dedicated workflow. Global parameters use `global.<parameter>`; ordinary module parameters use `modules.<module>.config.<parameter>`.

| Owner | Count | Parameters |
| --- | ---: | --- |
| `global` | 14 | `base_domain`, `chinese_build_speedup`, `chinese_speedup`, `container_prefix`, `default_language`, `default_locale`, `dns_server`, `email`, `host_ip`, `ipv4`, `ipv6`, `network_prefix`, `timezone`, `virtual_domain` |
| `authentik` | 6 | `db_name`, `db_type`, `domain_prefix`, `ldap_enabled`, `ldap_password_writeback`, `log_level` |
| `collabora` | 5 | `admin_password`, `admin_username`, `auto_save`, `domain_prefix`, `log_level` |
| `ddns_go` | 10 | `dns_provider`, `domain_prefix`, `interval`, `ipv4_gettype`, `ipv4_interface`, `ipv4_urls`, `ipv6_gettype`, `ipv6_interface`, `ipv6_urls`, `web_enabled` |
| `ddns_updater` | 10 | `dns_provider`, `domain_prefix`, `forward_auth_interface`, `publicip_dns_providers`, `publicip_fetchers`, `publicip_ipv4_providers`, `publicip_ipv6_providers`, `publicip_providers`, `ttl`, `zone_identifier` |
| `eturnal` | 2 | `domain_prefix`, `port` |
| `lam` | 3 | `admin_password`, `domain_prefix`, `language` |
| `lego` | 2 | `dns_provider`, `dns_server` |
| `llng` | 8 | `adminer_enabled`, `db_name`, `db_type`, `domain_prefix`, `enable_test`, `log_level`, `manager_domain_prefix`, `test_domain_prefix` |
| `mariadb` | 2 | `adminer_enabled`, `root_password` |
| `meshcentral` | 5 | `db_name`, `db_type`, `domain_prefix`, `iam_protocol`, `mps_port` |
| `netbird` | 3 | `adminer_enabled`, `domain_prefix`, `iam_protocol` |
| `nextcloud` | 13 | `db_name`, `db_type`, `domain_prefix`, `iam_protocol`, `language`, `locale`, `log_level`, `memories_enabled`, `memory_limit`, `phone_region`, `rm_skeleton_files`, `talk_enabled`, `upload_max_size` |
| `oauth2_proxy` | 3 | `allow_groups`, `domain_prefix`, `iam_protocol` |
| `postgres` | 3 | `adminer_enabled`, `password`, `username` |
| `samba_dc` | 30 | `admin_name`, `admin_password`, `administrator_password`, `anchor_bind_name`, `anchor_bind_password`, `anchor_scan_interval`, `app_filter`, `create_structure`, `dns_allowed_networks`, `dns_cache_size`, `dns_debug`, `dns_forwarders`, `ldap_bind_name`, `ldap_bind_password`, `log_level`, `max_log_size`, `netbios_name`, `password_bind_name`, `password_bind_password`, `realm`, `template_homedir`, `template_shell`, `user_complex_pass`, `user_lockout_duration`, `user_lockout_reset_after`, `user_lockout_threshold`, `user_max_pass_age`, `user_min_pass_age`, `user_min_pass_length`, `user_password_history` |
| `samba_fs` | 7 | `hostname`, `log_level`, `share_access_mode`, `share_dir_name`, `share_guest_read_only`, `use_default_domain`, `wsdd_log_level` |
| `traefik` | 2 | `base_port`, `domain_prefix` |

The Nextcloud administrator password is not a configuration parameter. It is
owned by the managed `break_glass` Secret and application handler and cannot be
written in YAML.

`global.default_service_root_password` has been removed. LAM, Collabora, and
Samba DC own their password parameters and generate independent Secrets when
those parameters are omitted; no cross-application administrator password remains.

Four `samba_fs` parameters have manifest metadata but use top-level `env:` because `config.exports` publishes a bare name:

| YAML address | Owner | Environment key |
| --- | --- | --- |
| `env.SHARE_ACCESS_MODE` | `samba_fs` | `SHARE_ACCESS_MODE` |
| `env.SHARE_DIR_NAME` | `samba_fs` | `SHARE_DIR_NAME` |
| `env.SHARE_GUEST_READ_ONLY` | `samba_fs` | `SHARE_GUEST_READ_ONLY` |
| `env.USE_DEFAULT_DOMAIN` | `samba_fs` | `USE_DEFAULT_DOMAIN` |

For example, `anas config set samba_fs.share_guest_read_only Yes` accepts the logical parameter path but writes `env.SHARE_GUEST_READ_ONLY`.

## What changing a parameter does

`anas config list --json` is the authoritative machine-readable inventory for parameter names, environment keys, defaults, and change outcomes. The 128 current entries group by effect as follows. An effect describes the action required on an existing deployment; transporting a value into `.env` does not by itself mean it was applied.

| Effect | Count | Change outcome |
| --- | ---: | --- |
| `container_recreate` | 88 | Re-render and recreate the affected container or Compose project |
| `credential_rotate` | 7 | Ordinary setting and replacement import are refused; use a credential-rotation transaction to update application state and the Secret Store together |
| `data_migrate` | 9 | Ordinary setting and deployment activation are blocked until persistent data, a database, or membership is migrated |
| `hot_reload` | 8 | The declared target is a Samba management command; the current executor conservatively creates a deployment and runs Compose `up` for the affected container |
| `image_rebuild` | 1 | Rebuild with `anas apply --build`, then deploy |
| `immutable` | 3 | Generic `config set` refuses the change; use a replacement or domain-migration workflow |
| `reconcile` | 12 | The declared target is application/API/file reconciliation; the current executor applies it through a new deployment and container startup |

### Actual execution boundary in this release

An effect states the intended lifecycle semantics; `executor` identifies the path this release actually takes. A generic module `config_apply` hook does not exist yet, so both `hot_reload` and `reconcile` use `deployment_apply_fallback`. Tests intentionally do not claim that an in-place hot reload already exists.

`deployment_apply_fallback` is not another effect; it is the Runner-reported
name of the executor actually used. When a workspace has a running active
deployment, `config set` stores the value, renders a new immutable deployment,
and selects Modules whose render digest changed for Compose `up`. This
conservative path may recreate containers, so it is not a `hot_reload`.
Setting the same value still records a deployment but selects no containers for
`up`. If activation fails, the Runner restarts the previous deployment and
restores `config.yml` plus its managed digest. Before the initial apply, while
the workspace is stopped, or with an explicit defer request, the value instead
enters a pending/deferred state and does not start containers unexpectedly.

| Effect | Current observable execution result |
| --- | --- |
| `container_recreate` | Store the value, generate and activate a deployment, and run Compose `up` for Modules whose render digest changed; the real-Docker lifecycle suite also requires the container ID to change |
| `hot_reload`, `reconcile` | Render the target value into a new deployment and run Compose `up` only for affected Modules, without Compose `build` |
| `image_rebuild` | Generate a deployment, run Compose `build` before `up`, and record `images_built: true` in its manifest |
| `credential_rotate` | Refuse both `config set` and replacement import; leave the Secret Store and runtime unchanged |
| `data_migrate` | A candidate can be rendered, but ordinary activation stops before any Compose or host-network mutation and reports the parameter and migration operation |
| `immutable` | Stop at the same pre-runtime boundary and report the replacement/domain-migration operation |

### In-place update capability audit

The following results answer whether the upstream service can apply a change in place; they do not claim that the current Runner calls that interface. On 2026-08-15, `test-env/scripts/server-parameter-inplace-e2e.sh` ran against the isolated Docker daemon on ln. It compared container IDs, read application state after each change, and restored every live value before exiting. `config set` still uses the deployment fallback described above.

| Parameters | Complete effect in place | Observed result |
| --- | --- | --- |
| All eight `samba_dc.user_*` `hot_reload` settings | Yes | `samba-tool domain passwordsettings set/show` changed and restored all eight policies while the Samba DC container ID stayed unchanged |
| `global.default_language` | Yes | Nextcloud changed through `occ`; LAM rewrote its profile while Apache was running, with neither container recreated. A handler must serialize this with startup configuration writes |
| `global.default_locale` | Yes | Its current consumer, Nextcloud, changed through `occ config:system:set` while the container ID stayed unchanged |
| `nextcloud.language`, `nextcloud.locale` | Yes | The actual system configuration written by `occ` was immediately readable with the same container ID |
| `samba_fs.share_access_mode`, `samba_fs.share_guest_read_only` | Yes, with a new handler | `smbcontrol all reload-config` made a running `smbd` publish the changed configuration while ACLs changed online. The existing `fix_perm.sh` cannot yet accept explicit inputs from a new deployment |
| `nextcloud.memories_enabled` | Conditional | Nextcloud supports online app enable/disable, but enabling may also download a pinned release, modify the database, and start places initialization. This needs a transaction, progress, and verification rather than a lone `occ app:enable` |
| `authentik.domain_prefix`, `llng.domain_prefix`, `nextcloud.domain_prefix` | No | Their complete effect includes Traefik Docker labels. Docker 24.0.7 cannot change a container label with `docker update`; at least the routed containers must be recreated while IAM clients/metadata are reconciled |
| `lego.dns_provider` | No | A one-shot `docker exec` can receive a new provider, but PID 1's cron environment retains the old value and later renewals revert to the old provider |
| `global.virtual_domain` | No | Lego retains the old mode in its long-lived environment, and certificate replacement also requires every TLS consumer to reload or recreate; one certificate command cannot complete the global effect |

Dedicated `config_apply` handlers are therefore possible for the first six groups. The domain, DNS-provider, and certificate-mode groups cannot promise that every container stays unchanged. `reconcile` means converging external state; it does not inherently mean hot reload.

The table below states what every owner's inputs ultimately change. Each named parameter is covered by the runtime-consumer audit in `internal/runner`. Dynamically assembled keys require an explicit, explained exception. A value read only by an upstream image must likewise cite exact upstream evidence. No current parameter is accepted merely because `env_file` passes it through: retained Collabora, Nextcloud, and database-image settings all have an explicit hook, container-script, or Compose translation.

| Owner | Parameters | Observable result / consumer boundary |
| --- | --- | --- |
| `global` | `base_domain`, `virtual_domain`, `email` | Derive application URLs, AD realm, ACME/internal-CA mode, and service contact email |
| `global` | `container_prefix`, `network_prefix` | Change Compose container names, network names, and cross-container addresses |
| `global` | `host_ip`, `dns_server`, `ipv4`, `ipv6` | Change host route targets, container DNS, and DDNS A/AAAA intent |
| `global` | `timezone`, `default_language`, `default_locale` | Produce final `TZ`/BCP 47 defaults and deliver them only to applications declaring support |
| `global` | `chinese_speedup`, `chinese_build_speedup` | Select runtime image/download mirrors and build-time image/package mirrors; the latter rebuilds images |
| `authentik` | `db_name`, `db_type`, `domain_prefix`, `ldap_enabled`, `ldap_password_writeback`, `log_level` | Select the database resource and generate IAM URLs/blueprints, LDAP source/writeback, and logging |
| `collabora` | `admin_username`, `admin_password`, `auto_save`, `domain_prefix`, `log_level` | Map explicitly to upstream `username`/`password` and generate `extra_params`, server name, and route |
| `ddns_go` | `dns_provider`, `domain_prefix`, `interval`, `ipv4_gettype`, `ipv4_interface`, `ipv4_urls`, `ipv6_gettype`, `ipv6_interface`, `ipv6_urls`, `web_enabled` | Generate ddns-go desired state, polling arguments, address discovery, local login, and the host-network route |
| `ddns_updater` | `dns_provider`, `domain_prefix`, `forward_auth_interface`, `publicip_fetchers`, `publicip_providers`, `publicip_ipv4_providers`, `publicip_ipv6_providers`, `publicip_dns_providers`, `ttl`, `zone_identifier` | Generate updater JSON, public-address probes, DNS-provider/Cloudflare-zone configuration, and the IAM-gated route |
| `eturnal` | `domain_prefix`, `port` | Generate the TURN domain, listen/published port, and Nextcloud Talk connection values |
| `lam` | `admin_password`, `domain_prefix`, `language` | Generate the LAM profile, administrator login, route, and supported POSIX locale |
| `lego` | `dns_provider`, `dns_server` | Select the ACME DNS-01 provider and resolver; `global.virtual_domain` selects certificate mode |
| `llng` | `adminer_enabled`, `db_name`, `db_type`, `domain_prefix`, `enable_test`, `log_level`, `manager_domain_prefix`, `test_domain_prefix` | Select database/optional Adminer and generate Portal, Manager, Test routes and LLNG configuration |
| `mariadb` | `adminer_enabled`, `root_password` | Change the optional service; translate the password to the upstream initialization variable under rotation policy |
| `meshcentral` | `db_name`, `db_type`, `domain_prefix`, `iam_protocol`, `mps_port` | Generate database, OIDC/LDAP configuration, web route, and MPS published port |
| `netbird` | `adminer_enabled`, `domain_prefix`, `iam_protocol` | Change the optional service, NetBird URLs, and Runner-selected IAM interface |
| `nextcloud` | `db_name`, `db_type`, `domain_prefix`, `iam_protocol`, `language`, `locale`, `log_level`, `memories_enabled`, `memory_limit`, `phone_region`, `rm_skeleton_files`, `talk_enabled`, `upload_max_size` | Generate installation/database settings; `task.sh` reconciles authentication, locale, apps, PHP limits, and skeleton state |
| `oauth2_proxy` | `allow_groups`, `domain_prefix`, `iam_protocol` | Generate the OIDC client, allowed groups, callback URL, and Traefik ForwardAuth middleware |
| `postgres` | `adminer_enabled`, `password`, `username` | Change the optional service and translate credentials to upstream initialization variables with migration/rotation semantics |
| `samba_dc` | The 30 entries in the inventory table | Generate AD/BIND configuration, directory structure, and three service accounts; password policy declares a `samba-tool` hot reload but currently uses the deployment fallback, while identity/password values remain migration/rotation guarded |
| `samba_fs` | `hostname`, `log_level`, `share_access_mode`, `share_dir_name`, `share_guest_read_only`, `use_default_domain`, `wsdd_log_level` | Generate the member join, smb.conf, share directory/ACLs, guest state, and WSDD announcement |
| `traefik` | `base_port`, `domain_prefix` | Change the entrypoint listen/published port, derived application URLs, and dashboard router |

## Removed ineffective parameters

Static consumer analysis, rendered output, and upstream image entrypoints jointly showed that the following fields could not produce their advertised result. They were removed instead of retained as compatibility placeholders. Their structured spellings now fail clearly. Top-level `env:` remains an open escape hatch, but putting a retired environment key there is not a supported interface.

The upstream check used the container variables documented by the [official Collabora CODE SDK manual](https://sdk.collaboraonline.com/CO-SDK-manual.pdf), plus the official Nextcloud image's [environment documentation](https://github.com/nextcloud/docker/blob/master/README.md#auto-configuration-via-environment-variables) and [entrypoint source](https://github.com/nextcloud/docker/blob/master/docker-entrypoint.sh).

| Removed field | Reason | Replacement |
| --- | --- | --- |
| `global.basicauth_user` | Produced an unread `BASICAUTH_USER`; Traefik now uses its managed local account | Manage the Traefik account through `anas admin ...` |
| `global.image_prefix` | Produced an unread `IMAGE_PREFIX`; Compose images use fixed names plus `ANAS_IMAGE_REGISTRY` | Set `ANAS_IMAGE_REGISTRY` when a registry mirror is needed |
| `global.default_service_root_password` | The schema had already removed it, but one remote E2E fixture still carried it and failed import | Use each Module's password parameter or managed Secret |
| `collabora.interface` | Produced `COLLABORA_INTERFACE`, which the hook, Compose file, and published upstream environment contract do not read | None; container networking owns the interface |
| `lego.virtual_domain` | Produced the wrong namespace, `LEGO_VIRTUAL_DOMAIN`; certificate logic reads the global key | `global.virtual_domain` |
| `nextcloud.debug` | Produced `NEXTCLOUD_DEBUG`, which neither the local entrypoint nor published upstream environment contract reads | Use `nextcloud.log_level`; a future debug mode needs real reconciliation logic |
| `traefik.subnet`, `traefik.gateway_ip` | Undeclared, and Compose now lets Docker allocate the subnet; values in six remote fixtures never affected rendering | None; avoid fixed-subnet collisions |
| `administration.bootstrap.display_name` | Was accepted by the schema but never reached account creation | Not currently supported |
| `administration.bootstrap.email` | Was accepted by the schema but never reached account creation | Use `global.email` for service contact; administrator email is not currently supported |
| `administration.bootstrap.roles[]` | Was accepted but never mapped to AD/application groups | Manage authorization through directory groups |
| `administration.local_accounts.password_policy` | Accepted one constant and never selected runtime behavior; setting it and omitting it were identical | Per-module managed passwords are fixed; `password_length` controls generated length |

## Overrides currently requiring top-level `env:`

These repository-consumed keys have no global field or module-manifest parameter. They therefore lack manifest-level type validation, sensitivity metadata, and change policy, and do not appear in `config list`.

### Downloads, package managers, and registries

| Key | Purpose |
| --- | --- |
| `APT_MIRROR_URL` | Debian/Ubuntu APT mirror |
| `APK_MIRROR_URL` | Alpine APK mirror |
| `NPM_REGISTRY_URL` | npm registry |
| `GOPROXY_URL` | Go module proxy |
| `GITHUB_DOWNLOAD_PROXY_PREFIX` | GitHub download proxy |
| `BUILD_GITHUB_DOWNLOAD_PROXY_PREFIX` | Build-time GitHub download proxy |
| `NEXTCLOUD_APPSTORE_URL` | Nextcloud app-store API |
| `DOCKER_HUB_REGISTRY` | Build-time Docker Hub base-image prefix |
| `LLNG_DOCKER_HUB_REGISTRY` | LLNG-specific Docker Hub prefix |
| `ANAS_IMAGE_REGISTRY` | Runtime registry for ANAS-derived and mirrored images |
| `GHCR_REGISTRY` | Build-time GHCR prefix |
| `NEXTCLOUD_APT_MIRROR_URL` | Nextcloud-only APT override |
| `LAM_APT_MIRROR_URL` | LAM-only APT override |
| `LAM_DOWNLOAD_URL` | Direct LAM package URL override |

`global.chinese_speedup: true` supplies runtime defaults for `ANAS_IMAGE_REGISTRY`, `NEXTCLOUD_APPSTORE_URL`, and `GITHUB_DOWNLOAD_PROXY_PREFIX`. `global.chinese_build_speedup: true` supplies APT, APK, npm, Go, Docker Hub, GHCR, LLNG, and build-time GitHub defaults; changing it or another build input requires `anas apply --build`. Explicit top-level `env:` values win.

### Host build and Compose integration

| Key | Purpose |
| --- | --- |
| `DOCKER_BUILD_NETWORK` | Docker network used while building module images; default `default` |
| `DOCKER_SOCKET_PATH` | Host Docker socket mounted by Traefik; default `/var/run/docker.sock` |

These are advanced host-integration settings. Promote them to a structured global or module parameter if they need stable validation and lifecycle semantics.

## Environment variables that must not be configured manually

Hook outputs, generated secrets, workspace paths, host-network probes, and cross-module `ANAS_*` contracts appear in rendered `.env` files but are not raw user settings. Examples include `DATA_PATH`, `USER_DATA_PATH`, `LOCAL_DNS_SERVER`, `ANAS_TLS_*`, `ANAS_IAM_*`, and `POSTGRES_HOST`. Overriding such keys does not create a supported interface.

Put user-supplied credentials under `secrets.<name>` or a manifest parameter marked `sensitive`, never in raw top-level `env:` merely because the final transport is an environment variable.

## Maintenance and review

Obtain the authoritative declared inventory:

```bash
go run ./cmd/anas config list --json
```

Count totals by owner:

```bash
go run ./cmd/anas config list --json \
  | sed -n '/^{/,$p' \
  | jq '.parameters | {total: length, by_module: group_by(.module) | map({module: .[0].module, count: length})}'
```

Find declared parameters that use top-level `env:` because of a bare export:

```bash
go run ./cmd/anas config list --json \
  | sed -n '/^{/,$p' \
  | jq '.parameters[] | select(.path | startswith("env.")) | {path, module, parameter, env_key}'
```

Maintenance rules:

1. Prefer the global schema or a module `config` declaration for ordinary user settings.
2. Document every new raw-only key and why it is not yet structured.
3. Recalculate counts and update both language versions after adding, removing, or renaming manifest parameters.
4. Do not list runner/module-derived values as user configuration.
5. Put credentials in `secrets` or parameters marked `sensitive`.

Validation commands:

```bash
go test ./internal/config ./internal/runner
test-env/scripts/test-parameters.sh
test-env/scripts/test-parameter-effects.sh
test-env/scripts/test-render.sh
```

The first rejects declared parameters with no runtime consumer. If the only consumer is an upstream image, the exception must record source evidence for the pinned version. The remaining suites cover the 128-entry inventory and retired paths, the real CLI→hook→render→deployment→Compose/refusal boundary for all seven effects, and require every one of the 128 parameter keys to appear in at least one freshly rendered Module artifact. `test-lifecycle.sh` additionally verifies against real Docker that `container_recreate` changes the container ID.
