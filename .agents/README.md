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
`/cortex-agents:bugfinder`, `/cortex-agents:docswriter`,
and `/cortex-agents:release`, and makes the agents dispatchable as
`cortex-agents:<name>`. Your repository's own `.claude/` is never touched.
Review runs from an inline prompt in the reusable review workflow rather than
a plugin command.

The `.github/` folder of a consumer holds only the *activation*: a config
file and a ~50-line stub workflow that lists the triggers and calls cortex's
single entry workflow (`cortex-agents-entry.yaml`). The entry workflow holds
everything else — config parsing, the allowlist and branch gates, and the
fan-out to the feature workflows — so logic changes land in cortex alone and
reach every consumer when it re-pins.

Three things must live in the consuming repository, because GitHub requires
the caller to consent to them: the `on:` triggers (they cannot be inherited),
`secrets: inherit`, and the `permissions:` grant — the union of what the
agents may request, since a called workflow may only maintain or reduce the
caller's grant, never elevate it. Every job inside cortex downgrades to what
it actually uses.

## Version pinning

The stub pins the entry workflow once
(`uses: cobaltcore-dev/cortex/.github/workflows/cortex-agents-entry.yaml@<tag
or SHA>`). That pin is the only place a cortex version is referenced: the
entry workflow resolves its own commit from it (`job.workflow_sha`), clones
cortex at exactly that commit to reuse its composite actions, and passes that
commit down as `cortex_ref` to the feature workflows, which fetch the
`.agents/` plugin from the same commit. Re-pinning the one line upgrades the
whole chain atomically.

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
repository secrets — the stub workflow passes `secrets: inherit`.

The `cortex-ai-agents` GitHub App must be installed on cortex with `contents: read`
so the runner can fetch the `.agents/` plugin.

## Adopting the agents (per repository)

1. Copy [`cortex-agents-hub.yaml`](cortex-agents-hub.yaml) to
   `.github/workflows/cortex-agents-hub.yaml` in your repository. Pin the
   `uses:` line in it to a released cortex tag or SHA for reproducibility —
   that one pin versions the entire chain (see *Version pinning* above).
2. Copy [`cortex-agents.config.yaml`](cortex-agents.config.yaml) to
   `.github/cortex-agents.config.yaml` and turn on the features you want.

That is all. With no config file, or with every feature set to `active: false`,
nothing runs. From then on, the stub never changes — feature and gating
changes are picked up by re-pinning the `uses:` line alone.

## Config schema (`.github/cortex-agents.config.yaml`)

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `allowlist` | list of logins | empty (deny all) | Who may trigger review and assistant |
| `allowed_bots` | list of bot logins | empty (deny all bots) | Bots who may trigger actions. Read by workflows that opt in |
| `review.active` | bool | `false` | Review allowlisted-author PRs |
| `review.model` | string | `sap/anthropic--claude-4.6-opus` | Model for review |
| `review.command` | string | `/cortex-agents:review` | Accepted but ignored; review runs from an inline prompt |
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
  stub workflow — there is no cadence config field, and the cron need not be
  weekly. Both passes share that one cron and are then gated by their own `active`
  flag, so you can enable either alone. To change the cadence, edit the `cron:` in
  your `.github/workflows/cortex-agents-hub.yaml`. Each pass examines a fixed 7-day
  change window regardless of cadence, so a sub-weekly cron re-examines overlapping
  commits (the dedup step still prevents duplicate PRs).
- **Review runs via `pull_request_target`.** Fork safety rests on checking out the
  base ref (never the fork head) for config parsing, the author allowlist, and the
  read-only nature of the review pass (its only mutation is PR comments).
- **The `.agents/` plugin is fetched from cortex at run time.** This needs the App
  installation and the private-repository access setting above.
- **The stub grants write permissions up front.** GitHub requires the caller
  to consent to everything a called workflow may do, so the stub carries the
  union (`contents`, `pull-requests`, `issues`, `id-token: write`). Tightening
  happens inside cortex: the gates and config jobs run read-only, and each
  feature workflow requests only what it needs. Fork-originated
  `pull_request` events never see secrets regardless.
- **`job.workflow_sha` self-pinning needs github.com**, not GitHub Enterprise
  Server.
- **Non-Go repositories** may need cortex to extend the agent workflows for
  their build toolchain; the Go setup step is skipped automatically when there
  is no `go.mod`.
- **`effort` is intentionally not supported** yet.
