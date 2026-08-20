ARG GHCR_REGISTRY=ghcr.io
FROM ${GHCR_REGISTRY}/ylianst/meshcentral:1.2.4

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
    passport@0.7.0 \
    connect-flash@0.1.1 \
    openid-client@5.7.1 \
    mysql2@3.15.1 \
    pg@8.16.3; \
  npm cache clean --force

COPY ./config.base.json /opt/anas/config.base.json
COPY ./configure.js /opt/anas/configure.js
COPY ./wait-for-oidc.js /opt/anas/wait-for-oidc.js
COPY ./enforce-oidc-only.js /opt/anas/enforce-oidc-only.js
COPY ./startup.sh ./startup.sh

# MeshCentral's showPasswordLogin setting hides the form but does not disable
# its password authenticators. Patch the pinned upstream source so the
# generated OIDC-only configuration is also enforced server-side.
RUN node /opt/anas/enforce-oidc-only.js /opt/meshcentral/meshcentral/webserver.js

ENTRYPOINT ["/bin/bash", "/opt/meshcentral/startup.sh"]
