ARG GHCR_REGISTRY=ghcr.io
FROM ${GHCR_REGISTRY}/goauthentik/server:2026.5.6

# Keep ANAS-specific runtime code in the release image. Deployment-specific
# applications, credentials, and directory values remain generated blueprints
# and environment variables mounted at runtime.
COPY --chmod=0755 directory_watch.py /opt/anas/bin/directory-watch
COPY --chmod=0755 worker-entrypoint.sh /opt/anas/bin/worker-entrypoint
COPY --chmod=0755 server-entrypoint.sh /opt/anas/bin/server-entrypoint
