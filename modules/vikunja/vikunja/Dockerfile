ARG VIKUNJA_VERSION=2.4.0
ARG VIKUNJA_SOURCE_COMMIT=907850feae3866ae9b16ea1c7b84a4d77273415a
ARG VIKUNJA_SOURCE_SHA256=a89e64ffd6e79aad06531ee8f79f0b1b36f648c8d8696efddac9eb23ad6f8e3e
ARG DOCKER_HUB_REGISTRY=docker.io
ARG GO_BUILDER_REGISTRY=docker.io
ARG GHCR_REGISTRY=ghcr.io
ARG CHINESE_BUILD_SPEEDUP=false
ARG APK_MIRROR_URL
ARG NPM_REGISTRY_URL=https://registry.npmjs.org
ARG GOPROXY_URL
ARG BUILD_GITHUB_DOWNLOAD_PROXY_PREFIX

FROM --platform=$BUILDPLATFORM ${DOCKER_HUB_REGISTRY}/library/node:24.18.0-alpine AS frontendbuilder
ARG VIKUNJA_VERSION
ARG VIKUNJA_SOURCE_COMMIT
ARG VIKUNJA_SOURCE_SHA256
ARG CHINESE_BUILD_SPEEDUP
ARG APK_MIRROR_URL
ARG NPM_REGISTRY_URL
ARG BUILD_GITHUB_DOWNLOAD_PROXY_PREFIX
WORKDIR /build
ENV PNPM_CACHE_FOLDER=.cache/pnpm/ \
    PUPPETEER_SKIP_DOWNLOAD=true \
    CYPRESS_INSTALL_BINARY=0
RUN if [ "$CHINESE_BUILD_SPEEDUP" = "true" ] && [ -n "$APK_MIRROR_URL" ]; then \
      sed -i "s|https://dl-cdn.alpinelinux.org|${APK_MIRROR_URL%/}|g" /etc/apk/repositories; \
    fi && \
    apk add --no-cache patch
RUN source_url="https://codeload.github.com/go-vikunja/vikunja/tar.gz/${VIKUNJA_SOURCE_COMMIT}" && \
    if [ "$CHINESE_BUILD_SPEEDUP" = "true" ] && [ -n "$BUILD_GITHUB_DOWNLOAD_PROXY_PREFIX" ]; then \
      source_url="${BUILD_GITHUB_DOWNLOAD_PROXY_PREFIX%/}/${source_url#https://}"; \
    fi && \
    wget -q -O /tmp/vikunja-source.tar.gz "$source_url" && \
    echo "${VIKUNJA_SOURCE_SHA256}  /tmp/vikunja-source.tar.gz" | sha256sum -c - && \
    tar -xzf /tmp/vikunja-source.tar.gz --strip-components=1 && \
    rm /tmp/vikunja-source.tar.gz
COPY patches/0001-logout-local-session-first.patch /tmp/0001-logout-local-session-first.patch
RUN patch -p1 < /tmp/0001-logout-local-session-first.patch
WORKDIR /build/frontend
RUN npm config set registry "$NPM_REGISTRY_URL" && \
    npm install -g pnpm@11.13.1 && \
    pnpm config set registry "$NPM_REGISTRY_URL" && \
    pnpm install --frozen-lockfile
RUN echo "{\"VERSION\": \"${VIKUNJA_VERSION}\"}" > src/version.json && \
    pnpm vitest run src/stores/auth.logout.test.ts && \
    pnpm run build && \
    rm -rf node_modules .cache

FROM --platform=$BUILDPLATFORM ${GHCR_REGISTRY}/techknowlogick/xgo:go-1.26.x AS apibuilder
WORKDIR /go/src/code.vikunja.io/api
COPY --from=frontendbuilder /build ./
ARG GOPROXY_URL
RUN if [ -n "$GOPROXY_URL" ]; then go env -w GOPROXY="$GOPROXY_URL"; fi && \
    go install github.com/magefile/mage@v1.15.0 && \
    mv /go/bin/mage /usr/local/go/bin
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG VIKUNJA_VERSION
ENV RELEASE_VERSION=${VIKUNJA_VERSION}
RUN mage build:clean && \
    (cd build && mage release:xgo vikunja "${TARGETOS}/${TARGETARCH}/${TARGETVARIANT}")
RUN mkdir -p /tmp && chmod 1777 /tmp

FROM --platform=$BUILDPLATFORM ${GO_BUILDER_REGISTRY}/library/golang:1.26-alpine AS entrypoint
WORKDIR /src
COPY entrypoint.go ./
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /anas-vikunja-entrypoint ./entrypoint.go

FROM scratch
LABEL org.opencontainers.image.source="https://github.com/anas-project/ANAS" \
      org.opencontainers.image.upstream-source="https://github.com/go-vikunja/vikunja" \
      org.opencontainers.image.upstream-revision="907850feae3866ae9b16ea1c7b84a4d77273415a" \
      org.opencontainers.image.licenses="AGPL-3.0-or-later"
WORKDIR /app/vikunja
EXPOSE 3456
COPY --from=apibuilder --chown=1000:1000 --chmod=1777 /tmp /tmp
COPY --from=apibuilder /build/vikunja-* /app/vikunja/vikunja
COPY --from=apibuilder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=entrypoint /anas-vikunja-entrypoint /app/vikunja/anas-vikunja-entrypoint
ENV VIKUNJA_SERVICE_ROOTPATH=/app/vikunja/ \
    VIKUNJA_DATABASE_PATH=/db/vikunja.db

# A new attachment bind mount is root-owned. The entrypoint fixes only that
# tree, then permanently drops to the upstream uid/gid before execing Vikunja.
# The scratch runtime has no /etc/passwd, so use a numeric root identity.
USER 0:0
ENTRYPOINT ["/app/vikunja/anas-vikunja-entrypoint"]
