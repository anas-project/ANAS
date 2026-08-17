ARG LLNG_DOCKER_HUB_REGISTRY=docker.io
FROM ${LLNG_DOCKER_HUB_REGISTRY}/lemonldapng/lemonldap-ng:2.23.2

LABEL maintainer="Hailong"

ARG CHINESE_BUILD_SPEEDUP=false
ARG APT_MIRROR_URL

RUN if [ "$CHINESE_BUILD_SPEEDUP" = "true" ] && [ -n "$APT_MIRROR_URL" ]; then \
    for source in /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources; do \
      if [ -f "$source" ]; then \
        sed -i \
          -e "s|http://deb.debian.org|${APT_MIRROR_URL%/}|g" \
          -e "s|https://deb.debian.org|${APT_MIRROR_URL%/}|g" \
          -e "s|http://security.debian.org|${APT_MIRROR_URL%/}|g" \
          "$source"; \
      fi; \
    done; \
  fi \
  && apt-get update \
  && apt-get install -y --no-install-recommends \
    default-mysql-client \
    gettext-base \
    iputils-ping \
    jq \
    netcat-openbsd \
    postgresql-client \
  && rm -rf /var/lib/apt/lists/*

COPY --chmod=0755 enable-chinese-language.sh /usr/local/bin/anas-llng-enable-chinese-language

RUN /usr/local/bin/anas-llng-enable-chinese-language

# The upstream image declares four mutable runtime directories as Docker
# volumes. Compose mounts tmpfs over them so Docker does not allocate anonymous
# volumes. Preserve their image defaults outside those volume paths; the ANAS
# entrypoint restores them into the empty tmpfs mounts on every start.
RUN mkdir -p \
      /opt/anas/runtime-seed/etc-lemonldap-ng \
      /opt/anas/runtime-seed/etc-nginx-sites-enabled \
  && cp -a /etc/lemonldap-ng/. \
      /opt/anas/runtime-seed/etc-lemonldap-ng/ \
  && cp -a /etc/nginx/sites-enabled/. \
      /opt/anas/runtime-seed/etc-nginx-sites-enabled/

COPY --chmod=0755 anas-entrypoint.sh /usr/local/bin/anas-entrypoint.sh
COPY --chmod=0755 restore-runtime.sh /usr/local/bin/anas-llng-restore-runtime
COPY --chmod=0755 root/root/llng-config.sh /usr/local/bin/anas-configure-clients.sh
COPY root/root/lmConf.json /opt/anas/lmConf.json
COPY root/root/mysql_init.sql /opt/anas/mysql_init.sql
COPY root/root/postgre_init.sql /opt/anas/postgre_init.sql

ENTRYPOINT ["dumb-init", "--", "/usr/local/bin/anas-entrypoint.sh"]
