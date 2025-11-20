# APIs

## Custom Resource Definitions (CRDs)

With cortex CRDs you can control cortex and see which actions are performed.

### Knowledge Database CRDs

#### Datasources

```bash
kubectl get datasource
```

With datasources you can configure which raw data is ingested into the cortex system and into which database this data is ingested. For example, if you want cortex to download a metric from a prometheus endpoint, you create a new datasource and configure into which database this data should be downloaded. Additionally, you can configure in which interval this data should be synced.

When cortex sees new datasources, it will start downloading and expose how many objects were downloaded in the datasource's status. If cortex encounters an issue syncing, it will expose this as a status condition on the status objects as well. In this way you can keep track of which datasources have been synced, and which not. Use the timestamps provided by the resource to check if the data is recent enough to be processed further.

#### Knowledges

```bash
kubectl get knowledge
```

Knowledges provide condensed information from raw data. In the knowledge spec, you define which underlying datasources are needed to extract this information, and put a reference to the cortex plugin which implements the extraction logic. Knowledges can also depend on other knowledges. Using these dependencies cortex will automatically update knowledges whose underlying data has changed.

Compared to datasources, knowledges represent only condensed information and their payload is directly stored in the kubernetes resource status after the extraction has completed. This allows other cortex components to fetch these objects in a timely manner to reuse them for scheduling or analysis. Based on the knowledge status other components of cortex can check if the feature extraction has already completed and if the data can be used.

#### KPIs

```bash
kubectl get kpis
```

KPIs expose metrics for knowledges. They contain a reference to the prometheus metric implementation which ingests the extracted knowledge object(s) and outputs generated metrics. This allows the cortex knowledge database to expose counters, gauges, histograms and more.

Once the resource is created cortex will mount the KPI into the prometheus metrics endpoint and expose the implemented metrics.

### Scheduling CRDs

#### Steps

```bash
kubectl get steps
```

Steps provide scheduling logic such as filtering, weighing, or descheduling. Each step depends on a selected set of knowledges, and provides a reference to the step's implementation. Furthermore, the step resource allows to configure custom parameters or options which can be used to fine-tune the step's behavior.

Once created, cortex will check if the underlying step data is available and mark this step as ready. This allows the pipeline into which the step is mounted to react appropriately to changes in the step's dependencies.

#### Pipelines

```bash
kubectl get pipelines
```

Pipelines bundle scheduling steps together. As part of a pipeline, steps can be marked as mandatory or not. This determines if the pipeline becomes unready if a step is missing data.

The state of the pipeline is propagated automatically through the states of its steps. Check the pipeline state object to determine if the pipeline can currently be executed or not.

#### Decisions

```bash
kubectl get decisions
```

Decisions are generated when pipelines are executed with an appropriate request, such as an initial placement request for a virtual machine. Decisions contain the input data necessary to determine a valid workload placement and a reference to the external resource that is managed, such as a virtual machine id.

In its state, decisions reflect the outcome of the pipeline execution, for example the generated weights for each scheduling step. This outcome is reflected back to the caller of the pipeline. In addition, decisions provide a human-readable explanation why the workload was placed at this specific location.

#### Reservations

```bash
kubectl get reservations
```

Reservations take away space for a workload that is expected to be spawned in the future. They specify which kind of resource is allocated, how much, and on which workload host. During scheduling, reservations are considered in addition to the running workload.

The reservation state reflects where this reservation is currently placed as outcome of a pipeline decision.

#### Deschedulings

```bash
kubectl get deschedulings
```

Deschedulings are triggered when a descheduler pipeline containing descheduler steps detects workloads to move away from their current host. They provide an unambiguous reference to the resource to be descheduled.

The descheduling state tracks the progress of a descheduling, i.e. if the workload is just beginning to be descheduled or if the process was already completed, successfully or unsuccessfully.
