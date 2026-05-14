ARG GOLANG_VERSION=1.26.2
ARG BASE_IMAGE=docker.io/ubuntu:22.04
FROM golang:${GOLANG_VERSION} as build

WORKDIR /build
COPY . .

RUN mkdir -p /artifacts
RUN go build -o /artifacts/dra-driver-noop main.go

FROM ${BASE_IMAGE}

RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*

LABEL io.k8s.display-name="NOOP DRA Driver"
LABEL name="NOOP DRA Driver"
LABEL vendor="google"
ARG VERSION="v0.1.0"
LABEL version=${VERSION}
LABEL summary="DRA resource driver for NOOP"
LABEL description="See summary"

COPY --from=build /artifacts/dra-driver-noop /usr/bin/dra-driver-noop
