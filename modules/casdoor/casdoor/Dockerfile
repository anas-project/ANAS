ARG DOCKER_HUB_REGISTRY=docker.io
FROM ${DOCKER_HUB_REGISTRY}/library/golang:1.25.8-alpine AS casdoor-server
ARG CHINESE_BUILD_SPEEDUP=false
ARG BUILD_GITHUB_DOWNLOAD_PROXY_PREFIX
ARG GOPROXY_URL
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG CASDOOR_SOURCE_URL=https://github.com/casdoor/casdoor/archive/1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e.tar.gz
ARG CASDOOR_SOURCE_SHA256=365d61c7e8cae30a6b1a135204c74145c9ce6c692068d3fc044404703c0f9460
WORKDIR /src/casdoor
RUN apk add --no-cache ca-certificates curl patch tar && \
    source_url="$CASDOOR_SOURCE_URL" && \
    if [ "$CHINESE_BUILD_SPEEDUP" = "true" ] && [ -n "$BUILD_GITHUB_DOWNLOAD_PROXY_PREFIX" ]; then \
      source_url="${BUILD_GITHUB_DOWNLOAD_PROXY_PREFIX%/}/${source_url#https://}"; \
    fi && \
    curl -fL --retry 3 "$source_url" -o /tmp/casdoor.tar.gz && \
    echo "$CASDOOR_SOURCE_SHA256  /tmp/casdoor.tar.gz" | sha256sum -c - && \
    tar -xzf /tmp/casdoor.tar.gz --strip-components=1 && \
    rm /tmp/casdoor.tar.gz
COPY patches/0001-saml-directory-attributes.patch /tmp/0001-saml-directory-attributes.patch
RUN patch -p1 < /tmp/0001-saml-directory-attributes.patch && \
    if [ -n "$GOPROXY_URL" ]; then go env -w GOPROXY="$GOPROXY_URL"; fi && \
    go mod download && \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
      go build -trimpath -ldflags='-s -w' -o /out/server .

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
COPY --from=casdoor-server /out/server /server
COPY --from=helper /out/casdoor-helper /opt/anas/bin/casdoor-helper
COPY entrypoint.sh /opt/anas/bin/casdoor-entrypoint
# A container owns its process namespace, so Casdoor must not search for and
# kill a same-port "old instance". In the bootstrap child that lookup resolves
# to the child itself and exits it with SIGKILL before init data is committed.
RUN rm -f /usr/bin/lsof && \
    chmod 0755 /opt/anas/bin/casdoor-helper /opt/anas/bin/casdoor-entrypoint
ENTRYPOINT ["/opt/anas/bin/casdoor-entrypoint"]
CMD ["/server"]
