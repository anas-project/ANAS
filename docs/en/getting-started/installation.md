# Installation and requirements

## Host requirements

ANAS targets Linux hosts. Running services requires Docker Engine and Docker Compose v2. Btrfs is recommended for the workspace; other filesystems work, but cannot provide the Btrfs-backed local snapshot capability.

You also need:

- host permissions to create the workspace, data directories, and container networks;
- network access to the selected module images and package sources;
- durable storage for all services;
- control of DNS and the necessary DNS API credentials when using domains and HTTPS.

## Select a Module source

```yaml
module_source: official       # GHCR primary, CNB fallback
# module_source: cn           # CNB primary, GHCR fallback
```

`cn` normalizes to `official-cn`. Unless explicitly disabled, it also persists
`global.chinese_speedup: true`, keeping Module, runtime-image, and runtime-download traffic on the
mainland China distribution path. Set `global.chinese_speedup: false` to opt out of those runtime
defaults while keeping CNB as the Module source.

Import the external configuration as part of first initialization:

```bash
anas init /srv/anas --config ./anas.yml
```

The default is persisted in the newly generated `/srv/anas/config.yml` and renders as
`CHINESE_SPEEDUP=true`; the external `anas.yml` remains unchanged. Use
`anas config import ./anas.yml -w /srv/anas` for the same normalization in an existing workspace.

The CLI reads the OCI catalog and standard repository tag list, then installs an exact release into
the user content-addressed cache. `config.lock.yml` records the OCI manifest, unpacked-content, and
installed-tree digests, so ordinary sync/apply never follows a moved tag.

## Module installation and cache

Core releases contain the binary only. During first initialization, ANAS bootstraps Module packages
from the configured source when no local bundles are available, then resolves the deployment lock:

```bash
anas init /srv/anas --config ./anas.yml
anas module update -w /srv/anas
```

Discovery and explicit prefetch commands are:

```bash
anas module list --source cn
anas module versions nextcloud --source cn
anas module install nextcloud@34.0.2-r4 --source cn
```

On recovery or a new host, `anas module sync -w /srv/anas` restores missing packages strictly from
the existing lock without upgrading. The default cache is `~/.cache/anas/modules/`; override it with
`ANAS_MODULE_CACHE`. Basic private-registry credentials are read from Docker `config.json`, while
public GHCR/CNB access follows the standard Bearer challenge.

`--module-root` and `ANAS_MODULE_ROOT` remain the highest-priority local-development overrides. A
source checkout can continue to use its adjacent `modules/` and `contracts/` trees directly.

Running from source requires the Go toolchain declared by the repository:

```bash
go run ./cmd/anas --help
```

## Verify dependencies

```bash
docker version
docker compose version
anas --help
anas version
```

Then continue with the [first deployment](quick-start.md).
