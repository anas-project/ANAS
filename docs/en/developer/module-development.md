# Module development

A module is an independent release and deployment unit. It owns its manifest identity, version, ABI, dependencies, capabilities, configuration declarations, Compose definition, optional hook, templates, and assets.

The frozen deployment must carry everything needed to start. It must not depend on relative paths into a source checkout.

Declare hard dependencies explicitly. Use capability providers for alternatives, ordering edges only for ordering, and resource/provider operations for persistent resources. Scope generated environments to the module, its dependency closure, and explicitly consumed keys. Never log secrets or inject unrelated credentials.

## Supporting PostgreSQL and MariaDB

A consumer supporting both engines must integrate through the `relational_database` contract. It must not hard-code a dependency on either provider module or read provider administrator credentials. Its manifest must declare a `db_type` selector, both verified interfaces, a default, and a managed database resource:

```yaml
dependencies:
  contracts:
    - name: relational_database
      version: ">=1.0.0 <2.0.0"
      selected_by: db_type
      interfaces: [postgres, mariadb]
      default: postgres
resources:
  requires:
    - id: primary_database
      contract: relational_database
      binding: db_type
      spec_from: {name: db_name}
      spec:
        principal: example_app
        credential: {policy: generated}
        deletion_policy: retain
config:
  defaults:
    db_type: auto
    db_name: example_app
  changes:
    db_type:
      effect: data_migrate
      apply: migrate-example-app-database
```

Use `postgres`, `mariadb`, and `auto` as the public selector values; `mysql` is not a contract interface. After resolution, consume only the runner-owned `<PREFIX>_DB_TYPE`, `_DB_HOST`, `_DB_PORT`, `_DB_NAME`, `_DB_USERNAME`, `_DB_PASSWORD`, and `_NETWORK_DB` values. Join the selected provider's external network through `_NETWORK_DB`; do not attach both database networks or use cross-project `depends_on`.

The image must contain both required clients and application drivers. Translate the generic binding to any upstream-specific `POSTGRES_*` or `MYSQL_*` settings inside the consumer module. Provider `ensure` owns the database and dedicated user; application startup owns only its idempotent schema initialization and must not grant administrative privileges.

Unit tests must exercise both engine branches, dedicated credentials, and network mapping. Render and Compose tests must cover both interfaces across the matrix, and stable support requires a real-container test of schema initialization, restart, and idempotent re-apply. If upstream supports only one engine, declare only that verified interface.

## Documentation, timezone, and language

Every module must maintain `README.md` and `localization.yml` matching the current `module.yml` version. Derive supported languages from pinned source, official documentation, or the exact image, record canonical BCP 47 values, and distinguish browser negotiation, deployment defaults, fixed language, and services without a UI.

Follow the [module documentation standard](/en/developer/module-documentation) for fields, fallback policy, and generation, and run the [module upstream upgrade SOP](/en/developer/module-upgrade-sop) for every upstream version change.
