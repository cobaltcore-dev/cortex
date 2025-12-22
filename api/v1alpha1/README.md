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
      # Optional parameters to configure the filter.
      params:
        key: value
  # Ordered weighers should be completed successfully in order to ensure optimal placement and scheduling.
  # Failures raise a warning-level alert.
  weighers:
    - name: weigher1
      description: Short, human-readable description.
      # Optional parameters to configure the weigher.
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
spec: {}
status: {}
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
