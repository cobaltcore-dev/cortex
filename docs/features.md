# Features

## Knowledge Database

**Datasources**: Cortex supports connecting to datasources such as OpenStack or Prometheus and making this information available to knowledge extraction and scheduling.

**Knowledge Extraction**: From the provided datasources, Cortex can extract knowledge about the infrastructure and applications running on it. This knowledge is then used to inform scheduling decisions.

**KPIs**: Additionally, Cortex provides the capability to export extracted knowledge as metrics to Prometheus, so-called KPIs. This allows to collect information over a longer time span that can be re-ingested as a datasource.

## Reservations

Cortex can reserve space for upcoming or committed workloads based on services like [Limes](https://github.com/sapcc/limes).

## Scheduling

**Decision Making**: Cortex uses the extracted knowledge and reservations to make informed scheduling decisions for workloads. It can help placing openstack or kubernetes workloads based on the current state of the infrastructure and existing reservations.

**Descheduling**: In addition, Cortex can also deschedule workloads based on defined policies, e.g., to free up resources for higher priority workloads.
