# Traefik operations

Traefik is the HTTPS entry point for ANAS. The current module uses Traefik `3.7.10` and depends on `lego` for certificates; Traefik does not run the ACME challenge itself.

## Request path

```text
client -> host TRAEFIK_BASE_PORT -> Traefik https entrypoint
       -> Docker-provider or file-provider route -> target service
```

The default port is `9000`, both on the host and inside the container. The default dashboard URL is:

```text
https://traefik.<base_domain>:9000
```

The port can be omitted when `base_port` is changed to `443`.

## Configuration

```yaml
modules:
  lego:
    config:
      dns_provider: cloudflare
  traefik:
    config:
      base_port: 9000
      domain_prefix: traefik

global:
  base_domain: nas.example.com
  email: admin@example.com

secrets:
  cloudflare_dns_api_token: replace-me
```

`lego` obtains a wildcard certificate through DNS-01. Its certificate directory is mounted read-only into Traefik. Keep DNS-provider credentials out of version control. Changing `base_port` or `domain_prefix` recreates the Traefik container and affected routes.

The dashboard uses the managed `primary` BasicAuth account. Its default physical
username is `admin_traefik`; before first deployment it can be overridden at
`modules.traefik.administration.local_accounts.primary.username`. Passwords are
generated in the Secret Store and cannot be supplied through argv, `config.yml`,
or top-level `env`.

```bash
anas admin local credential traefik -w /srv/anas
anas admin local rotate traefik --prompt -w /srv/anas
```

The active file-provider document stores only a bcrypt verifier. Rotation writes
the candidate state, requests the real Dashboard with candidate BasicAuth, then
commits the Secret; failure restores the old state.

## Route providers

Containers on the shared Traefik network declare routes through Docker labels. Traefik discovers only containers that explicitly set `traefik.enable=true`, match the current ANAS instance label, and join the current Traefik network.

Host-network services, non-Docker processes, and address-only upstreams use the file provider. Modules publish these variables:

```text
ANAS_TRAEFIK_ROUTE__<NAME>__RULE          required Traefik rule
ANAS_TRAEFIK_ROUTE__<NAME>__URL           required upstream URL
ANAS_TRAEFIK_ROUTE__<NAME>__MIDDLEWARES   optional comma-separated list
ANAS_TRAEFIK_ROUTE__<NAME>__ENTRYPOINTS   optional, defaults to https
ANAS_TRAEFIK_ROUTE__<NAME>__TLS           optional, defaults to true
```

These are module-to-module contracts, not normal user settings. A publishing module must also declare `ANAS_TRAEFIK_ROUTE__*` in `config.exports`.

## Security and troubleshooting

- The Docker socket is read-only, but still provides extensive host visibility. Only the trusted Traefik image should access it.
- Keep Dashboard BasicAuth enabled and secrets out of public examples.
- `DOCKER_SOCKET_PATH` is an advanced raw environment override for compatible sockets.
- Open only the configured entry port in the firewall; the default is `9000`, not `80/443`.

```bash
anas status -w /srv/anas
docker logs anas_traefik
```

Check DNS and the non-standard port first, then certificate generation, current-instance labels and network membership, upstream reachability, and host port conflicts. See [Networking](networking.md) for general checks.
