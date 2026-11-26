#!/usr/bin/env bash
# Copyright SAP SE
# SPDX-License-Identifier: Apache-2.0
#
# Demo for the nova filter_expression step on top of a dynamically added
# knowledge. It applies a "host-cpu-contention" knowledge (synthetic demo data)
# and a pipeline that keeps only hosts with low CPU contention, then sends a
# scheduling request to the external scheduler and shows the returned hosts once
# without the filter (baseline pipeline) and once with it. Mirrors the
# Knowledge DB 2.0 slide example.
#
# Prerequisites:
#   - A cortex-nova deployment (e.g. under Tilt) reachable via kubectl.
#   - jq and curl are installed.
#   - The scheduler is reachable (under Tilt, localhost:8001 -> pod 8080).
#
# Usage:
#   ./demo.sh [command]
#
# Commands:
#   all          Apply everything, then compare baseline vs filtered (default).
#   knowledge    Apply only the host-cpu-contention knowledge and wait for it.
#   pipelines    Apply only the baseline and filtered pipelines.
#   apply        Apply the knowledge and both pipelines.
#   counts       Print the avg CPU contention per host from the knowledge.
#   schedule     Send a request and print the returned hosts. Args:
#                  ./demo.sh schedule <pipeline-name> [request.json]
#   compare      Send the request to the baseline and filtered pipelines and diff.
#   walkthrough  Guided, narrated run of the whole demo (pauses for Enter).
#   clean        Delete all demo resources to start over.
#
# Environment:
#   SCHEDULER_URL   External scheduler endpoint (default localhost:8001).

set -euo pipefail

cd "$(dirname "$0")"

SCHEDULER_URL="${SCHEDULER_URL:-http://localhost:8001/scheduler/nova/external}"

KNOWLEDGE_NAME="host-cpu-contention"
METRICS_KNOWLEDGE_NAME="host-cpu-metrics"
PIPELINE_FILTERED="nova-contention-demo"
PIPELINE_BASELINE="nova-contention-demo-baseline"

SCHEDULER_DEPLOY="deploy/cortex-nova-scheduling-controller-manager"
# Local port of SCHEDULER_URL, used to offer/auto-start a port-forward.
SCHEDULER_PORT="$(echo "$SCHEDULER_URL" | sed -n 's#.*://[^:/]*:\([0-9][0-9]*\).*#\1#p')"
PORT_FORWARD_PID=""

cleanup_port_forward() {
  if [ -n "$PORT_FORWARD_PID" ]; then
    kill "$PORT_FORWARD_PID" 2>/dev/null || true
    PORT_FORWARD_PID=""
  fi
}
trap cleanup_port_forward EXIT

# Ensure the scheduler endpoint is reachable. If not, offer to start a
# kubectl port-forward for the lifetime of this script. Set AUTO_PORT_FORWARD=1
# to start it without prompting, or AUTO_PORT_FORWARD=0 to never start it.
ensure_scheduler_reachable() {
  if curl -sS -o /dev/null --max-time 2 "$SCHEDULER_URL" 2>/dev/null; then
    return 0
  fi
  local cmd="kubectl port-forward $SCHEDULER_DEPLOY ${SCHEDULER_PORT:-8001}:8080"
  echo "!! Cannot reach the scheduler at $SCHEDULER_URL." >&2
  echo "   The scheduling manager serves on pod port 8080. Start a port-forward:" >&2
  echo "     $cmd" >&2
  if [ -z "${SCHEDULER_PORT:-}" ]; then
    return 1
  fi
  local answer="${AUTO_PORT_FORWARD:-}"
  if [ -z "$answer" ] && [ -t 0 ]; then
    printf "   Start it now for this run? [y/N] " >&2
    read -r answer || answer=""
  fi
  case "$answer" in
    1|y|Y|yes)
      echo "==> Starting $cmd" >&2
      $cmd >/dev/null 2>&1 &
      PORT_FORWARD_PID=$!
      for _ in 1 2 3 4 5 6 7 8 9 10; do
        if curl -sS -o /dev/null --max-time 2 "$SCHEDULER_URL" 2>/dev/null; then
          return 0
        fi
        sleep 0.5
      done
      echo "!! Port-forward did not become ready in time." >&2
      return 1
      ;;
    *)
      return 1
      ;;
  esac
}

