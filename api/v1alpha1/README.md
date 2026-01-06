This document specifies Cortex CRDs. 

## Pipeline

```yaml
apiVersion: cortex.cloud/v1alpha1
kind: Pipeline
metadata:
  name: nova-general-purpose
spec:
  description: The primary objective of this pipeline is to load-balance general purpose workloads.
  # Ordered filters must be completed successfully in order to ensure valid placement and scheduling.
  # Failures stop both placement and scheduling and raise a critical alert.
  filters:
    - name: filter1
      description: Short, human-readable description.
      # Optional parameters to configure the filter. Early iteration to be refined later.
      params:
        key: value
  # Ordered weighers should be completed successfully in order to ensure optimal placement and scheduling.
  # Failures raise a warning-level alert.
  weighers:
    - name: weigher1
      description: Short, human-readable description.
      # Optional parameters to configure the weigher. Early iteration to be refined later.
      params:
        key: value
status:
  conditions:
    - type: Ready
      status: True|False|Unknown
      reason: AllFiltersAndWeighersReady
      message: All filters and weighers are ready
      lastTransitionTime: "<timestamp>"
```

## Knowledge

```yaml
apiVersion: cortex.cloud/v1alpha1
kind: Knowledge
metadata:
  name: <name>
  labels:
    domain: nova
    entityKind: VM
    projectID: "9a2e2b7b-1b5a-4d8f-a0c4-2a9f7e3e1c02"
spec: {}
status:
  entity:
    kind: VM
    id: "6f2c2b5d-4a5b-4d0d-9b33-0f2d7c2b9c11"
    domain: nova
  
  samples:
    cpuUtilization:
      value:
        quantity: "730m"
      observedAt: "2026-01-06T10:10:00Z"
      period: 5m

    migrationCostClass:
      value:
        string: high
      observedAt: "2026-01-06T09:00:00Z"
      period: 60m

  conditions:
    - type: Ready
      status: "True"
      reason: SamplesFresh
      message: All samples within staleness budget
      lastTransitionTime: "2026-01-05T10:10:05Z"

  observedGeneration: 1
  lastUpdateTime: "2026-01-05T10:10:05Z"
```

## Reservation

```yaml
apiVersion: cortex.cloud/v1alpha1
kind: Reservation
metadata:
  name: <project>-hana
spec:
  resources:
    cpu: 1
    memory: 1TiB
  
  domain: nova
  startTime: <timestamp>
  endTime: <timestamp>
  activeTime: <duration>
  
  projectID: <uuid>
  
  selector:
    isNUMAAlignedHost: "true"

  # everything nova we can't generalize goes here
  nova: {}

status:
  conditions:
    - type: Ready
      status: True|False|Unknown
      reason: ReservationReady
      message: Reservation is successfully scheduled
      lastTransitionTime: "<timestamp>"
```
