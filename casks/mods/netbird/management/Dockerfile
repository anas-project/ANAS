FROM netbirdio/management:debug-latest

COPY --chmod=775 root/ /
RUN apt update && apt install -y jq curl gettext-base inetutils-ping && rm -fr /var/cache/apt

ENTRYPOINT ["/root/start.sh"]
