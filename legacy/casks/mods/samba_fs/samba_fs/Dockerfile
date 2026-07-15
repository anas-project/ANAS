FROM ghcr.io/linuxserver/baseimage-ubuntu:jammy
LABEL maintainer="Hailong"

ENV DEBIAN_FRONTEND noninteractive

ARG CHINESE_SPEEDUP
RUN if [ "$CHINESE_SPEEDUP" = "true" ] ; then  \
  sed -i "s/archive.ubuntu.com/mirrors.aliyun.com/g" /etc/apt/sources.list; \
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
  && apt-get clean \
  && rm -rf \
  /tmp/* \
  /var/lib/apt/lists/* \
  /var/tmp/* \
  /var/log/* \
  && sed -i 's/#enable-dbus=.*/enable-dbus=no/g' /etc/avahi/avahi-daemon.conf \
  && rm -vf /etc/avahi/services/*

COPY root/ /

EXPOSE 139 445


# HEALTHCHECK CMD ["/container/scripts/docker-healthcheck.sh"]