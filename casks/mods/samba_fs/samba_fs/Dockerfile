ARG DOCKER_HUB_REGISTRY=docker.io
FROM ${DOCKER_HUB_REGISTRY}/library/ubuntu:resolute
LABEL maintainer="Hailong"

ENV DEBIAN_FRONTEND noninteractive

RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates \
  && rm -rf /var/lib/apt/lists/*

ARG CHINESE_SPEEDUP
ARG APT_MIRROR_URL
RUN if [ "$CHINESE_SPEEDUP" = "true" ] && [ -n "$APT_MIRROR_URL" ] ; then \
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
  avahi-daemon \
  samba \
  smbclient \
  winbind \
  libpam-winbind \
  libnss-winbind \
  krb5-kdc \
  libpam-krb5 \
  ldb-tools \
  gettext \
  iproute2 \
  wsdd2 \
  acl \
  runit \
  tini \
  tzdata \
  util-linux \
  && apt-get clean \
  && rm -rf \
  /tmp/* \
  /var/lib/apt/lists/* \
  /var/tmp/* \
  /var/log/* \
  && sed -i 's/#enable-dbus=.*/enable-dbus=no/g' /etc/avahi/avahi-daemon.conf \
  && rm -vf /etc/avahi/services/*

COPY root/ /

RUN chmod 0755 \
  /usr/bin/with-contenv \
  /etc/cont-init.d/*.sh \
  /etc/services.d/*/run \
  /usr/local/bin/*.sh

EXPOSE 139 445

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/anas-entrypoint.sh"]


# HEALTHCHECK CMD ["/container/scripts/docker-healthcheck.sh"]
