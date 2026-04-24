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
LOCATION=${LOCATION:-us-west4}
zone=${1:-a}

# NOTE: Topology for 32 chips (4 nodes x 8 chips) is assumed to be 4x8.
# Please verify and update if a different topology is required.
MACHINE_TYPE="ct5lp-hightpu-8t"
TPU_TOPOLOGY="4x8"

gcloud container node-pools create tpu-v5e-8t-4n-pool-${zone} \
    --cluster=${CLUSTER_NAME} \
    --location=${LOCATION} \
    --node-locations=${LOCATION}-${zone} \
    --machine-type="${MACHINE_TYPE}" \
    --num-nodes=4 \
    --node-labels=cloud.google.com/compute-class=vllm-tpu-ccc,cloud.google.com/gke-tpu-dra-driver=true \
    --node-taints="cloud.google.com/compute-class=vllm-tpu-ccc:NoSchedule" \
    --disk-size=300 \
    --enable-autorepair --enable-autoupgrade
