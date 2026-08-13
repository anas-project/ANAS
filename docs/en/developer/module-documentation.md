# Module documentation standard

This standard defines the version, timezone, language, and upstream evidence every module must maintain, and how README and reference pages are generated.

## Required files

Every `modules/<name>/` directory containing `module.yml` must also contain:

- `README.md`, with manual module guidance and a generated timezone/language section;
- `localization.yml`, the machine-readable inventory for the current module version.

`localization.yml` is the source of truth. Do not edit content between `<!-- generated:localization:start -->` and `<!-- generated:localization:end -->` in a module README.

Generate or verify all module READMEs and both reference pages with:

```bash
go run ./cmd/gen-module-docs
go run ./cmd/gen-module-docs --check
```

The check form does not modify files and is suitable for CI.

The generator treats `docs/reference/module-localization.md` and `docs/en/reference/module-localization.md` as an inseparable output pair: one run writes both, and `--check` fails when either is missing or stale. An AI agent changing the generator or inventory must inspect both outputs and navigation, never commit only the Chinese page.

## Inventory rules

The schema is `anas.module-localization/v1`. `module_version` and `module_revision` must exactly match `module.yml`; `reviewed_at` records the actual review date. Timezone metadata must state which processes consume `TZ` or an application-specific setting.

`language.status` is `supported`, `fixed`, or `not_applicable`. `language.selection` is `browser`, `integration`, `application`, `deployment_default`, `fixed`, `client`, or `none`. `global_default` and `global_locale` are `applied`, `fallback`, `not_consumed`, or `not_applicable`; they record application consumption, not merely that a key appears in `.env`. Write `supported` values as canonical BCP 47 tags and describe upstream spellings such as `zh_CN`, `pt-br`, or POSIX locales in `upstream_format`.

Every evidence item must be tied to the pinned upstream version. Prefer a locale directory, translation manifest, or resource keys in versioned source, followed by versioned official documentation and inspection of the exact image. A rolling marketing page is supplementary evidence only.

## Selection and unsupported values

For `selection: browser`, the application keeps using the user preference or browser `Accept-Language`. `global.default_language` is a deployment fallback only when `global_default` says `fallback`; `not_consumed` means upstream uses its own fallback. Language and regional formatting remain separate, so applications such as Nextcloud also consume `global.default_locale`.

When `global.default_locale` is absent, derivation from language is allowed only for an explicit region: `en-GB` can become the locale directly, while `en` or `zh-Hans` must try the host locale before `x/text` CLDR likely-subtag completion. Modules must not duplicate this policy locally.

Modules accepting an explicit language use BCP 47 at the ANAS boundary and `internal/localization` for upstream conversion. A supported explicit value is converted. An unsupported explicit value emits `module_localization_fallback` and continues with the declared fallback; an unsupported inherited global value also continues with that fallback. Browser-negotiated applications delegate unknown browser values to upstream. `fixed` and `not_applicable` modules must not expose a setting that has no effect.

Never cross script variants: `zh-Hant` must not silently match `zh-Hans`. Do not scatter ad-hoc locale replacement tables across hooks.

See the [module upstream upgrade SOP](/en/developer/module-upgrade-sop) for the recurring review workflow.
