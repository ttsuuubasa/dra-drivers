IMAGE ?= registry.k8s.io/dra-driver-image-configurator
TAG ?= latest

.PHONY: build
build:
	go build -o bin/controller .

.PHONY: gofmt
gofmt:
	gofmt -s -w .

.PHONY: docker-build
docker-build:
	docker build -t $(IMAGE):$(TAG) .
