ARG GHCR_REGISTRY=ghcr.io
FROM ${GHCR_REGISTRY}/processone/eturnal:1.12.2

ENV ETURNAL_ETC_DIR=/run/anas

# /opt/eturnal is an inherited Docker VOLUME, even though it contains the
# application release rather than user data. Keep a build-time copy outside the
# volume path. The entrypoint restores it into Compose's explicit tmpfs mount,
# preventing Docker from creating an anonymous volume for every container.
USER root
RUN mkdir -p /opt/anas/eturnal-seed \
  && cp -a /opt/eturnal/. /opt/anas/eturnal-seed/ \
  && chown -R eturnal:eturnal /opt/anas/eturnal-seed
USER eturnal

COPY --chmod=0755 anas-entrypoint.sh /usr/local/bin/anas-entrypoint

# Preserve the upstream tini process while inserting deterministic config
# generation immediately before the inherited command.
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/anas-entrypoint"]
CMD ["run.sh"]
