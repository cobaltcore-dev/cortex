#!/usr/bin/env bash
# Demo script for the committed-resource LIQUID API.
#
# Usage:
#   ./hack/demo-cr-lifecycle.sh [BASE_URL]
#
# Defaults to http://localhost:8001 (kubectl proxy or port-forward).
# Override any variable by setting it before sourcing or running the script.
#
# Examples:
#   BASE_URL=http://localhost:8080 ./hack/demo-cr-lifecycle.sh
#   AZ=qa-de-1d PROJECT_ID=abc123 ./hack/demo-cr-lifecycle.sh

set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8001}"
AZ="${AZ:-qa-de-1d}"
PROJECT_ID="${PROJECT_ID:-e9141fb24eee4b3e9f25ae69cda31132}"

# ─── helpers ──────────────────────────────────────────────────────────────────

info() { printf '\033[1;34m▶ %s\033[0m\n' "$*"; }
ok()   { printf '\033[1;32m✔ %s\033[0m\n' "$*"; }
fail() { printf '\033[1;31m✘ %s\033[0m\n' "$*" >&2; exit 1; }

# get_info — fetch /info, print it, and populate INFO_VERSION + FIRST_RESOURCE.
get_info() {
    info "GET /commitments/v1/info"
    local resp
    resp=$(curl -sf "$BASE_URL/commitments/v1/info" -H "Accept: application/json")
    echo "$resp" | jq .
    INFO_VERSION=$(echo "$resp" | jq -r '.version')
    FIRST_RESOURCE=$(echo "$resp" | jq -r '[.resources | to_entries[] | select(.value.handlesCommitments == true) | .key][0]')
    ok "infoVersion=$INFO_VERSION  first HandlesCommitments resource=$FIRST_RESOURCE"
}

# change_commitments DESCRIPTION JSON_BODY
# Sends one change-commitments request and prints the response.
change_commitments() {
    local description="$1"
    local body="$2"
    info "POST /commitments/v1/change-commitments — $description"
    echo "$body" | jq .
    local resp status
    resp=$(curl -sf -w '\n__STATUS__:%{http_code}' \
        -X POST "$BASE_URL/commitments/v1/change-commitments" \
        -H "Content-Type: application/json" \
        -d "$body")
    status=$(echo "$resp" | grep '__STATUS__:' | cut -d: -f2)
    resp=$(echo "$resp" | grep -v '__STATUS__:')
    echo "$resp" | jq .
    if [[ "$status" != "200" ]]; then
        fail "HTTP $status"
    fi
    local reason
    reason=$(echo "$resp" | jq -r '.rejectionReason // empty')
    if [[ -n "$reason" ]]; then
        fail "rejected: $reason"
    fi
    ok "accepted"
}

# ─── scenario functions ────────────────────────────────────────────────────────

# create_commitment UUID RESOURCE AMOUNT_FLAVORS
# Creates a new confirmed commitment.
create_commitment() {
    local uuid="$1" resource="$2" amount="$3"
    change_commitments "create $uuid ($amount × $resource)" "$(jq -n \
        --arg az "$AZ" \
        --argjson ver "$INFO_VERSION" \
        --arg pid "$PROJECT_ID" \
        --arg res "$resource" \
        --arg uuid "$uuid" \
        --argjson amt "$amount" \
        --argjson total "$amount" \
        '{
            az: $az,
            dryRun: false,
            infoVersion: $ver,
            byProject: {
                ($pid): {
                    byResource: {
                        ($res): {
                            totalConfirmedBefore: 0,
                            totalConfirmedAfter: $total,
                            commitments: [{
                                uuid: $uuid,
                                newStatus: "confirmed",
                                amount: $amt,
                                expiresAt: "2027-01-01T00:00:00Z"
                            }]
                        }
                    }
                }
            }
        }')"
}

# delete_commitment UUID RESOURCE OLD_AMOUNT_FLAVORS
# Deletes an existing confirmed commitment.
delete_commitment() {
    local uuid="$1" resource="$2" old_amount="$3"
    change_commitments "delete $uuid" "$(jq -n \
        --arg az "$AZ" \
        --argjson ver "$INFO_VERSION" \
        --arg pid "$PROJECT_ID" \
        --arg res "$resource" \
        --arg uuid "$uuid" \
        --argjson amt "$old_amount" \
        '{
            az: $az,
            dryRun: false,
            infoVersion: $ver,
            byProject: {
                ($pid): {
                    byResource: {
                        ($res): {
                            totalConfirmedBefore: $amt,
                            totalConfirmedAfter: 0,
                            commitments: [{
                                uuid: $uuid,
                                oldStatus: "confirmed",
                                newStatus: null,
                                amount: $amt,
                                expiresAt: "2027-01-01T00:00:00Z"
                            }]
                        }
                    }
                }
            }
        }')"
}

# resize_commitment UUID RESOURCE OLD_AMOUNT NEW_AMOUNT
# Resizes a confirmed commitment (down or up).
resize_commitment() {
    local uuid="$1" resource="$2" old_amount="$3" new_amount="$4"
    change_commitments "resize $uuid ($old_amount → $new_amount)" "$(jq -n \
        --arg az "$AZ" \
        --argjson ver "$INFO_VERSION" \
        --arg pid "$PROJECT_ID" \
        --arg res "$resource" \
        --arg uuid "$uuid" \
        --argjson old "$old_amount" \
        --argjson new "$new_amount" \
        '{
            az: $az,
            dryRun: false,
            infoVersion: $ver,
            byProject: {
                ($pid): {
                    byResource: {
                        ($res): {
                            totalConfirmedBefore: $old,
                            totalConfirmedAfter: $new,
                            commitments: [{
                                uuid: $uuid,
                                oldStatus: "confirmed",
                                newStatus: "confirmed",
                                amount: $new,
                                expiresAt: "2027-01-01T00:00:00Z"
                            }]
                        }
                    }
                }
            }
        }')"
}

# ─── demo entry point ─────────────────────────────────────────────────────────

demo_lifecycle() {
    local uuid="${1:-demo-$(date +%s)}"
    local resource="${RESOURCE:-}"

    get_info
    if [[ -z "$resource" ]]; then
        resource="$FIRST_RESOURCE"
    fi
    [[ -n "$resource" ]] || fail "no HandlesCommitments resource found in /info"

    echo
    info "=== Step 1: create commitment $uuid ==="
    create_commitment "$uuid" "$resource" 1

    echo
    info "=== Step 2: watch CommittedResource + Reservation CRDs appear (Ctrl-C to continue) ==="
    info "  kubectl get committedresources,reservations -w"
    read -r -p "Press Enter when done watching..."

    echo
    info "=== Step 3: delete commitment $uuid ==="
    delete_commitment "$uuid" "$resource" 1

    echo
    info "=== Step 4: watch CRDs disappear (Ctrl-C to continue) ==="
    info "  kubectl get committedresources,reservations -w"
    read -r -p "Press Enter when done watching..."

    ok "demo complete"
}

# Run the full lifecycle demo if called directly (not sourced).
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    demo_lifecycle "${1:-}"
fi
