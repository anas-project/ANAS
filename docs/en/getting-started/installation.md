# Installation and requirements

## Host requirements

ANAS targets Linux hosts. Running services requires Docker Engine and Docker Compose v2. Btrfs is recommended for the workspace; other filesystems work, but cannot provide the Btrfs-backed local snapshot capability.

You also need:

- host permissions to create the workspace, data directories, and container networks;
- network access to the selected module images and package sources;
- durable storage for all services;
- control of DNS and the necessary DNS API credentials when using domains and HTTPS.

## One-line Core installation

The installer currently supports Linux only and detects `x86_64`/`amd64` and
`aarch64`/`arm64`. It downloads the matching static binary, verifies it against the Release
`SHA256SUMS`, then runs the downloaded binary and requires its reported version to match the Release
tag before replacing an existing installation or writing source preferences. It installs the binary
as `/usr/local/bin/anas` and invokes `sudo` only when the destination is not writable.

Install from GitHub and keep GitHub/GHCR as the default distribution source:

```bash
curl --proto '=https' --tlsv1.2 -fsSL https://raw.githubusercontent.com/anas-project/ANAS/master/install.sh | sh
```

Install from the CN source, backed by CNB, and keep it as the default distribution source:

```bash
curl --proto '=https' --tlsv1.2 -fsSL https://cnb.cool/anas.dev/ANAS/-/git/raw/master/install.sh | sh -s -- --source cn
```

CNB Release attachments are byte-for-byte mirrors of the GitHub Release and are SHA-256 verified;
they are not rebuilt on CNB. The installer resolves the current stable tag through the public
`/-/releases/latest` redirect.

The installer records the choice in `${XDG_CONFIG_HOME:-$HOME/.config}/anas/source`. A CNB install
stores `official-cn`. A later `anas init`, or `anas config import` whose external file omits
`module_source`, persists:

```yaml
module_source: official-cn
global:
  chinese_speedup: true
```

Modules then come from CNB, Compose receives
`ANAS_IMAGE_REGISTRY=docker.cnb.cool/anas.dev/anas`, and runtime downloads use the mainland-China
mirrors. Once a workspace is initialized, its own `config.yml` retains the source, so moving the
workspace or changing the installer preference cannot silently change an existing deployment. An
explicit `module_source` in the external configuration always wins.

To avoid `sudo`, install into a writable directory already present in `PATH`:

```bash
curl --proto '=https' --tlsv1.2 -fsSL https://raw.githubusercontent.com/anas-project/ANAS/master/install.sh | sh -s -- --install-dir "$HOME/.local/bin"
```

The script requires `curl`, `tar`, `install`, and either `sha256sum` or `shasum`. Automation can set
`ANAS_INSTALL_SOURCE=github|cn` and `ANAS_INSTALL_DIR`; the CLI also accepts a temporary
`ANAS_DEFAULT_SOURCE=official|official-cn` override.

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
