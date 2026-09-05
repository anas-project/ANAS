# ANAS Core implementation standard

> Status: current mandatory architecture standard

ANAS Core is a generic orchestrator and contract executor, not a central home
for built-in Module business logic. A Module owns the business meaning of its
parameters, cross-parameter invariants, derived values, external-system
adapters, and persistent-state reconciliation. Core supplies declarative
schemas, ABI dispatch, security boundaries, and transaction mechanics.

## Parameter ownership boundary

The business meaning of `modules.<name>.config.*`, its environment spelling,
and Module exports belongs to that Module. Core may mechanically:

- parse structured addresses, apply literal defaults declared by the manifest,
  and normalize declared types, enums, formats, and generic constraints;
- resolve dependencies, capabilities, contracts, resources, and the effective
  topology;
- dispatch declared Hook and lifecycle phases with scoped environments and
  Secrets;
- validate Hook patches for schema, sensitivity, ownership, and mutation
  boundaries;
- produce plans, locks, and deployment manifests while enforcing transactions,
  rollback, and stable error contracts;
- publish Runner-owned global/runtime facts and generic Contract bindings.

These mechanics do not make Core a second owner of Module parameter semantics.
Core must not guess, correct, or overwrite Module parameters to make a
configuration pass. The owning Module must reject invalid combinations.

## Forbidden implementations

Core production code must not:

1. branch on a Module name, private environment prefix, or product name;
2. add validation, derivation, or repair helpers tied to one Module, such as
   `validateSambaDomainDNSConfig`;
3. synthesize, overwrite, or silently migrate private values such as
   `SAMBA_DC_*` or `NEXTCLOUD_*`;
4. duplicate one Module rule across config set/import/plan/render/apply paths;
5. special-case one Module's database, DNS, certificate, or persistent state;
6. rewrite an invalid requested value to a fallback, automatic downgrade, or
   nearest legal value.

Integration fixtures may use concrete Modules, but generic Core behavior should
be tested with synthetic Modules so tests do not institutionalize product
branches in production code.

## Correct extension path

For Module-specific parameter semantics:

1. declare types, default sources, sensitivity, change effects, and Hook phases
   in `module.yml`;
2. implement side-effect-free cross-parameter checks in the Module `validate`
   Hook;
3. produce derived values in the Module `calculate` Hook;
4. use Module lifecycle preflight, reconcilers, or operations for external and
   persistent state;
5. let Core only dispatch in effective dependency order, scope inputs, validate
   outputs, and propagate structured failures.

If several Modules share a concept, define a neutral schema, capability, or
contract. Core may understand generic vocabulary such as `oidc` and `saml` and
resolve Provider/Consumer compatibility. A Module's supported subset and legal
parameter combinations still belong to its manifest and implementation.

## Working by default, replaceable when advanced

ANAS is a NAS service launcher, not an assembly kit for experts. **A user who wants a service should
be able to install it without first understanding what it depends on, let alone preparing those
dependencies by hand.** Keeping the implementation invisible to the user is a product requirement, not
optional polish.

Two rules follow, and they hold for every module and every contract:

1. **The default path must work out of the box.** Whatever a module declares it needs -- a database,
   object storage, an isolation sandbox, an image -- ANAS must be able to satisfy automatically.
   "Prepare X elsewhere first, then paste its address and credentials into the configuration" is not
   an acceptable default install path. Anything the user has to prepare by hand is a service that,
   by default, does not install.
2. **The advanced path must exist and be equivalent.** A user who already runs their own instance of
   the same thing must be able to point the module at it through configuration and get the same
   capability. Automating the default must not cost the ability to use one's own.

The test for this section is a single question: **can a user who knows only which service they want,
and nothing about what it depends on, install it and use it?** If not, what is missing is automation,
not documentation.

This does not conflict with the parameter ownership boundary above: satisfying a dependency
automatically is Core scheduling a provider from what the manifest declares, not Core guessing a
module's semantics. Nor does it waive security boundaries -- an automatically generated credential is
still a credential, and an automatically installed component still needs an explicit privilege
boundary and an uninstall path.

A dependency that can only be satisfied automatically by elevating privilege is the real point of
tension between this section and "anas never elevates its own privilege". Such a dependency must be
recorded separately: where the elevation happens, what privileged artifact the user is left with
afterwards, and what the degraded path is when privilege is unavailable. This section is never a
licence to hide an implicit `sudo` inside a module hook.

## Samba domain-separation example

Core may dispatch generic `validateModules`; it must not implement
`validateSambaDomainDNSConfig` or contain business branches for `samba_dc`,
domain relationships, or `application_dns_mode`.

The Samba DC Module validates the application/AD domain relationship, resolves
`auto/ad_zone/separate_zone`, derives Realm/Base DN/DC FQDN/DNS zone, and
reconciles observed Samba/DNS state. Core performs generic schema normalization,
Hook dispatch, Secret isolation, mutation and ownership validation, plan
metadata persistence, and transactional failure handling.

## Review gate

Every Core change must answer:

- Does this logic apply to an unknown third-party Module? If not, move it to
  the Module.
- Does it depend only on manifest or Contract declarations, rather than a
  Module name or private environment variable?
- Does it mutate a requested Module value instead of rejecting it or letting
  the Module resolve it?
- Can the capability be expressed through a generic ABI and tested with a
  synthetic Module?
- For a cross-Module concept, is there a neutral schema, capability, or
  contract rather than implicit product coupling?
- Does the new dependency have an automatic default path? If it requires the
  user to prepare an external service by hand, the feature does not install
  under a default installation.

Only Runner-owned global facts and genuinely generic cross-Module contracts
belong in Core. Any exception requires a separate architecture decision and a
generic interface before implementation.
