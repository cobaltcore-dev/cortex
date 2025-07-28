Cortex Helm Charts
==================

This directory contains the Helm Charts required to install Cortex.

## Structure

Helm charts are organized into three main directories:
- `bundles`: Contain the Helm charts for each supported scheduling domain, such as `cortex-manila`, `cortex-nova`. They are the highest level of abstraction and part of the Cortex release.
- `library`: Contain shared library and common Helm charts, such as `cortex-core`, `cortex-postgres` used in the bundles.
- `dev`: Contain development-specific Helm charts. These are not bundles into releases but are used for local development and testing.

```
.
└── helm
    │
    ├── bundles
    │       ├── cortex-nova
    │       │       ├── Chart.yaml
    │       │       │       ├── lib/cortex-core
    │       │       │       ├── lib/cortex-postgres
    │       │       │       └── ...
    │       │       └── < Scheduling domain specific configuration, alerts, dashboards, etc. >
    │       ├── cortex-manila
    │       │       └── ...
    │       └── ...
    ├── library
    │       ├── cortex-core
    │       ├── cortex-postgres
    │       └── ...
    └── dev
            ├── cortex-prometheus-operator
            └── ...
```

## Versioning

We use [semantic versioning](https://semver.org/) for our Helm charts.
Each chart has its own `Chart.yaml` file that specifies the version of the chart and its dependencies.

