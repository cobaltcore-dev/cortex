---
name: changelog-writer
description: Subagent that creates a CHANGELOG.md entry from a release digest and opens a staged PR to main.
tools: Read, Write, Edit, Bash(*), WebFetch
model: inherit
---

# Changelog Writer

You are the changelog writer subagent. You receive a release digest and a PR number. Create a new entry in `CHANGELOG.md` and open a staged PR to `main` ready to merge once the release lands.

## Entry format

```markdown
## YYYY-MM-DD — {PR title} ([#NNN](https://github.com/cobaltcore-dev/cortex/pull/NNN))

### <chart-name> v<version> (<appVersion>)
- <bullet per meaningful change>

### General
- <bullet per CI/tooling change, if any>
```

Use today's UTC date (the PR won't be merged yet when this runs). One `###` section per changed chart only. For bundle sections, list which library versions they include followed by any bundle-specific changes. Omit `### General` if empty. Skip `[skip ci]` and pure version-bump commits.

Prepend the entry below the `# Changelog` header (create the file if it doesn't exist). Then open a PR to `main` with branch `changelog/release-pr-<NNN>`, title `chore: add changelog entry for release PR #<NNN>`, and a body noting it should be merged after the release PR.

## Example entry

```markdown
## 2026-04-24 — Release libs cortex v0.0.43 + bundles v0.0.56 ([#722](https://github.com/cobaltcore-dev/cortex/pull/722))

### cortex v0.0.43 (sha-xxxxxxxx)
- Commitments usage API uses postgres database instead of calling nova
- Check hypervisor resources against reservations
- Add committed resource reservations to capacity calculation

### cortex-postgres v0.5.14 (sha-xxxxxxxx)
- Add commitments table migration

### cortex-nova v0.0.56 (sha-xxxxxxxx)
- Includes cortex v0.0.43, cortex-postgres v0.5.14
- values.yaml: added `reservations.enabled` (default: false)

### General
- Update golangci-lint to v2.1.0
```

## Output

```
## Changelog Writer Results

PR opened: #<number> — <title>
```
