#!/usr/bin/env python3

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

import datetime
import json
import os
import re
import subprocess
import sys

def run_command(cmd):
    try:
        result = subprocess.run(cmd, shell=True, check=True, capture_output=True, text=True)
        return result.stdout
    except subprocess.CalledProcessError as e:
        return None

def strip_ansi(text):
    ansi_escape = re.compile(r'\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])')
    return ansi_escape.sub('', text)

def parse_ts(ts_str):
    # Replace Z with +00:00 for isoformat compatibility
    ts_str = ts_str.replace('Z', '+00:00')
    # If there are fractional seconds, truncate to 6 digits (microseconds)
    if '.' in ts_str:
        parts = ts_str.split('.')
        sub_parts = parts[1].split('+')
        frac = sub_parts[0][:6]
        ts_str = f"{parts[0]}.{frac}+{sub_parts[1]}"
    return datetime.datetime.fromisoformat(ts_str)

def format_duration(td):
    seconds = int(td.total_seconds())
    if seconds < 60:
        return f"{seconds}s"
    minutes = seconds // 60
    if minutes < 60:
        return f"{minutes}m"
    hours = minutes // 60
    if hours < 24:
        return f"{hours}h"
    days = hours // 24
    return f"{days}d"

def main():
    kube_context = os.environ.get('KUBE_CONTEXT')
    kubectl_base = f"kubectl --context {kube_context}" if kube_context else "kubectl"

    # Get all pods in JSON format
    pods_json = run_command(f"{kubectl_base} get pods -o json")
    if not pods_json:
        print("Failed to get pods.")
        return

    try:
        pods_data = json.loads(pods_json)
    except json.JSONDecodeError:
        print("Failed to parse pods JSON.")
        return

    # Fetch all ResourceClaims
    claims_json = run_command(f"{kubectl_base} get resourceclaims -o json")
    claims_map = {}
    if claims_json:
        try:
            claims_data = json.loads(claims_json)
            for claim in claims_data.get('items', []):
                claims_map[claim['metadata']['name']] = claim
        except json.JSONDecodeError:
            pass

    # Print headers with requested columns
    # Renamed TO RUNNING to TO RUN and narrowed by 4 (to 8)
    print(f"{'POD':<45} {'STATUS':<11} {'READY':<6} {'AGE':<6} {'NODE':<40} {'GPU DEVICES':<17} {'MODEL':<26} {'MODEL DEVICES':<30} {'TO RUN':<8} {'LOAD TIME':<10}")

    now = datetime.datetime.now(datetime.timezone.utc)

    for pod in pods_data.get('items', []):
        pod_name = pod['metadata']['name']
        # Filter for vLLM pods (assuming they start with vllm-)
        if not pod_name.startswith('vllm-'):
            continue

        if pod.get('metadata', {}).get('deletionTimestamp'):
            pod_phase = "Terminating"
        else:
            pod_phase = pod.get('status', {}).get('phase', 'Unknown')
        node_name = pod.get('spec', {}).get('nodeName', '<none>')
        creation_ts_str = pod['metadata']['creationTimestamp']

        try:
            creation_ts = parse_ts(creation_ts_str)
            age_td = now - creation_ts
            age_str = format_duration(age_td)
        except Exception as e:
            age_str = "Unknown"
            creation_ts = None

        # Calculate time to reach Running state and check readiness
        to_running = "N/A"
        container_statuses = pod.get('status', {}).get('containerStatuses', [])
        
        ready_count = 0
        total_count = len(container_statuses)
        for cs in container_statuses:
            if cs.get('ready', False):
                ready_count += 1
            
            if cs['name'] == 'vllm-gpu':
                state = cs.get('state', {})
                if 'running' in state:
                    started_at_str = state['running'].get('startedAt')
                    if started_at_str and creation_ts:
                        try:
                            started_at = parse_ts(started_at_str)
                            diff = started_at - creation_ts
                            to_running = format_duration(diff)
                        except Exception as e:
                            pass
                break
        
        ready_str = f"{ready_count}/{total_count}" if total_count > 0 else "0/0"

        model_claim_name = "Unknown"
        model_devices_str = "Unknown"
        gpu_devices_str = "Unknown"
        
        # Attempt to get devices and modelId from ResourceClaim
        resource_claims = pod.get('spec', {}).get('resourceClaims', [])
        model_devices = []
        gpu_devices = []
        
        for rc in resource_claims:
            claim_name = rc.get('source', {}).get('resourceClaimName')
            if not claim_name:
                claim_statuses = pod.get('status', {}).get('resourceClaimStatuses', [])
                for cs in claim_statuses:
                    if cs.get('name') == rc.get('name'):
                        claim_name = cs.get('resourceClaimName')
                        break
            
            if claim_name and claim_name in claims_map:
                claim_obj = claims_map[claim_name]
                
                # Extract modelId from spec.devices.config.opaque.parameters
                spec = claim_obj.get('spec', {})
                devices_spec = spec.get('devices', {})
                config_list = devices_spec.get('config', [])
                
                for config in config_list:
                    opaque = config.get('opaque', {})
                    if opaque.get('driver') == 'modelcache.x-k8s.io':
                        params = opaque.get('parameters', {})
                        if 'modelId' in params:
                            model_claim_name = params['modelId']
                            break
                
                # Extract devices from status.allocation.devices.results
                status = claim_obj.get('status', {})
                allocation = status.get('allocation', {})
                devices_obj = allocation.get('devices', {})
                results = devices_obj.get('results', [])
                
                for res in results:
                    driver = res.get('driver')
                    device = res.get('device')
                    if driver == 'modelcache.x-k8s.io' and device:
                        model_devices.append(device)
                    elif driver == 'gpu.nvidia.com' and device:
                        gpu_devices.append(device)

        if gpu_devices:
            seen = set()
            gpu_devices = [x for x in gpu_devices if not (x in seen or seen.add(x))]
            gpu_devices_str = ",".join(gpu_devices)

        if model_devices:
            seen = set()
            model_devices = [x for x in model_devices if not (x in seen or seen.add(x))]
            model_devices_str = ",".join(model_devices)

        # Default load time based on phase
        if pod_phase == "Running":
            load_time = "Loading"
        else:
            load_time = pod_phase

        # Get logs for the vllm-gpu container (still needed for load time)
        logs = run_command(f"{kubectl_base} logs --timestamps=true {pod_name} -c vllm-gpu")
        
        if logs:
            clean_logs = strip_ansi(logs)

            # Search for load time in logs
            load_time_match = re.search(r"Loading weights took ([^\s]+) seconds", clean_logs, re.IGNORECASE)
            if load_time_match:
                load_time = load_time_match.group(1) + "s"
            else:
                load_time_match = re.search(r"(?:Loaded weights|Model weights loaded|weights loaded) in ([^\s]+)\s*(s|ms)", clean_logs, re.IGNORECASE)
                if load_time_match:
                    load_time = load_time_match.group(1) + load_time_match.group(2)

            if load_time == "Loading" and "Application startup complete" in clean_logs:
                load_time = "Unknown"
        else:
            if pod_phase == "Running":
                load_time = "Loading"
            else:
                load_time = pod_phase

        print(f"{pod_name:<45} {pod_phase:<11} {ready_str:<6} {age_str:<6} {node_name:<40} {gpu_devices_str:<17} {model_claim_name:<26} {model_devices_str:<30} {to_running:<8} {load_time:<10}")

if __name__ == "__main__":
    main()
