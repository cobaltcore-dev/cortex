# APIs

## Custom Resource Definitions (CRDs)

With cortex CRDs you can control cortex and see which actions are performed.

### Datasources

```bash
kubectl get datasource
```

With datasources you can configure which raw data is ingested into the cortex system and into which database this data is ingested. For example, if you want cortex to download a metric from a prometheus endpoint, you create a new datasource and configure into which database this data should be downloaded. Additionally, you can configure in which interval this data should be synced.

When cortex sees new datasources, it will start downloading and expose how many objects were downloaded in the datasource's status. If cortex encounters an issue syncing, it will expose this as a status condition on the status objects as well. In this way you can keep track of which datasources have been synced, and which not. Use the timestamps provided by the resource to check if the data is recent enough to be processed further.

### Knowledges

```bash
kubectl get knowledge
```

Knowledges provide condensed information from raw data. In the knowledge spec, you define which underlying datasources are needed to extract this information, and put a reference to the cortex plugin which implements the extraction logic. Knowledges can also depend on other knowledges. Using these dependencies cortex will automatically update knowledges whose underlying data has changed.

Compared to datasources, knowledges represent only condensed information and their payload is directly stored in the kubernetes resource status after the extraction has completed. This allows other cortex components to fetch these objects in a timely manner to reuse them for scheduling or analysis. Based on the knowledge status other components of cortex can check if the feature extraction has already completed and if the data can be used.

### KPIs

```bash
kubectl get kpis
```

KPIs expose metrics for knowledges. They contain a reference to the prometheus metric implementation which ingests the extracted knowledge object(s) and outputs generated metrics. This allows the cortex knowledge database to expose counters, gauges, histograms and more.

Once the resource is created cortex will mount the KPI into the prometheus metrics endpoint and expose the implemented metrics.
