<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Make targets

Day-to-day development goes through the `Makefile` at the repository root. It wraps code generation,
formatting, linting, and tests so you never invoke the underlying tools by hand, and it pins the
versions of those tools so every contributor and CI run behaves identically. This page walks through
the targets you will actually use and the one rule that keeps CI green: generated code must be
committed.

## The default target

Running `make` with no argument runs a phase runner that iterates over `PHASES` in order:

```make
PHASES ?= crds deepcopy lint-fix format test
```

For each phase the runner prints a concise `▶ phase ok` or `▶ phase FAILED` status line, capturing
the full output per phase and only showing it when a phase fails. It stops on the first failure. The
separate `lint` target is not in the default list because `lint-fix` already runs `golangci-lint`
with `--fix`, which reports and fixes in one pass.

This is the "did I break anything" command — run it before opening a pull request.

## The targets, one by one

| Target | What it does |
|---|---|
| `all` | Phase runner that executes `PHASES` in order, printing concise status per phase and stopping on the first failure. |
| `generate` | `deepcopy` + `crds` — regenerate everything derived from the Go types. |
| `crds` | Regenerate CRD manifests from the `api/v1alpha1` types (via `controller-gen`). |
| `deepcopy` | Regenerate the `zz_generated.deepcopy.go` files (via `controller-gen`). |
| `format` | `gofmt -w .` — rewrite Go files in place. |
| `lint` | `golangci-lint run` — report issues without changing files. Not part of the default `make all` run. |
| `lint-fix` | `golangci-lint run --fix` — apply the autofixable subset. |
| `test` | `go test ./...`. |
| `testsum` | The tests via `gotestsum` for a nicer, grouped summary. |

## Tools are pinned and auto-installed

You do not install `controller-gen`, `golangci-lint`, or `gotestsum` yourself. The Makefile installs
each pinned version into `./bin` (`LOCALBIN`) on first use and runs that copy, so the version is the
same on your machine and in CI:

| Tool | Pinned version |
|---|---|
| `controller-gen` | v0.23.0 |
| `golangci-lint` | v2.15.0 |
| `gotestsum` | v1.13.0 |

`./bin` is git-ignored; deleting it just triggers a re-install on the next `make`.

> [!IMPORTANT]
> Regenerated CRDs and deepcopy code **must be committed**. The `lint.yaml` workflow (see
> [CI/CD and packaging](03-cicd-and-packaging.md)) runs `make crds deepcopy lint-fix` and fails the
> build if that produces a git diff — its way of enforcing that what is generated in CI matches what
> you committed. So after changing anything under `api/v1alpha1`, run `make generate` (or `make all`)
> and commit the result.

The generated artifacts are load-bearing everywhere else in the book. `make crds` produces the very
`cortex.cloud/v1alpha1` CustomResourceDefinitions that the Go types under `api/v1alpha1/*_types.go`
define (rendered into `helm/library/cortex/files/crds/`) and that the `cortex-crds` bundle ([The Helm charts](04-helm-charts.md)) installs;
`make deepcopy` produces the runtime plumbing every controller needs. The Makefile is simply the
front door to keeping those in sync with the Go types you edit when you
[extend Cortex](../02-external-scheduler-api/07-extending-cortex.md).

## Next

[Prev: The Helm charts](04-helm-charts.md) · [Next: Postgres »](06-postgres.md)
