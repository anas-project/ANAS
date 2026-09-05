# Structured `config.yml` and environment-variable inventory

This reference distinguishes settings with a structured `config.yml` entry from overrides that currently require top-level `env:`. Inventory statistics are generated from the current working tree and are not a fixed ABI.

## Summary

<!-- generated:configuration-summary:start -->
- Built-in Modules: `23`
- Declared parameters: `174` total (`17` global and `157` Module-owned; `153` structured Module parameters and `4` bare `env.*` parameters)
- Resolution phases: `input_required` `2`, `must_resolve` `30`, unknown types `0`
- Type distribution: `bool` `25`, `enum` `23`, `int` `27`, `string` `99`
- Default-source distribution: `generated` `10`, `host` `3`, `inherited` `7`, `none` `8`, `runtime` `4`, `static` `142`
<!-- generated:configuration-summary:end -->
- Control fields under `modules`, `administration`, `identity`, `dynamic_dns`, `rollback`, and `secrets` are structured but are not parameter-to-environment mappings, so they are not included in the parameter inventory.
- Top-level `env:` is an intentionally open escape hatch for valid environment keys. Input is canonicalized to uppercase and must match `[A-Z_][A-Z0-9_]*`. The raw-only inventory below covers keys explicitly consumed by this repository, not every possible environment key.

“Structured” has two layers:

1. native fields parsed and validated by the Go YAML schema;
2. manifest-declared parameters whose names, defaults, types, sensitivity, and change policies are visible to `config list/set/explain/plan` even though their final transport is an environment variable.

## Native structured fields

| YAML path | Meaning | Status |
| --- | --- | --- |
| `module_source` | Module distribution profile: `official`, `official-cn`, or the `cn` shorthand | used for catalog, download, cache, lock, and CN defaults |
| `modules.<module>` | Requested modules and their settings | used |
| `global.*` | deployment-wide parameters | used |
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
| `env.<KEY>` | Raw environment escape hatch | open map; keys must match `[A-Z_][A-Z0-9_]*` |

`config.Load` uses `KnownFields(true)`. Misspelled structured fields fail instead of being silently ignored, except inside the intentionally open `secrets`, `modules`, and `env` maps. Although `modules.<module>.config` is a map at YAML decode time, `config import` and deployment resolution now validate every key and value type against the manifest; hand-written YAML cannot bypass the declaration checks used by `config set`.

Before validation and managed persistence, `config import` gives every YAML
address one canonical spelling: `env` and `secrets` keys use their uppercase
runtime spelling, while Module names, global parameter names, and Module
parameter names become lowercase. If two source keys collapse to the same
address—for example `env.custom_key` plus `env.CUSTOM_KEY`,
`secrets.demo_token` plus `secrets.DEMO_TOKEN`, or `modules.TRAEFIK` plus
`modules.traefik`—the whole import is rejected instead of letting order decide
which value wins. A `secrets`, `env`, and structured Module input that map to
the same runtime key also collide. A structured Module parameter declared as a
bare export is moved to its sole managed address: source
`modules.samba_fs.config.share_dir_name` becomes `env.SHARE_DIR_NAME`. Supplying
both addresses is likewise a canonicalization collision. The source file is
never modified.

`secrets.<KEY>` is a sensitive spelling of a runtime input, not an untyped way
around the schema. When the key maps to a declared parameter, import still
applies that parameter's type, constraints, sensitivity, and change effect, and
errors never echo the candidate value.
Structural selectors that choose a provider, interface, backend, or DNS
platform cannot be supplied through `secrets:` or the lifecycle Secret Store:
their canonical identifier must be persisted in plan and the resolution lock,
so import/plan safely rejects that source and asks for ordinary configuration.
Secret storage is reserved for the actual credential. Ordinary deployment
secrets remain in the mode-0600 managed `config.yml`; `credential_rotate` inputs and managed local
administrator bootstrap passwords move to `.anas/secrets.yml`. When a
normalized managed configuration is reimported, only an existing non-empty
Secret Store record whose kind is `lifecycle_managed` may satisfy caller-input
requirements in the private validation view. `generated` and `local_admin`
records cannot masquerade as such input. Reimporting the same value is
idempotent; replacing it with a different value is rejected in favor of the
declared rotation workflow. `config plan` revalidates the same private view
against the current schema, while `config list` reports only that such a value
is set; neither projects plaintext.

