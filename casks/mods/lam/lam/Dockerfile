FROM ldapaccountmanager/lam:7.8
RUN apt-get update \
  && apt-get upgrade -y \
  && apt-get install --no-install-recommends -y gettext \
  && rm -rf /var/cache/apt /var/lib/apt/lists/* 

RUN rm /etc/apache2/conf-enabled/laminit.conf \
  && sed -i 's/Alias.*/DocumentRoot \/usr\/share\/ldap-account-manager\//g' /etc/apache2/conf-enabled/ldap-account-manager.conf
COPY lam.conf.j2 /var/lib/ldap-account-manager/config
COPY config.sh /usr/local/bin/config.sh
RUN chmod 0755 /usr/local/bin/config.sh

CMD [ "/usr/local/bin/config.sh" ]

HEALTHCHECK --interval=1m --timeout=10s \
  CMD wget -qO- http://localhost/ | grep -q '<title>LDAP Account Manager</title>'
