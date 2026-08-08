ARG DDNS_GO_VERSION=v6.17.4

# The reconciler is built from source in the bundle rather than shipped as a
# binary, so the image is reproducible from this repository alone.
FROM golang:1.26-alpine AS build
ARG GOPROXY_URL
ENV CGO_ENABLED=0
# Keyed on GOPROXY_URL alone rather than on CHINESE_SPEEDUP: which module proxy
# a build can reach is a different question from whether the host wants Chinese
# package mirrors. A host with good registry connectivity but no route to
# proxy.golang.org has to be able to say so without claiming the other.
RUN if [ -n "$GOPROXY_URL" ]; then go env -w GOPROXY="$GOPROXY_URL"; fi
WORKDIR /src
COPY reconcile/go.mod reconcile/go.sum ./
RUN go mod download
COPY reconcile/ ./
RUN go build -trimpath -ldflags="-s -w" -o /anas-ddns-go-reconcile .

FROM ghcr.io/jeessy2/ddns-go:${DDNS_GO_VERSION}
COPY --from=build /anas-ddns-go-reconcile /anas-ddns-go-reconcile
# Fail the build, not the container, if upstream moves the binary: the
# reconciler execs it by path and a wrong one surfaces at runtime as a bare
# "no such file or directory" with nothing pointing at the cause.
RUN [ -x /app/ddns-go ]
# The reconciler merges the declared record set into the persistent
# configuration and then execs ddns-go with the same arguments, so ddns-go
# remains PID 1's only long-running process and signals reach it unchanged.
ENTRYPOINT ["/anas-ddns-go-reconcile"]
