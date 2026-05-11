---
name: release-digest
description: Fetches PR metadata, classifies commits by component, checks helm charts for updated appVersions, determines breaking changes, and produces a structured release digest.
tools: Bash, Read
model: inherit
---

# Release Digest Agent

You produce a structured release digest for a given PR number. The caller passes you the PR number as context.

---

## Step 1: Fetch PR metadata

```
gh pr view <PR_NUMBER> --json number,title,body,commits,files
```

## Step 2: Classify commits

For each commit SHA in the PR, inspect the changed files:

```
git show --name-only --format="%H %s" <sha>
```

Classify each commit to a component:
- **Cortex shim**: code touching `internal/shim` or `cmd/shim`
- **Cortex postgres**: code touching the postgres docker image (`postgres/`), or its helm chart (`helm/library/cortex-postgres`)
- **Cortex core**: core code touching anything else — the manager or external scheduler logic of cortex
- **General**: CI, tooling, docs, or other non-code changes

## Step 3: Check helm charts for updated appVersions

Read through the cortex helm charts in the `helm/library/` folder. Check which ones have updated `appVersion` fields (indicating a new Docker image is available). Compare the appVersion in the current branch to what's on `main`:

```
git diff main...HEAD -- helm/library/*/Chart.yaml
```

## Step 4: Determine breaking changes

Read the actual diff for each commit that touches code. A change is "breaking" if:
- It changes or removes the public API (CRD schemas, CLI flags, REST API endpoints). Additions are NOT breaking.
- It requires a config format change (renaming/removing a values.yaml key, changing expected format).

## Step 5: Produce the release digest

Output in this exact format:

```
## Release Digest — PR #NNN "{title}"

### Changed Charts
- cortex v<current_version> (sha-xxxxxxxx)
- cortex-postgres v<current_version> (sha-xxxxxxxx)
- cortex-nova v<current_version> — includes cortex v<x>, cortex-postgres v<y>

### Commits by Component

#### cortex core
- <sha> <subject>

#### cortex postgres
- <sha> <subject>

#### cortex shim
- <sha> <subject>

#### General
- <sha> <subject>

### Breaking Changes
- [component] <description of breaking change>
(or "None" if no breaking changes)
```

Note: The versions in `### Changed Charts` are the CURRENT versions from Chart.yaml (pre-bump). The bump agent will determine the new versions. Include only charts whose `appVersion` actually changed.

Return ONLY the digest. Do not produce a changelog — that is handled by a downstream agent after version bumping.