The shared schema boundary is not limited to `config set`. Set,
import/reimport, `config plan`, deployment lock/plan/materialize, and remote
lock first build the same registry-aware runtime view, then validate address
uniqueness, environment-key syntax, declarations, types, constraints, and
caller input. A failure occurs before replacing managed configuration,
integrity state, the Secret Store, or a lock; `--update-lock` cannot persist a
lock for a configuration that schema validation subsequently rejects. Values
from every Secret Store kind propagate source sensitivity to equal-value
aliases in memory, so a later Module metadata drift cannot make old plaintext
appear in errors or ordinary projections. Only `lifecycle_managed` records are
merged into the effective input view.

Without an installer preference, `module_source` defaults to `official`. The one-line installer
records its choice in `${XDG_CONFIG_HOME:-$HOME/.config}/anas/source`; a new workspace or an import
whose external file omits the field first persists that choice into managed configuration. The `cn`
shorthand is normalized to `official-cn`. When `official-cn` / `cn` is selected and
`global.chinese_speedup` is not explicitly set, importing persists `global.chinese_speedup: true`;
an explicit `false` is never overridden.

`global.timezone`, `global.default_language`, and `global.default_locale` are optional and explicit values are validated and normalized. When `default_locale` is absent, the runner uses an explicit region-bearing `default_language`, then host locale, CLDR likely-subtag inference, and finally `en-US`. A language directly becomes the locale only when `language.Region()` returns `Exact`: `en-GB` qualifies, while `en` and `zh-Hans` do not replace an available host locale. `config list` and rendered environments show the final BCP 47 value.

Language controls the UI-text fallback and locale controls regional formatting. Derivation supplies a default; it does not merge these concepts. See the [module timezone and language matrix](/en/reference/module-localization) for actual consumers.

`global.container_prefix` defaults to `anas_`. Omitting it, including when a
configuration export removes a value equal to the default, does not change the
resolved value. The runner uses `<container_prefix><module>` as the Docker
Compose project name and also uses the prefix for container names; with the
default configuration, the Nextcloud project is `anas_nextcloud`. Workspaces
sharing one Docker daemon must use distinct prefixes. Changing the prefix of an
existing deployment creates a different set of projects, container names, and
cross-container addresses. It is a static deployment change, not an in-place
rename, so the old deployment must be explicitly migrated or removed first.

## Declared parameters

Every entry below appears in `anas config list`. Ordinary editable parameters can be addressed by `anas config set`; `credential_rotate`, `data_migrate`, and `immutable` entries are inventory/explain-only and require their dedicated workflow. Global parameters use `global.<parameter>`; ordinary module parameters use `modules.<module>.config.<parameter>`.

The JSON inventory reports `type` as `string`, `bool`, `int`, or `enum`; enum
entries also provide `allowed_values`. `unknown` remains a compatibility value
for legacy Modules and incomplete development declarations, but the built-in
Module release gate rejects it; the current `unknown` count is in the generated summary.

Configuration metadata separates “the operator must enter a value” from “the
resolved value must exist”:

- `required` is a compatibility field and always equals `input_required`.
  `input_required` is true only when no default, host discovery, or other
  unconditional source can supply the value and every applicable case requires
  the operator to provide a non-empty value. A Module parameter becomes
  applicable when that Module is enabled.
- `must_resolve` means the final value must be non-empty after canonicalization,
  defaults, host discovery, and runtime sources. It can therefore be true while
  `input_required` is false.
- `has_default` distinguishes no static default from an explicitly empty-string
  default. `default_source` is `none`, `static`, `host`, `runtime`, `generated`,
  or `inherited` and identifies an unconditional source available when input is
  omitted. An input-required parameter cannot also have a default or a source
  other than `none`. `none` means that no unconditional source exists; it does
  not rule out a deployment resolver that supplies the value conditionally.
- Optional `constraints` contains only declared single-field rules:
  `minimum`, `maximum`, `min_length`, `max_length`, `pattern`, and `format`.
  Conditional requirements, relationships between fields, and rules that read
  runtime state remain resolver, application-layer, plan, or Hook validation; they must
  not be flattened into a field-level required flag or constraint.

This inventory is **not JSON Schema**. Its `required` describes explicit input
for the current parameter rather than an array of object property names. ANAS
applies its defaults during resolution, whereas JSON Schema `default` is only
an annotation. `constraints` is a stable projection of the shared ANAS schema,
not support for arbitrary JSON Schema keywords. The CLI, future `anasd`
configuration API, and Web forms must consume the same application-layer schema.

