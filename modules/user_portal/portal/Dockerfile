ARG DOCKER_HUB_REGISTRY=docker.io
FROM --platform=$BUILDPLATFORM ${DOCKER_HUB_REGISTRY}/library/golang:1.26-alpine AS build
WORKDIR /src
COPY --from=shared go.mod go.sum ./
COPY --from=shared internal/appcatalog ./internal/appcatalog
COPY --from=shared internal/securefs ./internal/securefs
COPY . ./modules/user_portal/portal
ARG TARGETOS
ARG TARGETARCH
ARG GO_MODULE_PROXY=https://proxy.golang.org,direct
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOPROXY="$GO_MODULE_PROXY" go build -trimpath -ldflags="-s -w" -o /out/anas-user-portal ./modules/user_portal/portal
FROM ${DOCKER_HUB_REGISTRY}/library/alpine:3.22.1
ARG CHINESE_BUILD_SPEEDUP=false
ARG APK_MIRROR_URL
RUN if [ "$CHINESE_BUILD_SPEEDUP" = "true" ] && [ -n "$APK_MIRROR_URL" ]; then \
      sed -i "s|https://dl-cdn.alpinelinux.org|${APK_MIRROR_URL%/}|g" /etc/apk/repositories; \
    fi && \
    apk add --no-cache ca-certificates && mkdir -p /var/lib/anas-user-portal
COPY --from=build /out/anas-user-portal /usr/local/bin/anas-user-portal
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/anas-user-portal"]
