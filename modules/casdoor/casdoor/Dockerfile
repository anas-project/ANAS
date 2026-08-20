ARG DOCKER_HUB_REGISTRY=docker.io
FROM ${DOCKER_HUB_REGISTRY}/library/golang:1.25.8-alpine AS helper
WORKDIR /src
COPY helper/go.mod helper/go.sum ./
RUN go mod download
COPY helper/*.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/casdoor-helper .

FROM ${DOCKER_HUB_REGISTRY}/casbin/casdoor:3.143.0
USER root
COPY --from=helper /out/casdoor-helper /opt/anas/bin/casdoor-helper
COPY entrypoint.sh /opt/anas/bin/casdoor-entrypoint
RUN chmod 0755 /opt/anas/bin/casdoor-helper /opt/anas/bin/casdoor-entrypoint
ENTRYPOINT ["/opt/anas/bin/casdoor-entrypoint"]
CMD ["/server"]