`ddns_go.dns_provider` and
`ddns_updater.dns_provider` can be injected conditionally from deployment
`dynamic_dns.dns_provider`, so each reports `input_required: false`,
`must_resolve: true`, and `default_source: none`. Module-local input is needed
only when that resolver cannot supply the value.

<!-- generated:configuration-constraints:start -->
Current explicit portable constraints: `24`.

| Parameter path | Portable constraints |
| --- | --- |
| `casdoor.ldap_auto_sync_minutes` | <code>minimum=1</code> |
| `eturnal.port` | <code>minimum=1; maximum=65535</code> |
| `forgejo.actions_runner_image` | <code>pattern=&#34;^(?:[0-9a-f]{64})?$&#34;</code> |
| `forgejo.domain_prefix` | <code>min_length=1; max_length=63; pattern=&#34;^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$&#34;</code> |
| `forgejo.ssh_port` | <code>minimum=1; maximum=65535</code> |
| `global.base_domain` | <code>format=&#34;dns_name&#34;</code> |
| `global.default_language` | <code>format=&#34;language_tag&#34;</code> |
| `global.default_locale` | <code>format=&#34;locale&#34;</code> |
| `global.host_ip` | <code>format=&#34;ipv4&#34;</code> |
| `global.host_lan_bridge_ip` | <code>format=&#34;ipv4&#34;</code> |
| `global.host_lan_ip` | <code>format=&#34;ipv4&#34;</code> |
| `global.timezone` | <code>format=&#34;iana_timezone&#34;</code> |
| `incus.endpoint` | <code>pattern=&#34;^(?:https://[A-Za-z0-9.:_-]+)?$&#34;</code> |
| `incus.storage_pool` | <code>pattern=&#34;^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$&#34;</code> |
| `meshcentral.mps_port` | <code>minimum=1; maximum=65535</code> |
| `oauth2_proxy.console_proxy_port` | <code>minimum=1; maximum=65535</code> |
| `samba_dc.domain` | <code>format=&#34;dns_name&#34;</code> |
| `samba_dc.max_log_size` | <code>minimum=1</code> |
| `traefik.base_port` | <code>minimum=1; maximum=65535</code> |
| `versitygw.domain_prefix` | <code>min_length=1; max_length=63; pattern=&#34;^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$&#34;</code> |
| `versitygw.region` | <code>min_length=1; max_length=64; pattern=&#34;^[A-Za-z0-9][A-Za-z0-9._-]*$&#34;</code> |
| `versitygw.root_access_key` | <code>min_length=3; max_length=64; pattern=&#34;^[A-Za-z0-9._-]+$&#34;</code> |
| `versitygw.root_secret_key` | <code>min_length=16; max_length=128</code> |
| `vikunja.domain_prefix` | <code>min_length=1; max_length=63; pattern=&#34;^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$&#34;</code> |
<!-- generated:configuration-constraints:end -->

