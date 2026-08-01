FROM ldapaccountmanager/lam:7.8

RUN rm /etc/apache2/conf-enabled/laminit.conf \
  && sed -i 's/Alias.*/DocumentRoot \/usr\/share\/ldap-account-manager\//g' /etc/apache2/conf-enabled/ldap-account-manager.conf
COPY lam.conf.envsubst /var/lib/ldap-account-manager/config
COPY configure.php /opt/anas/configure.php
COPY config.sh /usr/local/bin/config.sh
RUN chmod 0755 /usr/local/bin/config.sh

CMD [ "/usr/local/bin/config.sh" ]

HEALTHCHECK --interval=1m --timeout=10s \
  CMD wget -qO- http://localhost/ | grep -q '<title>LDAP Account Manager</title>'
