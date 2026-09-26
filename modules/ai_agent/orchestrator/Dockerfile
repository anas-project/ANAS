ARG DOCKER_HUB_REGISTRY=docker.io
ARG GO_BUILDER_REGISTRY=${DOCKER_HUB_REGISTRY}

FROM --platform=$BUILDPLATFORM ${GO_BUILDER_REGISTRY}/library/golang:1.26-alpine AS build
WORKDIR /src
# The orchestrator compiles against the repository's shared compute client,
# which lives outside this image's own Docker context. `shared` is a named build
# context pointing at the repository root (declared as shared_paths in
# .github/images.json and as additional_contexts in the Compose file), so the
# import resolves against the working tree rather than a published module.
COPY --from=shared go.mod go.sum ./
COPY --from=shared internal/computeclient ./internal/computeclient
COPY --from=shared internal/securefs ./internal/securefs
COPY --from=shared internal/computeingress ./internal/computeingress
COPY *.go ./modules/ai_agent/orchestrator/
ARG TARGETOS
ARG TARGETARCH
# Unlike the Forgejo controller this build does reach the module proxy: the
# orchestrator speaks PostgreSQL, and pgx is a real dependency rather than
# something to reimplement. The versions are the ones go.sum pins, so the
# network supplies bytes, not choices.
ARG GO_MODULE_PROXY=https://proxy.golang.org,direct
ENV GOFLAGS=-mod=mod
# The override selects transport only; retain the default checksum verifier.
# Build arguments are not a channel for proxy credentials.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOPROXY="$GO_MODULE_PROXY" \
    go build -trimpath -ldflags="-s -w" -o /out/anas-ai-agent ./modules/ai_agent/orchestrator

FROM ${DOCKER_HUB_REGISTRY}/library/alpine:3.22.1
RUN apk add --no-cache ca-certificates \
    && addgroup -g 65532 -S orchestrator \
    && adduser -u 65532 -S -D -H -G orchestrator orchestrator \
    && install -d -o 65532 -g 65532 -m 0700 /var/lib/anas-ai-agent/sessions
COPY --from=build /out/anas-ai-agent /usr/local/bin/anas-ai-agent
USER 65532:65532
EXPOSE 8130
ENTRYPOINT ["/usr/local/bin/anas-ai-agent"]
