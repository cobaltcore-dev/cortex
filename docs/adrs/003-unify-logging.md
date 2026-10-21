Architecture Decision Record 003

---

# Unify Logging on controller-runtime (logr), Remove log/slog

Status: Accepted

## Background

cortex historically carried **two parallel logging systems**:

- **`log/slog`** — used by the knowledge pipeline, OpenStack datasources, the
  scheduling filter/weigher pipeline, KPIs, and shared `pkg/` helpers
  (~130 files, ~400 call sites). Configured once in `cmd/manager/main.go` via
  `slog.SetDefault` with a JSON handler wrapped by a custom metrics handler.
- **controller-runtime `logr`** (backed by zap) — used by every Kubernetes
  controller/reconciler. Obtained via `ctrl.LoggerFrom(ctx)` or named
  package-level loggers off `ctrl.Log` / `logf.Log`.

The two were only loosely bridged: both honored the same `logLevel` helm value
(fanned out to `--zap-log-level` *and* a `LOG_LEVEL` env var), and both fed
warn/error counts into one Prometheus metric (`cortex_log_messages_total`) via
two separate hooks — an slog `MetricsSlogHandler` and a zap core wrapper.

This split meant two configuration paths, two metrics hooks, and inconsistent
behavior — notably `cmd/shim/main.go` never configured slog at all, so slog
calls reachable from the shim logged unconfigured (text to stderr, Info level).

## Decision

Unify all service logging on **controller-runtime's `logr.Logger`** (backed by
zap) and remove `log/slog` from the service entirely. The shim and manager both
configure exactly one logger via `ctrl.SetLogger(zap.New(...))`.

Why logr and not slog:

- cortex is a Kubernetes operator; controller-runtime, client-go, and the whole
  ecosystem already speak logr. Routing everything through logr fits the grain
  of the codebase rather than fighting it.
- It collapses two config paths into one (`--zap-log-level`) and one metrics
  hook (the zap core wrapper), deleting the slog `MetricsSlogHandler`.

The per-request scheduler **trace logger stays a threaded parameter** — only its
type changed (`*slog.Logger` → `logr.Logger`); the pipeline design is unchanged.

`tools/` is out of scope (developer CLIs, not the service) and may keep stdlib
`log` / `fmt`.

## Translation rules

| slog | logr |
|---|---|
| `slog.Info(msg, kv...)` | `log.Info(msg, kv...)` |
| `slog.Debug(msg, ...)` | `log.V(1).Info(msg, ...)` |
| `slog.Warn(msg, ...)` | `log.Info(msg, ...)` |
| `slog.Error("msg", "error", err)` | `log.Error(err, "msg")` |
| `slog.Error("msg")` (no err) | `log.Error(nil, "msg")` |
| `slog.With(k, v)` | `log.WithValues(k, v)` |
| `slog.Default()` (in tests) | `logr.Discard()` |

Non-controller packages obtain a logger via a package-level
`var log = logf.Log.WithName("<pkg>")`, matching the pattern already used by the
reservation controllers. `logf.Log` is a delegating logger that binds to
whatever `ctrl.SetLogger` installs, so it works in both binaries.

## Consequences

- **The `warn` level disappears as a concept.** logr has no warn level — former
  `slog.Warn` calls become `log.Info` (or `log.Error` when they carried an
  error, so the error keeps its first-class slot). The `warn` label on
  `cortex_log_messages_total` is now only produced by code that logs at
  zapcore.WarnLevel, which converted code no longer does. This was an
  slog-only concept and is an accepted loss.
- **`V(1)` is debug.** `zapr` maps logr `V(1)` to `zapcore.Level(-1)` (Debug),
  so former `slog.Debug` surfaces exactly at `--zap-log-level=debug`.
- **Single config knob.** The `LOG_LEVEL` env var and the slog setup block in
  `cmd/manager/main.go` are gone; `logLevel` in helm renders only
  `--zap-log-level`. The shim's previously-unconfigured slog output is now
  routed through its zap logger (a fix; output format for those lines changes).
- **Package loggers must not shadow controller locals.** Packages whose
  reconcilers use the `log := ctrl.LoggerFrom(ctx)` idiom name their
  package-level logger distinctly (e.g. `pipelineLog`, `schedulerLog`) to avoid
  a govet `shadow` finding.
- **A lint gate prevents regressions.** `.golangci.yaml`'s `forbidigo` forbids
  any `slog.*` usage with a message pointing at the controller-runtime logger,
  so `log/slog` cannot re-enter the service.

## Rollout

Landed as a stack of four PRs, each independently building and passing tests:

1. Binaries + `pkg/` helpers + remove the slog metrics handler.
2. Knowledge pipeline (datasources, db, extractor, kpis).
3. Scheduling pipeline (lib, nova/cinder/manila/pods/machines plugins,
   `api/external` messages) — the interface-level change.
4. Helm/docs cleanup + the `forbidigo` lint gate + this ADR.
