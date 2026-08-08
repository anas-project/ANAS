FROM ubuntu:resolute

ENV DEBIAN_FRONTEND=noninteractive
ENV PYTHONDONTWRITEBYTECODE=1

ARG CHINESE_SPEEDUP
ARG APT_MIRROR_URL

# The Ubuntu minimal image does not include the trust store. Install it from
# the image's original HTTP source before switching that source to an HTTPS
# mirror, otherwise apt cannot authenticate the mirror on a clean build.
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates \
  && rm -rf /var/lib/apt/lists/*

RUN if [ "$CHINESE_SPEEDUP" = "true" ] && [ -n "$APT_MIRROR_URL" ] ; then \
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
