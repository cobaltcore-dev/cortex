# Cortex Scheduling Operator

## Minimal Setup

From the cortex root folder, build the scheduling operator docker image locally:
```bash
docker build --build-arg GO_MOD_PATH=scheduling \
    -t cortex-scheduling-operator:latest .
```

Then, install the prometheus CRDs:
```bash
helm upgrade --install cortex-prometheus helm/dev/cortex-prometheus-operator
```

Finally, install the scheduling operator using the locally built image:
```bash
helm upgrade --install cortex-scheduling scheduling/dist/chart \
    --set controllerManager.container.image.tag=latest \
    --set controllerManager.container.image.repository=cortex-scheduling-operator \
    --set controllerManager.container.image.pullPolicy=Never
```

Now you can apply sample resources to test the operator:
```bash
kubectl apply -f scheduling/samples/
```
