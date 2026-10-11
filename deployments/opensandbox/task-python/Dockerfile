FROM python@sha256:78387bc3881b8273120a12ebe6c1ab22b018ccc2c9adf565ae1ac9b536e184ea
COPY execd /opt/opensandbox/execd
COPY bootstrap.sh /opt/opensandbox/bootstrap.sh
COPY empty.env /opt/opensandbox/.env
COPY LICENSE-OpenSandbox /usr/share/licenses/OpenSandbox/LICENSE
COPY dependency-licenses /usr/share/licenses/dependencies
RUN chmod 0555 /opt/opensandbox/execd /opt/opensandbox/bootstrap.sh && chmod 0444 /opt/opensandbox/.env
LABEL codeops.execd_sha256="e9a58d8965b98782012921c280fcc0a40535e2afbd1be6b1155653ee2d2aba3c"
LABEL codeops.bootstrap_sha256="499114628856ef088dacd848daefb90ba141fdd39d23608906357f61e6fd4bef"
