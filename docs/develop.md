# Development Guide

This guide provides an overview of the Cortex development process, including setting up the development environment, developing plugins, and testing the service.

## Golang Tooling, Testing, Linting

Cortex is developed using the Go programming language. To get started with the development, follow these steps:

**Install Golang:** Ensure you have Go installed on your machine by following the instructions [here](https://golang.org/doc/install).

**go-makefile-maker:** This tool, available at [github.com/sapcc/go-makefile-maker](https://github.com/sapcc/go-makefile-maker), automates the configuration of linters, workflows, and more. Run `make check` in your terminal to perform linting tasks and ensure all files have appropriate license headers. This will install go-makefile-maker if it is not already installed.

You can also simply use your own golang tooling to lint and test the code. For example, the following will run tests more quickly than with `make check` since it does not consider the code coverage:

```bash
go test ./...
```

## Helm Charts

Helm charts bundle the application into a package, containing all the [Kubernetes](https://kubernetes.io/docs/tutorials/hello-minikube/) resources needed to run the application. The configuration for the application is specified in the [Helm `values.yaml`](helm/cortex/values.yaml) file.

### `cortex` Helm Chart

The `cortex` Helm chart includes the core Go services of this repository.

### `postgres` Helm Chart

The `postgres` Helm chart provides the database setup used by these services.

### `prometheus` Helm Chart

The `prometheus` Helm chart provides a Prometheus setup for local testing and alerting rules for the service. For local development, this chart also contains a [Prometheus operator](https://github.com/prometheus-community/helm-charts/tree/d20c3db997ac3d1b225a8c8b8cd407b5d63fbae9/charts/kube-prometheus-stack), which simplifies the deployment of Prometheus, metrics, and alerts to the Kubernetes cluster.

### `owner-info` Helm Chart

Additionally, you will see an `owner-info` Helm dependency in the charts. This chart adds a resource to the Kubernetes cluster that describes who owns this service and who to contact in case something goes wrong.

## Service Configuration

The service includes a [values.yaml](helm/cortex/values.yaml) file that not only specifies how Kubernetes resources should be deployed but also outlines the service configuration.

Within the `values.yaml` file, there is a `conf` key, and the contents of this key are mounted into the service as a configuration file. The configuration file also includes settings for the syncer, feature extractor, and scheduler plugins.

For local development, use the `cortex.secrets.example.yaml` file to override the default Helm values. You can write your OpenStack credentials here to use the OpenStack syncer plugin or include other credentials, such as SSO certificates to access Prometheus metrics.

## Tilt Setup

[Tilt](https://docs.tilt.dev/) acts like a makefile/dockerfile for Kubernetes services, allowing you to spin up a local Kubernetes cluster with all the services needed to run Cortex. The recommended method for local development is to use [minikube](https://minikube.sigs.k8s.io/docs/start/).

First, export the path of your secrets file with `export TILT_VALUES_PATH="<path-to-cortex.secrets.yaml>"`. Then, start the cluster and the tilt setup:

```bash
export TILT_VALUES_PATH="${HOME}/cortex.secrets.yaml" && minikube start && tilt up
```

The service will be accessible at [http://localhost:10350/](http://localhost:10350/).

## Simulating a Nova Request

To simulate Nova requests to your Cortex instance in Tilt, you can run the following command:
```bash
go run commands/fillup/fillup.go
```

The script will show where random new VMs would be placed.

## Prometheus Metrics and Alerts

[Prometheus](https://prometheus.io/docs/prometheus/latest/getting_started/) is used for monitoring and alerting.

The Tilt setup comes with a [Prometheus operator](https://github.com/prometheus-community/helm-charts/tree/d20c3db997ac3d1b225a8c8b8cd407b5d63fbae9/charts/kube-prometheus-stack) that runs a Prometheus instance for local testing. You can access the Prometheus dashboard by clicking on Prometheus in the Tilt dashboard. If needed, you can modify the alerting rules contained in the [Prometheus Helm chart](helm/cortex/charts/cortex-prometheus/), which deploys a `PrometheusRule` Kubernetes resource to specify the alerts to add to the Prometheus instance. Metrics from the services are automatically scraped by the Prometheus instance, and for this purpose, a `ServiceMonitor` Kubernetes resource is deployed to the cluster, instructing the Prometheus operator to scrape metrics from the service.

## Dashboards

The Tilt setup includes a Plutono instance, a fork of Grafana used to display dashboards for the services. Documentation for Plutono can be found [here](https://grafana.com/docs/grafana/v7.5/).

By using the Tilt dashboard, you can click on Plutono and then access the dashboards link. You can modify the dashboards as needed to display the metrics you want, and from the Plutono dashboard, you can save your progress by exporting the updated dashboard to JSON and overriding the [dashboards.json](plutono/provisioning/dashboards/cortex.json) file.
