ARG DOCKER_HUB_REGISTRY=docker.io
FROM ${DOCKER_HUB_REGISTRY}/library/debian:trixie-slim

ARG LAM_VERSION=9.6
ARG LAM_DEB_SHA256=d44ccf15e4428becb150c2372e00f4f455053751458674c2d5fb7826156fc5e2
ARG CHINESE_SPEEDUP=false
ARG APT_MIRROR_URL
ARG LAM_APT_MIRROR_URL
ARG LAM_DOWNLOAD_URL
ARG GITHUB_DOWNLOAD_PROXY_PREFIX

ENV DEBIAN_FRONTEND=noninteractive

# Bootstrap certificates over the selected mirror's HTTP endpoint, then put
# the original source definition back so the regular mirror switch below uses
# the configured HTTPS URL. Debian verifies signed repository metadata during
# this bootstrap even though transport TLS is not available yet.
RUN cp /etc/apt/sources.list.d/debian.sources /tmp/debian.sources \
  && bootstrap_mirror="" \
  && if [ -n "$LAM_APT_MIRROR_URL" ]; then bootstrap_mirror="$LAM_APT_MIRROR_URL"; \
     elif [ "$CHINESE_SPEEDUP" = "true" ] && [ -n "$APT_MIRROR_URL" ]; then bootstrap_mirror="$APT_MIRROR_URL"; fi \
  && case "$bootstrap_mirror" in https://*) bootstrap_mirror="http://${bootstrap_mirror#https://}";; esac \
  && if [ -n "$bootstrap_mirror" ]; then sed -i \
       -e "s|http://deb.debian.org|${bootstrap_mirror%/}|g" \
       -e "s|https://deb.debian.org|${bootstrap_mirror%/}|g" \
       -e "s|http://security.debian.org|${bootstrap_mirror%/}|g" \
       /etc/apt/sources.list.d/debian.sources; fi \
  && apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates \
  && rm -rf /var/lib/apt/lists/* \
  && mv /tmp/debian.sources /etc/apt/sources.list.d/debian.sources

RUN if [ -n "$LAM_APT_MIRROR_URL" ]; then \
    sed -i \
      -e "s|http://deb.debian.org|${LAM_APT_MIRROR_URL%/}|g" \
      -e "s|https://deb.debian.org|${LAM_APT_MIRROR_URL%/}|g" \
      -e "s|http://security.debian.org|${LAM_APT_MIRROR_URL%/}|g" \
      /etc/apt/sources.list.d/debian.sources; \
  elif [ "$CHINESE_SPEEDUP" = "true" ] && [ -n "$APT_MIRROR_URL" ]; then \
    sed -i \
      -e "s|http://deb.debian.org|${APT_MIRROR_URL%/}|g" \
      -e "s|https://deb.debian.org|${APT_MIRROR_URL%/}|g" \
      -e "s|http://security.debian.org|${APT_MIRROR_URL%/}|g" \
      /etc/apt/sources.list.d/debian.sources; \
  fi \
  && apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl dumb-init locales \
  && sed -i \
    -e 's/^# *\(en_US.UTF-8\)/\1/' \
    -e 's/^# *\(zh_CN.UTF-8\)/\1/' \
    /etc/locale.gen \
  && locale-gen

RUN download_url="https://github.com/LDAPAccountManager/lam/releases/download/${LAM_VERSION}/ldap-account-manager_${LAM_VERSION}-1_all.deb" \
  && if [ -n "$LAM_DOWNLOAD_URL" ]; then download_url="$LAM_DOWNLOAD_URL"; elif [ "$CHINESE_SPEEDUP" = "true" ] && [ -n "$GITHUB_DOWNLOAD_PROXY_PREFIX" ]; then download_url="${GITHUB_DOWNLOAD_PROXY_PREFIX%/}/${download_url#https://}"; fi \
  && curl --http1.1 -fsSL --retry 5 --retry-delay 2 --retry-all-errors --connect-timeout 30 "$download_url" \
    -o /tmp/lam.deb \
  && echo "${LAM_DEB_SHA256}  /tmp/lam.deb" | sha256sum -c - \
  && apt-get install -y --no-install-recommends /tmp/lam.deb \
  && rm -f /tmp/lam.deb \
  && rm -rf /var/lib/apt/lists/* \
  && rm -f /etc/apache2/sites-enabled/* /etc/apache2/conf-enabled/ldap-account-manager.conf \
  && printf '%s\n' \
    '<VirtualHost *:80>' \
    '  DocumentRoot /usr/share/ldap-account-manager' \
    '  <Directory /usr/share/ldap-account-manager>' \
    '    AllowOverride All' \
    '    Require all granted' \
    '  </Directory>' \
    '  ErrorLog /proc/self/fd/2' \
    '  CustomLog /proc/self/fd/1 combined' \
    '</VirtualHost>' \
    >/etc/apache2/sites-available/lam.conf \
  && ln -s /etc/apache2/sites-available/lam.conf /etc/apache2/sites-enabled/lam.conf \
  && a2enmod rewrite \
  && mkdir -p /var/lib/ldap-account-manager/config \
  && chown -R www-data:www-data /var/lib/ldap-account-manager /etc/ldap-account-manager

COPY configure.php /opt/anas/configure.php
COPY config.sh /usr/local/bin/config.sh
RUN chmod 0755 /usr/local/bin/config.sh

ENTRYPOINT ["/usr/bin/dumb-init", "--"]
CMD ["/usr/local/bin/config.sh"]

HEALTHCHECK --interval=1m --timeout=10s \
  CMD curl -fsS http://localhost/ | grep -q '<title>LDAP Account Manager</title>'