<!-- generated:configuration-owners:start -->
| Owner | Parameters | Parameter paths |
| --- | ---: | --- |
| `global` | 17 | `global.base_domain`<br>`global.chinese_build_speedup`<br>`global.chinese_speedup`<br>`global.container_prefix`<br>`global.default_language`<br>`global.default_locale`<br>`global.dns_server`<br>`global.email`<br>`global.host_ip`<br>`global.host_lan_arp_check`<br>`global.host_lan_bridge_ip`<br>`global.host_lan_ip`<br>`global.ipv4`<br>`global.ipv6`<br>`global.network_prefix`<br>`global.timezone`<br>`global.virtual_domain` |
| `authentik` | 6 | `authentik.db_name`<br>`authentik.db_type`<br>`authentik.domain_prefix`<br>`authentik.ldap_enabled`<br>`authentik.ldap_password_writeback`<br>`authentik.log_level` |
| `casdoor` | 4 | `casdoor.db_name`<br>`casdoor.db_type`<br>`casdoor.domain_prefix`<br>`casdoor.ldap_auto_sync_minutes` |
| `collabora` | 5 | `collabora.admin_password`<br>`collabora.admin_username`<br>`collabora.auto_save`<br>`collabora.domain_prefix`<br>`collabora.log_level` |
| `ddns_go` | 10 | `ddns_go.dns_provider`<br>`ddns_go.domain_prefix`<br>`ddns_go.interval`<br>`ddns_go.ipv4_gettype`<br>`ddns_go.ipv4_interface`<br>`ddns_go.ipv4_urls`<br>`ddns_go.ipv6_gettype`<br>`ddns_go.ipv6_interface`<br>`ddns_go.ipv6_urls`<br>`ddns_go.web_enabled` |
| `ddns_updater` | 10 | `ddns_updater.dns_provider`<br>`ddns_updater.domain_prefix`<br>`ddns_updater.forward_auth_interface`<br>`ddns_updater.publicip_dns_providers`<br>`ddns_updater.publicip_fetchers`<br>`ddns_updater.publicip_ipv4_providers`<br>`ddns_updater.publicip_ipv6_providers`<br>`ddns_updater.publicip_providers`<br>`ddns_updater.ttl`<br>`ddns_updater.zone_identifier` |
| `eturnal` | 2 | `eturnal.domain_prefix`<br>`eturnal.port` |
| `forgejo` | 12 | `forgejo.actions_allowed_scopes`<br>`forgejo.actions_enabled`<br>`forgejo.actions_isolation`<br>`forgejo.actions_runner_image`<br>`forgejo.custom_git_hooks_enabled`<br>`forgejo.db_name`<br>`forgejo.db_type`<br>`forgejo.domain_prefix`<br>`forgejo.iam_protocol`<br>`forgejo.language`<br>`forgejo.local_path_import_enabled`<br>`forgejo.ssh_port` |
| `incus` | 5 | `incus.admin_certificate_b64`<br>`incus.admin_key_b64`<br>`incus.endpoint`<br>`incus.server_certificate_b64`<br>`incus.storage_pool` |
| `lam` | 3 | `lam.admin_password`<br>`lam.domain_prefix`<br>`lam.language` |
| `lego` | 2 | `lego.dns_provider`<br>`lego.dns_server` |
| `llng` | 7 | `llng.db_name`<br>`llng.db_type`<br>`llng.domain_prefix`<br>`llng.enable_test`<br>`llng.log_level`<br>`llng.manager_domain_prefix`<br>`llng.test_domain_prefix` |
| `mariadb` | 3 | `mariadb.adminer_enabled`<br>`mariadb.forward_auth_interface`<br>`mariadb.root_password` |
| `meshcentral` | 5 | `meshcentral.db_name`<br>`meshcentral.db_type`<br>`meshcentral.domain_prefix`<br>`meshcentral.iam_protocol`<br>`meshcentral.mps_port` |
| `netbird` | 2 | `netbird.domain_prefix`<br>`netbird.iam_protocol` |
| `nextcloud` | 13 | `nextcloud.db_name`<br>`nextcloud.db_type`<br>`nextcloud.domain_prefix`<br>`nextcloud.iam_protocol`<br>`nextcloud.language`<br>`nextcloud.locale`<br>`nextcloud.log_level`<br>`nextcloud.memories_enabled`<br>`nextcloud.memory_limit`<br>`nextcloud.phone_region`<br>`nextcloud.rm_skeleton_files`<br>`nextcloud.talk_enabled`<br>`nextcloud.upload_max_size` |
| `oauth2_proxy` | 4 | `oauth2_proxy.console_proxy_enabled`<br>`oauth2_proxy.console_proxy_port`<br>`oauth2_proxy.domain_prefix`<br>`oauth2_proxy.iam_protocol` |
| `postgres` | 4 | `postgres.adminer_enabled`<br>`postgres.forward_auth_interface`<br>`postgres.password`<br>`postgres.username` |
| `samba_dc` | 40 | `samba_dc.admin_complex_pass`<br>`samba_dc.admin_lockout_duration`<br>`samba_dc.admin_lockout_reset_after`<br>`samba_dc.admin_lockout_threshold`<br>`samba_dc.admin_max_pass_age`<br>`samba_dc.admin_min_pass_age`<br>`samba_dc.admin_min_pass_length`<br>`samba_dc.admin_name`<br>`samba_dc.admin_password`<br>`samba_dc.admin_password_history`<br>`samba_dc.administrator_password`<br>`samba_dc.anchor_bind_name`<br>`samba_dc.anchor_bind_password`<br>`samba_dc.anchor_scan_interval`<br>`samba_dc.app_filter`<br>`samba_dc.application_dns_mode`<br>`samba_dc.create_structure`<br>`samba_dc.dns_allowed_networks`<br>`samba_dc.dns_cache_size`<br>`samba_dc.dns_debug`<br>`samba_dc.dns_forwarders`<br>`samba_dc.domain`<br>`samba_dc.ldap_bind_name`<br>`samba_dc.ldap_bind_password`<br>`samba_dc.log_level`<br>`samba_dc.max_log_size`<br>`samba_dc.netbios_name`<br>`samba_dc.password_bind_name`<br>`samba_dc.password_bind_password`<br>`samba_dc.realm`<br>`samba_dc.template_homedir`<br>`samba_dc.template_shell`<br>`samba_dc.user_complex_pass`<br>`samba_dc.user_lockout_duration`<br>`samba_dc.user_lockout_reset_after`<br>`samba_dc.user_lockout_threshold`<br>`samba_dc.user_max_pass_age`<br>`samba_dc.user_min_pass_age`<br>`samba_dc.user_min_pass_length`<br>`samba_dc.user_password_history` |
| `samba_fs` | 7 | `env.SHARE_ACCESS_MODE`<br>`env.SHARE_DIR_NAME`<br>`env.SHARE_GUEST_READ_ONLY`<br>`env.USE_DEFAULT_DOMAIN`<br>`samba_fs.hostname`<br>`samba_fs.log_level`<br>`samba_fs.wsdd_log_level` |
| `traefik` | 3 | `traefik.base_port`<br>`traefik.domain_prefix`<br>`traefik.forwarded_headers_trusted_ips` |
| `versitygw` | 5 | `versitygw.domain_prefix`<br>`versitygw.read_only`<br>`versitygw.region`<br>`versitygw.root_access_key`<br>`versitygw.root_secret_key` |
| `vikunja` | 5 | `vikunja.db_name`<br>`vikunja.db_type`<br>`vikunja.domain_prefix`<br>`vikunja.iam_protocol`<br>`vikunja.language` |
<!-- generated:configuration-owners:end -->

