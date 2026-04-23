#!/bin/bash
set -e

echo "Removing vllm deployments..."

for file in vllm-gemma-4-*.yaml vllm-phi-2.yaml vllm-opt-125m.yaml; do
  kubectl delete -f $file || echo
done

# Embedded cleanup manifest
MANIFEST=$(cat <<'EOF'
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: node-cleanup
  namespace: default
spec:
  selector:
    matchLabels:
      name: node-cleanup
  template:
    metadata:
      labels:
        name: node-cleanup
    spec:
      hostPID: true
      tolerations:
      - key: "nvidia.com/gpu"
        operator: "Exists"
        effect: "NoSchedule"
      - key: "cloud.google.com/compute-class"
        operator: "Exists"
        effect: "NoSchedule"
      nodeSelector:
        cloud.google.com/compute-class: vllm-gpu-ccc
      containers:
      - name: cleanup
        image: busybox
        command: ["/bin/sh", "-c"]
        args:
        - |
          echo "Cleaning up images..."
          chroot /host crictl rmi --prune
          echo "Cleaning up model caches..."
          chroot /host rm -rf /var/lib/model-cache/google /var/lib/model-cache/huggingface
          chroot /host /bin/bash -c "rm -rf /var/run/cdi/k8s.modelcache.x-k8s.io*"
          echo "Cleanup complete. Sleeping indefinitely to prevent restarts."
          sleep infinity
        securityContext:
          privileged: true
        volumeMounts:
        - name: host-root
          mountPath: /host
      volumes:
      - name: host-root
        hostPath:
          path: /
      restartPolicy: Always
EOF
)

echo "Creating node-cleanup DaemonSet..."
echo "$MANIFEST" | kubectl apply -f -

echo "Waiting for pods to be created..."
while [ -z "$(kubectl get pods -l name=node-cleanup -o name)" ]; do
  echo "Waiting for pods to appear..."
  sleep 2
done

echo "Monitoring logs for completion..."
# Function to check if all pods are done
check_pods_done() {
  local pods=$(kubectl get pods -l name=node-cleanup -o jsonpath='{.items[*].metadata.name}')
  if [ -z "$pods" ]; then
    return 1
  fi
  for pod in $pods; do
    # Check if the pod is running
    local status=$(kubectl get pod $pod -o jsonpath='{.status.phase}')
    if [ "$status" != "Running" ]; then
      echo "Pod $pod is in status $status, waiting..."
      return 1
    fi
    # Check logs
    if ! kubectl logs $pod | grep -q "Cleanup complete. Sleeping indefinitely to prevent restarts."; then
      return 1
    fi
  done
  return 0
}

until check_pods_done; do
  echo "Still waiting for cleanup to complete on all pods..."
  sleep 5
done

echo "Cleanup complete on all pods."

# Rolling restart of kubeletplugin
echo "Doing a rolling restart of kubeletplugin..."
kubectl rollout restart daemonset dra-driver-model-cache-kubeletplugin -n dra-driver-model-cache

echo "Waiting for kubeletplugin rollout to complete..."
kubectl rollout status daemonset dra-driver-model-cache-kubeletplugin -n dra-driver-model-cache

echo "Deleting node-cleanup DaemonSet..."
echo "$MANIFEST" | kubectl delete -f -

echo "Script execution finished."
