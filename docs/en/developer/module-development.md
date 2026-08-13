# Module development

A module is an independent release and deployment unit. It owns its manifest identity, version, ABI, dependencies, capabilities, configuration declarations, Compose definition, optional hook, templates, and assets.

The frozen deployment must carry everything needed to start. It must not depend on relative paths into a source checkout.

Declare hard dependencies explicitly. Use capability providers for alternatives, ordering edges only for ordering, and resource/provider operations for persistent resources. Scope generated environments to the module, its dependency closure, and explicitly consumed keys. Never log secrets or inject unrelated credentials.

## Documentation, timezone, and language

Every module must maintain `README.md` and `localization.yml` matching the current `module.yml` version. Derive supported languages from pinned source, official documentation, or the exact image, record canonical BCP 47 values, and distinguish browser negotiation, deployment defaults, fixed language, and services without a UI.

Follow the [module documentation standard](/en/developer/module-documentation) for fields, fallback policy, and generation, and run the [module upstream upgrade SOP](/en/developer/module-upgrade-sop) for every upstream version change.
