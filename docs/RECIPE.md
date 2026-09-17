<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Documentation Recipe

This document defines **how the Cortex documentation under `docs/` should be written and
organized**. It is the source of truth for the shape of the docs: what belongs where, how
each page reads, and how the whole thing stays coherent as the codebase evolves.

It is a *standard*, not a task list. It describes the target state and the reasoning behind
it so that any competent writer — human or automated — reaches the same structure from the
same code. It deliberately says nothing about *when* or *how often* the docs are checked, or
what tooling drives those checks; that is the concern of whoever applies this recipe, not of
the recipe itself.

Two properties matter above all and everything below serves them:

1. **Convergence.** Given the same codebase, independent writers following this recipe should
   produce substantially the same file layout, the same page set, and the same content in
   each page. Ambiguity is the enemy: wherever a choice exists, this recipe makes it for you.
2. **Stability.** The docs change *only* when the code they describe changes. Reorganizing
   files, renaming pages, or rewriting prose that is still accurate is a defect, not an
   improvement. See [Stability rules](#stability-rules) — they override any stylistic
   preference below.

---

## 1. Audiences

Cortex documentation serves three audiences. Every page targets exactly one primary audience;
naming it (implicitly, through placement and voice) keeps pages from drifting into serving
everyone and no one.

| Audience | Who they are | What they need |
|---|---|---|
| **Operators** | Deploy and run Cortex on a cluster (typically OpenStack/KVM operators integrating with Nova, Cinder, Manila, Placement). | Install per domain, configure via Helm values and CRDs, wire feature toggles, monitor metrics/alerts, run multicluster, operate reservations. This is the largest surface. |
| **Integrators** | Wire Cortex into an existing scheduler — chiefly Nova's external-scheduler hook and the Placement API shim (passthrough vs. KVM backend, auth). | Understand the delegation model and the shim contract; know exactly which requests are intercepted and how. |
| **Developers** | Extend Cortex — write filters/weighers/detectors, datasource/knowledge/KPI plugins, or new controllers; hack on the code. | Local dev loop (Tilt), the plugin model, CRD Go types and webhooks, testing and `make`. |

When a topic is relevant to more than one audience, write it once for its primary audience and
**link** from the others (see [Cross-linking](#cross-linking)) rather than duplicating it.

---

## 2. Content types (Diátaxis)

Every page in `docs/` is **exactly one** of four content types. This is the single most
important structural rule: it is what makes the docs navigable and what makes independent
writers converge. The four types come from the [Diátaxis framework](https://diataxis.fr/),
which the Kubernetes docs use directly.

| Type | Reader's question | Purpose | Voice | Why/how ratio |
|---|---|---|---|---|
| **Concept** (explanation) | "Why does it work this way? How do the pieces fit?" | Build a mental model. | Discursive, connects ideas, may use diagrams. | ~80% why |
| **Guide** (how-to) | "How do I accomplish *X*?" | Get a competent reader to a goal. | Imperative steps; assumes competence. | ~20% why, one sentence of rationale up top |
| **Tutorial** | "Teach me by doing." | Take a beginner end-to-end to a guaranteed-working result. | "We'll…"; hand-holding; every step succeeds. | why *before* each step |
| **Reference** | "What is the exact field / flag / value?" | State facts exhaustively and terse-ly. | Austere, table-driven, no teaching. | ~0% why |

**The one rule that prevents the most damage:** a page does not mix types. A Reference page
does not lecture; a Tutorial does not catalog every option; a Concept page does not become a
step-by-step. When a page starts to mix, split it and cross-link.

Mapping to Cortex material (non-exhaustive, but this is the intended assignment):

- **Concept** — what Cortex is and the knowledge→pipeline→decision→reservation flow; the Nova
  delegation model; the CRD/controller reconciliation model; the Placement API shim concept;
  multicluster topology (home vs. remote); the pending-cache overlay's rationale.
- **Guide** — install a domain bundle in production; configure a datasource; enable a shim
  endpoint; set up multicluster; operate committed-resource and failover reservations;
  monitor via Prometheus/alerts.
- **Tutorial** — the local Tilt quickstart (dev), taken end-to-end to a running Cortex.
- **Reference** — the CRD reference (the `cortex.cloud/v1alpha1` Kinds); the configuration
  reference (Helm values keys, `conf` fields including `enabledControllers`/`enabledTasks`,
  feature toggles, secrets); CLI flags per binary; metrics and alerts.

---

## 3. Information architecture

The `docs/` tree is organized **by content type and reader need, not by code module**. A
reader arrives with an intent ("understand", "do", "look up"), not with a directory name in
mind. The top level therefore mirrors the Diátaxis types, ordered easy→deep.

### 3.1 Canonical layout

This is the target directory structure. Writers place new material into these directories by
content type; they do not invent new top-level directories (see [Stability rules](#stability-rules)).

```
docs/
├── readme.md              # Landing page: what Cortex is + intent-based entry points
├── glossary.md            # One-line definitions of every domain term
├── concepts/              # Explanation — mental models, architecture
├── guides/                # How-to — goal-oriented, one goal per page
├── tutorials/             # Learning-oriented, end-to-end, chained
├── reference/             # Facts — CRDs, config, flags, metrics
├── adrs/                  # Architecture Decision Records (append-only log; not Diátaxis)
└── assets/                # Images, diagrams
```

- **`readme.md`** is the only entry point. It states what Cortex is in a few sentences, then
  offers **intent-based links**: "Understand Cortex → concepts", "Install / operate →
  guides", "Try it locally → tutorials", "Look up a field → reference". It does not itself
  explain anything at depth; it routes.
- **`adrs/`** is a special case: an append-only historical record of decisions, not one of the
  four content types. ADRs are never rewritten to match new code — a superseded decision gets
  a *new* ADR that supersedes it. Leave existing ADRs alone.
- Sub-directories under `concepts/`, `guides/`, `reference/`, `tutorials/` are permitted **only**
  when a single topic genuinely spans multiple pages or ships companion assets (e.g.
  `reference/crds/`, or a tutorial's script directory like `tutorials/multicluster/`).
  Prefer a single page until it demonstrably needs splitting; see
  [Page length](#page-length-and-splitting).

### 3.2 Deterministic placement

To keep placement convergent, apply these rules in order when deciding where a piece of
content lives:

1. **Ask the reader's question.** "why/how-it-fits" → `concepts/`; "how do I *X*" → `guides/`;
   "teach me from zero" → `tutorials/`; "what is the exact value" → `reference/`.
2. **One goal or one concept per page.** A guide covers one goal end-to-end. A concept covers
   one mental model. Do not merge two goals to save a file.
3. **Name the file after the reader's need, not the code.** `guides/install-nova-bundle.md`,
   not `guides/cortex-nova-chart.md`. `concepts/delegation-model.md`, not
   `concepts/external.go.md`. Filenames are kebab-case.
4. **Reference is mechanically derived from the code surface.** The set of reference pages is
   determined by the code, so it is the most convergent: one page (or one section) per CRD
   Kind, per binary's flags, per config layer. When the code adds a CRD Kind, a reference
   entry is added; when it removes one, the entry is removed. Nothing else moves.

---

## 4. The Cortex-specific content map

This section fixes *what pages must exist* for the current system, so that writers converge on
the same page set rather than inventing their own. It is expressed in terms of the real
artifacts in the repo. When the code changes, this map is how you know whether a doc change is
warranted (see [Stability rules](#stability-rules)).

### 4.1 Components

Cortex ships **three released components**; the docs must make the distinction explicit
because operators deploy them separately:

- **cortex core** — the `manager` binary (`cmd/manager`) and the `cortex` library Helm chart;
  the operator running the knowledge and scheduling controllers.
- **cortex-postgres** — the bundled Postgres image/chart storing ingested and enriched data.
- **cortex-shim** — the `shim` binary (`cmd/shim`) and the `cortex-shim` chart; the OpenStack
  Placement-API-compatible HTTP front (passthrough or KVM backend).

### 4.2 Required pages

**Concepts**
- Overview: what Cortex is and the end-to-end flow (datasources → knowledge → pipeline →
  decision → reservation/descheduling). One architecture diagram lives here.
- Delegation model: how Nova calls Cortex as an extra scheduling step; forced destinations.
- CRD & controller model: desired-vs-actual reconciliation, the knowledge/scheduling CRD split.
- Placement API shim: what it intercepts, passthrough vs. KVM backend, auth.
- Multicluster: home vs. remote clusters and resource routing.
- Pending-cache overlay: why it exists (informer lag) and how it behaves.

**Guides** (one goal each)
- Install a domain bundle in production (name the bundles: `cortex-nova`, `cortex-cinder`,
  `cortex-manila`, `cortex-ironcore`, `cortex-pods`; install `cortex-crds` first — state the
  deploy order).
- Configure datasources / knowledge / KPIs via CRs.
- Configure and enable shim endpoints (the `features.*` toggles).
- Set up multicluster.
- Operate committed-resource reservations and failover reservations.
- Monitor Cortex (Prometheus metrics, alerts, dashboards).
- Extend Cortex: write a filter/weigher/detector or a datasource/knowledge/KPI plugin (developer).

**Tutorials**
- Local development with Tilt, end-to-end to a running Cortex (the current `quickstart.md`
  material belongs here, rewritten as a tutorial).

**Reference**
- CRD reference: the `cortex.cloud/v1alpha1` Kinds — currently **Datasource, Knowledge, KPI,
  Pipeline, Decision, Descheduling, Reservation, CommittedResource, ProjectQuota,
  FlavorGroupCapacity, History** — plus externally consumed CRDs noted as *consumed, not owned*
  (Hypervisor `kvm.cloud.sap/v1`; IronCore `Machine`/`MachinePool`/`MachineClass`).
- Configuration reference: Helm values keys per chart; `conf` fields including
  `enabledControllers` and `enabledTasks`; feature toggles; secrets shape.
- CLI flags per binary (`manager`, `shim` — including `--self-heal`, `--placement-shim`).
- Metrics and alerts (`cortex_*`).

> [!NOTE]
> The Kind list above is illustrative of the *current* surface. The authoritative source is
> `PROJECT` plus `api/v1alpha1/*_types.go`. When those change, the CRD reference changes to
> match — and only then.

---

## 5. Writing style per content type

The style rules below are what make prose convergent. They are keyed to content type; apply
the row for the page you are writing.

### 5.1 Concepts (explanation)

- Lead with the mental model in the first paragraph ("Cortex models placement as…"), then
  develop it. Steer away from procedure; link to the relevant guide instead of listing steps.
- **At most one canonical example**, placed *after* the prose, lightly annotated. Delegate
  exhaustive fields to the reference page.
- Diagrams belong here (architecture, the reconciliation loop, multicluster topology). Prefer
  Mermaid so diagrams live in-repo and diff cleanly. Diagrams do **not** appear in guides,
  tutorials, or reference.
- Headings are nouns or questions: "The delegation model", "What is a shim?".

### 5.2 Guides (how-to)

- One goal per page; the page title *is* the goal ("Install the Nova bundle").
- Open with **one sentence** of rationale (why you'd do this), then imperative steps.
- Each step: the command in a fenced block → one line of what it did → **show the expected
  output as its own block** when the command produces meaningful output (`kubectl get …`,
  `helm install …`). Showing output is a trust-builder and is expected of Cortex guides.
- Assume competence; do not re-teach Kubernetes or Helm basics — link to their docs.

### 5.3 Tutorials

- Beginner-safe and end-to-end: every step must succeed for a reader starting from zero.
- **Literate style:** interleave prose *between* code blocks; front-load a sentence saying what
  the next block does before showing it. Explain any non-obvious marker or flag on first use.
- Keep steps short; a tutorial reads top-to-bottom as one session.

### 5.4 Reference

- Terse, exhaustive, table-driven. No teaching, no rationale.
- CRD fields, config keys, and flags are documented in tables with columns like
  `Field | Type | Default | Description`. One row per field.
- Examples are minimal syntax specs only (a `kubectl get <kind>` line, a one-line values
  snippet) — never a walkthrough.
- Reference pages are modular: each is independently addressable so a reader with one question
  lands on exactly the right page.

---

## 6. Formatting conventions

These are fixed so that repeated passes don't churn on cosmetics.

- **License header.** Every markdown file begins with the standard SPDX comment block (see the
  top of this file). Never omit it.
- **Headings.** Concept/reference headings are nouns/questions; guide/tutorial headings are
  action verbs. One `#` H1 per page (the title). Use `##`/`###` for structure; avoid going
  deeper than `####`.
- **Callouts.** Use GitHub alert syntax with a fixed, minimal set — do not invent others:
  - `> [!NOTE]` — a constraint or clarification.
  - `> [!TIP]` — practical advice.
  - `> [!WARNING]` — danger, data loss, or a footgun.
- **Code blocks.** Always language-tagged (```bash`, ```yaml`, ```go`, ```mermaid`). Commands
  and their output go in separate blocks.
- **Tables** for any enumerable set (fields, toggles, flags, endpoints).
- **Terminology.** Use the glossary's term for each concept consistently; do not introduce
  synonyms for an already-named concept.

### Page length and splitting

- **Concepts:** bounded (~a screen or two). If a concept sprawls, it's probably two concepts —
  split and cross-link.
- **Guides:** may be long, but stay single-goal. Length from covering one goal thoroughly is
  fine; length from covering three goals is not.
- **Tutorials:** short, sequential steps.
- **Reference:** as long as the surface requires; keep it modular so each Kind/flag/key is
  addressable.

---

## 7. Findability

A reader with one specific question must reach the right page fast.

### Titles
Title a guide as the reader's goal, a concept as its noun/question, a reference page as the
thing it documents. A reader scanning the sidebar or search results should recognize the page
from its title alone.

### Cross-linking
- On the **first mention** of any domain term on a page, link it to its concept page or
  glossary entry.
- Every concept links **down** to its reference; every reference links **up** to its concept;
  every guide links to both the concept it relies on and the reference it uses.
- End substantive pages with a short **"Next steps"** section linking to the next logical page
  and to the other content types on the same topic.
- Link out to upstream docs (Kubernetes, Helm, OpenStack) rather than re-explaining them.

### Progressive disclosure
- A beginner reaches a working result via the Tilt tutorial in a handful of commands, and an
  operator reaches a production install via one guide — **onboarding pages link out to depth
  rather than inlining it.**
- An expert reaches an exact field or flag in the reference in at most a couple of clicks from
  the landing page.

---

## 8. Stability rules

These rules **override** the stylistic guidance above. Their purpose is to guarantee that the
docs change only in response to code changes, and that repeated passes converge instead of
churning. When a preference elsewhere in this recipe conflicts with a stability rule, the
stability rule wins.

1. **Change is code-driven.** A documentation change is warranted only when a corresponding
   change exists in the code, config, CRDs, charts, flags, or behavior. Prose that is still
   accurate is left exactly as it is — accurate prose is never "improved" for style alone.

2. **The layout is fixed.** The top-level directories in [§3.1](#31-canonical-layout) and the
   required page set in [§4.2](#42-required-pages) are the target. Do not move files, rename
   pages, split, or merge unless the underlying code change *mandates* it (a Kind was added,
   a component was removed, a goal no longer exists). Reorganizing a stable, correct tree is a
   defect.

3. **Prefer the smallest edit that restores accuracy.** When code changes, update the specific
   sentence, row, or block that is now wrong. Do not rewrite a whole page because one field
   changed. Do not restructure a section because one step was added.

4. **Reference tracks the code mechanically.** The CRD reference reflects `PROJECT` +
   `api/v1alpha1/*_types.go`; the config reference reflects the `conf` structs and
   `values.yaml`; the flags reference reflects each binary's flag set. These are the pages that
   *should* change when the code does — a new Kind means a new reference entry, a removed flag
   means a removed row. Nothing else moves as a side effect.

5. **New material lands in its determined place.** When genuinely new content is needed, place
   it using the [deterministic placement](#32-deterministic-placement) rules — the content
   type and reader's-need naming decide the location, so two writers put it in the same file.

6. **Never touch ADRs to match new code.** ADRs are historical. Supersede with a new ADR;
   never edit an old one to reflect current behavior.

7. **Ambiguity resolves toward the status quo.** If it is unclear whether a change is
   warranted, or where new content belongs, and this recipe does not decide it, keep the
   existing structure and make the minimal edit. Do not resolve ambiguity by reorganizing.