# --- building blocks --------------------------------------------------------

# Sort a list of host names (one per line, on stdin) grouped by their building
# block (the "bb<NN>" suffix after the last dash), then by node name within each
# block. Hosts without a "-bb" suffix sort after, by name.
sort_hosts() {
  sort -t- -k2,2 -k1,1V
}

# Run kubectl, echoing the command first so the audience sees exactly what is
# being executed against k8s. Arguments with spaces or shell-special characters
# are quoted so the echoed line is copy-pasteable. Output is indented.
kube() {
  local shown="" a
  for a in "$@"; do
    case "$a" in
      *[^a-zA-Z0-9=_/.:-]*) shown="$shown '$a'" ;;
      *)                    shown="$shown $a" ;;
    esac
  done
  echo "    \$ kubectl${shown}" >&2
  kubectl "$@"
}

apply_knowledge() {
  echo "==> Applying raw metrics knowledge $METRICS_KNOWLEDGE_NAME"
  echo "    (synthetic demo data — standing in for synced Prometheus CPU-contention metrics)"
  kube apply -f knowledge-metrics.yaml
  kube wait --for=condition=Ready "knowledge/$METRICS_KNOWLEDGE_NAME" --timeout=120s

  echo "==> Applying join knowledge $KNOWLEDGE_NAME"
  echo "    (expr-lang join of the metrics onto the real nova-hypervisors datasource)"
  kube apply -f knowledge.yaml
  echo "==> Applied knowledge spec:"
  kube get knowledge "$KNOWLEDGE_NAME" \
    -o jsonpath='{.spec.description}{"\n"}{"  expression.run:\n"}{.spec.expression.run}' \
    2>/dev/null | sed 's/^/    /'
  echo
  echo "==> Waiting for $KNOWLEDGE_NAME to become ready"
  kube wait --for=condition=Ready "knowledge/$KNOWLEDGE_NAME" --timeout=120s
  local n
  n="$(kubectl get knowledge "$KNOWLEDGE_NAME" -o jsonpath='{.status.rawLength}' 2>/dev/null || true)"
  echo "==> Result: $KNOWLEDGE_NAME is Ready with ${n:-0} features"
  print_counts
}

apply_pipelines() {
  echo "==> Applying pipelines ($PIPELINE_BASELINE, $PIPELINE_FILTERED)"
  kube apply -f pipeline-baseline.yaml -f pipeline.yaml
  echo "==> Applied pipelines:"
  for p in "$PIPELINE_BASELINE" "$PIPELINE_FILTERED"; do
    local filters status
    filters="$(kubectl get pipeline "$p" -o jsonpath='{.spec.filters[*].name}' 2>/dev/null || true)"
    [ -z "$filters" ] && filters="(none)"
    status="$(kubectl get pipeline "$p" \
      -o jsonpath='{range .status.conditions[?(@.type=="AllStepsIndexed")]}{.status}{end}' 2>/dev/null || true)"
    printf '    %-32s filters: %-20s AllStepsIndexed=%s\n' "$p" "$filters" "${status:-<pending>}"
  done
}

# Echo the raw knowledge features json, or fail if the knowledge has no data.
knowledge_features() {
  local raw
  raw="$(kubectl get knowledge "$KNOWLEDGE_NAME" -o jsonpath='{.status.raw}' 2>/dev/null || true)"
  if [ -z "$raw" ] || [ "$(echo "$raw" | jq '.features | length')" -eq 0 ]; then
    echo "!! The $KNOWLEDGE_NAME knowledge has no features. Run './demo.sh apply' first." >&2
    return 1
  fi
  echo "$raw"
}