The Nextcloud administrator password is not a configuration parameter. It is
owned by the managed `break_glass` Secret and application handler and cannot be
written in YAML.

`global.default_service_root_password` has been removed. LAM, Collabora, and
Samba DC own their password parameters and generate independent Secrets when
those parameters are omitted; no cross-application administrator password remains.

The following declared `samba_fs` parameters have manifest metadata, but their canonical managed
YAML address is top-level `env:` because `config.exports` publishes a bare
name. `config import` may accept a structured source address, but migrates it
here before persistence:

<!-- generated:configuration-bare-parameters:start -->
| YAML path | Owner | Environment key |
| --- | --- | --- |
| `env.SHARE_ACCESS_MODE` | `samba_fs` | `SHARE_ACCESS_MODE` |
| `env.SHARE_DIR_NAME` | `samba_fs` | `SHARE_DIR_NAME` |
| `env.SHARE_GUEST_READ_ONLY` | `samba_fs` | `SHARE_GUEST_READ_ONLY` |
| `env.USE_DEFAULT_DOMAIN` | `samba_fs` | `USE_DEFAULT_DOMAIN` |
<!-- generated:configuration-bare-parameters:end -->

For example, `anas config set samba_fs.share_guest_read_only Yes` accepts the logical parameter path but writes `env.SHARE_GUEST_READ_ONLY`.

## What changing a parameter does

`anas config list --json` is the authoritative machine-readable inventory for parameter names, environment keys, defaults, and change outcomes. An effect describes the action required on an existing deployment; transporting a value into `.env` does not by itself mean it was applied.

<!-- generated:configuration-effects:start -->
| Module parameter effect | Parameters | Change outcome |
| --- | ---: | --- |
| `container_recreate` | 99 | Re-render and recreate the affected container or Compose project |
| `credential_rotate` | 9 | Use a credential-rotation transaction to update application state and the Secret Store together |
| `data_migrate` | 15 | Migrate persistent data, a database, or membership before activation |
| `hot_reload` | 16 | Apply through the declared management command; the current executor may conservatively recreate the container |
| `immutable` | 3 | Use a replacement or dedicated migration workflow |
| `reconcile` | 15 | Reconcile application, API, or file state through the Module lifecycle |
<!-- generated:configuration-effects:end -->

### Actual execution boundary in this release

An effect states the intended lifecycle semantics; `executor` identifies the path this release actually takes. A generic module `config_apply` hook does not exist yet, so both `hot_reload` and `reconcile` use `deployment_apply_fallback`. Tests intentionally do not claim that an in-place hot reload already exists.

