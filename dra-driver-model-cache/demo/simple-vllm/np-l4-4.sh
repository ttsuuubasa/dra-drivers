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

CLUSTER_NAME=dra
LOCATION=${LOCATION:-us-west2}
zone=${1:-a}

gcloud container node-pools create l4-4-pool-${zone} \
    --cluster=${CLUSTER_NAME} \
    --location=${LOCATION} \
    --node-locations=${LOCATION}-${zone} \
    --machine-type="g2-standard-48" \
    --accelerator="type=nvidia-l4,count=4,gpu-driver-version=disabled" \
    --enable-autoscaling \
    --total-min-nodes=1 \
    --total-max-nodes=3 \
    --num-nodes=1 \
    --node-labels=gke-no-default-nvidia-gpu-device-plugin=true,nvidia.com/gpu.present=true,cloud.google.com/compute-class=vllm-gpu-ccc,cloud.google.com/gke-nvidia-gpu-dra-driver=true \
    --node-taints="cloud.google.com/compute-class=vllm-gpu-ccc:NoSchedule" \
    --disk-size=300

