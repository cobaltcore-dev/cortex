# Pipeline for initial placement of compute workloads within KVM

| Metric                      | Optimization Behaviour   | Metric                     | Filter / Weigher | Algorithm / Implementation | Description                                                              |
|-----------------------------|--------------------------|----------------------------|------------------|----------------------------|--------------------------------------------------------------------------|
| CPU resources               | Enforce                  | VM flavor, Host specs      | Filter           |                            | Ensure CPU resource requirements as per flavor are met.                  |
| Memory resources            | Enforce                  | VM flavor, Host specs      | Filter           |                            | Ensure memory resource requirements as per flavor are met.               |
| CPU compatibility           | Enforce                  | Host metadata              | Filter           |                            | Ensure VMs are placed on hosts with compatible CPU features              | 
| Tenant Isolation            | Isolate                  | Host, VM metadata (traits) | Filter           |                            | Ensure VMs from specific tenants are placed on designated hosts.         |
| Compute Host Status         | Validate                 | Host status                | Filter           |                            | Ignore hosts in maintenance, error or states that don't allow VMs.       |
| Compute Capabilities        | Match                    | VM extra specs, Host specs | Filter           |                            | Match extra specs to host capabilities.                                  |
| Large VM Host Suitability   | Enforce                  | VM, Host, Configuration    | Filter           |                            | Place large VMs on large hosts, small VMs on small hosts.                |
| Image Property Requirements | Match                    | VM spec.image, Host        | Filter           |                            | Filter hosts based on image properties.                                  |
| Server Group Anti-Affinity  | Separate                 |                            |                  |                            |                                                                          |
| Server Group Affinity       | Co-locate                |                            |                  |                            |                                                                          |
|                             |                          |                            |                  |                            |                                                                          |
| CPU Noisy Neighbor          | Minimize                 |                            | Weigher          |                            | Anti-affinity for VMs with elevated CPU utilization at similar times     |
| Memory Noisy Neighbor       | Minimize                 |                            | Weigher          |                            | Anti-affinity for VMs with elevated memory utilization at similar times  |
| Network Noisy Neighbor      | Minimize                 |                            | Weigher          |                            | Anti-affinity for VMs with elevated network utilization at similar times |
| Storage Noisy Neighbor      | Minimize                 |                            | Weigher          |                            | Anti-affinity for VMs with elevated storage utilization at similar times |

## General Purpose additions

Primary objective: Load balancing and performance optimization.


## HANA additions

Primary objective: Bin-packing based on memory resources.

| Metric                      | Optimization Behaviour   | Metric                    | Filter / Weigher   | Algorithm / Implementation | Description            |
|-----------------------------|--------------------------|---------------------------|--------------------|----------------------------|------------------------|
| Memory                      | Bin-packing              | VM flavor, Host specs     | Weigher            | Best Fit Decreasing        | Ensure bin-packing of  |



# VMware specific additions

| Metric                      | Optimization Behaviour | Metric                     | Filter / Weigher | Algorithm / Implementation | Description |
|-----------------------------|------------------------|----------------------------|------------------|----------------------------|-------------|
| Resize vCPU Limits          | Enforce                |                            |                  |                            |             |
|                             |                        |                            |                  |                            |             |
