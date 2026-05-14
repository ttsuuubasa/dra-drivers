IMAGE_NAME ?= dra-driver-noop
IMAGE_TAG ?= latest

.PHONY: build
build:
	go build -o dra-driver-noop main.go

.PHONY: docker-build
docker-build:
	docker build -t $(IMAGE_NAME):$(IMAGE_TAG) -f deployments/container/Dockerfile .

.PHONY: docker-push
docker-push:
	docker push $(IMAGE_NAME):$(IMAGE_TAG)
