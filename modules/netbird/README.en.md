# NetBird

Incomplete WireGuard overlay network module.

> [!WARNING]
> Lifecycle is `developing`; use it for development and validation only, not recommended production deployments.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `netbird` |
| Version / revision | `0.76.1-r5` |
| Status | `developing` |
| Category | `network` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `eturnal` | Module | — |
| `iam` | Capability | `oidc` |

## Minimal configuration

```yaml
modules:
  netbird: {}
```

This module also requires a deployment-level IAM provider, for example:

```yaml
identity:
  iam:
    provider: llng
```

## Identity, users, and groups

It declares an OIDC consumer and application group, but administrator-role mapping remains a release blocker.

Pinned Dashboard `2.90.9` discovers the provider logout endpoint and initiates RP logout, but exposes no standard IAM-to-Dashboard notification endpoint. ANAS upgrades it to “Module-initiated logout” only after the browser matrix verifies `state`, local-session invalidation first, and termination of the central session. The current status is “upstream support, integration pending”; browserless bidirectional logout is not declared.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | oidc |
| Group | `APP_netbird` / `APP_all` |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

### Directory attribute changes

**Matching key**: the OIDC `sub`. The management configuration sets `AuthUserIDClaim` explicitly to
`sub`, and NetBird uses it as its own user id. **Whether this key survives a directory rename depends
on which IAM Provider the deployment selected** — `authentik` (an internal user UUID) and `casdoor`
(an immutable User ID) keep `sub` unchanged across a rename; `llng` derives `sub` from the login name
by default, so after a rename it changes, NetBird treats the same person as a new user, and their
peers, setup keys, and access-policy memberships all stay behind on the old one.

**This Module does not currently request the `anasIdentityAnchor` claim**: the claims it registers are
`name:displayName`, `cn:cn`, `sAMAccountName:sAMAccountName`, and `email:email`. The `sAMAccountName`
is used for display only and enters no persistent key; `sub` is the only persistent key.

That `AuthUserIDClaim` is configurable is this Module's opportunity in M2: once the subject identifier
becomes the anchor, the field can stay as it is (`sub` already *is* the anchor) or be pointed at the
anchor claim instead. Neither path needs a new upstream capability.

| Directory change | What NetBird does | Evidence |
| --- | --- | --- |
| `sAMAccountName` changes | The same user when the Provider's `sub` is stable, and NetBird creates nothing; the displayed name comes from the `name` claim. When the Provider's `sub` is the login name (`llng`), a second user appears | `inferred` (from the rendered `NETBIRD_AUTH_USER_ID_CLAIM = "sub"` and each Provider's subject-identifier shape; NetBird has no identity E2E) |
| `mail` changes | Read from the `email` claim; the refresh timing has not been re-checked, and it takes no part in account binding | `inferred` |
| `displayName` and other profile attributes | Read from the `name`/`cn` claims; the refresh timing has not been re-checked | `inferred` |
| Direct or recursive group membership changes | **Affect only whether the person can log in**, decided on the IAM side against `APP_netbird`/`APP_all`/the administrator group and taking effect at the next login. **NetBird's groups, access policies, and administrator role are owned by NetBird's own database and directory groups are not projected into them**; the administrator role mapping itself remains a release blocker | directory groups not being projected: `verified` (no group→role mapping exists in the Hook or the management configuration); admission and moment of convergence: `inferred` |
| Account disabled | The next login is refused by the IAM. **Existing Dashboard sessions do not expire** (the pinned Dashboard `2.90.9` has no IAM→Module receiver); more importantly, **registered peers and setup keys never pass through an interactive login at all**, so the VPN tunnels keep working | absence of a receiver: `verified` (see the logout matrix in [Module IAM / OIDC support](/en/reference/module-iam-support)); survival of peers and setup keys: `inferred` |
| Account deleted | As above, and the NetBird user keeps its peers and setup keys untouched; **peer ownership is never transferred or deregistered automatically** | `inferred` |
| Identifier recycled and reassigned | Depends on the Provider: where `sub` is an internal immutable id the newcomer gets a new user (fail-closed); where `sub` is the recycled login name (`llng`) the newcomer lands directly on the old user's peers and policy memberships (**fail-open** — which hands an already-established VPN access to a new person) | `inferred` |

**Fallback path** — what operations must do for every "no automatic path" row above:

1. When a person is disabled or deleted in the directory, **delete that user in the NetBird management
   interface and revoke every peer and setup key they own, one by one**. Disabling the directory
   account does not tear down an established tunnel — this is the highest revocation risk in this
   Module;
2. Before deleting the user, transfer any peers that must survive to a successor or re-own them under
   a service account;
3. When group revocation must take effect immediately, change NetBird's access policies separately;
   a directory group change never propagates into a policy.

## Administrator login and IAM-outage recovery

There is no supported private recovery administrator or documented IAM-bypass entry.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

## Database support

This module neither consumes nor provides a relational-database contract.

## All configuration parameters

This inventory comes from the current `module.yml` and `anas config list`. The environment key is the rendered module-private key, not the preferred configuration interface.

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `netbird.domain_prefix` | string | — | `netbird` | `static` | `NETBIRD_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `netbird.iam_protocol` | enum (`auto`, `oidc`, `saml`) | — | `auto` | `static` | `NETBIRD_IAM_PROTOCOL` | no | no | no | yes | `container_recreate` | The OIDC issuer and client configuration change together. |

### Query and modify

```bash
anas config list netbird -w /srv/anas
anas config explain netbird.iam_protocol
anas config set netbird.iam_protocol oidc -w /srv/anas
anas config plan -w /srv/anas
```

Parameters with `editable=false` cannot be completed by ordinary `config set`. A named workflow is a lifecycle declaration, not a guarantee that a generic command of that name exists. Raw `env.<KEY>` is only a compatibility escape hatch and cannot rotate an application-internal password.

## Timezone and language

- Timezone status: `partial`
- Timezone mechanism: Dashboard, signal, and management receive the module environment; the relay service does not currently receive TZ.
- Language status: `fixed`
- Supported languages (1): `en`
- Fallback: English is the only Dashboard language in the fixed source version.

## Storage, backup, and verification

Protect persistent state with the workspace snapshot/backup. Database consumers must also back up their bound database resource; generated secrets and local-administrator state must share the same recovery point.

`TURN_SECRET` is explicitly bound to `eturnal.secret` through
`credentials.consumes`. The Runner starts NetBird only after Eturnal's
credential ready barrier verifies successfully; this Module neither owns nor
rotates that value.

```bash
anas plan -c /srv/anas/config.yml
anas config list netbird -w /srv/anas
anas status -w /srv/anas
```

## Current limitations

Status is `developing` and it is excluded from recommended deployments.

## Technical documentation

See [technical documentation](docs/technical.en.md) for password storage, environment scope, hooks, networks, resources, and tests.
