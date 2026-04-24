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

CLUSTER_NAME=${CLUSTER_NAME:-dra}
LOCATION=${LOCATION:-us-east5}
zone=${1:-a}

MACHINE_TYPE="ct6e-standard-4t"
TPU_TOPOLOGY="2x2"

gcloud container node-pools create tpu-v6e-4-pool-${zone} \
    --cluster=${CLUSTER_NAME} \
    --location=${LOCATION} \
    --node-locations=${LOCATION}-${zone} \
    --machine-type="${MACHINE_TYPE}" \
    --tpu-topology="${TPU_TOPOLOGY}" \
    --num-nodes=1 \
    --node-labels=cloud.google.com/compute-class=vllm-tpu-ccc,cloud.google.com/gke-tpu-dra-driver=true \
    --node-taints="cloud.google.com/compute-class=vllm-tpu-ccc:NoSchedule" \
    --disk-size=300 \
    --enable-autorepair --enable-autoupgrade
