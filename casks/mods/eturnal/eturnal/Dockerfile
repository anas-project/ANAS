ARG GHCR_REGISTRY=ghcr.io
FROM ${GHCR_REGISTRY}/processone/eturnal:1.12.2

ENV ETURNAL_ETC_DIR=/run/anas

COPY --chmod=0755 anas-entrypoint.sh /usr/local/bin/anas-entrypoint

# Preserve the upstream tini process while inserting deterministic config
# generation immediately before the inherited command.
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/anas-entrypoint"]
CMD ["run.sh"]
