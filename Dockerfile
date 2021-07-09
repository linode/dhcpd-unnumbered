FROM debian:bullseye
COPY dhcpd-unnumbered /usr/sbin
EXPOSE 67/udp
ENTRYPOINT ["/usr/sbin/dhcpd-unnumbered"]
