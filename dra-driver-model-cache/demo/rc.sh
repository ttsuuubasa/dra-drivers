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

kubectl get resourceclaim -n default -o json | jq -r '
  ["NAME", "STATE", "DEVICES"],
  (.items[] | [
    .metadata.name,
    (if .status.allocation and ((.status.reservedFor // []) | length > 0) then "allocated,reserved" 
     elif .status.allocation then "allocated" 
     elif ((.status.reservedFor // []) | length > 0) then "reserved" 
     else "pending" end),
    ([.status.allocation.devices.results[]? | "\(.driver)/\(.device)"] | join(", "))
  ]) | @tsv' | column -t -s $'\t'
