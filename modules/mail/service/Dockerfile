ARG DOCKER_HUB_REGISTRY=docker.io
FROM --platform=$BUILDPLATFORM ${DOCKER_HUB_REGISTRY}/library/golang:1.26-alpine AS build
WORKDIR /src
COPY --from=shared go.mod go.sum ./
COPY --from=shared internal/directorymail ./internal/directorymail
COPY --from=shared internal/configschema ./internal/configschema
COPY --from=shared internal/localization ./internal/localization
COPY store ./modules/mail/store
COPY identity ./modules/mail/identity
COPY dms ./modules/mail/dms
COPY smtpconfig ./modules/mail/smtpconfig
COPY ingress ./modules/mail/ingress
COPY service ./modules/mail/service
ARG TARGETOS
ARG TARGETARCH
ARG GO_MODULE_PROXY=https://proxy.golang.org,direct
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOPROXY="$GO_MODULE_PROXY" go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/anas-mail ./modules/mail/service
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chmod=0755 /out/anas-mail /anas-mail
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/anas-mail"]
