FROM docker.io/keycloak/keycloak:23.0.6-0

LABEL maintainer="Hailong"

COPY root /

CMD ["--proxy", "edge", "--hostname-strict=false"]
