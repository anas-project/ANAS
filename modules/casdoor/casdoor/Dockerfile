ARG DOCKER_HUB_REGISTRY=docker.io
FROM ${DOCKER_HUB_REGISTRY}/library/golang:1.25.8-alpine AS helper
ARG GOPROXY_URL
WORKDIR /src
COPY helper/go.mod helper/go.sum ./
RUN if [ -n "$GOPROXY_URL" ]; then go env -w GOPROXY="$GOPROXY_URL"; fi && \
    go mod download
COPY helper/*.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/casdoor-helper .

FROM ${DOCKER_HUB_REGISTRY}/casbin/casdoor:3.143.0
USER root
COPY --from=helper /out/casdoor-helper /opt/anas/bin/casdoor-helper
COPY entrypoint.sh /opt/anas/bin/casdoor-entrypoint
# A container owns its process namespace, so Casdoor must not search for and
# kill a same-port "old instance". In the bootstrap child that lookup resolves
# to the child itself and exits it with SIGKILL before init data is committed.
RUN rm -f /usr/bin/lsof && \
    chmod 0755 /opt/anas/bin/casdoor-helper /opt/anas/bin/casdoor-entrypoint
ENTRYPOINT ["/opt/anas/bin/casdoor-entrypoint"]
CMD ["/server"]
