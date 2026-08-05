FROM ghcr.io/linuxserver/baseimage-ubuntu:noble
LABEL maintainer="Hailong"

ENV DEBIAN_FRONTEND noninteractive

ARG CHINESE_SPEEDUP
ARG APT_MIRROR_URL
RUN if [ "$CHINESE_SPEEDUP" = "true" ] && [ -n "$APT_MIRROR_URL" ] ; then  \
  sed -i "s|http://archive.ubuntu.com|${APT_MIRROR_URL%/}|g" /etc/apt/sources.list; \
  fi

COPY root/ /

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
  netcat-openbsd \
  && apt-get clean \
  && rm -rf \
  /tmp/* \
  /var/lib/apt/lists/* \
  /var/tmp/* \
  /var/log/* \
  && chmod +x /etc/services.d/samba_dc/run  \
  /etc/services.d/sshd/run \ 
  /usr/local/bin/structure.sh \
  /usr/local/bin/anas_zone.sh \
  /etc/cont-init.d/11-samba_dc.sh \
  /etc/cont-init.d/12-ssh.sh

VOLUME /var/lib/samba
EXPOSE 88 88/udp 135 137-138/udp 139 389 389/udp 445 464 464/udp 636 3268-3269 49152-65535 \
  2049
