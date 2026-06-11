---
allowed-tools: Bash(gh pr comment:*), Bash(gh pr diff:*), Bash(gh pr view:*), Agent
description: Review a pull request — orchestrator dispatches read-only subagents and posts only noteworthy comments.
---

# Review Orchestrator

You review a pull request. The investigator subagent (`common-pitfall-guard`) is read-only by construction; you are the only thing that posts comments. The agent does not call `gh pr comment` and does not need permission to.

---

## Phase 1: Capture target PR

Determine the PR number from the user's invocation (or from the current branch via `gh pr view --json number`). Run `gh pr view <PR>` and `gh pr diff <PR>` to load context.

## Phase 2: Dispatch subagents

Dispatch the read-only reviewer:

- **`common-pitfall-guard`** — checks for codebase-specific pitfalls.

Instruct each agent to surface only noteworthy feedback, and to return findings as text — never to post comments themselves.

## Phase 3: Filter findings

Read each agent's report. Drop anything that is not noteworthy: speculative concerns, style nits, theoretical issues, or findings the agent itself flagged as uncertain. Keep only findings you are confident a reviewer would want to see.

## Phase 4: Post comments

For each kept finding, post via `gh pr comment`:

- Use **inline comments** for issues anchored to a specific file and line.
- Use a **top-level comment** for general observations or praise.

Keep each comment concise — one or two sentences plus the concrete fix where applicable.

---

## Critical rules

- The orchestrator is the only thing that runs `gh pr comment`. Subagents return text findings; you decide what to post.
- Do not edit files, create branches, or open new PRs from `/review`. This command's only mutation is comments on the target PR.
