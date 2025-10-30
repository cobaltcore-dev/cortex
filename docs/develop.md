# Development Guide

This guide provides an overview of the Cortex development process, including setting up the development environment, developing plugins, and testing the service.

## Golang Tooling, Testing, Linting

Cortex is developed using the Go programming language. To get started with the development, follow these steps:

**Install Golang:** Ensure you have Go installed on your machine by following the instructions [here](https://golang.org/doc/install).

**Install golangci-lint:** Cortex uses `golangci-lint` for linting the code. You can install it by following the instructions [here](https://golangci-lint.run/docs/welcome/install/).

Run `make` in your terminal from the cortex root directory to perform linting and testing tasks.

## Helm Charts

Helm charts bundle the application into a package, containing all the [Kubernetes](https://kubernetes.io/docs/tutorials/hello-minikube/) resources needed to run the application. The configuration for the application is specified in the [Helm `values.yaml`](cortex.secrets.example.yaml).

Read [the helm chart structure documentation](helm/README.md) for more information about the structure of the Helm charts used in this repository.

For local development, use the `cortex.secrets.example.yaml` file to override the default Helm values. You can write your OpenStack credentials here to include credentials, such as SSO certificates to access Prometheus metrics or OpenStack credentials to authenticate with Keystone.

## Tilt Setup

[Tilt](https://docs.tilt.dev/) acts like a makefile/dockerfile for Kubernetes services, allowing you to spin up something in a local Kubernetes cluster with all the services needed to run Cortex. Which kubernetes cluster you use is up to you; common choices are [Minikube](https://minikube.sigs.k8s.io/docs/) or [Kind](https://kind.sigs.k8s.io/docs/user/quick-start/).

You need to tell Tilt where to find your custom configuration file. First, export the path of your secrets file with `export TILT_VALUES_PATH="<path-to-cortex.secrets.yaml>"`. Then, start the cluster and the tilt setup:

```bash
export TILT_VALUES_PATH="${HOME}/cortex.secrets.yaml" && tilt up
```

The service will be accessible at [http://localhost:10350/](http://localhost:10350/).

## Prometheus Metrics and Alerts

[Prometheus](https://prometheus.io/docs/prometheus/latest/getting_started/) is used for monitoring and alerting.

The Tilt setup comes with a [Prometheus operator](https://github.com/prometheus-community/helm-charts/tree/d20c3db997ac3d1b225a8c8b8cd407b5d63fbae9/charts/kube-prometheus-stack) that runs a Prometheus instance for local testing. You can access the Prometheus dashboard by clicking on Prometheus in the Tilt dashboard. From there you can explore the metrics collected from the various Cortex services.

## Dashboards

The Tilt setup includes a Plutono instance, a fork of Grafana used to display dashboards for the services. Documentation for Plutono can be found [here](https://grafana.com/docs/grafana/v7.5/).

By using the Tilt dashboard, you can click on Plutono and then access the dashboards link. You can modify the dashboards as needed to display the metrics you want, and from the Plutono dashboard, you can save your progress by exporting the updated dashboard to JSON file.
