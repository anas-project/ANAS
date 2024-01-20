FROM ghcr.io/linuxserver/baseimage-ubuntu:jammy
LABEL maintainer="Hailong"

ENV DEBIAN_FRONTEND noninteractive

ARG CHINESE_SPEEDUP
RUN if [ "$CHINESE_SPEEDUP" = "true" ] ; then  \
  sed -i "s/archive.ubuntu.com/mirrors.aliyun.com/g" /etc/apt/sources.list; \
  fi
RUN apt-get update \
  && apt-get install -y \
  samba \
  smbclient \
  winbind \
  libpam-winbind \
  libnss-winbind \
  krb5-kdc \
  libpam-krb5 \
  iproute2 \
  dnsutils \
  ldb-tools \
  gettext \
  openssh-server \
  netcat \
  && apt-get clean \
  && rm -rf \
  /tmp/* \
  /var/lib/apt/lists/* \
  /var/tmp/* \
  /var/log/*

COPY root/ /

VOLUME /var/lib/samba
EXPOSE 88 88/udp 135 137-138/udp 139 389 389/udp 445 464 464/udp 636 3268-3269 49152-65535 \
  2049
