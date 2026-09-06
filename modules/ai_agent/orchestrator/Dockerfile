ARG GO_BUILDER_REGISTRY=docker.io

FROM --platform=$BUILDPLATFORM ${GO_BUILDER_REGISTRY}/library/golang:1.26-alpine AS build
WORKDIR /src
# The orchestrator compiles against the repository's shared compute client,
# which lives outside this image's own Docker context. `shared` is a named build
# context pointing at the repository root (declared as shared_paths in
# .github/images.json and as additional_contexts in the Compose file), so the
# import resolves against the working tree rather than a published module.
COPY --from=shared go.mod go.sum ./
COPY --from=shared internal/computeclient ./internal/computeclient
COPY *.go ./modules/ai_agent/orchestrator/
ARG TARGETOS
ARG TARGETARCH
# Unlike the Forgejo controller this build does reach the module proxy: the
# orchestrator speaks PostgreSQL, and pgx is a real dependency rather than
# something to reimplement. The versions are the ones go.sum pins, so the
# network supplies bytes, not choices.
ENV GOFLAGS=-mod=mod
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/anas-ai-agent ./modules/ai_agent/orchestrator

FROM alpine:3.22.1
RUN apk add --no-cache ca-certificates \
    && addgroup -g 65532 -S orchestrator \
    && adduser -u 65532 -S -D -H -G orchestrator orchestrator \
    && install -d -o 65532 -g 65532 -m 0700 /var/lib/anas-ai-agent/sessions
COPY --from=build /out/anas-ai-agent /usr/local/bin/anas-ai-agent
USER 65532:65532
EXPOSE 8130
ENTRYPOINT ["/usr/local/bin/anas-ai-agent"]
