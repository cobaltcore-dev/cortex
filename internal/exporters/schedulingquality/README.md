# cortex-efficiency-exporter

Prometheus exporter measuring scheduling quality and resource efficiency for Cortex-managed OpenStack infrastructure.  
Reads Hypervisor CRDs from the [openstack-hypervisor-operator](https://github.com/cobaltcore-dev/openstack-hypervisor-operator) and (optionally) Nova flavors to compute per-hypervisor and cluster-wide metrics.

## Metrics

### Per-hypervisor

| Metric | Labels | Description |
|--------|--------|-------------|
| `cortex_hypervisor_capacity` | hypervisor, resource, zone, building_block, group | Raw physical capacity |
| `cortex_hypervisor_effective_capacity` | hypervisor, resource, zone, building_block, group | Capacity after overcommit ratios |
| `cortex_hypervisor_allocation` | hypervisor, resource, zone, building_block, group | Current resource allocation |
| `cortex_hypervisor_free` | hypervisor, resource, zone, building_block, group | Free resources (effective_capacity − allocation) |
| `cortex_hypervisor_utilization_ratio` | hypervisor, resource, zone, building_block, group | allocation / effective_capacity |
| `cortex_hypervisor_balance_score` | hypervisor, zone, building_block, group | 1 − σ(fraction_i); 1.0 = balanced, 0.0 = imbalanced |
| `cortex_hypervisor_stranded` | hypervisor, resource, zone, building_block, group | Free capacity unusable due to other dimension exhausted |
| `cortex_hypervisor_instances` | hypervisor, zone, building_block, group | VM count |
| `cortex_hypervisor_maintenance` | hypervisor, zone, building_block, group, maintenance | 1 if in maintenance |

### Cluster-wide

| Metric | Labels | Description |
|--------|--------|-------------|
| `cortex_cluster_capacity` | resource | Total raw capacity |
| `cortex_cluster_effective_capacity` | resource | Total effective capacity |
| `cortex_cluster_allocation` | resource | Total allocation |
| `cortex_cluster_free` | resource | Total free capacity |
| `cortex_cluster_stranded` | resource | Total stranded resources |
| `cortex_cluster_fragmentation_ratio` | resource | stranded / free; 0 = no fragmentation |
| `cortex_cluster_balance_score` | - | Mean balance score across hypervisors |
| `cortex_cluster_hypervisor_count` | - | Number of hypervisors |
| `cortex_cluster_instance_count` | - | Total VM instances |
| `cortex_cluster_mean_utilization_ratio` | resource | Mean utilization across hypervisors |
| `cortex_cluster_mean_tetris_alignment` | - | Mean Tetris alignment score |

## Formulas

### Balance score (Kubernetes BalancedAllocation)

```
fraction_i = allocation_i / effective_capacity_i
score = 1 − stddev(fraction_1, ..., fraction_d)
```

Score of 1.0 means all resource dimensions are consumed proportionally. Score near 0.0 means one dimension is saturated while others are idle.

### Stranded resources (Borg, EuroSys 2015)

Resources on a host that cannot be used because other resources on the same host are depleted. Two modes:

**Exhaustion mode**: If free capacity in dimension _j_ falls below the minimum placeable unit, all free capacity in dimensions _i ≠ j_ is stranded.

**Proportional mode**: When no dimension is exhausted, stranding is computed via the bottleneck dimension:

```
placeable = min(free_cpu / min_cpu, free_mem / min_mem)
stranded_cpu = free_cpu − placeable × min_cpu
stranded_mem = free_mem − placeable × min_mem
```

The minimum placeable unit is derived from the smallest Nova flavor (queried live) or configured via `--min-cpu` and `--min-memory-mb`.

### Fragmentation ratio (Tetris, SIGCOMM 2014; FGD, ATC 2023)

```
fragmentation_ratio = total_stranded / total_free
```

Per resource dimension across the cluster. 0.0 = no fragmentation; 1.0 = all free capacity is stranded.

### Tetris alignment score (Tetris, SIGCOMM 2014)

```
alignment(host) = normalize(free_vec) · normalize(demand_vec)
```

The demand vector is the mean per-instance allocation across the cluster. Score of 1.0 means the host's free capacity shape perfectly matches the workload demand shape

#### TODO

1) Hypervisor overcommit ratio. Future: Dynamic
2) Hypervisor blocking density
3) Number of (live-)migrations
4) Number of "no valid host found". 
5) Packing density (Protean, OSDI 2020): `allocated / (non_empty_hosts × host_capacity)`. Excludes failover-reserved hosts. Different from utilization_ratio.
6) Failover-reserved host count
7) Placement latency histogram per-VM p50/p99 from Nova
 
## Usage

```
cortex-efficiency-exporter \
  --listen-address=:9199 \
  --kubeconfig=$HOME/.kube/config \
  --min-cpu=1 \
  --min-memory-mb=512
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--listen-address` | `:9199` | Metrics endpoint |
| `--metrics-path` | `/metrics` | HTTP path |
| `--kubeconfig` | (in-cluster) | Path to kubeconfig |
| `--disable-nova` | `false` | Skip Nova flavor lookups |
| `--min-cpu` | `1` | Fallback min CPU cores |
| `--min-memory-mb` | `512` | Fallback min memory MB |
| `--scrape-interval` | `30s` | Nova API poll interval |
| `--nova-endpoint` | `$OS_AUTH_URL` | Nova endpoint |
| `--log-level` | `info` | Log verbosity |

### In-cluster deployment

The exporter needs RBAC to list Hypervisor CRDs (cluster-scoped):

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: cortex-efficiency-exporter
rules:
  - apiGroups: ["openstack.sapcloud.io"]
    resources: ["hypervisors"]
    verbs: ["get", "list", "watch"]
```

For Nova integration, mount OpenStack credentials as environment variables (`OS_AUTH_URL`, `OS_USERNAME`, `OS_PASSWORD`, `OS_PROJECT_NAME`, `OS_USER_DOMAIN_NAME`, `OS_PROJECT_DOMAIN_NAME`).

## Example Grafana queries

```promql
# Cluster-wide CPU fragmentation ratio
cortex_cluster_fragmentation_ratio{resource="cpu"}

# Top 10 hypervisors by stranded memory (GB)
topk(10, cortex_hypervisor_stranded{resource="memory"} / 1e9)

# Hypervisors with balance score below 0.5 (high stranding risk)
cortex_hypervisor_balance_score < 0.5

# Mean cluster utilization over time
cortex_cluster_mean_utilization_ratio

# Total stranded CPU cores across cluster
cortex_cluster_stranded{resource="cpu"}

# NUMA cell imbalance detection
cortex_cell_balance_score < 0.6
```

## References

- Verma et al., "Large-scale cluster management at Google with Borg," EuroSys 2015
- Grandl et al., "Multi-Resource Packing for Cluster Schedulers" (Tetris), SIGCOMM 2014
- Weng et al., "Beware of Fragmentation: Scheduling GPU-Sharing Workloads with Fragmentation Gradient Descent" (FGD), ATC 2023
- Ghodsi et al., "Dominant Resource Fairness," NSDI 2011
- Kubernetes `NodeResourcesBalancedAllocation` scoring plugin
