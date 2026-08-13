ARG DOCKER_HUB_REGISTRY=docker.io
FROM ${DOCKER_HUB_REGISTRY}/netbirdio/management:0.76.1

ARG CHINESE_SPEEDUP=false
ARG APT_MIRROR_URL

COPY --chmod=0755 root/ /
RUN if [ "$CHINESE_SPEEDUP" = "true" ] && [ -n "$APT_MIRROR_URL" ]; then \
    for sources in /etc/apt/sources.list /etc/apt/sources.list.d/*.sources; do \
      [ -f "$sources" ] || continue; \
      sed -i \
        -e "s|http://deb.debian.org|${APT_MIRROR_URL%/}|g" \
        -e "s|https://deb.debian.org|${APT_MIRROR_URL%/}|g" \
        -e "s|http://security.debian.org|${APT_MIRROR_URL%/}|g" \
        "$sources"; \
    done; \
  fi \
  && apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl gettext-base iputils-ping jq \
  && rm -rf /var/lib/apt/lists/*

ENTRYPOINT ["/root/start.sh"]
