# Cortex Scheduling Operator

## Minimal Setup

From the cortex root folder, build the scheduling operator docker image locally:
```bash
docker build --build-arg GO_MOD_PATH=scheduling \
    -t cortex-scheduling-operator:latest .
```

Then, install the prometheus CRDs and the cortex CRDs:
```bash
helm upgrade --install cortex-prometheus helm/dev/cortex-prometheus-operator
helm upgrade --install cortex-crds helm/bundles/cortex-crds
```

Finally, install the scheduling operator using the locally built image:
```bash
helm upgrade --install cortex-scheduling scheduling/dist/chart \
    --set crd.enable=false \
    --set namePrefix=cortex-nova \
    --set conf.operator=cortex-nova \
    --set controllerManager.container.image.tag=latest \
    --set controllerManager.container.image.repository=cortex-scheduling-operator \
    --set controllerManager.container.image.pullPolicy=Never
```

Now you can apply sample resources to test the operator:
```bash
helm template \
    -s templates/steps.yaml \
    -s templates/pipelines.yaml \
    -s templates/datasources.yaml \
    -s templates/knowledges.yaml \
    helm/bundles/cortex-nova/ | kubectl apply -f -
kubectl apply -f scheduling/samples/nova-decisions.yaml
```
