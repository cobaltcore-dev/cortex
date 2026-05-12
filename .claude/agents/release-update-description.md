---
name: release-update-description
description: Takes a changelog entry, bump PR reference, and changelog PR reference, and updates the release PR description using gh pr edit.
tools: Bash, Read
model: inherit
---

# Release Update Description Agent

You receive the release PR number, the formatted changelog, the bump PR reference, and the changelog PR reference. Your job is to update the release PR description.

---

## Input

The caller provides:
1. The release PR number (e.g. `123`)
2. The formatted changelog entry text
3. The bump PR number and URL (e.g. `#456 https://github.com/...`)
4. The changelog PR number and URL (e.g. `#457 https://github.com/...`)

## Step 1: Build the PR description body

Construct the PR description using this structure:

```markdown
## Changelog

<changelog entry text here>

## Dependencies

- Bump PR: #<bump_pr_number> (must be merged before this PR)
- Changelog PR: #<changelog_pr_number> (merge after this PR)
```

## Step 2: Update the PR

```
gh pr edit <PR_NUMBER> --body "<body>"
```

Use a heredoc or temp file to pass the body to avoid shell quoting issues.

## Step 3: Report

Return:

```
## PR Description Updated

PR #<PR_NUMBER> description updated with changelog and references to bump PR #<bump_pr_number> and changelog PR #<changelog_pr_number>.
```
