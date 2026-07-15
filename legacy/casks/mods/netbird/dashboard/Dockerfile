FROM wiretrustee/dashboard:v1.17.16

RUN apk add --no-cache jq && rm -rf /var/cache/apk/*

COPY --chmod=775 start.sh /root/

ENTRYPOINT ["/root/start.sh"]
