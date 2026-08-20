ARG GO_BUILDER_REGISTRY=docker.io
ARG ANAS_IMAGE_REGISTRY=ghcr.io/anas-project

FROM --platform=$BUILDPLATFORM ${GO_BUILDER_REGISTRY}/library/golang:1.26-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' -o /out/anas-oauth2-proxy-bootstrap .

FROM ${ANAS_IMAGE_REGISTRY}/anas-mirror-oauth2-proxy:7.15.3
COPY --from=builder /out/anas-oauth2-proxy-bootstrap /bin/anas-oauth2-proxy-bootstrap
ENTRYPOINT ["/bin/anas-oauth2-proxy-bootstrap"]
