ARG GO_BUILDER_REGISTRY=docker.io

FROM --platform=$BUILDPLATFORM ${GO_BUILDER_REGISTRY}/library/golang:1.26-alpine AS controller-build
WORKDIR /src
# The controller compiles against the repository's shared compute client, which
# lives outside this image's own Docker context. `shared` is a named build
# context pointing at the repository root (declared as shared_paths in
# .github/images.json and as additional_contexts in the Compose file), so the
# import resolves against the working tree rather than a published module.
# GOFLAGS/GOPROXY make that explicit: a build that reaches the network fails
# here instead of silently compiling someone else's copy of the code.
COPY --from=shared go.mod go.sum ./
COPY --from=shared internal/computeclient ./internal/computeclient
COPY *.go ./modules/forgejo/actions-controller/
ARG TARGETOS
ARG TARGETARCH
ENV GOFLAGS=-mod=mod GOPROXY=off
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/anas-forgejo-actions-controller ./modules/forgejo/actions-controller

FROM --platform=$BUILDPLATFORM ${GO_BUILDER_REGISTRY}/library/golang:1.26-alpine AS incus-build
ARG TARGETOS
ARG TARGETARCH
RUN GOBIN=/out CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go install github.com/lxc/incus/v7/cmd/incus@v7.3.0

FROM alpine:3.22.1
RUN apk add --no-cache ca-certificates \
    && addgroup -g 65532 -S controller \
    && adduser -u 65532 -S -D -H -G controller controller \
    && install -d -o 65532 -g 65532 -m 0700 /var/lib/anas-actions
COPY --from=controller-build /out/anas-forgejo-actions-controller /usr/local/bin/anas-forgejo-actions-controller
COPY --from=incus-build /out/incus /usr/local/bin/incus
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/anas-forgejo-actions-controller"]