`deployment_apply_fallback` is not another effect; it is the Runner-reported
name of the executor actually used. When a workspace has a running active
deployment, `config set` stores the value, renders a new immutable deployment,
selects Modules whose render digest changed, removes their old Compose projects
with `down`, and starts them from the target artifacts with `up`. This
conservative path recreates containers, so it is not a `hot_reload`. Setting the
same value still records a deployment but selects no containers for `down` or
`up`. If activation fails, the Runner removes candidate containers, starts the
previous deployment, and restores `config.yml` plus its managed digest. Before the initial apply, while
the workspace is stopped, or with an explicit defer request, the value instead
enters a pending/deferred state and does not start containers unexpectedly.

| Effect | Current observable execution result |
| --- | --- |
| `container_recreate` | Store the value, generate and activate a deployment, and run Compose `down → up` for Modules whose render digest changed; the real-Docker lifecycle suite also requires the container ID to change |
| `hot_reload`, `reconcile` | Render the target value into a new deployment and run Compose `down → up` only for affected Modules, without Compose `build` |
| `image_rebuild` | Generate a deployment, run Compose `build`, then `down → up` changed Modules, and record `images_built: true` in its manifest |
| `credential_rotate` | Refuse both `config set` and replacement import; leave the Secret Store and runtime unchanged |
| `data_migrate` | A candidate can be rendered, but ordinary activation stops before any Compose or host-network mutation and reports the parameter and migration operation |
| `immutable` | Stop at the same pre-runtime boundary and report the replacement/domain-migration operation |

### In-place update capability audit

The following results answer whether the upstream service can apply a change in place; they do not claim that the current Runner calls that interface. On 2026-08-15, `test-env/scripts/server-parameter-inplace-e2e.sh` ran against a dedicated non-production isolated Docker daemon. It compared container IDs, read application state after each change, and restored every live value before exiting. `config set` still uses the deployment fallback described above.

| Parameters | Complete effect in place | Observed result |
| --- | --- | --- |
| All eight `samba_dc.user_*` `hot_reload` settings | Yes | `samba-tool domain passwordsettings set/show` changed and restored all eight policies while the Samba DC container ID stayed unchanged |
| `global.default_language` | Yes | Nextcloud changed through `occ`; LAM rewrote its profile while Apache was running, with neither container recreated. A handler must serialize this with startup configuration writes |
| `global.default_locale` | Yes | Its current consumer, Nextcloud, changed through `occ config:system:set` while the container ID stayed unchanged |
| `nextcloud.language`, `nextcloud.locale` | Yes | The actual system configuration written by `occ` was immediately readable with the same container ID |
| `samba_fs.share_access_mode`, `samba_fs.share_guest_read_only` | Yes, with a new handler | `smbcontrol all reload-config` made a running `smbd` publish the changed configuration while ACLs changed online. The existing `fix_perm.sh` cannot yet accept explicit inputs from a new deployment |
| `nextcloud.memories_enabled` | Conditional | Nextcloud supports online app enable/disable, but enabling may also download a pinned release, modify the database, and start places initialization. This needs a transaction, progress, and verification rather than a lone `occ app:enable` |
| `authentik.domain_prefix`, `casdoor.domain_prefix`, `llng.domain_prefix`, `nextcloud.domain_prefix` | No | Their complete effect includes Traefik Docker labels. Docker 24.0.7 cannot change a container label with `docker update`; at least the routed containers must be recreated while IAM clients/metadata are reconciled |
| `lego.dns_provider` | No | A one-shot `docker exec` can receive a new provider, but PID 1's cron environment retains the old value and later renewals revert to the old provider |
| `global.virtual_domain` | No | Lego retains the old mode in its long-lived environment, and certificate replacement also requires every TLS consumer to reload or recreate; one certificate command cannot complete the global effect |

Dedicated `config_apply` handlers are therefore possible for the first six groups. The domain, DNS-provider, and certificate-mode groups cannot promise that every container stays unchanged. `reconcile` means converging external state; it does not inherently mean hot reload.

The table below states what every owner's inputs ultimately change. Each named parameter is covered by the runtime-consumer audit in `internal/runner`. Dynamically assembled keys require an explicit, explained exception. A value read only by an upstream image must likewise cite exact upstream evidence. No current parameter is accepted merely because `env_file` passes it through: retained Collabora, Nextcloud, and database-image settings all have an explicit hook, container-script, or Compose translation.

