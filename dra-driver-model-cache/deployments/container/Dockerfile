# Copyright Google LLC.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

ARG GOLANG_VERSION=1.25.5
ARG BASE_IMAGE=docker.io/ubuntu:22.04
FROM golang:${GOLANG_VERSION} as build

WORKDIR /build
COPY . .

RUN mkdir /artifacts
RUN make PREFIX=/artifacts cmds

FROM ${BASE_IMAGE}

RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*

LABEL io.k8s.display-name="Node Model Cache DRA Driver"
LABEL name="Node Model Cache DRA Driver"
LABEL vendor="modelcache.x-k8s.io"
ARG VERSION="v0.1.0"
LABEL version=${VERSION}
LABEL summary="DRA resource driver for Node Model Cache"
LABEL description="See summary"

COPY --from=build /artifacts/model-cache-kubeletplugin /usr/bin/model-cache-kubeletplugin
COPY --from=build /artifacts/model-cache-webhook       /usr/bin/model-cache-webhook
