<!-- Copyright SAP SE -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Cortex Knowledge Database 2.0

A four-slide IPCEI-CIS Hackathon pitch, followed by an end-to-end terminal demonstration:

1. **Context and full pipeline** — what Cortex does, followed by ingestion → storage → knowledge extraction → filtering and smart weighing, with new capabilities marked directly in the flow.
2. **Before / now** — fork Cortex versus importing it as a library or declaring expressions for both Knowledge and Pipeline resources.
3. **Knowledge deep-dive** — resolve inputs, join metrics with Nova hypervisors, aggregate contention, and publish the resulting status YAML.
4. **Storage deep-dive** — abstract Postgres behind an object API, then implement that API with Valkey.

The terminal demo follows slide 4; no additional demo slide is included.

## Present

Open [index.html](index.html) directly in a modern browser. All visuals, styles, and scripts are local; no server or internet connection is needed to present. From the repository root on macOS:

```sh
open docs/presentations/knowledge-database-2.0/index.html
```

- **← / →** or **Page Up / Page Down**: navigate slides.
- **Space / Shift+Space**: next / previous slide; on the demo, Space plays or pauses.
- **Home / End**: first / last slide.
- **O / N / F**: overview / speaker notes / fullscreen.
- **?**: shortcuts and Print / Save as PDF.
- **Esc**: close a panel. Swipe horizontally to change slides on touch screens.

`index.html#slide-3` opens the knowledge deep-dive directly. A [PDF export](cortex-knowledge-2.0.pdf) is included for sharing; its manifest links point to the files on GitHub rather than a local checkout.

## Knowledge deep-dive

Slide 3 **autoplays on entry and loops continuously**. The animation highlights each YAML block and shows:

1. Ingest two datasources: Prometheus CPU contention signals and Nova hypervisors.
2. Join metric `Node` to hypervisor `Hostname`, resolving the Nova `ServiceHost`.
3. Aggregate six samples into three host-contention features (average and maximum).
4. Highlight the resulting **`status.raw.features` YAML**, including all three extracted features.
5. Illustrate how smart weighing can prefer `compute-02`, with 8% average contention, highlighting that feature in the status YAML.

Timing: **ingest 2s → join 3s → aggregate 3s → status YAML 3s → weighing 4s → reset 0.8s**, then repeat. Host colors stay consistent across sources, charts, and the selected result. Matching hostname pairs light up together; values become bars and reorder before the YAML output is revealed.

Use **P** or **Space** to pause/resume, **R** to restart the loop, or **1–5** to select a step and pause. The on-slide buttons provide the same controls. Playback automatically suspends while another slide, a dialog, or a hidden browser tab obscures the demo; it resumes from the remaining step time when visible again. An explicit user pause stays paused.

Reduced-motion preferences start with the final result visible and autoplay off. Manual playback and step selection remain available. Printing shows the resulting status YAML and final placement, then restores the prior playback state.

The overview pipeline, expression card, and new Valkey adapter use brief entrance animations. They respect reduced-motion preferences and settle after their introductions.

Slide 2 uses short code excerpts. The fork example follows the original `ExtractSQL` pattern; the framework example follows the documented `pkg/manager` entry point and plugin self-registration. The Knowledge expression projects `ServiceHost` values. The Pipeline expression is the body of the built-in `filter_expression` step, with the complete variable binding in [pipeline.yaml](pipeline.yaml).

This is an animated fixture walkthrough, not a live cluster connection or an in-browser Expr interpreter. The manifests use the actual PR API; the join, aggregation, and output are verified with Cortex's evaluator, Nova model, and Prometheus `NodeExporterMetric` model.

The average / maximum values are `compute-01: 80 / 86`, `compute-02: 8 / 10`, and `compute-03: 42 / 46`. Knowledge features are sorted by average contention. The sample Pipeline keeps hosts below 50%, retaining `compute-02` and `compute-03`. Pipeline expressions are boolean candidate filters evaluated before existing weighers. The animated VM placement illustrates preference when other constraints and weights are equal.

## End-to-end terminal demo

