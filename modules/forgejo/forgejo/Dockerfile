ARG FORGEJO_VERSION=15.0.7
ARG FORGEJO_REGISTRY=codeberg.org
ARG GO_BUILDER_REGISTRY=docker.io

FROM --platform=$BUILDPLATFORM ${GO_BUILDER_REGISTRY}/library/golang:1.26-alpine AS helper
WORKDIR /src
COPY entrypoint.go ./
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /anas-forgejo-entrypoint ./entrypoint.go

FROM ${FORGEJO_REGISTRY}/forgejo/forgejo:${FORGEJO_VERSION}-rootless
COPY --from=helper /anas-forgejo-entrypoint /usr/local/bin/anas-forgejo-entrypoint

# A new bind mount is root-owned. The wrapper prepares the persistent root and
# permanently drops to Forgejo's upstream uid/gid before invoking its entrypoint.
USER root
ENTRYPOINT ["/usr/local/bin/anas-forgejo-entrypoint"]
CMD ["/usr/local/bin/forgejo", "web"]
