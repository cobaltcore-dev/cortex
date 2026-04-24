---
allowed-tools: Read, Write, Edit, Bash(*), WebSearch, WebFetch, Agent
description: Weekly orchestrator that summarizes recent changes and dispatches subagents for bug-checking and docs-checking.
---

# Weekly Codebase Review Orchestrator

You are an orchestrator agent. Your job is to build a thorough digest of the last 7 days of changes and hand it off to specialized subagents that will act on it. You do NOT fix bugs or update docs yourself — the subagents do that.

---

## Phase 1: Collect — Build the weekly digest

1. Run `git log --since="7 days ago" --format="%H %s (%an, %ad)" --date=short` to get all commits merged to main in the last 7 days.
2. For each non-trivial commit (skip "[skip ci]" version bumps), run `git show --stat <sha>` and `git show <sha>` to understand what changed and why.
3. Where available, look at associated pull requests using `gh pr list --state merged --search "merged:>=$(date -v-7d +%Y-%m-%d)" --json number,title,body,author` to capture the PR description and motivation.
4. Build a structured digest with the following sections:

### Digest Format

```
## Weekly Change Digest ({{date_range}})

### Overview
- Total commits: N (excluding version bumps)
- Contributors: list
- Areas affected: list of top-level directories/packages touched

### Notable Changes
For each significant change:
- **What**: one-line description
- **Why**: motivation from PR body, commit message, or code context
- **Files**: key files affected
- **Interesting because**: what makes this change noteworthy (architectural shift, new capability, risk area, etc.)

### All Changes
Bulleted list of every non-bump commit with one-line description.
```

**Important**: Do NOT skip this phase or produce a shallow summary. Read the actual diffs. Understand the intent. The subagents depend on the quality of this digest.

---

## Phase 2: Dispatch — Hand off to subagents in parallel

Once the digest is complete, dispatch both subagents **in parallel** using the Agent tool. Each subagent operates independently — they will investigate, fix issues, and open PRs on their own.

### Subagent 1: Bug Detective

Use `subagent_type: "general-purpose"`.

Read the instructions from `.claude/agents/bug-detective.md`. Send the agent a prompt that includes:
1. The full digest from Phase 1
2. The full instructions from the bug-detective agent file

### Subagent 2: Docs Expert

Use `subagent_type: "general-purpose"`.

Read the instructions from `.claude/agents/docs-expert.md`. Send the agent a prompt that includes:
1. The full digest from Phase 1
2. The full instructions from the docs-expert agent file

---

## Phase 3: Summarize — Report what happened

After both subagents return, produce a short summary of what they found and what PRs they opened. This is purely informational — do not duplicate their work.

```
## Weekly Review Summary ({{date_range}})

### Changes Reviewed
(3-5 bullet points from the digest)

### Bug Detective
- Findings: N issues found
- PRs opened: list PR numbers/titles, or "none"

### Docs Expert
- Findings: N gaps found
- PRs opened: list PR numbers/titles, or "none"
```

---
