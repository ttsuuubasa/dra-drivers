#!/usr/bin/env python3
import argparse
import subprocess
import json
import uuid
import os

def get_service_name(model_id):
    parts = model_id.split('/')
    name = parts[-1].lower()
    return f"vllm-{name}"

def main():
    parser = argparse.ArgumentParser(description="Query a vLLM model in K8s")
    parser.add_argument("--no-chat", action='store_true', help="Use /v1/completions")
    parser.add_argument("--model", required=True, help="Model ID (e.g., google/gemma-4-31B-it)")
    parser.add_argument("--prompt", required=True, help="Prompt to send")
    parser.add_argument("--max-tokens", type=int, default=50, help="Max tokens to generate")
    parser.add_argument("--temperature", type=float, default=1.0, help="Temperature")

    args = parser.parse_args()

    kube_context = os.environ.get('KUBE_CONTEXT')
    service_name = get_service_name(args.model)
    url = f"http://{service_name}:8000/v1/chat/completions"

    data = {
        "model": args.model,
        "max_tokens": args.max_tokens,
        "temperature": args.temperature
    }

    if args.no_chat:
        url = f"http://{service_name}:8000/v1/completions"
        data["prompt"] = args.prompt
    else:
        data["messages"] = [{"role": "user", "content": args.prompt}]

    json_data = json.dumps(data)

    print(f"Sending request to {url}...")

    pod_name = f"query-pod-{uuid.uuid4().hex[:8]}"

    # Use curlimages/curl as it has curl installed
    kubectl_cmd = ["kubectl"]
    if kube_context:
        kubectl_cmd.extend(["--context", kube_context])
        
    kubectl_cmd.extend([
        "run", pod_name,
        "--image=curlimages/curl",
        "--restart=Never",
        "--rm", "-i",
        "--", "curl", "-s", "-X", "POST", url,
        "-H", "Content-Type: application/json",
        "-d", json_data
    ])

    print(f"Running pod {pod_name} to query {args.model} at {url}...")
    try:
        result = subprocess.run(kubectl_cmd, check=True, capture_output=True, text=True)
        print("Response:")
        print(result.stdout)
    except subprocess.CalledProcessError as e:
        print(f"Error running pod: {e}")
        print(f"Stderr: {e.stderr}")

if __name__ == "__main__":
    main()

