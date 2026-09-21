<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# RECIPE — the documentation standard for `docs/`

This file is the **single source of truth** for what the Cortex textbook under `docs/` should
look like. Every page is measured against it. When this RECIPE and an author's (or an agent's)
instinct disagree, **RECIPE wins**; when RECIPE is silent on a question, propose nothing and
leave the existing text alone rather than inventing a rule.

The goal is a stable, kubernetes.io-grade **textbook**: read front to back it teaches Cortex;
dipped into, each page stands on its own. It is not a changelog, not marketing, and not a
reference dump of every field.

## §1 Scope and audience

- **Audience:** operators and contributors running or extending Cortex. Assume general
  Kubernetes and OpenStack literacy; do not assume familiarity with Cortex internals.
- **Docs-only.** RECIPE governs `docs/` only. It never mandates changes to Go, Helm, CI, or
  `.claude/` files. A documentation change is warranted only by a change in the **code surface**
  it describes (a new CRD Kind, component, config key, capability, or a removed one).
- **Derive from code, never invent.** Every command, column, field path, flag, metric, and
  step name shown must exist in the repository. Do not invent example names — reuse real ones
  (e.g. `filter_has_enough_capacity`, `kvm_binpack`, `host_utilization_extractor`). If you
  cannot ground a detail in source, omit it.

## §2 Vendor-neutral, upstream voice

This is an open-source textbook. Keep it neutral:

- No internal hostnames, cluster names, ticket systems, or company-internal URLs. The leak
  scan must stay clean: `grep -rniE 'wdf\.sap\.corp|sapcc/|qa-de-1' docs` returns nothing, and
  the only `cloud.sap` hits are the legitimate API groups `kvm.cloud.sap` and `cortex.cloud`.
- Describe behaviour as it ships in this repository, not as any one deployment configures it.
- Prefer "the operator", "a deployment", "your cluster" over any specific environment.

## §3 Canonical layout (the tree)

The book is a fixed set of numbered chapters, each a directory with a `readme.md` and
sequentially numbered pages. This tree is canonical; a code change may add a page or a chapter,
but the shape (numbered chapters → numbered pages → per-chapter readme) does not change.

```
docs/
  readme.md                     # book table of contents; lists every chapter and page
  RECIPE.md                     # this file
  assets/                       # images referenced by pages
  0N-<chapter-slug>/
    readme.md                   # chapter intro + "In this chapter" numbered list
    0M-<page-slug>.md           # content pages, numbered from 01, no gaps
```

Rules that follow from the tree:

- **Sequential numbering, no gaps.** Pages within a chapter are `01`, `02`, … with no missing
  numbers. If a page is merged away or deleted, renumber the rest and fix every reference so the
  sequence stays contiguous. Track renames as `git mv` so history is preserved.
- **One concept per page.** A page teaches one thing. A concept page and a hands-on walkthrough
  of the same subject may live on one page (walkthrough as a trailing `## Walkthrough: …`
  section) or as adjacent pages — but do not scatter one concept across the book.
- **Every page is reachable.** It appears in its chapter `readme.md` list and in the top-level
  `docs/readme.md`, and it is on the prev/next chain (§5).

## §4 Required page structure

Every content page (everything except `readme.md` index pages) has, in order:

1. **SPDX header**, exactly this 4-line HTML comment, as the first bytes of the file:
   ```
   <!--
   # SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
   #
   # SPDX-License-Identifier: Apache-2.0
   -->
   ```
2. **A single `# Title`** (H1) — the only H1 on the page.
3. **A short intro paragraph** stating what the page covers and what the reader will know by the
   end. No preamble like "In this document we will…".
4. **Body sections** (`##` / `###`) teaching the concept, in a logical reading order.
5. **An unheaded closing paragraph** — the last prose before `## Next`, tying the page back to the
   wider system and pointing forward. Do **not** give it a heading (no `## How this relates to
   Cortex` or similar); it reads as the natural conclusion of the last body section. Keep it
   substantive — real cross-links and a forward pointer, not a rehearsal of what was just said.
6. **`## Next`** — exactly one per page, the last section, a single prev/next line (§5).

`readme.md` index pages have the SPDX header, an `# Chapter title`, a short intro, an
`## In this chapter` numbered list linking every page in the chapter, and a `## Next` line.

## §5 Cross-links and navigation

