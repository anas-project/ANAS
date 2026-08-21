ARG VIKUNJA_VERSION=2.4.0
ARG DOCKER_HUB_REGISTRY=docker.io
ARG GO_BUILDER_REGISTRY=docker.io

FROM --platform=$BUILDPLATFORM ${GO_BUILDER_REGISTRY}/library/golang:1.26-alpine AS entrypoint
WORKDIR /src
COPY entrypoint.go ./
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /anas-vikunja-entrypoint ./entrypoint.go

FROM ${DOCKER_HUB_REGISTRY}/vikunja/vikunja:${VIKUNJA_VERSION}
COPY --from=entrypoint /anas-vikunja-entrypoint /app/vikunja/anas-vikunja-entrypoint

# The upstream scratch image runs as uid 1000 but a newly created bind mount is
# root-owned. The entrypoint fixes only the attachment tree, then permanently
# drops privileges before execing the unmodified upstream binary.
USER root
ENTRYPOINT ["/app/vikunja/anas-vikunja-entrypoint"]
