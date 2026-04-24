---
allowed-tools: Read, Write, Edit, Bash(*), WebSearch, WebFetch
description: Create a changelog entry for a merged release PR and open a PR to main. Usage: /update-changelog PR_NUMBER
---

A release PR (#$ARGUMENTS) was merged into the `release` branch. Create a changelog entry for it and open a PR to `main`.

To build the entry, use the PR's commit subjects (no diffs) and the changed Helm charts as your sources. Only include charts whose Chart.yaml actually changed in this PR.

Format each entry as:

## {merged_at date in UTC, formatted YYYY-MM-DD} — {PR title} ([#NNN](https://github.com/cobaltcore-dev/cortex/pull/NNN))

One `###` section per changed chart: `### <chart-name> v<version> (<appVersion>)`
Under each section, bullet the commit subjects that relate to that chart.

Attribution: for each commit, inspect its changed files with `git show --name-only <sha>` and map to the chart whose files were touched:

- `postgres/**` → cortex-postgres
- `cmd/shim/**` or `internal/shim/**` → cortex-shim
- `helm/bundles/cortex-<name>/**` → that specific bundle chart
- anything else → cortex (core)

Commits that only touch CI, docs, or tooling go into `### General`. Skip commits containing "[skip ci]" or that are pure version-bump message.

For bundle chart sections (helm/bundles/*), add a note listing which library chart versions they now include (read the bundle's Chart.yaml dependencies). Then inspect the actual diff of the bundle's own files with `git show <sha> -- helm/bundles/<name>/` for any commit that touched that bundle, and surface specific changes:

- **values.yaml** changes: call out new, removed, or renamed keys and changed defaults
- **templates/** or **crds/** changes: call out added, removed, or modified resources by kind and name

Prepend the new entry below the `# Changelog` header in `CHANGELOG.md` (create the file if it doesn't exist). Then open a PR to `main` referencing this release PR.

## Example

```markdown
## 2026-04-24 — Release libs cortex v0.0.43 + bundles v0.0.56 ([#722](https://github.com/cobaltcore-dev/cortex/pull/722))

### cortex v0.0.43 (sha-xxxxxxxx)
- Commitments usage API uses postgres database instead of calling nova
- Check hypervisor resources against reservations
- Add committed resource reservations to capacity calculation

### cortex-postgres v0.5.14 (sha-xxxxxxxx)
- Add commitments table migration

### cortex-nova v0.0.56 (sha-xxxxxxxx)
- Update nova bundle for committed reservations support

### cortex-manila v0.0.56 (sha-xxxxxxxx)
- Update manila bundle for committed reservations support

### General
- Update golangci-lint to v2.1.0
```
