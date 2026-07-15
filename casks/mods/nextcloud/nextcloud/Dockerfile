FROM crazymax/nextcloud:30.0.1
LABEL maintainer="Hailong"

ARG CHINESE_SPEEDUP
RUN if [ "$CHINESE_SPEEDUP" = "true" ]; then \
    sed -i 's|dl-cdn.alpinelinux.org|mirrors.aliyun.com|g' /etc/apk/repositories; \
  fi; \
  apk --update --no-cache add \
  jq \
  && rm -rf /tmp/*

COPY root/ /
