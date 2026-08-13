# Installation and requirements

## Host requirements

ANAS targets Linux hosts. Running services requires Docker Engine and Docker Compose v2. Btrfs is recommended for the workspace; other filesystems work, but cannot provide the Btrfs-backed local snapshot capability.

You also need:

- host permissions to create the workspace, data directories, and container networks;
- network access to the selected module images and package sources;
- durable storage for all services;
- control of DNS and the necessary DNS API credentials when using domains and HTTPS.

## Installation layout

Keep the binary and module bundle together in a release installation:

```text
/opt/anas/
  bin/anas
  modules/
```

ANAS discovers modules automatically in this layout. If they have separate roots, set:

```bash
export ANAS_MODULE_ROOT=/opt/anas/modules
```

Running from source requires the Go toolchain declared by the repository:

```bash
go run ./cmd/anas --help
```

## Verify dependencies

```bash
docker version
docker compose version
anas --help
```

Then continue with the [first deployment](quick-start.md).
