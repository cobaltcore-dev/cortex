# Cortex IronCore Bundle

## Setup with IronCore-in-a-Box

To deploy the cortex machine scheduler with [ironcore in a box](https://github.com/ironcore-dev/ironcore-in-a-box), follow these steps.

This guide was made with the following tag:
```bash
git checkout c2385066a45b036a0d0dedc19acc43e738cbcbdf
```

### Deploying IronCore-in-a-Box and Disabling the Machine Scheduler

First, follow the instructions in [this section](https://github.com/ironcore-dev/ironcore-in-a-box?tab=readme-ov-file#installation) to set up your kind cluster with ironcore in a box.

The ironcore-in-a-box setup comes with an operator which schedules machines to machine pools. Since we want to use the cortex machine scheduler instead, we need to disable the default machine scheduler controller. We can do this by editing the deployment and changing the controller args from `--controllers=*` to only include the controllers we want to keep (excluding `machinescheduler`). See also [this link](https://github.com/ironcore-dev/ironcore/blob/3562a97a828d2d5dd5825dbf00d1abadfd0350a6/cmd/ironcore-controller-manager/main.go#L63) for the controllers that can be enabled.

```bash
kubectl --namespace ironcore-system patch deploy ironcore-controller-manager --type='json' -p='[{"op": "replace", "path": "/spec/template/spec/containers/0/args", "value": ["--controllers=machineephemeralnetworkinterface,machineephemeralvolume,machineclass,bucketscheduler,volumerelease,volumescheduler,volumeclass,prefix,prefixallocationscheduler,loadbalancer,loadbalancerephemeralprefix,networkprotection,networkpeering,networkrelease,networkinterfaceephemeralprefix,networkinterfaceephemeralvirtualip,networkinterfacerelease,virtualiprelease,resourcequota,certificateapproval"]}]'
```

### Deploying the Cortex Machine Scheduler

Follow these steps from the cortex repository root:

```bash
git clone https://github.com/cobaltcore-dev/cortex.git && cd cortex
```

#### Deploying from Upstream

Currently not supported yet, as the cortex chart is not published yet on our [ghcr.io](https://github.com/orgs/cobaltcore-dev/packages?repo_name=cortex) registry.

#### Deploying from Local

First, make sure the helm dependencies of the bundle are up to date:

```bash
helm dependency update helm/bundles/cortex-ironcore
```

Build the cortex machine scheduler docker image and load it into the kind cluster.

```bash
docker build -t cortex:dev .
```

```bash
kind load docker-image cortex:dev --name ironcore-in-a-box
```

Now we can deploy our custom cortex machine scheduler. `values.iiab.yaml` contains the necessary overrides to work with ironcore-in-a-box.

```bash
helm upgrade --install cortex-ironcore ./helm/bundles/cortex-ironcore \
    -f ./helm/bundles/cortex-ironcore/values.yaml \
    -f ./helm/bundles/cortex-ironcore/values.iiab.yaml
```

> [!TIP]
> If you made changes to the scheduling/ helm chart, you can update it in the bundle and run helm upgrade again:
> ```bash
> helm package ./scheduling/dist/chart --destination ./helm/bundles/cortex-ironcore/charts
> ```

### Demo

To test the setup, we can create a machine resource. The machine scheduler should then assign a machine pool to the machine and the ironcore machine controller should pick it up and create a corresponding ironcore machine.

```bash
kubectl apply -f https://raw.githubusercontent.com/ironcore-dev/ironcore-in-a-box/refs/heads/main/examples/machine/machine.yaml
```

Watch the resource, and after the cortex setup has started, you should see the machine pool being assigned to the machine.

```bash
kubectl get machine -w
```

```log
NAME     MACHINECLASSREF   IMAGE                                               MACHINEPOOLREF   STATE     AGE
webapp   t3-small          ghcr.io/ironcore-dev/os-images/gardenlinux:latest   <none>           Pending   10s
webapp   t3-small          ghcr.io/ironcore-dev/os-images/gardenlinux:latest   ironcore-in-a-box-control-plane   Pending   103s
```

You can see cortex' decision like this:

```bash
kubectl describe decision
```

Which will show something like this:

```
Name:         machine-twt4h
Namespace:
Labels:       <none>
Annotations:  <none>
API Version:  cortex.cloud/v1alpha1
Kind:         Decision
Spec:
  Machine Ref:
    Name:       webapp
    Namespace:  default
  Operator:     cortex-ironcore
  Pipeline Ref:
    Name:       default
  Resource ID:  webapp
  Type:         ironcore-machine
Status:
  Result:
    Aggregated Out Weights:
      Ironcore - In - A - Box - Control - Plane:  0.7615941559557649
    Normalized In Weights:
      Ironcore - In - A - Box - Control - Plane:  0
    Ordered Hosts:
      ironcore-in-a-box-control-plane
    Raw In Weights:
      Ironcore - In - A - Box - Control - Plane:  0
    Step Results:
      Activations:
        Ironcore - In - A - Box - Control - Plane:  1
      Step Name:                                    noop
    Target Host:                                    ironcore-in-a-box-control-plane
  Took:                                             101.80875ms
```

Also check the logs of the cortex machine scheduler to see the scheduling in action.

```bash
kubectl logs deploy/cortex-ironcore-controller-manager
```

The logs show that the cortex scheduler pipeline has been executed and a machine pool has been assigned to the machine:

```log
2025/10/13 11:11:08 INFO scheduler: starting pipeline subjects=[ironcore-in-a-box-control-plane]
2025/10/13 11:11:08 INFO scheduler: running step stepName=noop stepAlias=""
2025/10/13 11:11:08 INFO scheduler: finished step stepName=noop stepAlias="" name=noop alias="" inWeights=map[ironcore-in-a-box-control-plane:0] outWeights=map[ironcore-in-a-box-control-plane:1]
2025/10/13 11:11:08 INFO scheduler: modified subject weight stepName=noop stepAlias="" name=noop alias="" weight=1
2025/10/13 11:11:08 INFO scheduler: reordered subject stepName=noop stepAlias="" name=noop alias="" subject=ironcore-in-a-box-control-plane originalIdx=0 newIdx=0
2025/10/13 11:11:08 INFO scheduler: finished step stepName=noop stepAlias=""
2025/10/13 11:11:08 INFO scheduler: finished pipeline
2025/10/13 11:11:08 INFO scheduler: input weights weights=map[ironcore-in-a-box-control-plane:0]
2025/10/13 11:11:08 INFO scheduler: output weights weights=map[ironcore-in-a-box-control-plane:0.7615941559557649]
2025/10/13 11:11:08 INFO scheduler: sorted subjects subjects=[ironcore-in-a-box-control-plane]
```
