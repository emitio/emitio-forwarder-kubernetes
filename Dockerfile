FROM ubuntu:xenial-20180412

COPY ./target/emitio-forwarder-kubernetes_linux_amd64 /usr/local/bin/emitio-forwarder-kubernetes

ENTRYPOINT [ "emitio-forwarder-kubernetes" ]