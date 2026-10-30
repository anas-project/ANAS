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
ARG TARGETOS
ARG TARGETARCH
ARG GO_MODULE_PROXY=https://proxy.golang.org,direct
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOPROXY="$GO_MODULE_PROXY" go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/anas-mail-dms ./modules/mail/dms/cmd
FROM ghcr.io/docker-mailserver/docker-mailserver:16.0.1
COPY --from=build --chmod=0755 /out/anas-mail-dms /usr/local/bin/anas-mail-dms
COPY dms/supervisor.conf /etc/supervisor/conf.d/anas-mail.conf
COPY --chmod=0755 dms/entrypoint.sh /usr/local/bin/anas-mail-dms-entrypoint
ENTRYPOINT ["/usr/bin/dumb-init", "--", "/usr/local/bin/anas-mail-dms-entrypoint"]
CMD ["supervisord", "-c", "/etc/supervisor/supervisord.conf"]
