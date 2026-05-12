---
allowed-tools: Read, Bash(*), Agent
description: Release orchestrator — builds a digest of what changed in a release PR, opens a changelog PR, and references the bump PR. Usage: /release PR_NUMBER
---

# Release Orchestrator

You orchestrate the release process for a given PR. You MUST complete all three deliverables in order:
1. A bump PR for helm chart versions
2. A changelog PR with the release notes (using the bumped versions)
3. The release PR description updated with the changelog and references to both PRs

You achieve this by dispatching focused subagents **sequentially**. Each step depends on the output of the previous one. Do NOT try to do the detailed work yourself — you are a dispatcher.

---

## Phase 1: Collect the release digest

Dispatch the **release-digest** agent.

Prompt: `Produce a release digest for PR #<PR_NUMBER>.`

Wait for it to return. Save its full output as the **digest**.

---

## Phase 2: Bump chart versions

Dispatch the **release-bump-charts** agent. Pass it the PR number and the full digest.

Prompt:
```
Release PR number: <PR_NUMBER>

<paste the full digest here>

Bump the helm chart versions and open/update a bump PR.
```

Wait for it to return. From its report, extract:
- The bump PR number and URL
- The list of bumped chart versions (e.g. `cortex: 0.0.47 → 0.0.48`)

---

## Phase 3: Create the changelog PR

Dispatch the **release-changelog** agent. Pass it the PR number, the full digest, and the bumped versions from Phase 2.

Prompt:
```
Release PR number: <PR_NUMBER>

Bumped chart versions:
<paste the bumped versions list from the bump agent's report>

Release digest:
<paste the full digest here>

Generate the changelog entry using the NEW bumped versions, prepend it to CHANGELOG.md, and open/update a changelog PR.
```

Wait for it to return. From its report, extract:
- The changelog PR number and URL
- The full changelog entry text

---

## Phase 4: Update the release PR description

Dispatch the **release-update-description** agent. Pass it the PR number, changelog entry, bump PR reference, and changelog PR reference.

Prompt:
```
Release PR number: <PR_NUMBER>

Changelog entry:
<paste the changelog entry text from the changelog agent's report>

Bump PR: #<bump_pr_number> (<bump_pr_url>)
Changelog PR: #<changelog_pr_number> (<changelog_pr_url>)

Update the release PR description with the changelog and references to both PRs.
```

Wait for it to return.

---

## Phase 5: Summarize

After all agents have completed, produce a short summary:

```
## Release #NNN Post-Open Summary

- Bump PR: #XXX opened/updated
- Changelog PR: #YYY opened/updated
- PR #NNN description: updated with changelog and PR references
```

If any agent reports a failure, include that in the summary and suggest next steps.

---

## Critical rules

- Execute phases 1 → 2 → 3 → 4 **strictly in order**. Each depends on the previous.
- You MUST complete ALL FOUR phases. Never skip one.
- Do NOT read code yourself — the release-digest agent handles that.
- Do NOT generate changelog text yourself — the release-changelog agent handles that.
- Keep your own context minimal — you are a dispatcher, not an analyst.
- Pass data between phases by extracting the relevant pieces from each agent's report and including them verbatim in the next agent's prompt.
