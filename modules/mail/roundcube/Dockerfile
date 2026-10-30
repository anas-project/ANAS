ARG DOCKER_HUB_REGISTRY=docker.io
FROM ${DOCKER_HUB_REGISTRY}/roundcube/roundcubemail:1.7.5-apache
COPY roundcube/plugins/anas_mail /usr/src/roundcubemail/plugins/anas_mail
COPY roundcube/config.php /var/roundcube/config/anas-mail.php
COPY --chmod=0755 roundcube/prepare-binding.sh /entrypoint-tasks/pre-setup/10-anas-mail-binding
COPY roundcube/health.php /usr/local/lib/anas-mail-webmail-health.php
COPY --chmod=0755 roundcube/check-database.sh /entrypoint-tasks/post-setup/90-anas-mail-database
