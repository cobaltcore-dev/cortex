# Features

This guide provides an overview of Cortex' scheduling features, including the metrics used, data sources, and the implementation status of each feature.

Legend:
📆 = Planned
✅ = Implemented

## KVM Scheduling

The following initial placement scheduler plugins are available for KVM:

|     | Metric                          | Data Sources                                                  | Implementation | Description                                                                                |
| --- | ------------------------------- | ------------------------------------------------------------- | -------------- | ------------------------------------------------------------------------------------------ |
| 📆   | Available CPU resources         | VM flavor vcpus, available vcpus on host, overcommit factor   | Filter         | Ensure CPU resource requirements as per flavor are met.                                    |
| 📆   | Available memory resources      | VM flavor memory, available memory on host, overcommit factor | Filter         | Ensure memory resource requirements as per flavor are met.                                 |
| 📆   | CPU compatibility               | VM CPU features, host CPU features                            | Filter         | Ensure VMs are placed on hosts with compatible CPU features.                               |
| 📆   | Tenant Isolation                | VM flavor spec, host traits                                   | Filter         | Ensure VMs from specific tenants are placed on designated hosts.                           |
| 📆   | Compute Host Status             | Host status                                                   | Filter         | Ignore hosts in maintenance, error or states that don't allow VMs.                         |
| 📆   | Compute Capabilities            | VM extra specs, host capabilities                             | Filter         | Match extra specs to host capabilities.                                                    |
| 📆   | Image Property Requirements     | VM image properties, host properties                          | Filter         | Filter hosts based on image properties.                                                    |
| 📆   | Large VM Host Suitability       | VM flavor, available space on host                            | Filter         | Place large VMs on large hosts, small VMs on small hosts.                                  |
| 📆   | Avoid Contended Hosts (CPU)     | Host CPU metrics over time                                    | Weigher        | Anti-affinity to hosts with high cpu contention (steal time).                              |
| ✅   | Avoid Overloaded Hosts (CPU)    | Host CPU metrics over time                                    | Weigher        | Anti-affinity to hosts with high cpu load.                                                 |
| ✅   | Avoid Overloaded Hosts (Memory) | Host memory metrics over time                                 | Weigher        | Anti-affinity to hosts with high memory utilization.                                       |
| ✅   | Flavor Binpacking               | VM flavor, available space on host                            | Weigher        | Best fit decreasing placement on hosts to maximize the number of placeable vms per flavor. |
| 📆   | Flavor-Host Affinity            | VM flavor, host traits                                        | Weigher        | Pull specific flavors to hosts with specific traits, e.g. pull HANA VMs to HANA hosts.     |
| 📆   | Server Group Anti-Affinity      | VM server group, all VMs                                      | Weigher        | Move server groups apart from each other.                                                  |
| 📆   | Server Group Affinity           | VM server group, all VMs                                      | Weigher        | Pull servers within a group together.                                                      |
| 📆   | CPU Noisy Neighbor              | All VMs, VM CPU metrics over time                             | Weigher        | Anti-affinity for VMs with elevated CPU utilization at similar times.                      |
| 📆   | Memory Noisy Neighbor           | All VMs, VM memory metrics over time                          | Weigher        | Anti-affinity for VMs with elevated memory utilization at similar times.                   |
| 📆   | Network Noisy Neighbor          | TBD                                                           | Weigher        | Anti-affinity for VMs with elevated network utilization at similar times.                  |
| 📆   | Storage Noisy Neighbor          | TBD                                                           | Weigher        | Anti-affinity for VMs with elevated storage utilization at similar times.                  |

## VMware Scheduling

The following initial placement scheduler plugins are available for VMware:

|     | Metric                      | Data Sources                              | Implementation | Description                                                                                |
| --- | --------------------------- | ----------------------------------------- | -------------- | ------------------------------------------------------------------------------------------ |
| ✅   | Flavor Binpacking           | VM flavor, available space on host        | Weigher        | Best fit decreasing placement on hosts to maximize the number of placeable vms per flavor. |
| ✅   | Avoid Contended Hosts       | Host CPU metrics over time                | Weigher        | Anti-affinity to hosts with high cpu contention (steal time).                              |
| ✅   | Noisy Project Anti-Affinity | VM project, metrics over time for all VMs | Weigher        | Pull VMs apart which are known to belong to a noisy project.                               |
| ?   | Resize vCPU Limits          | TBD                                       | TBD            | TBD                                                                                        |

