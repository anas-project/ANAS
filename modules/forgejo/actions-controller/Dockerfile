ARG DOCKER_HUB_REGISTRY=docker.io
ARG GO_BUILDER_REGISTRY=${DOCKER_HUB_REGISTRY}

FROM --platform=$BUILDPLATFORM ${GO_BUILDER_REGISTRY}/library/golang:1.26.10-alpine AS controller-build
WORKDIR /src
# The controller compiles against the repository's shared compute client, which
# lives outside this image's own Docker context. `shared` is a named build
# context pointing at the repository root (declared as shared_paths in
# .github/images.json and as additional_contexts in the Compose file), so the
# import resolves against the working tree rather than a published module.
# GOFLAGS/GOPROXY make that explicit: a build that reaches the network fails
# here instead of silently compiling someone else's copy of the code.
COPY --from=shared go.mod go.sum ./
# Fetch the checksum-pinned third-party dependency before offline compilation.
# Repository packages below still come exclusively from the named context.
ARG GO_MODULE_PROXY=https://proxy.golang.org,direct
RUN GOPROXY="$GO_MODULE_PROXY" go mod download golang.org/x/sys
COPY --from=shared internal/computeclient ./internal/computeclient
COPY --from=shared internal/securefs ./internal/securefs
COPY --from=shared internal/computeingress ./internal/computeingress
COPY *.go ./modules/forgejo/actions-controller/
ARG TARGETOS
ARG TARGETARCH
ENV GOFLAGS=-mod=mod GOPROXY=off
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/anas-forgejo-actions-controller ./modules/forgejo/actions-controller

FROM --platform=$BUILDPLATFORM ${GO_BUILDER_REGISTRY}/library/golang:1.26.10-alpine AS incus-build
ARG TARGETOS
ARG TARGETARCH
# The pinned upstream CLI also uses this operator-selected module proxy.
# The controller's shared repository compilation stays GOPROXY=off.
# Keep Go's default checksum verification; never pass credentials here.
ARG GO_MODULE_PROXY=https://proxy.golang.org,direct
WORKDIR /incus-cli
# Keep the upstream CLI version while applying its HTTP/SSH dependency fixes.
# A separate module prevents these build-tool dependencies entering ANAS.
RUN export GOPROXY="$GO_MODULE_PROXY" \
    && go mod init anas.local/incus-cli-build \
    && go get github.com/lxc/incus/v7/cmd/incus@v7.3.0 \
        golang.org/x/net@v0.60.0 golang.org/x/crypto@v0.57.0 \
    && GOBIN=/out CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
        go install github.com/lxc/incus/v7/cmd/incus

FROM ${DOCKER_HUB_REGISTRY}/library/alpine:3.22.1
RUN apk add --no-cache ca-certificates \
    && addgroup -g 65532 -S controller \
    && adduser -u 65532 -S -D -H -G controller controller \
    && install -d -o 65532 -g 65532 -m 0700 /var/lib/anas-actions
COPY --from=controller-build /out/anas-forgejo-actions-controller /usr/local/bin/anas-forgejo-actions-controller
COPY --from=incus-build /out/incus /usr/local/bin/incus
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/anas-forgejo-actions-controller"]
