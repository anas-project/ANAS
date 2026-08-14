# Configuration

## File responsibilities

`<workspace>/config.yml` is normalized desired state managed by the ANAS CLI, not a hand-edited input file. External YAML enters only through `anas config import SOURCE -w WORKSPACE`; the source is never modified. `config.lock.yml` records resolved module versions, providers, and host policy. Do not edit workspace configuration or `.anas/` state by hand: plan, lock, render, and apply verify the managed-config digest and reject out-of-band changes.

The structured YAML contains:

- `modules` for module selection;
- `global` for shared settings such as domain, email, and timezone;
- `administration` for bootstrap and module-local administrator policy;
- `identity` for directory and IAM provider selection;
- `dynamic_dns` for the selected DDNS implementation and DNS vendor;
- `rollback` for local snapshot policy;
- `modules.<name>` for enablement, identity protocol, and module parameters;
- `secrets` for explicitly supplied sensitive values;
- `env` for raw environment variables that have no structured field.

## Change and preview

Import an external configuration after initialization, then modify it only through the CLI:

```bash
anas config import ./my-config.yml -w /srv/anas
anas config explain nextcloud.domain_prefix
anas config set global.timezone Asia/Singapore -w /srv/anas
anas config plan -w /srv/anas
anas apply -w /srv/anas
```

With an active running deployment, `config set` immediately materializes and activates a new deployment; failure restores the prior managed configuration and runtime. `--defer` stores desired state only. An undeployed or explicitly stopped workspace reports a pending status and is never started implicitly.

`credential_rotate`, `data_migrate`, and `immutable` parameters are rejected before `config set` writes them and require their declared lifecycle, migration, or replacement workflow. `apply --allow-risky` is only an explicit takeover after an external migration has been completed and verified.

OIDC is the default `identity.iam.default_protocol` only for IAM consumers that declare OIDC support. Nextcloud and MeshCentral now use OIDC by default; Nextcloud can still select SAML explicitly through its module parameter. See [Module IAM and OIDC support](/en/reference/module-iam-support).

## Timezone, language, and regional formatting

The three global fields are independent:

```yaml
global:
  timezone: Asia/Singapore
  default_language: zh-Hans
  default_locale: zh-SG
```

`timezone` is an IANA name, `default_language` is the BCP 47 UI text fallback, and `default_locale` is the BCP 47 regional format for dates, numbers, and currency. Missing values resolve as follows:

1. timezone comes from `TZ` or system zoneinfo;
2. language comes from `LC_ALL`, `LC_MESSAGES`, and `LANG`, plus `AppleLocale` on macOS;
3. locale uses an explicit language that contains a region, such as `en-GB`, `pt-BR`, or `zh-Hant-TW`;
4. a region-less language such as `en`, `pt`, or `zh-Hans` uses the host locale instead;
5. if the host locale is unavailable, CLDR likely-subtags infer a region, with `en-US` as the final fallback.

All results are normalized to IANA/BCP 47 forms. Explicit `default_locale` remains preferable for reproducible production deployments. For example, `default_language: en-GB` implies locale `en-GB`, while `zh-Hans` does not assume a country when the host already supplies `zh-SG` or `zh-CN`.

Browser switching does not use a synthetic `auto` language. Modules marked `browser` keep the saved user preference and browser `Accept-Language`; ANAS does not write a force-language setting. A saved application preference normally outranks the browser. Only applications exposing a deployment fallback consume the global default; others keep their upstream fallback. Collabora receives language per WOPI session, while fixed-English modules and services without a UI expose no ineffective language switch.

See the [module timezone and language matrix](/en/reference/module-localization) for current languages, global-value consumption, selection behavior, and versioned evidence. An unsupported explicit module language emits a `module_localization_fallback` warning and continues with the module's declared fallback. An unsupported inherited global value also continues with that fallback.

A module inherits the global language simply by omitting its own language setting:

```yaml
global:
  default_language: zh-Hant
  default_locale: zh-SG
modules:
  lam: {}
  nextcloud: {}
```