- **Prev/next chain.** Each page ends with one `## Next` section containing a single line:
  ```
  [Prev: <prior page title>](<relative>.md) · [Next: <following page title> »](<relative>.md)
  ```
  The chain is continuous within and across chapters (last page of a chapter points to the next
  chapter's `readme.md` or `[Contents](../readme.md)`; a chapter `readme.md` points to its own
  page 01). When pages are added, removed, or renumbered, repair the whole chain.
- **Relative links only**, always resolving to a file that exists. Every `](target.md)` and
  `](target.md#anchor)` must point at a real file, and anchors must match a real heading
  (GitHub slug: lowercase, spaces→`-`, punctuation dropped — e.g.
  `## Walkthrough: multicluster with kind` → `#walkthrough-multicluster-with-kind`).
- **Link to the owning page.** Introduce each concept once, on the page that owns it, and link
  to that page from everywhere else rather than re-explaining it.

## §6 Content map (what must be documented)

The book documents the code surface. When one of these surfaces changes in code, the
corresponding documentation must follow; that is the only thing that warrants a change.

- **Every CRD Kind** in `api/v1alpha1/` is mentioned on the page that owns its concept **and**
  shows a useful `kubectl` interface for it (§7). New Kind → it must appear; removed Kind → its
  coverage goes.
- **Every scheduler domain** (`internal/scheduling/<domain>/`) has a page in
  `02-external-scheduler-api/` covering its hook, pipeline(s), and shipped steps.
- **Every `pkg/` library component** worth operator/contributor understanding has a page in
  `06-cortex-library/`.
- **Config, metrics, and packaging** surfaces (the `conf` structs, `/metrics`, Helm bundles,
  make targets, CI) are documented where the relevant chapter already owns them.
- **Plugin registration** (filters/weighers/detectors, extractors, KPIs, datasource kinds) and
  the extension workflow live in `02-external-scheduler-api/07-extending-cortex.md`.

## §7 `kubectl` and command conventions

- **All Cortex CRDs are cluster-scoped.** Never use `-n`/`--namespace` in examples for Cortex
  kinds. The API group is `cortex.cloud/v1alpha1`.
- **Show real `kubectl get` output.** Column headers in fenced output blocks must match the
  `+kubebuilder:printcolumn` markers on the type in `api/v1alpha1/<kind>_types.go`, in order.
  Field paths in prose (e.g. `.status.dependenciesReadyFrac`) must be real.
- **Point to `-o yaml`** for full spec/status, and name the specific conditions/fields worth
  reading rather than dumping everything.
- **Describe validation exactly as the code does.** Do not claim enforcement, rejection, or
  admission behaviour the webhooks/controllers do not implement. Example: the Pipeline admission
  webhook *hard-rejects* invalid params and wrong-kind steps but only *warns and ignores* an
  unknown step name (surfaced via the `All Steps Known` / `AllStepsIndexed` condition) — a
  successful apply is therefore not proof of registration.
- Fenced blocks are tagged with a language (` ```bash `, ` ```yaml `, plain ` ``` ` for output).

## §8 Stability rules

RECIPE exists to keep the docs **stable**: two independent passes over the same code state must
reach the same conclusions. When measuring the docs or proposing changes:

1. **Code-driven only.** A change is warranted only when a code change created, altered, or
   removed a surface this RECIPE says must be documented. If the code did not change the surface,
   the existing text stays — even if you would have written it differently.
2. **No stylistic churn.** Do not propose rewording, reordering, or reorganizing that RECIPE
   does not mandate. Preferring different prose is not a finding.
3. **Match, don't improve.** A page that already conforms to RECIPE is finished. "Good enough"
   is the target; do not gold-plate beyond what the code warrants.
4. **Scope the change to the divergence.** A new CRD Kind scopes to the coverage §6/§7 require;
   a new component scopes to its page; a genuine structural drift from this tree scopes to the
   realigning move. Neither pad nor shrink the scope artificially.
5. **Respect authored intent.** Existing conventions in a page that conform to RECIPE are load-
   bearing. Do not undo an author's structural choice (e.g. merging a walkthrough into its
   concept page, a chosen page order, a chosen example) unless a code change or a RECIPE rule
   requires it.
6. **When RECIPE is silent, do nothing.** If neither the code nor a RECIPE rule decides a
   question, propose nothing and leave the text as-is.
7. **Preserve the invariants.** SPDX header, single H1, single `## Next`, continuous prev/next
   chain, contiguous page numbering, clean leak scan, and code-grounded examples are invariants —
   never break one to make another change.
