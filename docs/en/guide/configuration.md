# Configuration

## File responsibilities

`<workspace>/config.yml` is the desired state maintained by the operator. `config.lock.yml` records resolved module versions, providers, and host policy. Do not edit runtime state below `.anas/` by hand.

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

Edit YAML directly or use the CLI:

```bash
anas config explain nextcloud.domain_prefix
anas config set global.timezone Asia/Singapore -w /srv/anas
anas config plan -w /srv/anas
anas apply -w /srv/anas
```

Some changes affect persistent state inside a service and require an explicit migration. ANAS rejects an ordinary apply and reports the risk. Use `anas apply --allow-risky` only after preparing the required migration; the flag removes the gate but does not rotate credentials or migrate a database for you.

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

## Secrets

Do not commit real secrets. `config secret list` returns names only; only the explicit `config secret get` operation returns clear text. Generated secrets live in the protected workspace runtime and are handled by the ANAS backup flow.