# Fail fast with a clear message if a pipeline is missing or not ready, instead
# of surfacing the raw "pipeline not found or not ready" error from the server.
require_pipeline_ready() {
  local p="$1" ready
  ready="$(kubectl get pipeline "$p" \
    -o jsonpath='{range .status.conditions[?(@.type=="Ready")]}{.status}{end}' 2>/dev/null || true)"
  if [ "$ready" != "True" ]; then
    echo "!! Pipeline '$p' is missing or not ready (Ready=${ready:-<absent>})." >&2
    echo "   Run './demo.sh apply' first to create the demo resources." >&2
    return 1
  fi
}

print_counts() {
  local top="${COUNTS_TOP:-15}"
  local feats total
  feats="$(knowledge_features)" || return 1
  total="$(echo "$feats" | jq '.features | length')"
  if [ "$top" -le 0 ] || [ "$top" -ge "$total" ]; then
    echo "==> Avg CPU contention per host (from $KNOWLEDGE_NAME), all $total:"
    echo "$feats" | jq -r '.features | sort_by(-.AvgCPUContention)[] | "    \(.AvgCPUContention)%\t\(.ComputeHost)"'
    return 0
  fi
  echo "==> Avg CPU contention per host (from $KNOWLEDGE_NAME), top $top of $total:"
  echo "$feats" | jq -r --argjson top "$top" \
    '.features | sort_by(-.AvgCPUContention)[:$top][] | "    \(.AvgCPUContention)%\t\(.ComputeHost)"'
  echo "    ... $((total - top)) more (run 'COUNTS_TOP=0 ./demo.sh counts' for all)"
}

# Build a request body for the given pipeline. If a request file is given (or the
# default request.json exists), use it and override only the pipeline name.
# Otherwise synthesize one from the knowledge hosts. The flavor carries the
# capabilities:hypervisor_type=CH extra spec so the request routes to the KVM
# path, which gathers the whole fleet and (with novaLimitHostsToRequest) limits
# the response back to the request hosts.
build_request() {
  local pipeline="$1" request_file="${2:-}"
  if [ -z "$request_file" ] && [ -f "request.json" ]; then
    request_file="request.json"
  fi
  if [ -n "$request_file" ]; then
    jq --arg p "$pipeline" '.pipeline = $p' "$request_file"
  else
    knowledge_features | jq --arg p "$pipeline" '{
      pipeline: $p,
      context: { request_id: "expression-filter-demo-1" },
      spec: { data: { instance_uuid: "expression-filter-demo-0001",
        flavor: { data: { name: "m1.small", memory_mb: 2048, vcpus: 1,
          extra_specs: { "capabilities:hypervisor_type": "CH" } } } } },
      hosts: [ .features[] | { host: .ComputeHost } ],
      weights: ( .features | map({ key: .ComputeHost, value: 0 }) | from_entries )
    }'
  fi
}

# Send a request body to the scheduler and echo the returned hosts, one per line.
post_request() {
  local body="$1" response
  ensure_scheduler_reachable || return 1
  response="$(echo "$body" | curl -sS -X POST "$SCHEDULER_URL" \
    -H 'Content-Type: application/json' --data-binary @-)"
  if ! echo "$response" | jq -e '.hosts' >/dev/null 2>&1; then
    echo "!! Unexpected response from $SCHEDULER_URL:" >&2
    echo "$response" >&2
    return 1
  fi
  echo "$response" | jq -r '.hosts[]?'
}

# --- commands ---------------------------------------------------------------

cmd_knowledge() { apply_knowledge; }
cmd_pipelines() { apply_pipelines; }
cmd_apply()     { apply_knowledge; apply_pipelines; }
cmd_counts()    { print_counts; }

