FROM ghcr.io/ylianst/meshcentral:1.1.5

ARG CHINESE_SPEEDUP=false
ENV NODE_PATH=/opt/meshcentral/ldap-deps/node_modules

# MeshCentral installs optional authentication providers at runtime when they
# are missing. The isolated runtime has no npm access, so an LDAP-enabled
# container otherwise exits after the two-minute npm timeout and restart-loops.
RUN set -eux; \
  registry="https://registry.npmjs.org"; \
  if [ "$CHINESE_SPEEDUP" = "true" ]; then \
    registry="https://registry.npmmirror.com"; \
  fi; \
  npm install \
    --prefix=/opt/meshcentral/ldap-deps \
    --no-audit \
    --no-fund \
    --no-package-lock \
    --no-save \
    --registry="$registry" \
    ldapauth-fork@6.1.0 \
    mysql@2.18.1; \
  npm cache clean --force

COPY ./config.base.json /opt/anas/config.base.json
COPY ./configure.js /opt/anas/configure.js
COPY ./startup.sh ./startup.sh
