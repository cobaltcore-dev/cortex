---
allowed-tools: Read, Write, Edit, Bash(*), WebSearch, WebFetch, Agent
description: Release orchestrator — builds a digest of what changed in a release PR, opens a changelog PR, and references the bump PR. Usage: /release PR_NUMBER
---

# Release Orchestrator

Your job is to orchestrate the release process for a given PR. This involves analyzing the PR's commits and changed files to build a structured digest of what changed, determining if there are any breaking changes, preparing a changelog, opening a PR to bump chart versions if needed, and updating the original PR description with the changelog and references to the new PRs.

---

## Phase 1: Collect — Build the release digest

1. Fetch PR metadata:
   ```
   gh pr view $ARGUMENTS --json number,title,body,commits,files
   ```

2. For each commit SHA in the PR, inspect the changed files:
   ```
   git show --name-only --format="%H %s" <sha>
   ```

3. Classify each commit to a component:
   - Cortex shim: code touching the shim layer (internal/shim and cmd/shim)
   - Cortex postgres: code touching the postgres docker image, or its helm chart
   - Cortex core: core code touching anything else: the manager or external scheduler logic of cortex
   - General: CI, tooling, docs, or other non-code changes

4. Finally, read through the cortex helm charts in the helm/ folder, and check which ones have updated appVersions, indicating a new Docker image is available and that the chart should be included in the release notes.

Produce a structured digest in this exact format — the subagents depend on it:

```
## Release Digest — PR #NNN "{title}"

### Changed Charts
- cortex v1.2.3 (sha-xxxxxxxx)
- cortex-postgres v1.2.3 (sha-xxxxxxxx)
- cortex-nova v1.2.3 — includes cortex v1.2.3, cortex-postgres v1.2.3

### Commits by Component

#### cortex core
- <sha> <subject>

#### cortex postgres
- <sha> <subject>

#### cortex shim
- <sha> <subject>

#### General
- <sha> <subject>
```

**Important**: Do NOT skip or shallow this phase. Read actual file diffs. The subagents depend entirely on the quality of this digest.

---

## Phase 2: Determine Breaking Changes and Prepare a Changelog

Reason for each change by looking at the commit's diff, if it is a breaking change that requires special attention.

**Important**: Do NOT skip or shallow this phase. Read actual file diffs. The PR reviewers depend entirely on the quality of this analysis to know what to focus on in their review.

### When is a change "breaking"?

A change should be classified as "breaking" if it meets any of the following criteria:

- It changes or removes the public API of any component (e.g., CRD schemas, CLI flags, or REST API endpoints). Note: additions to the public API are not breaking.
- It requires a config format change (e.g., renaming or removing a values.yaml key, changing the expected format of a value, etc)

Once the digest is complete, read each agent file, then dispatch all three **in parallel** using the Agent tool in a single message. Each subagent operates independently — do not wait for one before starting the others.

### Prepare the changelog

Generate a changelog following this template:

```markdown
# Changelog

## YYYY-MM-DD — [#NNN](<PR URL>)

### <chart-name> v<version> (<appVersion>)

Breaking changes:
- <bullet per meaningful change>

Non-breaking changes:
- <bullet per meaningful change>

... repeat for each changed chart ...

### General

Breaking changes:
- <bullet per meaningful change>

Non-breaking changes:
- <bullet per meaningful change>
```

One `###` section per changed chart only. For bundle sections, list which library versions they include, then any bundle-specific changes (values.yaml keys, template/CRD changes). Omit `### General` if empty. No commit SHAs, one line per bullet.

Example:
```markdown
# Changelog

## 2026-04-24 — [#123](https://github.com/cobaltcore-dev/cortex/pull/123)

### cortex v0.0.43 (sha-xxxxxxxx)

Breaking changes:
- Check hypervisor resources against reservations

Non-breaking changes:
- Commitments usage API uses postgres database instead of calling nova

### cortex-postgres v0.5.14 (sha-xxxxxxxx)

Non-breaking changes:
- Add commitments table migration

### cortex-nova v0.0.56 (sha-xxxxxxxx)

Includes updated charts cortex v0.0.43 and cortex-postgres v0.5.14.

Non-breaking changes:
- values.yaml: added `reservations.enabled` (default: false)

### General

Non-breaking changes:
- Update golangci-lint to v2.1.0
```

## Phase 3: Bump Chart Versions

Prepare chart version bumps so GitHub pushes bumped charts to the registry immediately after the release PR is merged.

For each changed library chart, patch-bump its `version` in `helm/library/<name>/Chart.yaml` (e.g. `0.0.43` → `0.1.0`), if there was no breaking change, otherwise minor-bump it. Do not touch `appVersion`. Then update the matching `dependencies[].version` entry in every `helm/bundles/*/Chart.yaml` that references it.

Open a single PR to `main` with all the bumps, branch `release/bump-charts-<NNN>`, noting in the body that it should be merged before the release PR. Use the pull-request-creator agent for this subtask, and include the chart changes in the motivation so they are included in the PR description.

## Phase 4: Update the PR Description

Use `gh pr edit` with `--body` to update the PR description with the changelog. It is fine for release pull request descriptions to utilize markdown formatting. Reference the opened bump PR in the description as well as a dependency.

## Phase 5: Create a Changelog PR

If the CHANGELOG.md does not exists, create it with a `# Changelog` header. Then create a new PR to `main` with branch `release/changelog-<NNN>`, title `Update changelog for release PR #<NNN>`, and a body noting it should be merged after the release PR. Use the pull-request-creator agent for this subtask.

## Phase 6: Summarize — Report what happened

After all subagents return, produce a short summary:

```
## Release #NNN Post-Open Summary

- PR description updated with changelog and bump PR reference
- Bump PR #XXX opened to update chart versions
- Changelog PR #YYY opened to update CHANGELOG.md
```