# Print a human-readable summary of a request body, plus the equivalent curl.
print_request() {
  local body="$1"
  echo "==> Request sent to the external scheduler:"
  echo "$body" | jq -r '
    "    pipeline:   \(.pipeline)",
    "    instance:   \(.spec.data.instance_uuid)",
    "    flavor:     \(.spec.data.flavor.data.name) (hypervisor_type=\(.spec.data.flavor.data.extra_specs["capabilities:hypervisor_type"] // "-"))"'
  local hosts count
  hosts="$(echo "$body" | jq -r '.hosts[].host' | sort_hosts | paste -sd, - | sed 's/,/, /g')"
  count="$(echo "$body" | jq '.hosts | length')"
  echo "    hosts ($count): $hosts"
  echo "    equivalent curl:"
  echo "      curl -sS -X POST $SCHEDULER_URL \\"
  echo "        -H 'Content-Type: application/json' \\"
  echo "        -d '$(echo "$body" | jq -c .)'"
}

cmd_schedule() {
  local pipeline="${1:-$PIPELINE_FILTERED}" request_file="${2:-}"
  require_pipeline_ready "$pipeline" || return 1
  local body
  body="$(build_request "$pipeline" "$request_file")"
  print_request "$body"
  echo "==> Scheduling against pipeline '$pipeline' ($SCHEDULER_URL)"
  echo "==> Returned hosts:"
  post_request "$body" | sort_hosts | sed 's/^/    /'
}

cmd_compare() {
  local request_file="${1:-}"
  require_pipeline_ready "$PIPELINE_BASELINE" || return 1
  require_pipeline_ready "$PIPELINE_FILTERED" || return 1

  # Resolve the request once (pipeline name is overridden per call below) so the
  # header reflects exactly which hosts are sent.
  local reqbody
  reqbody="$(build_request "$PIPELINE_BASELINE" "$request_file")" || return 1
  echo "==> Request sent to both pipelines:"
  echo "$reqbody" | jq -r '
    "    instance:   \(.spec.data.instance_uuid)",
    "    flavor:     \(.spec.data.flavor.data.name) (hypervisor_type=\(.spec.data.flavor.data.extra_specs["capabilities:hypervisor_type"] // "-"))",
    "    hosts (\(.hosts | length)):"'
  echo "$reqbody" | jq -r '.hosts[].host' | sort_hosts | sed 's/^/      /'
  echo

  local baseline filtered
  baseline="$(post_request "$(build_request "$PIPELINE_BASELINE" "$request_file")")"
  filtered="$(post_request "$(build_request "$PIPELINE_FILTERED" "$request_file")")"

  echo "==> Hosts returned WITHOUT the filter ($PIPELINE_BASELINE):"
  echo "$baseline" | sort_hosts | sed 's/^/    /'
  echo "==> Hosts returned WITH the filter ($PIPELINE_FILTERED):"
  echo "$filtered" | sort_hosts | sed 's/^/    /'

  echo "==> Excluded by the filter step (CPU contention >= 50%):"
  comm -23 <(echo "$baseline" | sort) <(echo "$filtered" | sort) | sort_hosts | sed 's/^/    /'
}

cmd_clean() {
  echo "==> Deleting demo resources"
  kube delete -f pipeline.yaml -f pipeline-baseline.yaml -f knowledge.yaml \
    -f knowledge-metrics.yaml --ignore-not-found=true
}

cmd_all() {
  apply_knowledge
  apply_pipelines
  echo
  cmd_compare "${1:-}"
  echo
  echo "==> Done. Re-run a single part with e.g. './demo.sh counts' or start over"
  echo "    with './demo.sh clean'."
}

# Wait for Enter between walkthrough steps. Skipped when stdin is not a TTY or
# AUTO=1 is set, so the walkthrough still runs unattended.
pause() {
  if [ "${AUTO:-}" = "1" ] || [ ! -t 0 ]; then
    return 0
  fi
  printf '\n-- press Enter to continue (Ctrl-C to stop) --' >&2
  read -r _ || true
  echo >&2
}