The runner first reads explicit `global.default_language`; when absent, it derives the value from host `LC_ALL`, `LC_MESSAGES`, and `LANG` (plus `AppleLocale` on macOS). It normalizes the value to BCP 47 and publishes global `DEFAULT_LANGUAGE`. A module's `modules.<name>.config.language` takes precedence; otherwise consuming hooks such as LAM and Nextcloud use `DEFAULT_LANGUAGE`. Browser-selected and fixed-language modules may see the global key in `.env` without consuming it, so the matrix's `Global language` column is authoritative.

`DEFAULT_LOCALE` is also the final resolved value. Explicit `global.default_locale` wins; otherwise the runner follows explicit region-bearing language, host locale, then CLDR inference. A module-specific locale still takes precedence over `DEFAULT_LOCALE`.

No additional global “format” knobs are introduced: character encoding is UTF-8; date, number, currency, first-day-of-week, and measurement formats derive from locale or application user preferences; business semantics such as a default phone country remain module settings (for example Nextcloud's `phone_region`). Automation that parses command output should set `LC_ALL=C` at that script boundary instead of using the UI language as a machine interface.

## PostgreSQL and MariaDB

The database is a per-consumer `relational_database` contract binding, not a global switch. A module that supports both engines selects `postgres`, `mariadb`, or `auto` through `modules.<module>.config.db_type`. For example, LLNG can use MariaDB while Nextcloud uses PostgreSQL:

```yaml
modules:
  postgres: {}
  mariadb: {}
  llng:
    config:
      db_type: mariadb
  nextcloud:
    config:
      db_type: postgres
```

For an existing deployment, `auto` preserves the binding recorded in `config.lock.yml`. On first resolution it uses the only explicitly selected compatible provider, when there is exactly one; otherwise it uses the module default, currently `postgres` for dual-database modules. Enabling both providers therefore does not migrate consumers to MariaDB. Verify the resolution with:

```bash
anas plan -c /srv/anas/config.yml
anas config explain llng.db_type
```

Choose the engine before the first `apply` for a new deployment. Changing `db_type` for an installed application is a `data_migrate` change: back up and migrate the application data first, then apply with the explicit risk acknowledgement. `--allow-risky` does not copy tables, translate SQL, or validate the migrated data. Never switch a database by editing `config.lock.yml`.

The runner creates a dedicated database, user, and stable generated credential for every consumer. It publishes only that resource's `*_DB_HOST`, `*_DB_PORT`, `*_DB_NAME`, `*_DB_USERNAME`, `*_DB_PASSWORD`, and `*_NETWORK_DB` values. Applications must not use the PostgreSQL superuser or MariaDB root credentials.

The current manifests declare the following compatibility. The runner rejects interfaces not listed here:

| Consumer module | PostgreSQL | MariaDB |
| --- | --- | --- |
| `llng` | supported | supported |
| `nextcloud` | supported | supported |
| `meshcentral` | supported | supported |
| `authentik` | supported | not declared/supported |

## Secret boundary

Ordinary deployment inputs such as DNS API tokens remain in the system-managed `config.yml`. The file is `0600`; inventory and plan output redact declared sensitive values, and operators change them through configuration commands such as `config set`. Do not commit an external source containing real secrets.

Only `lifecycle_managed` credentials are atomically extracted during import: module-local administrator passwords and settings whose Manifest effect is `credential_rotate`, where an application API or CLI is required and ordinary apply cannot update the live credential correctly. These values and system-generated passwords share the versioned `.anas/secrets.yml` store (`0600`), with stable logical keys plus owner, kind, and provenance metadata. Legacy `.anas/secrets.generated.yml` is unsupported and is never migrated automatically.

`config secret list` returns store keys and kinds only; only explicit `config secret get` reveals clear text. A failed import changes none of `config.yml`, `secrets.yml`, or the managed-config digest. Backups and snapshots must protect both files as plaintext-sensitive data.

### How `config.yml`, `config-managed.yml`, and `secrets.yml` relate

Consider this external input:

```yaml
# /tmp/my-anas.yml
global:
  base_domain: example.com
  timezone: Asia/Singapore

modules:
  nextcloud:
    administration:
      local_accounts:
        break_glass:
          password: Initial-Nextcloud-Password

secrets:
  cloudflare_dns_api_token: cloudflare-token-123
```

Import it through the controlled boundary:

```bash
anas config import /tmp/my-anas.yml -w /srv/anas
```

The external source remains unchanged. The workspace receives three related
forms of state.

`/srv/anas/config.yml` contains ordinary desired state and ordinary
deployment secrets:

```yaml
global:
  base_domain: example.com
  timezone: Asia/Singapore

modules:
  nextcloud:
    administration:
      local_accounts:
        break_glass: {}

secrets:
  cloudflare_dns_api_token: cloudflare-token-123
```

The Nextcloud local-administrator password has been removed, and its username
is determined by ANAS rather than configuration. The Cloudflare
token remains because ordinary apply can correctly re-render that deployment
input. The file is mode `0600`, and inventory and plan output redact values
declared sensitive.

`/srv/anas/.anas/secrets.yml` contains lifecycle-managed credentials and
system-generated secrets:

```yaml
api_version: anas.secrets/v2
secrets:
  ANAS_LOCAL_ADMIN__NEXTCLOUD__BREAK_GLASS__PASSWORD:
    value: Initial-Nextcloud-Password
    owner: nextcloud
    kind: local_admin
    provenance: config-import:modules.nextcloud.administration.local_accounts.break_glass.password
  NEXTCLOUD_DB_PASSWORD:
    value: 8QLRhDzA4ScpJp6h...
    owner: runner
    kind: generated
    provenance: runtime
```

The stable logical key identifies the credential. `owner` records ownership,
`kind` distinguishes local administrators, other lifecycle credentials, and
generated values, while `provenance` records the source. A later
`anas admin local rotate nextcloud break_glass` updates this file atomically
only after the application update and verification succeed; it never writes the
password back to `config.yml`.

`/srv/anas/.anas/config-managed.yml` contains only integrity metadata for
`config.yml`:

```yaml
api_version: anas.config/v1
digest: sha256:643136ee18baf6e3...
updated_by: config-import
```

The digest covers `config.yml` only, not `secrets.yml`. This file contains no
configuration copy or secret. Plan, lock, render, build, and apply compare the
digest and reject out-of-band edits. Snapshots and backups must restore it with
the matching `config.yml`.

| Operation | `config.yml` | `config-managed.yml` | `secrets.yml` |
| --- | --- | --- | --- |
| `config import` | Normalize and remove lifecycle passwords | Write a new digest | Import lifecycle credentials |
| `config set global.timezone UTC` | Change and execute, or report pending | Update digest | Unchanged |
| Change an ordinary DNS/API token | Change | Update digest | Unchanged |
| `admin local rotate nextcloud` | Unchanged | Unchanged | Update after application verification |
| Edit `config.yml` by hand | Content changes | Digest does not | Unchanged; plan/apply reject |
| Snapshot/backup restore | Restore | Restore with config | Restore together |

In short, `config.yml` says what the deployment should be,
`config-managed.yml` proves that the ANAS CLI wrote that desired state, and
`secrets.yml` stores credentials that ordinary apply cannot safely change plus
Runner-generated secrets.

## Module-local administrators

`management.local_accounts` is a capability declared by a Module Manifest.
Users cannot configure a local-administrator username. A Module
`fixed_username` wins; otherwise ANAS uses its fixed `admin_{module}` template.
IDs such as `primary` and `break_glass` are not usernames, and
lifecycle-managed passwords are not persistent `config.yml` fields. An external import may provide one as a one-time bootstrap input; the normalized workspace copy removes it after a successful import.

```bash
anas admin local credential nextcloud break_glass -w /srv/anas
anas admin local rotate nextcloud break_glass -w /srv/anas
anas admin local rotate ddns_go --prompt -w /srv/anas
```

Omitting the ID selects `primary`, then a sole account, otherwise reports
ambiguity. Once materialized, the physical username is locked in
`.anas/local-admins.yml`. Global `username_template` and module-level `username`
overrides are invalid fields, and the CLI provides no rename command.

Nextcloud does not declare `modules.nextcloud.config.admin_password`; that path
is invalid configuration. Its handler resets and verifies the recovery account
through `occ user:resetpassword --password-from-env`.
