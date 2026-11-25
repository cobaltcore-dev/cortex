<!-- Copyright SAP SE -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Cortex Knowledge Database 2.0

A four-slide IPCEI-CIS Hackathon pitch, approximately two to three minutes:

1. **Title** — improving the reusability of our data caching and aggregation layer for smart scheduling.
2. **Smart scheduling** — ingest Prometheus metrics and Nova hypervisors, combine them into host contention, and prefer less contended hosts for new VMs.
3. **Reusability** — side-by-side code examples: a Go extractor in a Cortex fork, a custom project importing Cortex, and an expression in a Knowledge spec.
4. **Animated demo** — join CPU contention metrics with Nova hypervisors, aggregate per-host contention, and illustrate its use by smart weighing.

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

`index.html#slide-4` opens the demo directly. A [PDF export](cortex-knowledge-2.0.pdf) is included for sharing; its manifest links point to the files on GitHub rather than a local checkout.

## Animated demo

Slide 4 **autoplays on entry and loops continuously**. The animation highlights each YAML block and shows:

1. Ingest two datasources: Prometheus CPU contention signals and Nova hypervisors.
2. Join metric `Node` to hypervisor `Hostname`, resolving the Nova `ServiceHost`.
3. Aggregate six samples into three host-contention features (average and maximum).
4. Highlight the resulting **`status.raw.features` YAML**, including all three extracted features.
5. Illustrate how smart weighing can prefer `compute-02`, with 8% average contention, highlighting that feature in the status YAML.

Timing: **ingest 2s → join 3s → aggregate 3s → status YAML 3s → weighing 4s → reset 0.8s**, then repeat. Host colors stay consistent across sources, charts, and the selected result. Matching hostname pairs light up together; values become bars and reorder before the YAML output is revealed.

Use **P** or **Space** to pause/resume, **R** to restart the loop, or **1–5** to select a step and pause. The on-slide buttons provide the same controls. Playback automatically suspends while another slide, a dialog, or a hidden browser tab obscures the demo; it resumes from the remaining step time when visible again. An explicit user pause stays paused.

Reduced-motion preferences start with the final result visible and autoplay off. Manual playback and step selection remain available. Printing shows the resulting status YAML and final placement, then restores the prior playback state.

The first three slides use brief entrance animations that settle within four seconds: the YAML card drifts into the title graphic, Cortex responds with a small halo, the VM lands beside a miniature server rack, and the YAML example drops into place with a confirmation badge. These animations replay on slide entry and respect reduced-motion preferences. Content stays still after the introductions.

Slide 3 uses short code excerpts. The fork example follows the original `ExtractSQL` pattern; the framework example follows the documented `pkg/manager` entry point and plugin self-registration. Its YAML example projects `ServiceHost` values from the Nova datasource. The next slide uses the full contention manifest.

This is an animated fixture walkthrough, not a live cluster connection or an in-browser Expr interpreter. The manifests use the actual PR API; the join, aggregation, and output are verified with Cortex's evaluator, Nova model, and Prometheus `NodeExporterMetric` model.

The average / maximum values are `compute-01: 80 / 86`, `compute-02: 8 / 10`, and `compute-03: 42 / 46`. The expression sorts features by average contention. Applying the Knowledge publishes data; a compatible consumer must reference that data for scheduling. The final VM placement illustrates the preference when other constraints and weights are equal.

## Run against a cluster

Download `datasource.yaml`, `metrics-datasource.yaml`, and `knowledge.yaml` from slide 4. The displayed code is the Knowledge's `spec`; the download contains the complete resource. Use the controller and CRDs from [PR #1556](https://github.com/cobaltcore-dev/cortex/pull/1556), enable datasource and knowledge controllers, and configure these example Secret references in namespace `cortex`:

- `openstack`: Keystone credentials for the Nova datasource.
- `prometheus`: the Prometheus endpoint (`url`).
- `postgres`: database connection credentials.

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
```

The expression yields three features for the displayed fixture. A live deployment returns its actual host contention values.

## Implementation grounding

- `api/v1alpha1/{datasource,knowledge}_types.go`: resource schemas. Inputs belong under `spec.inputs`, beside `spec.expression`.
- `pkg/knowledge/{datasources,store,inputs,extractor}`: public framework and object-based extraction.
- `pkg/knowledge/store/postgres`: implemented cache adapter. Redis and Kubernetes-backed caches are possible future adapters, not included backends.
- `pkg/knowledge/extractor/expression.go`: expression execution.
- `docs/04-knowledge-database/`: datasource, input, and extraction documentation.

Expression extraction over available datasources uses the same Cortex image with zero core-code changes. New compiled Go plugins or storage adapters still require implementation and integration. The reusable knowledge components are under `pkg/`; the bundled manager still includes internal domain integrations.

The official Cortex logo is copied unchanged from `docs/assets/Cortex_Logo_black_space_square_bg_rd@2x.png`, referenced by the repository README, into `assets/cortex-logo.png` so the deck stays portable and offline.

Keep this directory together when sharing the HTML version: `index.html` uses the adjacent CSS, JavaScript, example manifests, and logo asset.
