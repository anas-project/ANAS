# Structured `config.yml` and environment-variable inventory

This reference distinguishes settings with a structured `config.yml` entry from overrides that currently require top-level `env:`. Counts reflect the 2026-08-13 working tree and are not a fixed ABI.

## Summary

- `anas config list --json` currently declares **131** configurable parameters: 17 global and 114 module parameters.
- 110 module parameters live at `modules.<module>.config.<parameter>`. Four declared `samba_fs` parameters use `env.<KEY>` because they intentionally export bare environment names.
- Control fields under `modules`, `administration`, `identity`, `dynamic_dns`, `rollback`, and `secrets` are structured but are not parameter-to-environment mappings, so they are not included in the 131 count.
- Top-level `env:` is an intentionally open escape hatch. The raw-only inventory below covers keys explicitly consumed by this repository, not every possible environment key.

“Structured” has two layers:

1. native fields parsed and validated by the Go YAML schema;
2. manifest-declared parameters whose names, defaults, types, sensitivity, and change policies are visible to `config list/set/explain/plan` even though their final transport is an environment variable.

## Native structured fields

| YAML path | Meaning | Status |
| --- | --- | --- |
| `modules.<module>` | Requested modules and their settings | used |
| `global.*` | 17 deployment-wide parameters | used |
| `administration.bootstrap.username` | Bootstrap administrator username | used |
| `administration.bootstrap.display_name` | Bootstrap display name | accepted by schema, not yet published |
| `administration.bootstrap.email` | Bootstrap email | accepted by schema, not yet published |
| `administration.bootstrap.roles[]` | Bootstrap roles | accepted by schema, not yet published |
| `administration.local_accounts.username_template` | Global local-administrator username template | invalid; usernames are ANAS-managed |
| `administration.local_accounts.password_policy` | Currently only `generated_per_module` | used |
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
| `modules.<module>.depends_on[]` | User-added dependencies | used |
| `modules.<module>.identity.login_protocol` | `auto`, `oidc`, or `saml` | used |
| `modules.<module>.administration.local_accounts.<id>.username` | Module-local username override | invalid; usernames are ANAS-managed |
| `modules.<module>.config.<parameter>` | Manifest-declared module parameter | used |
| `env.<KEY>` | Raw environment escape hatch | open map |

`config.Load` uses `KnownFields(true)`. Misspelled structured fields fail instead of being silently ignored, except inside the intentionally open `secrets`, `modules`, and `env` maps.

`global.timezone`, `global.default_language`, and `global.default_locale` are optional and explicit values are validated and normalized. When `default_locale` is absent, the runner uses an explicit region-bearing `default_language`, then host locale, CLDR likely-subtag inference, and finally `en-US`. A language directly becomes the locale only when `language.Region()` returns `Exact`: `en-GB` qualifies, while `en` and `zh-Hans` do not replace an available host locale. `config list` and rendered environments show the final BCP 47 value.

Language controls the UI-text fallback and locale controls regional formatting. Derivation supplies a default; it does not merge these concepts. See the [module timezone and language matrix](/en/reference/module-localization) for actual consumers.

## The 130 declared parameters

Every entry below appears in `anas config list`. Ordinary editable parameters can be addressed by `anas config set`; `credential_rotate`, `data_migrate`, and `immutable` entries are inventory/explain-only and require their dedicated workflow. Global parameters use `global.<parameter>`; ordinary module parameters use `modules.<module>.config.<parameter>`.

