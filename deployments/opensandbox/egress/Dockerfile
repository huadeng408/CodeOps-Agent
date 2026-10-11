FROM golang@sha256:699337d620559a59b4a2bb298ad59611e535d2ee755a34cf2d2a98f37578dc80
COPY debs /prepared-debs
RUN dpkg -i /prepared-debs/*.deb && rm -r /prepared-debs
COPY egress /usr/local/bin/egress
COPY LICENSE-OpenSandbox /usr/share/licenses/OpenSandbox/LICENSE
COPY dependency-licenses /usr/share/licenses/dependencies
RUN chmod 0555 /usr/local/bin/egress
ENTRYPOINT ["/usr/local/bin/egress"]
