ARG DOCKER_HUB_REGISTRY=docker.io
FROM ${DOCKER_HUB_REGISTRY}/library/ubuntu:resolute

ENV DEBIAN_FRONTEND=noninteractive
ENV PYTHONDONTWRITEBYTECODE=1

ARG CHINESE_BUILD_SPEEDUP
ARG APT_MIRROR_URL

# The Ubuntu minimal image has no trust store. Bootstrap ca-certificates from
# the configured mirror over HTTP, then restore its requested HTTPS URL.
RUN bootstrap_mirror=""; \
  if [ "$CHINESE_BUILD_SPEEDUP" = "true" ] && [ -n "$APT_MIRROR_URL" ]; then \
    bootstrap_mirror="$APT_MIRROR_URL"; \
    case "$bootstrap_mirror" in https://*) bootstrap_mirror="http://${bootstrap_mirror#https://}";; esac; \
  fi; \
  if [ -n "$bootstrap_mirror" ]; then \
    for source in /etc/apt/sources.list /etc/apt/sources.list.d/*.sources; do \
      if [ -f "$source" ]; then \
        sed -i \
          -e "s|http://archive.ubuntu.com|${bootstrap_mirror%/}|g" \
          -e "s|http://security.ubuntu.com|${bootstrap_mirror%/}|g" \
          "$source"; \
      fi; \
    done; \
  fi; \
  apt-get update; \
  apt-get install -y --no-install-recommends ca-certificates; \
  rm -rf /var/lib/apt/lists/*; \
  if [ -n "$bootstrap_mirror" ] && [ "$bootstrap_mirror" != "${APT_MIRROR_URL%/}" ]; then \
    sed -i "s|${bootstrap_mirror%/}|${APT_MIRROR_URL%/}|g" /etc/apt/sources.list /etc/apt/sources.list.d/*.sources 2>/dev/null || true; \
  fi

RUN if [ "$CHINESE_BUILD_SPEEDUP" = "true" ] && [ -n "$APT_MIRROR_URL" ] ; then \
  for source in /etc/apt/sources.list /etc/apt/sources.list.d/*.sources; do \
    if [ -f "$source" ]; then \
      sed -i \
        -e "s|http://archive.ubuntu.com|${APT_MIRROR_URL%/}|g" \
        -e "s|http://security.ubuntu.com|${APT_MIRROR_URL%/}|g" \
        "$source"; \
    fi; \
  done; \
  fi \
  && apt-get update \
  && apt-get install -y --no-install-recommends python3 python3-ldap \
  && apt-get clean \
  && rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

COPY anchor_worker.py /app/anchor_worker.py
COPY event_journal.py /app/event_journal.py

RUN chmod 0555 /app/anchor_worker.py /app/event_journal.py

USER 65534:65534
ENTRYPOINT ["python3", "/app/anchor_worker.py"]
