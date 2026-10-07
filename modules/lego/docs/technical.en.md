# Lego ACME certificates technical implementation

This page records the current implementation, security boundaries, and verification entry points for `lego`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `5.3.1-r5` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| — | — | — |

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_lego` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-lego:5.3.1-r5` | `` | 1 |
<!-- generated:compose-topology:end -->

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `lego.dns_provider` | string | — | — | — | `LEGO_DNS_PROVIDER` | no | no | no | yes | `reconcile` | Changing the ACME DNS provider requires issuing a replacement certificate with that provider. |
| `lego.dns_server` | string | — | `223.5.5.5` | `static` | `LEGO_DNS_SERVER` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Identity and authorization data flow

There are no human users, directory sync, or IAM login. DNS API credentials are machine secrets.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | unsupported/not applicable |
| Group | not declared |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Management surfaces and secret lifecycle

There is no Web management surface or private administrator.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

### Secret boundaries

The manifest declares no cross-module password/secret consumption or managed local administrator.

## Database support

This module neither consumes nor provides a relational-database contract.

## Environment ownership

### Exports

- `ANAS_TLS_*`

### Explicit consumes

—

The dependency closure does not grant every environment value. Sensitive values enter this module's hook/container scope only through ownership or an explicit `config.consumes` claim.

## Hooks, changes, and rollback

The internal certificate and private key remain under `ANAS_TLS_CERTS_DIR` as `anas-internal.crt` and `anas-internal.key`, exported through `ANAS_TLS_INTERNAL_CERT_NAME` and `ANAS_TLS_INTERNAL_KEY_NAME`. `ANAS_TLS_INTERNAL_CA_NAME` identifies the internal root. Environment variables contain paths/names, never private key contents. Public issuance replaces only the serving certificate and preserves the internal pair. Startup and scheduled checks publish the internal pair when the serving certificate is missing, expired, covers the wrong domain, or has a missing/mismatched key. Internal leaves renew within 90 days of expiry. The trust bundle is refreshed with the internal CA; a missing or invalid CA is regenerated, requiring clients to trust the new root.

`ca.sh bootstrap` preserves an existing certificate that has not expired and covers the configured domain. The internal 90-day renewal threshold must not govern public certificate reuse at startup, because a restart would otherwise replace a valid ACME leaf. `ca.sh renew` retains the 90-day threshold for internal leaves; Lego manages public renewal.

- Hook command: `go run ./hook`
- `credential_rotate`, `data_migrate`, and `immutable` are blocked from ordinary edits; the declared lifecycle operation must update persistent application state.
- A local-administrator rotation commits the generated secret only after the module handler succeeds; failure keeps or restores the old application credential.

## Tests and implementation locations

- `go test ./modules/lego/hook` covers the parameter contract and certificate bootstrap/renewal using real OpenSSL fixtures. Certificate tests require OpenSSL on the host.
- Container validation: `DOCKER_HOST=unix:///run/<anas-test-socket> python3 test-env/scripts/server-lego-certificate-e2e.py`. The script requires an isolated daemon, supports the same `DOCKER_HUB_REGISTRY`, `CHINESE_BUILD_SPEEDUP`, `APK_MIRROR_URL`, and `DOCKER_BUILD_NETWORK` build inputs as Compose, builds a candidate image, and checks persistence, ACME adoption, expiry fallback, and TLS through the shared trust bundle using a temporary volume. A local alternate CA supplies ACME output; this does not validate public DNS-01 issuance. Logs and source/image digests go to `ANAS_LEGO_EVIDENCE_DIR` (a system temporary directory by default). When upstream is unavailable, explicitly set `ANAS_LEGO_BASE_IMAGE` to build a script candidate from an existing Lego runtime image. The report records its base image ID; this mode does not establish a successful rebuild of upstream layers with the full Dockerfile.
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Current limitations

Do not copy DNS secrets through ad-hoc environment variables; use structured secrets and module scoping.
