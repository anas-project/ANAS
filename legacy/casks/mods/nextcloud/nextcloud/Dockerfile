FROM crazymax/nextcloud:30.0.1
LABEL maintainer="Hailong"

RUN apk --update --no-cache add \
  jq \
  && rm -rf /tmp/*

COPY root/ /
