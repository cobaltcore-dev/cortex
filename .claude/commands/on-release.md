---
allowed-tools: Read, Write, Edit, Bash(*), WebSearch, WebFetch, Agent
description: Release orchestrator — builds a digest of what changed in a release PR and dispatches subagents to update the PR description, write the changelog, and bump chart versions. Usage: /on-release PR_NUMBER
---

# Release Orchestrator

You are an orchestrator agent. Your job is to build a complete digest of the release PR and hand it off to specialized subagents that will act on it. You do NOT edit the PR, write the changelog, or bump charts yourself — the subagents do that.

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

3. Classify each commit to a component using these path rules:
   - `postgres/**` → cortex-postgres
   - `cmd/shim/**` or `internal/shim/**` → cortex-shim
   - `helm/bundles/cortex-<name>/**` → that specific bundle chart
   - Files only touching `.github/**`, `docs/**`, `Makefile`, or tooling config → General
   - Anything else → cortex (core)
   - A commit touching multiple components is listed under each.
   - Skip commits whose subject contains `[skip ci]` or is a pure version-bump (e.g. "Bump … appVersion").

4. Read every `helm/library/*/Chart.yaml` and `helm/bundles/*/Chart.yaml` that appears in the PR's changed files. Collect `name`, `version`, `appVersion`.

5. For each changed bundle chart, also read its `Chart.yaml` dependencies to know which library versions it now ships.

6. For commits touching `helm/bundles/cortex-<name>/`, inspect the actual diff:
   ```
   git show <sha> -- helm/bundles/<name>/
   ```
   Note `values.yaml` key additions/removals/renames and `templates/`/`crds/` resource changes.

Produce a structured digest in this exact format — the subagents depend on it:

```
## Release Digest — PR #NNN "{title}"

### Changed Charts
- cortex v0.0.43 (sha-xxxxxxxx)
- cortex-postgres v0.5.14 (sha-xxxxxxxx)
- cortex-nova v0.0.56 — includes cortex v0.0.43, cortex-postgres v0.5.14

### Commits by Component

#### cortex
- <sha> <subject>

#### cortex-postgres
- <sha> <subject>

#### cortex-nova
- values.yaml: added `reservations.enabled` (default: false)
- <sha> <subject> (if any commits directly touched this bundle)

#### General
- <sha> <subject>
```

**Important**: Do NOT skip or shallow this phase. Read actual file diffs. The subagents depend entirely on the quality of this digest.

---

## Phase 2: Dispatch — Hand off to subagents in parallel

Once the digest is complete, read each agent file, then dispatch all three **in parallel** using the Agent tool in a single message. Each subagent operates independently — do not wait for one before starting the others.

Each subagent receives a prompt containing:
1. The full digest from Phase 1
2. The PR number: $ARGUMENTS
3. The full contents of its `.claude/agents/<name>.md` file

### Subagent 1: Release PR Updater

`subagent_type: "general-purpose"` — reads `.claude/agents/release-pr-updater.md`

### Subagent 2: Changelog Writer

`subagent_type: "general-purpose"` — reads `.claude/agents/changelog-writer.md`

### Subagent 3: Chart Bumper

`subagent_type: "general-purpose"` — reads `.claude/agents/chart-bumper.md`

---

## Phase 3: Summarize — Report what happened

After all subagents return, produce a short summary:

```
## Release #NNN Post-Open Summary

### Release PR Updater
- <what was updated in the PR description, or "no changes made">

### Changelog Writer
- PR opened: #<number> — <title>, or "failed: <reason>"

### Chart Bumper
- PR opened: #<number> — <title>, or "no chart bumps needed"
```
