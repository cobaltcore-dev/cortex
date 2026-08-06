---
allowed-tools: Read, Write, Edit, Bash(*), Agent
description: Release orchestrator — opens a single release-prep PR combining changelog and chart bumps, and rewrites the release PR description. Usage: /release PR_NUMBER
---

# Release Orchestrator

You orchestrate the release process for a given release PR. Two deliverables, in order:

1. A single prep PR (`release/prepare-<PR_NUMBER>`) combining the changelog entry and helm chart version bumps.
2. The release PR description updated with the changelog and a reference to the prep PR.

You are the only mutator. The investigator subagents — `release-digest`, `release-bump-planner`, `release-changelog-writer` — are read-only by construction. They return text; you apply edits, run git, push branches, and dispatch `pull-request-creator` to open PRs. Never call `gh pr create` directly.

---

## Phase 1: Setup

Read `AGENTS.md`. Capture `<PR_NUMBER>` from the user's invocation. If no number was provided, find the open PR targeting `main` whose head branch matches a release pattern:

```sh
gh pr list --state open --base main --json number,title,headRefName | \
  jq '.[] | select(.headRefName | test("release|bump-app-version"; "i"))'
```

If exactly one candidate is found, use it and tell the user which PR was detected. If none or multiple, abort and ask the user to specify the PR number explicitly.

Then:

```sh
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
- The new cortex library version (the `<new>` side of `cortex <old>→<new>`) — save as `<cortex_new_version>`.

---

## Phase 4: Apply the bump

Starting from `main` with a clean tree, apply the plan to the working tree:

- For each line in `### Library bumps`, use `Edit` on the named `helm/library/<name>/Chart.yaml` to change its `version:` field from old to new. Do not touch `appVersion`.
- For each line in `### Bundle dependency updates`, use `Edit` on the named `helm/bundles/<name>/Chart.yaml` to change the `version:` field of the dependency entry at the given index. Anchor your `Edit` on the specific old version string plus the dependency's `name:` and any `alias:` line so the match is unique.
- For each line in `### Bundle self-bumps`, use `Edit` on the named bundle's Chart.yaml to change the top-level `version:`. Anchor on the chart's `name: <bundle_name>` plus the version line to disambiguate from dependency `version:` entries.

Do NOT commit yet — leave the edits uncommitted in the working tree.

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

Read the first 100 lines of `CHANGELOG.md` (or the full file if it does not exist) to check whether an entry for `#<PR_NUMBER>` is already present (use `grep -F "[#<PR_NUMBER>]"`):

- **First run** (no existing entry): prepend `<changelog_entry>` (followed by a blank line) directly under the `# Changelog` header, before any existing entries.
- **Update run** (entry already present): replace the entire existing entry for `#<PR_NUMBER>` — from its `##` heading line down to (but not including) the next `##` heading or end of file — with `<changelog_entry>`. Do not prepend a second entry.

If `CHANGELOG.md` does not exist at all, create it with `# Changelog\n\n` followed by `<changelog_entry>`.

Do NOT commit yet — both `helm/` edits and `CHANGELOG.md` remain uncommitted in the working tree.

---

## Phase 6: Open the prep PR

Dispatch **`pull-request-creator`** with:

- `branch`: `release/prepare-<PR_NUMBER>`
- `commit_message`: `Release cortex <cortex_new_version>`
- `motivation`: `Release prep for #<PR_NUMBER>: changelog entry and helm chart version bumps. Merge this before merging #<PR_NUMBER>.`
- `assign_reviewers`: `false`

Capture `<prep_pr_number>` and `<prep_pr_url>` from its report. The agent leaves the working tree clean on `release/prepare-<PR_NUMBER>` — switch back yourself with `git checkout main` before Phase 7.

`pull-request-creator`'s idempotency handles the "update" case: if the branch already exists with only bot commits, it resets and force-pushes automatically.

---

## Phase 7: Update the release PR description

Build the new release PR description: `<changelog_entry>` followed by a Dependencies footer. Write it to a tempfile and pass `--body-file` to avoid shell quoting issues.

```sh
TMP=$(mktemp)
cat > "$TMP" <<'BODY'
## Release cortex <cortex_new_version>

<changelog_entry>

## Dependencies

- Prep PR: #<prep_pr_number> (must be merged before this PR)
BODY
gh pr edit <PR_NUMBER> --title "Release cortex <cortex_new_version>" --body-file "$TMP"
rm "$TMP"
```

This is the only GitHub mutation that does not flow through `pull-request-creator` — it is a single API call against a PR that already exists.

---

## Phase 8: Summary

Print:

```
## Release #<PR_NUMBER> Post-Open Summary

- Prep PR: #<prep_pr_number> (<prep_pr_url>)
- Release PR #<PR_NUMBER>: description updated with changelog and prep PR reference
- Bumped: <bumped_summary>
```

If any phase aborted, list which phase and why, and skip the remaining phases — do not pretend success.

---

## Critical rules

- Phases 2 → 7 strictly in order. Each depends on the previous.
- Never read chart files or `CHANGELOG.md` for analysis — that is what the investigator agents do. You read those files only for the mechanical `Edit` in Phase 4 and the mechanical prepend/replace in Phase 5 (reading the first 100 lines to detect an existing entry is explicitly permitted).
- All PR creation flows through `pull-request-creator`. Do not call `gh pr create` directly. The agent owns branch reset, commit, force-push, the human-commit guard, and clean-tree postcondition — you only stage the working-tree edits.
