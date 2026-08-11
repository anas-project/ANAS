ARG DOCKER_HUB_REGISTRY=docker.io
FROM ${DOCKER_HUB_REGISTRY}/library/traefik:v3.7.10

COPY --chmod=0755 anas-entrypoint.sh /usr/local/bin/anas-entrypoint

ENTRYPOINT ["/usr/local/bin/anas-entrypoint"]
