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

GOLANG_VERSION ?= 1.25.5

DRIVER_NAME := dra-driver-model-cache
MODULE := github.com/google/$(DRIVER_NAME)

VERSION  ?=
vVERSION := v$(VERSION:v%=%)

VENDOR := modelcache.x-k8s.io
APIS := v1

PLURAL_EXCEPTIONS  = DeviceClassParameters:DeviceClassParameters

ifeq ($(IMAGE_NAME),)
REGISTRY ?= registry.example.com
IMAGE_NAME = $(REGISTRY)/$(DRIVER_NAME)
endif
