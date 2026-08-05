FROM ghcr.io/linuxserver/baseimage-ubuntu:focal
LABEL maintainer="Hailong"

ENV DEBIAN_FRONTEND noninteractive
ARG CHINESE_SPEEDUP
ARG APT_MIRROR_URL

RUN if [ "$CHINESE_SPEEDUP" = "true" ] && [ -n "$APT_MIRROR_URL" ] ; then  \
  sed -i "s|http://archive.ubuntu.com|${APT_MIRROR_URL%/}|g" /etc/apt/sources.list; \
  fi
RUN apt-get update \
  && apt-get install -y \
  samba \
  smbclient \
  krb5-kdc \
  bind9 \
  dnsutils \
  sshfs \
  gettext \
  netcat \
  && apt-get clean \
  && rm -rf \
  /tmp/* \
  /var/lib/apt/lists/* \
  /var/tmp/* \
  /var/log/*

COPY root/ /

EXPOSE 53 53/udp