| Owner | Parameters | Observable result / consumer boundary |
| --- | --- | --- |
| `global` | `base_domain`, `virtual_domain`, `email` | Derive application URLs, SSO issuers, ACME/internal-CA mode, and service contact email; `samba_dc.domain` independently defines the AD domain |
| `global` | `container_prefix`, `network_prefix` | Change the Compose project/container names, network names, and cross-container addresses; the project is `<container_prefix><module>`, and separate workspaces must not reuse one prefix |
| `global` | `host_ip`, `dns_server`, `ipv4`, `ipv6` | Change host route targets, container DNS, and DDNS A/AAAA intent |
| `global` | `host_lan_ip`, `host_lan_bridge_ip`, `host_lan_arp_check` | Pin the host-LAN container and host bridge addresses and control occupancy probing; all three are optional, and the ARP check runs unless explicitly disabled |
| `global` | `timezone`, `default_language`, `default_locale` | Produce final `TZ`/BCP 47 defaults and deliver them only to applications declaring support |
| `global` | `chinese_speedup`, `chinese_build_speedup` | Select runtime image/download mirrors and build-time image/package mirrors; the latter rebuilds images |
| `authentik` | `db_name`, `db_type`, `domain_prefix`, `ldap_enabled`, `ldap_password_writeback`, `log_level` | Select the database resource and generate IAM URLs/blueprints, LDAP source/writeback, and logging |
| `casdoor` | `db_name`, `db_type`, `domain_prefix`, `ldap_auto_sync_minutes` | Select the PostgreSQL resource and generate IAM URLs, OIDC/SAML applications, LDAPS import configuration, and the synchronization interval |
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
| `oauth2_proxy` | `domain_prefix`, `iam_protocol` | Generate the OIDC client, callback URL, and Traefik ForwardAuth middleware; the admitted groups are derived from the `platform_admin` role rather than configured |
| `postgres` | `adminer_enabled`, `password`, `username` | Change the optional service and translate credentials to upstream initialization variables with migration/rotation semantics |
| `samba_dc` | The 40 entries in the inventory table | Use `domain` for Realm/Base-DN/Kerberos identity and `application_dns_mode` for the authoritative application-record zone, then generate AD/BIND configuration, directory structure, and three service accounts; both the domain user policy and privileged PSO are parameter-driven and declare a `samba-tool` hot reload but currently use the deployment fallback, while identity/password values remain migration/rotation guarded |
| `samba_fs` | `hostname`, `log_level`, `share_access_mode`, `share_dir_name`, `share_guest_read_only`, `use_default_domain`, `wsdd_log_level` | Generate the member join, smb.conf, share directory/ACLs, guest state, and WSDD announcement |
| `traefik` | `base_port`, `domain_prefix`, `forwarded_headers_trusted_ips` | Change the entrypoint listen/published port, derived application URLs, dashboard router, and trusted forwarded-proxy boundary |
| `vikunja` | `db_name`, `db_type`, `domain_prefix`, `iam_protocol`, `language` | Select the database Resource and OIDC binding, then generate the task-service route and default language for new users |

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
4. Give every built-in parameter an explicit type; `unknown` may read legacy Modules but cannot enter a release.
5. Keep CLI/JSON `required` equal to manifest `input_required`; an input-required parameter cannot also have a default or another unconditional source. Legacy manifest `required` retains its pre-Hook check, while `must_resolve` is the final post-Hook non-empty invariant.
6. Put single-field rules in `constraints`; keep conditional, cross-field, and runtime-state rules in the resolver, application layer, plan, or Hook.
7. Do not list runner/module-derived values as user configuration.
8. Put credentials in `secrets` or parameters marked `sensitive`.

Validation commands:

```bash
go test ./internal/config ./internal/runner
test-env/scripts/test-parameters.sh
test-env/scripts/test-parameter-effects.sh
test-env/scripts/test-render.sh
```

The first rejects declared parameters with no runtime consumer. If the only consumer is an upstream image, the exception must record source evidence for the pinned version. The remaining suites cover the complete inventory, type declarations, retired paths, the real CLI→hook→render→deployment→Compose/refusal boundary for every declared effect, and require each parameter key to appear in at least one freshly rendered Module artifact. `test-lifecycle.sh` additionally verifies against real Docker that `container_recreate` changes the container ID.
