ARG GHCR_REGISTRY=ghcr.io
FROM ${GHCR_REGISTRY}/immich-app/immich-server:v3.2.4@sha256:d317916b28090c33eb36b308464ea391f8b7df1d850fcfea227a39ec879718c2

COPY enforce-oidc-only.cjs /opt/anas/enforce-oidc-only.cjs
COPY fix-upload-queue-rollback.cjs /opt/anas/fix-upload-queue-rollback.cjs
COPY fix-revoked-sockets.cjs /opt/anas/fix-revoked-sockets.cjs
COPY guard-credential-races.cjs /opt/anas/guard-credential-races.cjs
COPY oidc-credentials.cjs /usr/src/app/server/dist/oidc-credentials.cjs
# The upstream password-login setting does not protect account creation,
# password writes, or OAuth rebinding. Keep the original OIDC callback and
# roleClaim initialization; guard confirmed write paths, duplicate anchors,
# and logout replay. All maintenance runs inside the native migration lock.
RUN node /opt/anas/enforce-oidc-only.cjs /usr/src/app/server/dist/services \
    && node /opt/anas/fix-upload-queue-rollback.cjs /usr/src/app/server/dist/services \
    && node /opt/anas/fix-revoked-sockets.cjs /usr/src/app/server/dist \
    && node /opt/anas/guard-credential-races.cjs /usr/src/app/server/dist/services
