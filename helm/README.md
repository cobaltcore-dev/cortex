# Cortex Helm Charts

This directory contains the Helm charts for deploying Cortex, organized into a layered architecture that provides separation of concerns and reusability across different scheduling domains.

## Architecture Overview

The Cortex helm chart architecture follows a three-tier structure:

```
Bundle Charts (umbrella charts)
    ↓ includes
Operator Charts (from dist/chart directories), Library Charts (shared components)
    + for local development
Dev Charts (development tooling)
```

This results in the following directory structure:

```
helm/
├── bundles/                   # Umbrella charts for each domain
│   ├── cortex-nova/             # Nova scheduling domain
│   ├── cortex-cinder/           # Cinder scheduling domain
│   ├── cortex-manila/           # Manila scheduling domain
│   ├── cortex-ironcore/         # IronCore scheduling domain
│   └── cortex-crds/             # CRDs for all operators
├── library/                   # Shared library charts
│   ├── cortex-alerts/           # Common alerting infrastructure
│   └── cortex-postgres/         # PostgreSQL database
├── dev/                       # Development-only charts
│   └── cortex-prometheus-operator/  # Local monitoring stack
*/dist/chart/                  # Generated operator charts
```

## Chart Types

### Bundle Charts (`bundles/`)

Bundle charts are **umbrella charts** that represent complete deployments for specific scheduling domains. They aggregate operator charts and library charts into deployable units.

**Available bundles:**
- `cortex-nova` - Nova compute scheduling domain
- `cortex-cinder` - Cinder block storage scheduling domain
- `cortex-manila` - Manila shared filesystem scheduling domain
- `cortex-ironcore` - IronCore scheduling domain (compute, ...)
- `cortex-crds` - Custom Resource Definitions for all operators

### Operator Charts (from `*/dist/chart/`)

Operator charts contain the core Kubernetes operators built from the Go modules. These are **not stored in the helm/ directory** but are generated in each service's `dist/chart` directory as they are [Kubebuilder](https://book.kubebuilder.io/reference/generating-crd) scaffolds.

**Available operators:**
- `cortex-knowledge-operator` (from `knowledge/dist/chart/`)
- `cortex-scheduling-operator` (from `scheduling/dist/chart/`)
- `cortex-reservations-operator` (from `reservations/dist/chart/`)

### Library Charts (`library/`)

Library charts provide **shared, reusable components** that are consumed by bundle charts as dependencies.

**Available library charts:**
- `cortex-alerts` - Common alerting infrastructure and templates
- `cortex-postgres` - PostgreSQL database deployment with monitoring

**Integration with bundles:**
- Library charts are **included as dependencies** in bundle Chart.yaml files
- Provide common infrastructure components used across multiple domains
- Reduce duplication of common services like databases and monitoring
- Enable consistent configuration across different scheduling domains

### Dev Charts (`dev/`)

Dev charts support **local development and testing** but are not included in production releases.

**Available dev charts:**
- `cortex-prometheus-operator` - Prometheus operator setup for local development

## Usage Patterns

### Production Deployment
1. Deploy CRDs first: `helm install cortex-crds bundles/cortex-crds/`
2. Deploy domain-specific bundle: `helm install cortex-nova bundles/cortex-nova/`

### Development Setup
1. Deploy monitoring: `helm install prometheus dev/cortex-prometheus-operator/`
2. Deploy CRDs: `helm install cortex-crds bundles/cortex-crds/`
3. Deploy and test bundles: `helm install cortex-nova bundles/cortex-nova/`

## Versioning

We use [semantic versioning](https://semver.org/) for all Helm charts. Each chart maintains independent versioning.
