<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Committed-resource transaction architecture (target)

## Components

| Component | Kind | Owned by |
|-----------|------|----------|
| `CommittedResourceChangeRequest` (CRCR) | CRD | Spec: created by API handler or syncer; Status: CRCR reconciler |
| `CommittedResource` (CR) | CRD | Spec: CRCR reconciler (sole writer); Status: CR controller |
| `Reservation` | CRD | Spec: CR controller (sole writer); Status: Reservation controller |
| Change-commitments API | HTTP handler | Creates CRCR with `source=api` |
| Limes syncer | Background poller | Creates CRCR with `source=syncer` on each diff |
| CRCR reconciler | Controller | Drives CRCR FSM; writes CR specs; rolls back on failure |
| CR controller | Controller | Derives Reservation slots from CR spec; reports CR readiness |
| Reservation controller | Controller | Attempts host placement; reports slot readiness |

## Ownership

```mermaid
flowchart TD
    API["API handler\nsource=api"] -->|creates| CRCR["CommittedResourceChangeRequest"]
    Syncer["Limes syncer\nsource=syncer"] -->|creates on diff| CRCR

    CRCR -->|"CRCR reconciler — writes spec"| CR["CommittedResource"]
    CR -->|"CR controller — writes spec"| Res["Reservation"]
    Res -->|"Reservation controller — tracks placement"| VM["VM / scheduler\nlive-migration"]

    CR -.->|"CR controller reports readiness\nCRCR reconciler gates on this"| CRCR
```

No controller writes its own spec. Spec flows down; status flows up.

## Component interaction

The three reconcilers operate independently and communicate only through CRD status conditions.

```mermaid
sequenceDiagram
    participant W as API handler or Syncer
    participant CRCR as CRCR reconciler
    participant CRCRD as CommittedResource CRD
    participant CRCtl as CR controller
    participant ResCRD as Reservation CRD
    participant ResCtl as Reservation controller

    W->>CRCRD: create CommittedResourceChangeRequest
    CRCR->>CRCRD: write CR.spec (Allocating phase)
    Note over CRCR,CRCRD: CRCR reconciler is sole writer of CR.spec
    CRCtl->>ResCRD: reconcile Reservation slots from CR.spec
    Note over CRCtl,ResCRD: CR controller is sole writer of Reservation.spec
    ResCtl->>ResCRD: attempt placement, write Reservation.status
    Note over ResCtl: reacts to VM moves, live-migration, in-flight actions
    CRCtl->>CRCRD: aggregate slot readiness, write CR.status
    CRCR->>CRCRD: read CR.status.Ready + observedGeneration, advance to Accepted or RollingBack
```

## CRCR state machine

```mermaid
stateDiagram-v2
    [*] --> Preparing : CRCR created by API handler or syncer

    Preparing --> Allocating : snapshot taken

    Allocating --> AwaitingScheduling : API mode — CR specs written
    Allocating --> Accepted : Syncer mode — apply-and-done
    Allocating --> Superseded : newer CRCR for same AZ took over

    AwaitingScheduling --> Accepted : all CRs Ready (observedGeneration matches)
    AwaitingScheduling --> DryRunCleanup : all CRs Ready, but DryRun=true
    AwaitingScheduling --> RollingBack : CR rejected or timeout

    DryRunCleanup --> Accepted : pre-state restored (would-accept)

    RollingBack --> Rejected : pre-state restored (RollbackFailed on permanent failure)

    Accepted --> [*]
    Rejected --> [*]
    Superseded --> [*]
```

Terminal phases (`Accepted`, `Rejected`, `Superseded`) set `Finished=True`. Non-terminal problems set
`Progressing=False` with a reason (`Stalled`, `TimedOut`, `Error`, `RetryExhausted`) without advancing
the phase. A stuck non-terminal CRCR triggers an alert; after a threshold it is forcibly moved to
`Superseded` so its AZ is unblocked and the syncer repairs state on the next poll.

## Request flows

### API-mode flow

1. **API handler** receives a change-commitments request from Limes; creates a CRCR (`source=api`, `allowRejection=true`, optional `dryRun=true`).
2. **CRCR reconciler — Preparing**: validates the request; snapshots current CR specs as the rollback journal.
3. **CRCR reconciler — Allocating**: writes the desired CR specs atomically; records which CRs were touched.
4. **CRCR reconciler — AwaitingScheduling**: watches the touched CRs; waits until each reports its ready condition against the generation just written.
5. **CR controller**: for each CR, derives and reconciles Reservation slots; reports `Ready` once all slots are placed.
6. **Reservation controller**: places each slot on a host; reports slot readiness.
7. **CRCR reconciler**: all CRs ready → `Accepted`; any CR rejected or timeout → rolls all CR specs back to the snapshot → `Rejected`.
8. **API handler** polls the CRCR by name; maps terminal phase to HTTP response.

### Syncer-mode flow

1. **Limes syncer** polls Limes per project; diffs desired state against current CRs; on any delta creates a CRCR (`source=syncer`, `allowRejection=false`).
2. **CRCR reconciler — Preparing**: no snapshot (non-rejectable; syncer repairs on next poll if anything drifts).
3. **CRCR reconciler — Allocating**: recency check — skips changes for any project another CRCR touched in the same AZ in the last ~10 minutes (prevents clobbering an in-flight API change); writes CR specs; advances directly to `Accepted`.
4. **CR controller**: reconciles Reservation slots async; CRCR is already terminal.
5. Slots converge in the background; the next syncer poll repairs any remaining drift.

## Key invariants

- **Single writer per spec layer.** The CRCR reconciler is the only component that writes `CommittedResource.spec`. The CR controller is the only component that writes `Reservation.spec`. No controller writes its own spec.
- **AwaitingScheduling gates on generation.** The CRCR reconciler advances from `AwaitingScheduling` only when a CR's ready condition carries an `observedGeneration` that matches the generation the CRCR reconciler wrote — not an older reconcile cycle's result.
- **Rollback is durable.** The pre-change snapshot is stored in `CRCR.status` before any CR spec is touched. A process restart resumes from the persisted phase and snapshot.
- **Syncer-mode never rolls back.** Syncer CRTs are non-rejectable: on any error they retry until `Accepted` or are superseded by a newer CRCR. The syncer repairs drift on its next poll.
- **At most one active CRCR per AZ.** The CRCR reconciler serialises with `MaxConcurrentReconciles=1` plus an in-memory per-AZ active-transaction map (rebuilt from CRDs on startup). Younger CRTs requeue until the active one reaches a terminal phase.
- **Syncer recency guard prevents clobbering.** A syncer CRCR skips changes to any project that an API CRCR touched in the same AZ within the last ~10 minutes — the API change is authoritative; the syncer will reconcile remaining drift on its next poll.
- **Terminal CRTs are retained for auditability.** `Accepted` CRTs are kept for ~1 hour; `Rejected` CRTs for ~5 days. Non-terminal CRCRs (including stuck `RollingBack`) are never GC'd — deleting one loses the rollback journal and the AZ active-marker.

---

[Prev: CR lifecycle internals](03-a-cr-lifecycle-internals.md) · [Next: Failover reservations for KVM HA »](03-failover-reservations.md)
