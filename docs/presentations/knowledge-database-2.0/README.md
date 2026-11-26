<!-- Copyright SAP SE -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Cortex Knowledge Database 2.0

A four-slide IPCEI-CIS Hackathon presentation:

1. **The Cortex flow** — a connected workshop path: source data cards enter a cache, become Kubernetes Knowledge, and guide a virtual machine to a host.
2. **Added support for Valkey / as data cache** — swap Postgres and Valkey cache cartridges while the surrounding connections stay in place; compare the real Datasource storage spec.
3. **Added support for expressions / for knowledge extraction** — YAML is the recipe, Cortex is the processor, and the resulting status YAML is its printed receipt.
4. **Added support for expressions / for scheduling pipelines** — a filter gate accepts or returns host tickets as you change the dynamic rule.

## Present

Open [index.html](index.html) in a modern browser. The presentation works directly from disk, without a server, build step, or internet connection. Keep this directory together when sharing the HTML version.

```sh
open docs/presentations/knowledge-database-2.0/index.html
```

A [PDF export](cortex-knowledge-2.0.pdf) is included. It shows all four slides in their After state, with the completed knowledge result. PDF download links point to the example manifests on GitHub, not a local checkout.

### Controls

- **← / →**, **Page Up / Page Down**, or **Space / Shift+Space**: change slides.
- **Home / End**: first / last slide.
- **Before / After** buttons, or **B / A**: compare versions on slides 2–4.
- **P** or **Pause / Play**: control the repeating knowledge animation on slide 3.
- **R** or **Replay**: restart the active illustration on slides 3–4.
- **O / N / F**: overview / speaker notes / fullscreen.
- **?**: help and Print / Save as PDF. **Esc** closes a dialog.
- Swipe horizontally to change slides on touch screens.

Link to any slide with its URL hash, e.g. `index.html#slide-3`.

The workshop visuals use short, explanatory motion. Slide 1's data transfers finish after a few seconds. Before/After swaps the cache cartridge on slide 2 and highlights the matching YAML change. Static labels and connections remain visible after the movement settles.

The knowledge animation starts automatically in its After state, highlighting input resolution, expression evaluation, and publication to `status.raw.features`. Input cards feed the processor, a receipt emerges, and the real status YAML is highlighted. It loops until paused. Leaving the slide, selecting Before, opening a dialog, or hiding the browser tab suspends playback. Explicit pauses remain paused.

The pipeline animation runs once on entry or after changing its rule. Gate arms open for matching host tickets, while rejected tickets stop and return. The slider changes the illustrated threshold between 10% and 90%; the download remains the complete **50% example**. At 50%, `compute-02` and `compute-03` survive; at 20%, only `compute-02` survives; at 90%, all three survive.

Reduced-motion preferences disable initial autoplay and show completed results. Before/after comparisons and the threshold control remain available. Printing switches to After/final states, then restores the previous comparison and animation state.

## What the examples show

The screenshots and animations are native HTML/CSS, not a live cluster connection or an in-browser Expr interpreter. The expression bodies, resource shapes, and displayed status are checked against the implementation and real input models.

- [datasource.yaml](datasource.yaml): Nova hypervisors cached with Valkey.
- [metrics-datasource.yaml](metrics-datasource.yaml): CPU waiting pressure as a contention signal, using the registered `node_exporter_metric` model.
- [knowledge.yaml](knowledge.yaml): group metric samples by `Node`, join to Nova `Hypervisor.Hostname`, and produce average/maximum contention per compute host.
- [pipeline.yaml](pipeline.yaml): the built-in `filter_expression` step keeps a candidate whose extracted average contention is below 50%.

The sample averages and maxima are `compute-01: 80 / 86`, `compute-02: 8 / 10`, and `compute-03: 42 / 46`. Knowledge features are sorted by average contention. Hypervisors without matching samples are omitted, not assumed contention-free.

The before Go code is a short excerpt of the original `ExtractSQL` pattern; receiver definitions, the query, and plugin registration are omitted. The before pipeline shows a real pre-implemented VMware contention weigher, with its parameters omitted. The displayed Datasource and Pipeline code blocks are spec excerpts; downloads contain complete resources.

## Implementation references

- [PR #1556](https://github.com/cobaltcore-dev/cortex/pull/1556): public knowledge framework, object-based extraction, and Knowledge expressions.
- [PR #1558](https://github.com/cobaltcore-dev/cortex/pull/1558): Valkey storage adapter and cache recovery. The branch replaces Postgres; the comparison illustrates the transition, not simultaneous backend support in that branch.
- [PR #1560](https://github.com/cobaltcore-dev/cortex/pull/1560): Nova's dynamic `filter_expression` step. It evaluates a boolean per host before existing weighers; it does not add dynamic numeric weighers.
- [Expr language definition](https://expr-lang.org/docs/language-definition).

New expression-based knowledge and filters use the operator containing those implementations, without custom Go additions or an image rebuild. New compiled plugins, datasource kinds, or storage adapters still require implementation and integration. This pitch covers the hackathon PRs, not a claim that all changes are already in an upstream release.

The official Cortex logo is copied unchanged from `docs/assets/Cortex_Logo_black_space_square_bg_rd@2x.png` as `assets/cortex-logo.png` for offline portability.

## Optional end-to-end terminal demo

For the prepared demo on `knowledge-db-2.0-and-expr-filter`, follow the existing
[expression filter demo walkthrough](../../../samples/nova/expression-filter-demo/README.md).
It uses synthetic metric samples and the deployment's Nova hypervisor datasource. The
presentation transfer leaves that demo unchanged; Valkey is covered by the separate storage PR.

Use matching controller and CRDs containing the relevant changes above, with the corresponding controllers enabled. Configure these Secret references in namespace `cortex`:

- `openstack`: Keystone credentials.
- `prometheus`: the Prometheus endpoint (`url`).
- `cortex-nova-valkey`: Valkey `host`, `port`, `password`; optionally `username` and `db`.

The Prometheus `node` label must match Nova's `Hypervisor.Hostname`. The query uses CPU waiting pressure over a five-minute window. Adjust source queries, labels, and Secret references for your environment. Reuse existing datasources instead of overwriting their configuration.

```sh
kubectl apply -f docs/presentations/knowledge-database-2.0/datasource.yaml
kubectl apply -f docs/presentations/knowledge-database-2.0/metrics-datasource.yaml
kubectl wait --for=condition=Ready datasource/nova-hypervisors --timeout=120s
kubectl wait --for=condition=Ready datasource/host-cpu-metrics --timeout=120s
kubectl apply -f docs/presentations/knowledge-database-2.0/knowledge.yaml
kubectl wait --for=condition=Ready knowledge/host-cpu-contention --timeout=90s
kubectl get knowledge host-cpu-contention -o yaml
kubectl apply -f docs/presentations/knowledge-database-2.0/pipeline.yaml
kubectl get pipeline nova-contention-demo -o yaml
```

The sample Pipeline demonstrates dynamic filtering. Use it in the intended scheduling request path, with the appropriate weighers, for a live placement demonstration. Live contention values will reflect the deployment, not the illustrative fixture.