Continue in the terminal after slide 4. The supplied manifests are [datasource.yaml](datasource.yaml), [metrics-datasource.yaml](metrics-datasource.yaml), [knowledge.yaml](knowledge.yaml), and [pipeline.yaml](pipeline.yaml). Use a controller and CRDs that include the framework changes from [#1556](https://github.com/cobaltcore-dev/cortex/pull/1556), Valkey from [#1558](https://github.com/cobaltcore-dev/cortex/pull/1558), and dynamic pipeline filtering from [#1560](https://github.com/cobaltcore-dev/cortex/pull/1560). Enable the corresponding controllers and configure these Secret references in namespace `cortex`:

- `openstack`: Keystone credentials for the Nova datasource.
- `prometheus`: the Prometheus endpoint (`url`).
- `cortex-nova-valkey`: Valkey connection settings (`host`, `port`, `password`; optional `username` and `db`).

Adjust the references for your environment. Datasource and Knowledge resources are cluster-scoped. If `nova-hypervisors` already exists, reuse it.

The metrics datasource uses `node_exporter_metric` with CPU waiting pressure as a contention signal: `100 * rate(node_pressure_cpu_waiting_seconds_total[5m])`, grouped by `node`. Your metrics must have a nonempty `node` label that matches Nova's `Hypervisor.Hostname`. The datasource queries a five-minute window, and the expression averages those samples. Hypervisors with no matching samples are omitted rather than treated as contention-free. Existing VMware/vROps bundles use their own hostsystem resolution; this is a standalone demonstration.

```sh
kubectl apply -f docs/presentations/knowledge-database-2.0/datasource.yaml
kubectl wait --for=condition=Ready datasource/nova-hypervisors --timeout=120s
kubectl apply -f docs/presentations/knowledge-database-2.0/metrics-datasource.yaml
kubectl wait --for=condition=Ready datasource/host-cpu-metrics --timeout=120s
kubectl apply -f docs/presentations/knowledge-database-2.0/knowledge.yaml
kubectl wait --for=condition=Ready knowledge/host-cpu-contention --timeout=90s
kubectl get knowledge host-cpu-contention -o jsonpath='{.status.raw}'
kubectl apply -f docs/presentations/knowledge-database-2.0/pipeline.yaml
kubectl get pipeline nova-contention-demo -o yaml
```

The expression yields three features for the displayed fixture. A live deployment returns its actual host contention values. The provided Pipeline demonstrates dynamic candidate filtering; integrate it into the intended scheduling request path and configure weighers for the live placement scenario.

## Implementation grounding

- [#1556](https://github.com/cobaltcore-dev/cortex/pull/1556): public framework, object-based extraction, and Knowledge expressions. Inputs belong under `spec.inputs`, beside `spec.expression`.
- [#1558](https://github.com/cobaltcore-dev/cortex/pull/1558): `pkg/knowledge/store/valkey`, generation-based object snapshots, cache recovery, and Valkey bundle configuration.
- [#1560](https://github.com/cobaltcore-dev/cortex/pull/1560): Nova's built-in `filter_expression` step and shared expression evaluator. `host`, `request`, and named Knowledge inputs form its environment.

The deck covers implementations across these hackathon PRs. Postgres is the original adapter; the Valkey PR replaces it in that branch. The diagrams show backend interchangeability and that progression, not simultaneous Postgres/Valkey support in the Valkey branch. Cached datasource objects are stored in Valkey, while extracted features remain in Kubernetes Knowledge status.

Expressions over available inputs allow new knowledge and scheduling filters with the same Cortex operator. New compiled Go plugins or backend implementations require code and integration. The reusable knowledge components are under `pkg/`; the bundled manager still includes internal domain integrations.

The official Cortex logo is copied unchanged from `docs/assets/Cortex_Logo_black_space_square_bg_rd@2x.png`, referenced by the repository README, into `assets/cortex-logo.png` so the deck stays portable and offline.

Keep this directory together when sharing the HTML version: `index.html` uses the adjacent CSS, JavaScript, example manifests, and logo asset.
