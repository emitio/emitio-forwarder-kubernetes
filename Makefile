.PHONY: bin
bin: target/emitio-forwarder-kubernetes_linux_amd64

SRC_FULL = $(shell find . -type f -not -path './target/*' -not -path './.*')
target/emitio-forwarder-kubernetes_linux_amd64: $(SRC_FULL)
	@GOOS="linux" GOARCH="amd64" go build -o $@ cmd/emitio-forwarder-kubernetes/main.go

.PHONY: image
image:
	docker build . -t emitio/emitio-forwarder-kubernetes

.PHONY: push
push:
	docker push emitio/emitio-forwarder-kubernetes
