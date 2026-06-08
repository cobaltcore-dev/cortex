---
allowed-tools: Read, Write, Edit, Bash(*), Agent
description: Release orchestrator — opens a chart-bump PR, opens a changelog PR, and rewrites the release PR description to reference both. Usage: /release PR_NUMBER
---

# Release Orchestrator

You orchestrate the release process for a given release PR. Three deliverables, in order:

1. A bump PR for helm chart versions (`release/bump-charts-<PR_NUMBER>`).
2. A changelog PR with the release notes (`release/changelog-<PR_NUMBER>`), using the bumped versions.
3. The release PR description updated with the changelog and references to both PRs.

You are the only mutator. The investigator subagents — `release-digest`, `release-bump-planner`, `release-changelog-writer` — are read-only by construction. They return text; you apply edits, run git, push branches, and dispatch `pull-request-creator` to open PRs. Never call `gh pr create` directly.

---

## Phase 1: Setup

Read `AGENTS.md`. Capture `<PR_NUMBER>` from the user's invocation. Then:

```
git fetch origin main
git status --porcelain
git rev-parse --abbrev-ref HEAD
```

Working tree must be clean and HEAD must be on `main`. If either precondition fails, abort and tell the user what to fix.

---

## Phase 2: Digest

Dispatch the **release-digest** agent.

Prompt: `Produce a release digest for PR #<PR_NUMBER>.`

Save its full output as `<digest>`.

---

## Phase 3: Plan the bump

Dispatch the **release-bump-planner** agent. Pass it the PR number and the full digest.

Prompt:
```
Release PR number: <PR_NUMBER>

Release digest:
<digest>

Produce the bump plan.
```

Save its full output as `<bump_plan>`. From the plan extract:

- The `### Library bumps` block — used in Phase 4 to drive `Edit` calls.
- The `### Bundle dependency updates` block — likewise.
- The `### Bundle self-bumps` block — likewise.
- The single `### Bumped Versions Summary` line — the only piece you forward to Phase 5. Save it as `<bumped_summary>`.

---

## Phase 4: Apply the bump

Starting from `main` with a clean tree, apply the plan to the working tree:

- For each line in `### Library bumps`, use `Edit` on the named `helm/library/<name>/Chart.yaml` to change its `version:` field from old to new. Do not touch `appVersion`.
- For each line in `### Bundle dependency updates`, use `Edit` on the named `helm/bundles/<name>/Chart.yaml` to change the `version:` field of the dependency entry at the given index. Anchor your `Edit` on the specific old version string plus the dependency's `name:` and any `alias:` line so the match is unique.
- For each line in `### Bundle self-bumps`, use `Edit` on the named bundle's Chart.yaml to change the top-level `version:`. Anchor on the chart's `name: <bundle_name>` plus the version line to disambiguate from dependency `version:` entries.

Dispatch **`pull-request-creator`** with:

- `branch`: `release/bump-charts-<PR_NUMBER>`
- `commit_message`: `Bump chart versions for release PR #<PR_NUMBER>`
- `motivation`: `Bump helm chart versions for release PR #<PR_NUMBER>. Bumped: <bumped_summary>. This PR must be merged before #<PR_NUMBER>.`
- `assign_reviewers`: `false` (release-mechanics PRs route to the release owner regardless of code area)

Capture `<bump_pr_number>` and `<bump_pr_url>` from its report. The agent leaves the working tree clean on `release/bump-charts-<PR_NUMBER>` — switch back yourself with `git checkout main` before the next phase.

---

## Phase 5: Write the changelog

Dispatch the **release-changelog-writer** agent. Pass the digest and the bumped-versions summary; do NOT pass the verbose bump plan.

Prompt:
```
Release PR number: <PR_NUMBER>

Bumped versions:
<bumped_summary>

Release digest:
<digest>

Produce the changelog entry.
```

Save its full output as `<changelog_entry>`.

---

## Phase 6: Apply the changelog

If `CHANGELOG.md` does not exist, write it with `# Changelog\n\n` followed by `<changelog_entry>`. Otherwise, read the file and prepend `<changelog_entry>` (followed by a blank line) directly under the `# Changelog` header, before any existing entries.

Dispatch **`pull-request-creator`** with:

- `branch`: `release/changelog-<PR_NUMBER>`
- `commit_message`: `Add changelog entry for release PR #<PR_NUMBER>`
- `motivation`: `Add changelog entry for release PR #<PR_NUMBER>. Merge after #<PR_NUMBER>.`
- `assign_reviewers`: `false`

Capture `<changelog_pr_number>` and `<changelog_pr_url>`. The agent leaves the working tree clean on `release/changelog-<PR_NUMBER>` — `git checkout main` yourself before Phase 7.

---

## Phase 7: Update the release PR description

Build the new release PR description: `<changelog_entry>` followed by a Dependencies footer linking the bump PR and the changelog PR. Write it to a tempfile and pass `--body-file` to avoid shell quoting issues.

```
TMP=$(mktemp)
cat > "$TMP" <<'BODY'
## Changelog

<changelog_entry>

## Dependencies

- Bump PR: #<bump_pr_number> (must be merged before this PR)
- Changelog PR: #<changelog_pr_number> (merge after this PR)
BODY
gh pr edit <PR_NUMBER> --body-file "$TMP"
rm "$TMP"
```

This is the only GitHub mutation that does not flow through `pull-request-creator` — it is a single API call against a PR that already exists.

---

## Phase 8: Summary

Print:

```
## Release #<PR_NUMBER> Post-Open Summary

- Bump PR: #<bump_pr_number> (<bump_pr_url>)
- Changelog PR: #<changelog_pr_number> (<changelog_pr_url>)
- Release PR #<PR_NUMBER>: description updated with changelog and PR references
- Bumped: <bumped_summary>
```

If any phase aborted, list which phase and why, and skip the remaining phases — do not pretend success.

---

## Critical rules

- Phases 2 → 7 strictly in order. Each depends on the previous.
- Never read chart files or `CHANGELOG.md` for analysis — that is what the investigator agents do. You read those files only for the mechanical `Edit` and prepend in Phases 4 and 6.
- All PR creation flows through `pull-request-creator`. Do not call `gh pr create` directly. The agent owns branch reset, commit, force-push, the human-commit guard, and clean-tree postcondition — you only stage the working-tree edits.
