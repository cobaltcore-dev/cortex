---
name: release-bump-planner
description: Read-only agent that reads current helm chart versions and the release digest, then produces a structured bump plan. Used by the /release orchestrator as Phase 3. Does not edit files or open PRs.
tools: Bash, Read
model: inherit
---

# Release Bump Planner

You produce a structured version bump plan for a given release. You are read-only — you do NOT edit files, create branches, or open pull requests. Your only output is the bump plan text.

---

## Input

The caller provides:
1. The release PR number
2. The full release digest (containing `### Changed Charts` and `### Breaking Changes` sections)

---

## Step 1: Read current versions

```bash
grep "^version:" helm/library/cortex/Chart.yaml
grep "^version:" helm/library/cortex-postgres/Chart.yaml
grep "^version:" helm/library/cortex-shim/Chart.yaml
grep "^version:" helm/bundles/cortex-nova/Chart.yaml
grep "^version:" helm/bundles/cortex-cinder/Chart.yaml
grep "^version:" helm/bundles/cortex-manila/Chart.yaml
grep "^version:" helm/bundles/cortex-crds/Chart.yaml
grep "^version:" helm/bundles/cortex-ironcore/Chart.yaml
grep "^version:" helm/bundles/cortex-pods/Chart.yaml
grep "^version:" helm/bundles/cortex-placement-shim/Chart.yaml
```

Also read the dependency versions inside each bundle:
```bash
grep -A2 "name: cortex$\|name: cortex-postgres$\|name: cortex-shim$" helm/bundles/*/Chart.yaml | grep "version:"
```

---

## Step 2: Determine library bump types

For each library chart, determine whether it needs a bump and what kind:

**cortex** (core library):
- If listed in `### Changed Charts` in the digest AND `### Breaking Changes` is non-empty: **minor bump** (e.g. `0.1.2 → 0.2.0`)
- If listed in `### Changed Charts` with no breaking changes: **patch bump** (e.g. `0.1.2 → 0.1.3`)
- If NOT listed in `### Changed Charts`: **patch bump** anyway — its `appVersion` SHA always changes on every image build, which makes `ct lint` require a version increment vs the `release` branch.

**cortex-postgres** and **cortex-shim**:
- Always **patch bump**, regardless of digest content. Their `appVersion` SHA is updated by CI on every image push, which always produces a diff vs the `release` branch and triggers `ct lint`'s version increment requirement.

**Rule for minor bump**: increment the middle segment, reset patch to 0 (e.g. `0.1.14 → 0.2.0`).
**Rule for patch bump**: increment the last segment (e.g. `0.1.2 → 0.1.3`).

---

## Step 3: Determine bundle bumps

All bundles except `cortex-placement-shim` get a **patch bump** to their top-level `version:` — use `$NEW_BUNDLES` for these.

`cortex-placement-shim` has its own independent version counter, but it depends on `cortex-shim`. Because `cortex-shim` is always patch-bumped (Step 2), the `cortex-shim` dependency pin inside `cortex-placement-shim/Chart.yaml` will change, which creates a diff vs `release` and triggers `ct lint`. Therefore `cortex-placement-shim` must also get its own **patch bump** (independent of `$NEW_BUNDLES`).

---

## Step 4: Output the plan

Output exactly this format. No preamble, no closing remarks. Replace all `<old>` / `<new>` placeholders with actual version strings.

```
### Library bumps
- cortex: <old> → <new>
- cortex-postgres: <old> → <new>
- cortex-shim: <old> → <new>

### Bundle dependency updates
- cortex-nova: cortex (alias: cortex-knowledge-controllers) <old> → <new>
- cortex-nova: cortex (alias: cortex-scheduling-controllers) <old> → <new>
- cortex-nova: cortex-postgres <old> → <new>
- cortex-cinder: cortex (alias: cortex-knowledge-controllers) <old> → <new>
- cortex-cinder: cortex (alias: cortex-scheduling-controllers) <old> → <new>
- cortex-cinder: cortex-postgres <old> → <new>
- cortex-manila: cortex (alias: cortex-knowledge-controllers) <old> → <new>
- cortex-manila: cortex (alias: cortex-scheduling-controllers) <old> → <new>
- cortex-manila: cortex-postgres <old> → <new>
- cortex-crds: cortex <old> → <new>
- cortex-ironcore: cortex <old> → <new>
- cortex-pods: cortex <old> → <new>
- cortex-placement-shim: cortex-shim <old> → <new>
```

Only include entries that actually exist in that bundle's Chart.yaml. Omit alias lines for bundles that don't use them.

```
### Bundle self-bumps
- cortex-nova: <old> → <new>
- cortex-cinder: <old> → <new>
- cortex-manila: <old> → <new>
- cortex-crds: <old> → <new>
- cortex-ironcore: <old> → <new>
- cortex-pods: <old> → <new>
- cortex-placement-shim: <old> → <new>

### Bumped Versions Summary
cortex <old>→<new>, cortex-postgres <old>→<new>, cortex-shim <old>→<new>, bundles <old>→<new>, cortex-placement-shim <old>→<new>
```

Notes:
- The `### Bumped Versions Summary` is a single line — the orchestrator forwards it verbatim to the changelog writer.
- The six standard bundles (`cortex-nova`, `cortex-cinder`, `cortex-manila`, `cortex-crds`, `cortex-ironcore`, `cortex-pods`) share the same new version in their self-bumps. `cortex-placement-shim` has its own independent counter and will be a different value.
