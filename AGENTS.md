<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Instructions for Agents

## Context

You are developing code in the open-source project github.com/cobaltcore-dev/cortex.

Cortex is a modular and extensible service for initial placement and scheduling in cloud-native environments covering workloads such as compute, storage, network, and other scheduling domains.

It improves resource utilization and operational performance by making smart placement decisions based on the current state of the environment and defined constraints and objectives.

Cortex is written in Golang and is designed for production-scale deployments using algorithmic approaches to balance decision quality, execution efficiency, and maintaining a low resource footprint.

## Best Practices

All code files must contain this license header:
```go
// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0
```

General:
- Keep it concise and always focus on good code quality. We go to production
- We are on modern Golang, so you no longer need `interface{}` and use `any` instead
- Similarly, you no longer have to capture loop variables in closures, as this is now the default behavior in Go
- Don’t document trivial steps you do and avoid unnecessary empty lines between code segments
- When adding imports, keep in mind that the autoformatter will remove them if you don't use them

Testing:
- `fmt.Errorf` should not be used when there are no parameters. Use `errors.New`
- Errors should always be lowercase like `errors.New("this is an error")`
- Ideally test files should be short and contain only the necessary cases
- Avoid creating testing libraries, keep helper functions in the same file as the tests that use them
- Use golang native testing whenever possible, avoid using Ginkgo or testify
- Don't test for the existence of interface methods
- If applicable, use struct based test cases, but limit yourself to the most relevant cases

Helm charts:
- Note the `# from: file://../../library/cortex-postgres` comment in `Chart.yaml` files, this is required and should point to the local chart path

## Repository Structure

Code:
- `cmd/main.go` is the entry point for the manager, which starts the controllers and webhooks
- `api/v1alpha1` is where the CRD specs of cortex lives
- `api/external` contains messages sent to cortex via http from external openstack services
- `internal/scheduling` contains the logic for scheduling in different cloud domains
- `internal/knowledge` has all the logic for feature extraction and raw data downloads from sources like prometheus and openstack
- `pkg` is the code that is very non-cortex-specific and can be used across other projects as well

Deployment:
- `helm/library` contains a generic cortex setup, i.e. the manager and its dependencies
- `helm/dev` contains charts that can deploy cortex dependencies that a typical production cluster already has, such as a fine-tuned kube-prometheus-stack for monitoring
- `helm/bundles` here are the charts that stylize the library chart into a deployment for a specific domain, for example a bundle for deploying cortex with openstack nova
- In the `helm` folders there are also helpers for syncing helm dependencies which are used by the tiltfile for local development and our ci pipelines to replace oci dependencies with local paths

Tooling:
- `tools` contains miscallaneous tools for development, which should typically **not** be used by agents

Documentation:
- `docs` contains documentation for cortex, which should be written in markdown

## Tooling

Before finishing your task, you should always ensure local tests and lints are passing:
- `make` regenerates CRDs and deepcopy methods, runs tests, and performs lints
- Avoid running `make` when you don't want to apply your crd changes just yet
- `make lint` runs golangci-lint, `make lint-fix` runs golangci-lint with `--fix`
- `make test` runs all the unit tests with `go test ./...`
- If you are struggling with the Makefile, you can use `make help` to get a list of all available commands and their descriptions
