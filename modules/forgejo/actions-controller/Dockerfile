ARG GO_BUILDER_REGISTRY=docker.io

FROM --platform=$BUILDPLATFORM ${GO_BUILDER_REGISTRY}/library/golang:1.26-alpine AS controller-build
WORKDIR /src
COPY *.go ./
ARG TARGETOS
ARG TARGETARCH
RUN GO111MODULE=off CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/anas-forgejo-actions-controller .

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
