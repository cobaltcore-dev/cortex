```yaml
apiVersion: cortex.cloud/v1alpha1
kind: Pipeline
metadata:
  name: nova-general-purpose
spec:
  # Ordered filters must be completed successfully in order to ensure valid placement and scheduling.
  filters:
    - name: 
      description:
      params:
        a: b
  # Ordered weighers should be completed successfully in order to ensure optimal placement and scheduling.
  weighers:
    - name: 
      description:
      params:
        c: d
```


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
  
  nova:
    // everything nova we can't generalize goes here
```
