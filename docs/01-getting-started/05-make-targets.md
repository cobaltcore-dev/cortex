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

Running `make` with no argument runs the full local loop:

```make
all: crds deepcopy lint-fix format lint test
```

In order, that regenerates CRDs and deepcopy code, applies lint autofixes, formats, lints, and runs
the tests. It is the "did I break anything" command — run it before opening a pull request.

## The targets, one by one

| Target | What it does |
|---|---|
| `all` | The full loop above. The default. |
| `generate` | `deepcopy` + `crds` — regenerate everything derived from the Go types. |
| `crds` | Regenerate CRD manifests from the `api/v1alpha1` types (via `controller-gen`). |
| `deepcopy` | Regenerate the `zz_generated.deepcopy.go` files (via `controller-gen`). |
| `format` | `gofmt -w .` — rewrite Go files in place. |
| `lint` | `golangci-lint run` — report issues without changing files. |
| `lint-fix` | `golangci-lint run --fix` — apply the autofixable subset. |
| `test` | `go test ./...`. |
| `testsum` | The tests via `gotestsum` for a nicer, grouped summary. |

## Tools are pinned and auto-installed

You do not install `controller-gen`, `golangci-lint`, or `gotestsum` yourself. The Makefile installs
each pinned version into `./bin` (`LOCALBIN`) on first use and runs that copy, so the version is the
same on your machine and in CI:

| Tool | Pinned version |
|---|---|
| `controller-gen` | v0.22.0 |
| `golangci-lint` | v2.13.2 |
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

[Prev: The Helm charts](04-helm-charts.md) · [Next: Postgres and tooling »](06-postgres-and-tooling.md)
