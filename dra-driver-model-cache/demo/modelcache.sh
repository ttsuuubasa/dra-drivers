#!/bin/bash

# Copyright Google LLC
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


KUBE_CONTEXT=${KUBE_CONTEXT:-gke_jbelamaric-dev_us-central1_dra}

kubectl --context $KUBE_CONTEXT get resourceslices -o json | jq -r '["NODE", "NAME", "PROVIDER", "ID", "CACHED"], ((if .kind == "List" then .items[] else .object // . end) | [select(.spec.driver == "modelcache.x-k8s.io") as $slice | $slice.spec.devices[] | {nodeName: $slice.spec.nodeName, name: .name, provider: .attributes.provider.string, id: .attributes.id.string, cached: (.attributes.cached.bool | tostring)}] | sort_by([.nodeName, .name])[] | [.nodeName, .name, .provider, .id, .cached]) | @tsv' | column -t -s $'\t'
