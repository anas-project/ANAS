# Module catalog

This is the human-readable index of `modules/*/module.yml`. Manifests remain authoritative for versions and experimental status.

## Stable modules

| Module | Purpose |
| --- | --- |
| `lego` | wildcard certificates and internal CA material |
| `traefik` | HTTPS reverse proxy, dashboard, and declared routes |
| `samba_dc` | AD-compatible directory, LDAP, Kerberos, and BIND9-DLZ DNS |
| `samba_fs` | domain-joined SMB file sharing on the host LAN |
| `postgres`, `mariadb` | relational database providers with optional Adminer |
| `eturnal` | TURN for realtime applications |
| `nextcloud` | file sync, sharing, Talk, Memories, and media services |
| `collabora` | online document editing for Nextcloud |
| `llng` | LemonLDAP::NG OIDC/SAML provider and application portal |
| `lam` | LDAP Account Manager web interface |
| `meshcentral` | LDAP-backed remote device management |
| `ddns_go`, `ddns_updater` | alternative dynamic DNS implementations |
| `oauth2_proxy` | OIDC gate for services without their own login |

## Non-stable modules

`authentik`, `netbird`, and `freeradius` are explicitly marked `experimental`.
`casdoor` and `versitygw` are `developing`; VersityGW provides a path-style S3 API
over a dedicated POSIX directory, normalized `object_storage/s3` capability bindings,
and per-Resource isolated buckets and credentials. It remains gated on the listed
real-client, architecture, restore, and upgrade E2E evidence. NetBird remains an
incomplete overlay-network scaffold; FreeRADIUS does not generate production client or user policy.

Select modules as a YAML mapping and let the runner resolve dependencies and contract providers:

```yaml
modules:
  traefik: {}
  nextcloud:
    config:
      domain_prefix: cloud
      db_type: auto
```

```bash
anas plan -c /srv/anas/config.yml
anas config list <module>
anas config explain <module>.<parameter>
```
