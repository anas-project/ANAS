ARG DOCKER_HUB_REGISTRY=docker.io
FROM ${DOCKER_HUB_REGISTRY}/library/nextcloud:34.0.2-apache

ARG CHINESE_SPEEDUP=false
ARG APT_MIRROR_URL=""
ARG NEXTCLOUD_APT_MIRROR_URL=""
RUN set -eu; \
  if [ -n "$NEXTCLOUD_APT_MIRROR_URL" ]; then \
    sed -i -E "s|https?://deb.debian.org|${NEXTCLOUD_APT_MIRROR_URL%/}|g" /etc/apt/sources.list.d/debian.sources; \
  elif [ "$CHINESE_SPEEDUP" = "true" ] && [ -n "$APT_MIRROR_URL" ]; then \
    sed -i -E "s|https?://deb.debian.org|${APT_MIRROR_URL%/}|g" /etc/apt/sources.list.d/debian.sources; \
  fi; \
  apt-get -o Acquire::ForceIPv4=true -o Acquire::Retries=3 -o Acquire::http::Timeout=30 update; \
  apt-get -o Acquire::ForceIPv4=true -o Acquire::Retries=3 -o Acquire::http::Timeout=30 install -y --no-install-recommends \
    ffmpeg \
    iputils-ping \
    jq \
    netcat-openbsd; \
  rm -rf /var/lib/apt/lists/*

COPY --chmod=0755 root/usr/local/bin/ /usr/local/bin/

ENTRYPOINT ["/usr/local/bin/anas-entrypoint.sh"]
CMD ["apache2-foreground"]
