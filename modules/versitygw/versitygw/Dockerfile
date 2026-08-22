ARG GHCR_REGISTRY=ghcr.io
FROM ${GHCR_REGISTRY}/versity/versitygw:v1.7.0

RUN apk add --no-cache su-exec

COPY anas-entrypoint.sh /usr/local/bin/anas-entrypoint.sh
RUN chmod 0755 /usr/local/bin/anas-entrypoint.sh

ENTRYPOINT ["/usr/local/bin/anas-entrypoint.sh"]
