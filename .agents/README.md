# Cortex Agents

Reusable Claude Code automation for `cobaltcore-dev` repositories. Adopt it and
your repository gains:

- **Review** — every pull request from an allowlisted author is reviewed for
  codebase-specific pitfalls.
- **Bugfinder** — a scheduled pass that reads the last 7 days of changes, hunts
  for bugs, and opens fix PRs.
- **Docswriter** — a scheduled pass that keeps `docs/` accurate and well-scoped,
  opening documentation PRs.
- **Assistant** — an `@claude` responder on issues and PRs, restricted to
  allowlisted users.
- **Release** — when a PR is opened into a release branch, prepares a release-prep
  PR (changelog + helm chart bumps) and rewrites the release PR description.

Everything is driven by the model hosted in SAP AI Core through a LiteLLM proxy.
The shared command/agent playbook lives in this `.agents/` folder in cortex and is
fetched onto the CI runner at run time — consumers do not copy it and it never
drifts.

## How it works

`.agents/` is a Claude Code plugin (`.claude-plugin/plugin.json` plus `commands/`
and `agents/`). At run time the reusable workflow fetches this folder from cortex
and loads it with `--plugin-dir`, which namespaces the commands as
`/cortex-agents:review`, `/cortex-agents:bugfinder`, `/cortex-agents:docswriter`,
and `/cortex-agents:release`, and makes the agents dispatchable as
`cortex-agents:<name>`. Your repository's own `.claude/` is never touched.

The `.github/` folder holds only the *activation*: a config file and a hub
workflow that calls cortex's reusable workflows.

## One-time organization setup (admin)

Define these as **organization** secrets (Settings → Secrets and variables →
Actions → Organization secrets) and grant them to the repositories that will use
the agents:

| Secret | Purpose |
| --- | --- |
| `AICORE_RESOURCE_GROUP` | SAP AI Core resource group |
| `AICORE_BASE_URL` | SAP AI Core base URL |
| `AICORE_AUTH_URL` | SAP AI Core OAuth2 token URL |
| `AICORE_CLIENT_ID` | SAP AI Core client id |
| `AICORE_CLIENT_SECRET` | SAP AI Core client secret |
| `CORTEX_AI_AGENTS_APP_ID` | GitHub App id used to mint run tokens |
| `CORTEX_AI_AGENTS_CLIENT_PKEY` | GitHub App private key (PEM) |

Because they are organization secrets, consumer repositories define **zero**
repository secrets — the hub workflow passes `secrets: inherit`.

The `cortex-ai-agents` GitHub App must be installed on cortex with `contents: read`
so the runner can fetch the `.agents/` plugin.

## Adopting the agents (per repository)

1. Copy [`cortex-agents-hub.yaml`](cortex-agents-hub.yaml) to
   `.github/workflows/cortex-agents-hub.yaml` in your repository. Pin `CORTEX_REF` (and
   the `@main` refs on the `uses:` lines) to a released cortex tag or SHA for
   reproducibility.
2. Copy [`cortex-agents.config.yaml`](cortex-agents.config.yaml) to
   `.github/cortex-agents.config.yaml` and turn on the features you want.

That is all. With no config file, or with every feature set to `active: false`,
nothing runs.

## Config schema (`.github/cortex-agents.config.yaml`)

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `allowlist` | list of logins | empty (deny all) | Who may trigger review and assistant |
| `review.active` | bool | `false` | Review allowlisted-author PRs |
| `review.model` | string | `sap/anthropic--claude-4.6-opus` | Model for review |
| `review.command` | string | `/cortex-agents:review` | Command prompt |
| `bugfinder.active` | bool | `false` | Enable the bugfinder pass |
| `bugfinder.model` | string | default model | Model for the bugfinder |
| `bugfinder.command` | string | `/cortex-agents:bugfinder` | Command prompt |
| `docswriter.active` | bool | `false` | Enable the docswriter pass |
| `docswriter.model` | string | default model | Model for the docswriter |
| `docswriter.command` | string | `/cortex-agents:docswriter` | Command prompt |
| `assistant.active` | bool | `false` | Respond to the trigger phrase |
| `assistant.trigger_phrase` | string | `@claude` | Phrase that triggers the assistant |
| `assistant.model` | string | default model | Model for the assistant |
| `release.active` | bool | `false` | Prepare releases on matching PRs |
| `release.branches` | list of globs | `[release, "release/*"]` | Base branches that trigger release |
| `release.model` | string | default model | Model for release |
| `release.command` | string | `/cortex-agents:release` | Command prompt |

## Limitations

- **The bugfinder/docswriter cadence is a static cron.** GitHub cannot read a
  cron expression from a file, so the schedule lives only as the `cron:` in the
  hub workflow — there is no cadence config field, and the cron need not be
  weekly. Both passes share that one cron and are then gated by their own `active`
  flag, so you can enable either alone. To change the cadence, edit the `cron:` in
  your `.github/workflows/cortex-agents-hub.yaml`. Each pass examines a fixed 7-day
  change window regardless of cadence, so a sub-weekly cron re-examines overlapping
  commits (the dedup step still prevents duplicate PRs).
- **Review runs via `pull_request_target`.** Fork safety rests on checking out the
  base ref (never the fork head) for config parsing, the author allowlist, and the
  read-only nature of `/cortex-agents:review` (its only mutation is PR comments).
- **The `.agents/` plugin is fetched from cortex at run time.** This needs the App
  installation and the private-repository access setting above.
- **Non-Go repositories** may need to extend the hub workflow for their own build
  toolchain; the Go setup step is skipped automatically when there is no `go.mod`.
- **`effort` is intentionally not supported** yet.
