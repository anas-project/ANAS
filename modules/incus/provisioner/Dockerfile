ARG GO_BUILDER_REGISTRY=docker.io

FROM --platform=$BUILDPLATFORM ${GO_BUILDER_REGISTRY}/library/golang:1.26-alpine AS build
WORKDIR /src
# The provisioner shares the contract's fixed names with the consumer-side
# client, so both sides cannot drift apart on what a lease's profile and
# network are called. `shared` is a named build context pointing at the
# repository root (see shared_paths in .github/images.json); GOPROXY=off makes
# a build that reaches the network fail here rather than silently compile a
# published copy of the shared package.
COPY --from=shared go.mod go.sum ./
COPY --from=shared internal/computeclient ./internal/computeclient
COPY *.go ./modules/incus/provisioner/
ARG TARGETOS
ARG TARGETARCH
ENV GOFLAGS=-mod=mod GOPROXY=off
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/anas-incus-provisioner ./modules/incus/provisioner

FROM alpine:3.22.1
RUN apk add --no-cache ca-certificates \
    && addgroup -g 65532 -S provisioner \
    && adduser -u 65532 -S -D -H -G provisioner provisioner
COPY --from=build /out/anas-incus-provisioner /usr/local/bin/anas-incus-provisioner
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/anas-incus-provisioner"]
