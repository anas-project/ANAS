ARG GHCR_REGISTRY=ghcr.io
FROM ${GHCR_REGISTRY}/ylianst/meshcentral:1.2.4

ARG CHINESE_SPEEDUP=false
ARG NPM_REGISTRY_URL=https://registry.npmjs.org
ENV NODE_PATH=/opt/meshcentral/ldap-deps/node_modules

# MeshCentral installs optional authentication providers at runtime when they
# are missing. The isolated runtime has no npm access, so an LDAP-enabled
# container otherwise exits after the two-minute npm timeout and restart-loops.
RUN set -eux; \
  npm install \
    --prefix=/opt/meshcentral/ldap-deps \
    --no-audit \
    --no-fund \
    --no-package-lock \
    --no-save \
    --registry="$NPM_REGISTRY_URL" \
    ldapauth-fork@5.0.5 \
    mysql2@3.15.1; \
  npm cache clean --force

COPY ./config.base.json /opt/anas/config.base.json
COPY ./configure.js /opt/anas/configure.js
COPY ./startup.sh ./startup.sh

ENTRYPOINT ["/bin/bash", "/opt/meshcentral/startup.sh"]
