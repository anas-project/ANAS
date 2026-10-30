ARG LLNG_DOCKER_HUB_REGISTRY=docker.io
FROM ${LLNG_DOCKER_HUB_REGISTRY}/lemonldapng/lemonldap-ng:2.23.4

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
    libauthen-webauthn-perl \
    libdbd-pg-perl \
    netcat-openbsd \
    postgresql-client \
  && rm -rf /var/lib/apt/lists/*

COPY --chmod=0755 enable-chinese-language.sh /usr/local/bin/anas-llng-enable-chinese-language
COPY --chmod=0755 configure-password-guidance.sh /usr/local/bin/anas-llng-configure-password-guidance

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
COPY --chmod=0644 root/root/lmConf.json /opt/anas/lmConf.json
COPY --chmod=0644 authentication.jq /opt/anas/authentication.jq
COPY theme/ /usr/share/lemonldap-ng/portal/htdocs/static/anas/
COPY --chmod=0644 AnasOIDC.pm /usr/share/perl5/Lemonldap/NG/Portal/Plugins/AnasOIDC.pm
COPY --chmod=0644 AnasDirectory.pm /usr/share/perl5/Lemonldap/NG/Portal/Plugins/AnasDirectory.pm
COPY --chmod=0644 DirectoryJournal.pm DirectoryWatch.pm DirectoryRevoke.pm /opt/anas/
COPY --chmod=0755 directory-watch.pl /opt/anas/directory-watch.pl
COPY --chmod=0644 root/root/mysql_init.sql /opt/anas/mysql_init.sql
COPY --chmod=0644 root/root/postgre_init.sql /opt/anas/postgre_init.sql

# Private source bundles may use umask 077. Public runtime code/assets must
# still be readable by the upstream www-data workers and directory watcher.
RUN find /usr/share/lemonldap-ng/portal/htdocs/static/anas -type d -exec chmod 0755 {} + \
  && find /usr/share/lemonldap-ng/portal/htdocs/static/anas -type f -exec chmod 0644 {} +

ENTRYPOINT ["dumb-init", "--", "/usr/local/bin/anas-entrypoint.sh"]
