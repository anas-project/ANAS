FROM netbirdio/management:0.76.1

COPY --chmod=0755 root/ /
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl gettext-base iputils-ping jq \
  && rm -rf /var/lib/apt/lists/*

ENTRYPOINT ["/root/start.sh"]