| Owner | Count | Parameters |
| --- | ---: | --- |
| `global` | 16 | `base_domain`, `basicauth_user`, `chinese_build_speedup`, `chinese_speedup`, `container_prefix`, `default_language`, `default_locale`, `dns_server`, `email`, `host_ip`, `image_prefix`, `ipv4`, `ipv6`, `network_prefix`, `timezone`, `virtual_domain` |
| `authentik` | 6 | `db_name`, `db_type`, `domain_prefix`, `ldap_enabled`, `ldap_password_writeback`, `log_level` |
| `collabora` | 6 | `admin_password`, `admin_username`, `auto_save`, `domain_prefix`, `interface`, `log_level` |
| `ddns_go` | 10 | `dns_provider`, `domain_prefix`, `interval`, `ipv4_gettype`, `ipv4_interface`, `ipv4_urls`, `ipv6_gettype`, `ipv6_interface`, `ipv6_urls`, `web_enabled` |
| `ddns_updater` | 9 | `dns_provider`, `domain_prefix`, `forward_auth_interface`, `publicip_dns_providers`, `publicip_fetchers`, `publicip_ipv4_providers`, `publicip_ipv6_providers`, `publicip_providers`, `ttl` |
| `eturnal` | 2 | `domain_prefix`, `port` |
| `lam` | 3 | `admin_password`, `domain_prefix`, `language` |
| `lego` | 2 | `dns_server`, `virtual_domain` |
| `llng` | 8 | `adminer_enabled`, `db_name`, `db_type`, `domain_prefix`, `enable_test`, `log_level`, `manager_domain_prefix`, `test_domain_prefix` |
| `mariadb` | 2 | `adminer_enabled`, `root_password` |
| `meshcentral` | 4 | `db_name`, `db_type`, `domain_prefix`, `mps_port` |
| `netbird` | 3 | `adminer_enabled`, `domain_prefix`, `iam_protocol` |
| `nextcloud` | 14 | `db_name`, `db_type`, `debug`, `domain_prefix`, `iam_protocol`, `language`, `locale`, `log_level`, `memories_enabled`, `memory_limit`, `phone_region`, `rm_skeleton_files`, `talk_enabled`, `upload_max_size` |
| `oauth2_proxy` | 3 | `allow_groups`, `domain_prefix`, `iam_protocol` |
| `postgres` | 3 | `adminer_enabled`, `password`, `username` |
| `samba_dc` | 30 | `admin_name`, `admin_password`, `administrator_password`, `anchor_bind_name`, `anchor_bind_password`, `anchor_scan_interval`, `app_filter`, `create_structure`, `dns_allowed_networks`, `dns_cache_size`, `dns_debug`, `dns_forwarders`, `ldap_bind_name`, `ldap_bind_password`, `log_level`, `max_log_size`, `netbios_name`, `password_bind_name`, `password_bind_password`, `realm`, `template_homedir`, `template_shell`, `user_complex_pass`, `user_lockout_duration`, `user_lockout_reset_after`, `user_lockout_threshold`, `user_max_pass_age`, `user_min_pass_age`, `user_min_pass_length`, `user_password_history` |

The Nextcloud administrator password is not a configuration parameter. It is
owned by the managed `break_glass` Secret and application handler and cannot be
written in YAML.

`global.default_service_root_password` has been removed. LAM, Collabora, and
Samba DC own their password parameters and generate independent Secrets when
those parameters are omitted; no cross-application administrator password remains.
| `samba_fs` | 7 | `hostname`, `log_level`, `share_access_mode`, `share_dir_name`, `share_guest_read_only`, `use_default_domain`, `wsdd_log_level` |
| `traefik` | 2 | `base_port`, `domain_prefix` |

Four `samba_fs` parameters have manifest metadata but use top-level `env:` because `config.exports` publishes a bare name:

| YAML address | Owner | Environment key |
| --- | --- | --- |
| `env.SHARE_ACCESS_MODE` | `samba_fs` | `SHARE_ACCESS_MODE` |
| `env.SHARE_DIR_NAME` | `samba_fs` | `SHARE_DIR_NAME` |
| `env.SHARE_GUEST_READ_ONLY` | `samba_fs` | `SHARE_GUEST_READ_ONLY` |
| `env.USE_DEFAULT_DOMAIN` | `samba_fs` | `USE_DEFAULT_DOMAIN` |

For example, `anas config set samba_fs.share_guest_read_only Yes` accepts the logical parameter path but writes `env.SHARE_GUEST_READ_ONLY`.

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