# Guided, narrated run of the whole demo with pauses between steps. Cleans up
# first for a fresh start, applies and shows the resources, runs the comparison,
# and cleans up at the end.
cmd_walkthrough() {
  local request_file="${1:-}"

  echo "=============================================================="
  echo " Expression-filter demo (nova): dynamic knowledge + filter"
  echo "=============================================================="
  echo
  echo "This walks through:"
  echo "  1. a clean start"
  echo "  2. applying the metrics + host-cpu-contention knowledges (and the result)"
  echo "  3. applying the empty and empty+filter_expression pipelines"
  echo "  4. comparing which hosts each returns for the same request"
  echo "  5. cleaning up"
  echo
  echo "For KVM the scheduler ranks the whole fleet, then limits the response"
  echo "to the request's hosts; the filter keeps only hosts whose average CPU"
  echo "contention is below 50%."
  pause

  echo
  echo ">>> Step 1/5: clean start (remove any leftover demo resources)"
  pause
  cmd_clean

  echo
  echo ">>> Step 2/5: apply the knowledges (fake metrics + expr-lang join) and show the result"
  pause
  apply_knowledge
  echo
  echo "    In the cluster now:"
  kube get knowledge "$METRICS_KNOWLEDGE_NAME" "$KNOWLEDGE_NAME" 2>&1 | sed 's/^/    /'

  echo
  echo ">>> Step 3/5: apply the two pipelines (empty baseline, empty+filter)"
  pause
  apply_pipelines
  echo
  echo "    In the cluster now:"
  kube get pipeline "$PIPELINE_BASELINE" "$PIPELINE_FILTERED" 2>&1 | sed 's/^/    /'
  echo
  echo "    The filter_expression step in $PIPELINE_FILTERED:"
  kube get pipeline "$PIPELINE_FILTERED" \
    -o jsonpath='{.spec.filters[?(@.name=="filter_expression")].params[?(@.key=="run")].stringValue}' \
    2>&1 | sed 's/^/        /'

  echo
  echo ">>> Step 4/5: schedule the same request against both and compare"
  pause
  cmd_compare "$request_file"

  echo
  echo ">>> Step 5/5: clean up the demo resources"
  pause
  cmd_clean
  echo
  echo "==> Walkthrough complete."
}

usage() {
  cat <<'USAGE'
Demo for the nova filter_expression step on top of a dynamically added knowledge.

Usage: ./demo.sh [command]

Commands:
  all                            Apply everything, then compare baseline vs
                                 filtered (default).
  knowledge                      Apply only the host-cpu-contention knowledge.
  pipelines                      Apply only the baseline and filtered pipelines.
  apply                          Apply the knowledge and both pipelines.
  counts                         Print the avg CPU contention per host.
  schedule <pipeline> [req.json] Send a request and print the returned hosts.
  compare [req.json]             Schedule baseline and filtered and diff them.
  walkthrough [req.json]         Guided, narrated run of the whole demo with a
                                 clean start and cleanup, pausing for Enter
                                 between steps. Set AUTO=1 to skip the pauses.
  clean                          Delete all demo resources to start over.
  help                           Show this help.

Environment:
  SCHEDULER_URL   External scheduler endpoint (default localhost:8001).
  COUNTS_TOP      Rows shown by 'counts' (default 15, 0 for all).
  AUTO            Set to 1 to run 'walkthrough' without pausing.
USAGE
}

# --- dispatch ---------------------------------------------------------------

command="${1:-all}"
shift || true
case "$command" in
  all)       cmd_all "$@" ;;
  knowledge) cmd_knowledge ;;
  pipelines) cmd_pipelines ;;
  apply)     cmd_apply ;;
  counts)    cmd_counts ;;
  schedule)  cmd_schedule "$@" ;;
  compare)   cmd_compare "$@" ;;
  walkthrough) cmd_walkthrough "$@" ;;
  clean)     cmd_clean ;;
  help|-h|--help) usage ;;
  *)
    echo "unknown command: $command" >&2
    echo >&2
    usage >&2
    exit 1
    ;;
esac
