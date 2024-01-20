FROM goacme/lego:v4.11.0

WORKDIR /certs
COPY cert.sh run.sh /root/
RUN echo "0 3 * * 6 /root/cert.sh > /dev/stdout" > /etc/crontabs/root \
  && chmod +x /root/run.sh /root/cert.sh

ENTRYPOINT [ "/root/run.sh" ]