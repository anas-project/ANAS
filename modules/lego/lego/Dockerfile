ARG DOCKER_HUB_REGISTRY=docker.io
FROM ${DOCKER_HUB_REGISTRY}/goacme/lego:v5.3.1

ARG CHINESE_BUILD_SPEEDUP
ARG APK_MIRROR_URL
# openssl backs the internal CA; the upstream image only ships the lego binary.
RUN if [ "$CHINESE_BUILD_SPEEDUP" = "true" ] && [ -n "$APK_MIRROR_URL" ]; then \
    sed -i "s|https://dl-cdn.alpinelinux.org|${APK_MIRROR_URL%/}|g" /etc/apk/repositories; \
  fi; \
  apk --update --no-cache add openssl

WORKDIR /certs
COPY cert.sh run.sh ca.sh /root/
# Weekly ACME renewal, daily internal-certificate check. The internal check is
# cheap and has to notice a domain change promptly, since every service URL is
# derived from it.
RUN printf '0 3 * * 6 /root/cert.sh > /dev/stdout 2>&1\n0 4 * * * /root/ca.sh renew > /dev/stdout 2>&1\n' > /etc/crontabs/root \
  && chmod +x /root/run.sh /root/cert.sh /root/ca.sh

ENTRYPOINT [ "/root/run.sh" ]
