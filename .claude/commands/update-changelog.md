---
allowed-tools: Read, Write, Edit, Bash(*), WebSearch, WebFetch
description: Create a changelog entry for a merged release PR and open a PR to main. Usage: /update-changelog PR_NUMBER
---

A release PR (#$ARGUMENTS) was merged into the `release` branch. Create a changelog entry for it and open a PR to `main`.

To build the entry, use the PR's commit subjects (no diffs) and the changed Helm charts as your sources. Only include charts whose Chart.yaml actually changed in this PR.

Format each entry as:

## YYYY-MM-DD — <PR title> ([#NNN](https://github.com/cobaltcore-dev/cortex/pull/NNN))

One `###` section per changed chart: `### <chart-name> v<version> (<appVersion>)`
Under each section, bullet the commit subjects that relate to that chart.

Attribution: "postgres" → cortex-postgres; "nova"/"manila"/"cinder"/"placement"/"shim"/"bundle" → the matching bundle chart; unattributable commits → `### General` at the end.
Skip commits containing "[skip ci]" or that are pure version-bump messages.

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
