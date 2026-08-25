ARG DOCKER_HUB_REGISTRY=docker.io
FROM ${DOCKER_HUB_REGISTRY}/library/ubuntu:resolute AS rootfs

ENV DEBIAN_FRONTEND=noninteractive

ARG CHINESE_BUILD_SPEEDUP
ARG APT_MIRROR_URL
RUN bootstrap_mirror=""; \
  if [ "$CHINESE_BUILD_SPEEDUP" = "true" ] && [ -n "$APT_MIRROR_URL" ]; then \
    bootstrap_mirror="$APT_MIRROR_URL"; \
    case "$bootstrap_mirror" in https://*) bootstrap_mirror="http://${bootstrap_mirror#https://}";; esac; \
  fi; \
  if [ -n "$bootstrap_mirror" ]; then \
    for source in /etc/apt/sources.list /etc/apt/sources.list.d/*.sources; do \
      if [ -f "$source" ]; then \
        sed -i \
          -e "s|http://archive.ubuntu.com|${bootstrap_mirror%/}|g" \
          -e "s|http://security.ubuntu.com|${bootstrap_mirror%/}|g" \
          "$source"; \
      fi; \
    done; \
  fi; \
  apt-get update; \
  apt-get install -y --no-install-recommends ca-certificates; \
  rm -rf /var/lib/apt/lists/*; \
  if [ -n "$bootstrap_mirror" ] && [ "$bootstrap_mirror" != "${APT_MIRROR_URL%/}" ]; then \
    sed -i "s|${bootstrap_mirror%/}|${APT_MIRROR_URL%/}|g" /etc/apt/sources.list /etc/apt/sources.list.d/*.sources 2>/dev/null || true; \
  fi

RUN if [ "$CHINESE_BUILD_SPEEDUP" = "true" ] && [ -n "$APT_MIRROR_URL" ] ; then \
  for source in /etc/apt/sources.list /etc/apt/sources.list.d/*.sources; do \
    if [ -f "$source" ]; then \
      sed -i \
        -e "s|http://archive.ubuntu.com|${APT_MIRROR_URL%/}|g" \
        -e "s|http://security.ubuntu.com|${APT_MIRROR_URL%/}|g" \
        "$source"; \
    fi; \
  done; \
  fi

RUN apt-get update \
  && apt-get install -y \
  samba \
  samba-ad-dc \
  samba-ad-provision \
  samba-dsdb-modules \
  smbclient \
  winbind \
  libpam-winbind \
  libnss-winbind \
  krb5-kdc \
  libpam-krb5 \
  bind9 \
  bind9-utils \
  iproute2 \
  dnsutils \
  ldb-tools \
  gettext \
  netcat-openbsd \
  procps \
  runit \
  tini \
  tzdata \
  util-linux \
  && apt-get clean \
  && rm -rf \
  /tmp/* \
  /var/lib/apt/lists/* \
  /var/tmp/* \
  /var/log/*

COPY root/ /

RUN chmod 0755 \
  /etc \
  /etc/bind \
  /etc/cont-init.d \
  /etc/services.d \
  /etc/services.d/named \
  /etc/services.d/samba_dc \
  /usr \
  /usr/bin \
  /usr/local \
  /usr/local/bin \
  /var \
  /var/cache \
  /var/cache/bind-source \
  && chmod +x \
  /usr/bin/with-contenv \
  /etc/services.d/samba_dc/run \
  /etc/services.d/named/run \
  /usr/local/bin/anas-entrypoint.sh \
  /usr/local/bin/install-identity-schema.sh \
  /usr/local/bin/structure.sh \
  /usr/local/bin/anas_zone.sh \
  /etc/cont-init.d/11-samba_dc.sh \
  /etc/cont-init.d/12-bind.sh

FROM scratch

LABEL maintainer="Hailong"
ENV DEBIAN_FRONTEND=noninteractive

COPY --from=rootfs / /

VOLUME /var/lib/samba
EXPOSE 53 53/udp 88 88/udp 135 137-138/udp 139 389 389/udp 445 464 464/udp 636 3268-3269

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/anas-entrypoint.sh"]
