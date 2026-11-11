<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Failover host reservations

The previous page reserved **headroom** — a shared pool of free capacity spread across the fleet, sized to
absorb `m` host failures. This page describes a different, coarser-grained mechanism that is replacing it:
instead of reserving scattered slots, Cortex keeps a fraction of **whole hosts** empty, so that when a host
dies its *entire* workload can be gang-evacuated onto one reserved host in a single move (*gang-evacuated*:
all the host's VMs moved together, in one placement decision, rather than one at a time).

> [!IMPORTANT]
> Failover host reservations **supersede** the slot-based failover reservations described on the
> [previous page](03-failover-reservations.md). The older system is deprecated and will be removed. New
> deployments should enable the failover-host reservations controller; the old slot-based controller is
> retained only for migration. The key reason for the switch: whole-host reservations trade a little more
> idle capacity for a pre-validated, gang-evacuable landing zone, so no placement has to be re-solved under
> pressure the moment a host dies.

## Partitions: hosts are not interchangeable

Hosts cannot fail over onto *any* other host. A HANA host, a general-purpose host, and an external-customer
host each carry scheduling constraints — flavors, image properties, aggregates, availability zone — that only
matching hosts satisfy. A failover target is only useful if the dead host's VMs would actually be *accepted*
there.

Cortex therefore groups hosts into **failover partitions**: sets of hosts within which every host can be
evacuated onto every other host. Crucially, these groups are **not configured by hand**. They are *discovered*
from the scheduler's own placement decisions, so the grouping always reflects the live filters, weights and
constraints — a partition can never contain a target the scheduler would in fact reject.

One could imagine solving this differently: hand-craft the partitions from the hypervisors directly — write
rules that read each host's flavors, image properties, aggregates and availability zone and group the
"compatible" ones together. But those are exactly the constraints the VM scheduling pipeline *already*
encodes. Re-expressing them here would mean re-implementing a second, parallel copy of the placement logic,
which then has to be kept in lock-step with the real one forever; the moment a filter changes in the pipeline
and not in the failover rules, the partitions quietly diverge from what the scheduler would actually accept,
and a "failover target" may reject the very workload it was reserved for.

So Cortex takes a **self-partitioning approach** instead. Rather than restating the rules, it asks the real
scheduler — per **flavor** — which hosts would accept that flavor, and lets the partitions *fall out* of those
answers (the next section works through how). The compatibility rules are never duplicated: whatever the
pipeline does, the partitions inherit, automatically and for free, including every future change to the
filters and weighers.

The probes are seeded from the **flavor catalogue**, not from the VMs that happen to be running. Cortex takes
every flavor in the `flavor-groups` Knowledge CRD whose hypervisor type it supports — the
`capabilities:hypervisor_type` extra-spec is `qemu` (the Nova label for KVM) or `ch` (Cloud Hypervisor);
flavors with any other, or no, hypervisor type
are skipped — crosses each with every availability zone in the fleet, and asks the scheduler which hosts would
accept it. These `(flavor, availability zone)` permutations are the **seeds** of the partitioning.
Seeding from flavors rather than running VMs closes two blind spots the VM-based approach had:

- **A group of hosts running no VMs is still discovered.** Flavor seeds exist whether or not anything is
  currently scheduled, so a partition is found up front rather than only once a VM happens to land on one of its
  hosts.
- **One "exotic" VM can no longer veto a host.** Because seeds carry no host affiliation, a single VM whose
  scheduling constraints no peer satisfies can never keep its host out of the partition it matches under every
  other flavor.

Everything on this page is a continuous background process: Cortex periodically re-derives the partitions and
the set of reserved hosts from the current state of the fleet, and nudges the actual reservations toward that
freshly computed target. Nothing here is a one-time setup step; the picture re-settles every few minutes as
the fleet changes.

## How a reservation is kept up to date

The mechanism has two cooperating halves:

- A **planner** runs on a fixed interval (default every `5m`). It looks at the current fleet, works out which
  hosts *should* be reserved as failover targets, and publishes that as the desired picture. This is the
  expensive part — it talks to the scheduler — so it runs on a slow cadence, never on every event.
- A **reconciler** continuously nudges reality toward that desired picture: it creates reservations that
  should exist, removes ones that no longer should, and updates ones that have drifted. It is cheap, and it
  does nothing clever — it just closes the gap.

```mermaid
flowchart LR
    PLAN["Planner (periodic)<br/>decides which hosts to reserve"] -->|"publishes desired picture"| STORE[("desired picture")]
    STORE -.->|"read"| RECON["Reconciler (continuous)<br/>makes reality match"]
    RECON -->|"create / update / remove"| REAL[("actual reservations")]
```

Because the planner republishes the **whole** desired picture each pass (rather than editing it in place), the
reconciler gets pruning for free: any reservation that exists in the fleet but is absent from the latest
picture is simply one the planner no longer wants, and is removed.

A corollary matters for safety: the planner only ever publishes a picture it computed from a *complete*
snapshot. If anything goes wrong while gathering the inputs — the fleet snapshot can't be collected, or a
scheduler probe fails — the whole pass is aborted and the previously published picture is left untouched; the
planner simply retries on the next cycle. The reconciler therefore never acts on a half-gathered view, and
reservations are never recomputed from partial data.

```mermaid
stateDiagram-v2
    direction LR
    [*] --> compare
    compare --> create: wanted, missing
    compare --> remove: present, no longer wanted
    compare --> update: present but drifted
    compare --> noop: already matches
    create --> [*]
    remove --> [*]
    update --> [*]
    noop --> [*]
```

## How the planner decides: building the failover graph

The planner's job — "which hosts should be reserved?" — comes down to building a graph of who can rescue whom,
grouping that graph into partitions, and then reserving a fraction of each partition.

The important thing to understand is that the graph is **narrowed in several steps**, and each step throws out
candidates for a *different* reason. Keeping those reasons straight is the whole algorithm, because the single
most common question — *"why does this host have no failover target?"* — has a different answer at each step:

1. **Discover who can rescue whom** (Step 1 below). For every flavor seed, ask the scheduler which hosts would
   accept it, then pair up the hosts that co-accept a seed and keep the pair only if the target is big enough
   for the whole source host — yielding one-way rescue links `source → target`. *A target drops out if it is
   smaller than the source.*
2. **Group into partitions** (Step 2). Keep only links that work *both* ways, then cluster those mutual pairs
   into groups where everyone can rescue everyone. *A one-way link leaves no trace in the partitions.*
3. **Make the partitions disjoint** (Step 3). A host can land in two overlapping groups; assign it to one.
   *A group left with a single host is then discarded.*
4. **Pick the failover hosts** (Step 4). Reserve a fraction of each partition as empty landing zones.

Each emphasised clause is an edge case, worked through below. A
[troubleshooting table](#why-does-this-host-have-no-failover-target) at the end maps each symptom back to the
step that caused it.

### Why Bron-Kerbosch for the partitioning

Step 2 reduces to a classic graph question: in the undirected graph of mutual rescue pairs, a failover
partition is a set of hosts that are *all pairwise* connected — a **maximal clique**. Cortex enumerates those
cliques with the **Bron-Kerbosch** algorithm, and the choice is deliberate:

- **The problem is literally maximal-clique enumeration, not something weaker.** "Every host can evacuate onto
  every other host" is the definition of a clique. Cheaper alternatives solve a *different* problem and would
  silently admit invalid partitions: connected components or a union-find grouping would lump together hosts
  linked only transitively (`a`↔`b`, `b`↔`c` but not `a`↔`c`), producing a "partition" in which `a` cannot
  actually rescue `c`. A greedy "grow a group while everyone stays connected" heuristic avoids that but picks
  an arbitrary clique and misses overlapping ones, so some hosts would lose a partition they genuinely belong
  to.
- **We need *all maximal* cliques, including overlapping ones.** A host can legitimately belong to more than
  one partition (it is Step 3's own job to resolve that overlap afterwards). Bron-Kerbosch is the standard,
  well-understood algorithm that enumerates *exactly* the maximal cliques — no duplicates, nothing missed —
  which is what makes the downstream overlap-resolution well-defined.
- **The fleet is partitioned by construction, so the worst case never bites.** Maximal-clique enumeration is
  exponential in the general case, which sounds alarming. But the mutual-rescue graph is not a general graph:
  it is already almost-disconnected into compatibility islands (HANA, general-purpose, external-customer, …),
  and cliques only form *within* an island. Each clique-search therefore runs over a small, dense subgraph,
  not the whole fleet, which keeps it comfortably fast in practice.

With that settled, the four steps in detail:

### Step 1 — Discover who can rescue whom

Cortex finds, for every host, the hosts that host's *entire* workload could be gang-evacuated onto. The result
is a set of directed **rescue links**:

> A link `source → target` means *"`source`'s whole workload would be accepted on `target`, and `target` is big
> enough to hold it at once."*

Direction matters: `source → target` does **not** imply `target → source`. The target might simply be too
small to take the source's workload even when the reverse would fit.

Rather than reimplementing placement logic, Cortex **asks the real scheduler** — once per **flavor seed** —
which hosts would accept that flavor in that availability zone. This guarantees the rescue links obey exactly
the same rules as ordinary placement. The hosts that *co-accept* a seed are mutually compatible under that
flavor, so Cortex pairs them up: for every ordered pair `(a, b)` of hosts that both accept a seed it emits the
link `a → b`, subject to the whole-host fit check below.

```mermaid
flowchart TB
    SEED["seed: flavor F in az1"] --> SCHED["scheduler accepts: {A, B, C}"]
    SCHED --> PAIR["pair up co-accepting hosts:<br/>A↔B, A↔C, B↔C"]
    PAIR --> FIT{"for each pair (a, b):<br/>is b big enough for all of a?"}
    FIT -->|yes| EDGE["rescue link a → b"]
    FIT -->|no| DROP["discard that direction"]
```

Because the seeds come from the flavor catalogue rather than the running VMs, two failure modes of the old
VM-based discovery simply disappear:

- **A group of hosts running no VMs is still discovered.** A flavor seed exists regardless of what is currently
  scheduled, so such a group produces rescue links and forms its partition up front — no VM has to land on it
  first.
- **One "exotic" VM can no longer veto a host.** Discovery no longer intersects the answers for a *host's own*
  VMs, so a single VM whose constraints no peer satisfies can never empty that intersection and keep its host
  out of the partition it matches under every other flavor.

Three things keep this probing faithful and safe:

- **Each probe mirrors a real evacuation** — it carries the flavor, its availability zone and a KVM image
  hypervisor type, so exactly the same filters apply as in a genuine move, and tells the scheduler to treat it
  as an **evacuation** (a cold migration). The choice of *evacuation* over *live migration* is deliberate. A
  failover ultimately *evacuates* a dead host's workload — a cold migration, which is the **weaker** check: it
  does not verify the CPU architecture, CPU modes, hypervisor features and emulated devices that live migration
  does. A failover target is still reserved **empty**, so before its reservation goes active Cortex must drain
  whatever runs on it; that drain uses live migration. But rather than prove per host that every peer is
  live-migratable at partition time, Cortex assumes a partition holds enough peers to absorb that drain and
  leaves arranging it to the draining code. Probing with the evacuation (cold-migration) requirement is what
  lets a partition form even across hosts that are not pairwise live-migratable.
- **Candidates are judged as if empty.** The probe tells the scheduler to measure each candidate against its
  *total* capacity, ignoring whatever VMs currently run there. This is deliberate: a failover target is reserved
  **empty**, so the right question is "could this host hold the workload once cleared?", not "does it have room
  right now?". Were the probe to measure live free capacity instead, a busy host would accept no one, gain no
  inbound rescue link, form no mutual edge, and stay orphaned — even though reserving it would drain it. Whether
  two hosts are *compatible* is thus independent of how full they happen to be; which host is cheapest to
  actually empty is decided later, in Step 4.
- **The probing never changes anything.** It is strictly read-only, so asking "where could this flavor go?" for
  every seed never disturbs live scheduling. It also tells the scheduler to *ignore* existing failover host
  reservations — otherwise a host already reserved for failover would look occupied and vanish from its own
  partition, fragmenting the grouping.

A hard **whole-host fit** check discards any target that is smaller than its source (memory first, then CPU).
An evacuation moves *all* of the source's workload onto the target at once, so the target must be at least as
large as the source. This check compares *total* capacities, independent of the empty-host probing above, so an
undersized target is dropped whether or not it currently runs anything. Equal-sized co-accepting hosts
therefore produce links in *both* directions, which Step 2 turns into a partition; an undersized host only ever
appears as a source, never as a landing-zone target for a larger peer.

Two outcomes of a seed lookup must not be confused, because they are handled oppositely — and the distinction
is exactly what upholds the "consistent picture or nothing" rule:

- **The scheduler accepts the seed on fewer than two hosts** (one host, or none — a short candidate set, no
  error). This is a valid answer, not a failure. A seed accepted on fewer than two hosts can form no pair, so it
  simply contributes no link, and the pass continues normally.
- **The lookup itself fails** (the scheduler is unreachable, times out, or returns a non-200) — or the seed set
  itself cannot be gathered, because the `flavor-groups` Knowledge CRD is missing or not yet ready. This is an
  infrastructure failure, not an answer — on
  a partial view Cortex cannot assert anything about the failover graph, so it **aborts the entire pass** and
  leaves the last published picture untouched, retrying next cycle. Swallowing the error and skipping just that
  seed would collapse rescue links and prune reservations that are in fact still valid, so a single transient
  blip must never be allowed to erase coverage.

### Step 2 — Group hosts into partitions

A **failover partition is a set of hosts in which every host can be evacuated onto every other host.** For that
to hold, rescue must work in *both* directions between any two members: `a` can rescue `b` **and** `b` can
rescue `a`. So Step 2 first keeps only the **mutual** links — the one-way links from Step 1 are discarded here.

**One-way links are dropped.** This is why a link can exist in Step 1 yet leave no trace in the partitions:

The reason mutuality is required — not just a convenient graph property — is that a failover must be
*reversible*. A VM evacuated onto a target host should not be **locked in** there once the original host
recovers. If rescue only worked one way (`source → target` but not back), the evacuated workload could land on
the target and then have nowhere to return to, stranding it. Insisting the link work in *both* directions
guarantees the partition can be restored: after the evacuation, the VMs can fail back off the target just as
they failed onto it, so the partition returns to a fully failover-able state rather than degrading with each
event.

```mermaid
flowchart LR
    subgraph dir["rescue links from Step 1"]
        direction LR
        a1((a)) --> b1((b))
        b1 --> a1
        b1 --> c1((c))
        c1 -. "no c→b<br/>(c too small for b)" .-> b1
    end
    dir ==> mut
    subgraph mut["only mutual pairs survive"]
        direction LR
        a2((a)) --- b2((b))
        b2 -. "dropped:<br/>works one way only" .- c2((c))
    end
    style c2 fill:#ffebee,stroke:#c62828
```

Within the mutual graph, a partition is a group where **everyone can rescue everyone**. Finding the largest
such groups is a classic graph problem (finding *cliques*), and that is exactly what Cortex does — no hand
tuning, the groups fall straight out of the mutual links.

**Why groups, and why they can overlap.** Consider three hosts where `a`↔`b` and `b`↔`c` are mutual but `a`
and `c` cannot rescue each other. Then `{a, b, c}` is *not* a valid partition — `a` and `c` don't fit the "all
rescue all" rule. Instead there are two valid groups, `{a, b}` and `{b, c}`, and `b` sits in both. That overlap
is expected here; Step 3 resolves it.

**Hosts that accept no seed get folded in.** Because discovery now probes flavor seeds rather than running VMs,
a host with no VMs on it is probed exactly like any other and, as long as it *accepts some seed*, forms rescue
links and joins a clique directly — no special handling needed. The one case left over is a host that accepts
**no seed at all** (for example an unschedulable host that every filter rejects): it has no rescue links of its
own and would otherwise be a group of one. Such a host is **recruited** into a partition as long as **every**
member of that partition can rescue onto it *and* it is the same effective size as those members. The
reachability check is one-directional on purpose: the host would be reserved empty, so it is assumed able to
evacuate anywhere; only the inbound direction (`member → host`) is verified. But the host also becomes a
*source* of the partition's eventual failover target, so it must itself be rescuable by its new peers. Having no
rescue links to prove this, Cortex falls back to the same whole-host fit rule used in Step 1 and requires equal
capacity (memory then CPU, rounded to GiB): a host larger than the partition could never be evacuated onto a
smaller reserved target, so it is **not** recruited. If even one member can't reach it, or the sizes differ, the
host stays on its own:

Flavor seeding also removes a blind spot the old VM-based discovery had: **a partition no longer needs a running
VM to be discovered.** Rescue links used to be derived *from* running VMs, so a group of hosts with no VMs at
all produced no links, formed no clique, and stayed invisible — a HANA partition with no HANA VM running simply
did not exist until one landed. With flavor seeds the HANA flavor is probed whether or not anything runs, so the
partition is found up front and a failover host can be reserved before the first VM ever arrives.

```mermaid
flowchart TB
    subgraph ok["host E (accepts no seed) is recruited"]
        direction LR
        a3((a)) -->|reaches| e1((E))
        b3((b)) -->|reaches| e1
        okr["every member of {a, b} reaches E<br/>⇒ partition becomes {a, b, E}"]
    end
    subgraph no["host F stays alone"]
        direction LR
        a4((a)) -->|reaches| f1((F))
        b4((b)) -. "b does NOT reach F" .-> f1
        nor["not every member reaches F<br/>⇒ F is left on its own"]
    end
    style nor fill:#ffebee,stroke:#c62828
```

The whole grouping is computed deterministically: given the same fleet, the same partitions come out every
time, so reservations don't flap between passes for no reason.

### Step 3 — Make the partitions disjoint

Step 2 can leave a host in **two** partitions at once (like `b` above). But a host can only be reserved for one
partition — reserving it for two would produce conflicting records about who it's meant to rescue, and one would
silently overwrite the other. So before planning, Cortex forces the partitions apart: each shared host is kept
in exactly **one** partition — the **smallest** one it belongs to (ties broken deterministically by the
partition's first host name).

Keeping the shared host in the *smallest* partition is what preserves coverage for as many partitions as
possible. A small partition only survives if it keeps its shared hosts — losing one can drop it below the
two-host minimum and collapse it entirely. A large partition, by contrast, usually stays well above that
minimum even after giving a host up. So assigning the shared host to the smaller group lets *both* groups
survive, whereas assigning it to the larger one would often strand the smaller group:

```mermaid
flowchart TB
    subgraph before["overlapping groups from Step 2"]
        p1["{a, b, c, d}  (4 hosts)"]
        p2["{c, e}        (2 hosts)"]
    end
    before ==>|"shared host c stays with the SMALLER group"| after
    subgraph after["disjoint partitions — both survive"]
        q1["{a, b, d}  ✓ shrank by one, still ≥ 2"]
        q2["{c, e}    ✓ keeps c, stays a valid pair"]
    end
    style q1 fill:#e8f5e9,stroke:#2e7d32
    style q2 fill:#e8f5e9,stroke:#2e7d32
```

Had `c` instead been kept with the larger `{a, b, c, d}`, the pair `{c, e}` would have collapsed to the lone
host `{e}` and been discarded (a lone host has no peer to fail over from), leaving `e` unprotected this pass
for no reason other than how the overlap was resolved. Preferring the smaller partition removes that failure
mode wherever the larger partition can spare the host — which, being larger, it usually can.

**Edge case — coverage can still be lost when neither partition can spare the host.** The smallest-first rule
does not make the problem vanish entirely: if a host is shared between two partitions that are *both* size two,
whichever one loses it collapses to a lone host and is discarded. There is no assignment that saves both — the
host is the only peer each has — so one partition is unavoidably stranded, and the deterministic tiebreak
decides which. This is now the only way the disjoint step costs a host its failover target.

### Step 4 — Pick the failover hosts in each partition

For each partition, Cortex decides how many hosts to reserve as failover targets. It deliberately does **not**
pick a fixed number: too few leaves no room to evacuate into, too many wastes capacity. The count scales with
the partition size, under three rules:

- **A ratio of the partition** (default `10%`), rounded **down**.
- **A floor** (default `1`), so even a small partition keeps at least one target.
- **Never all of them** — at least one host must be left to actually run workload, so a partition is never
  fully reserved. And a partition of fewer than two hosts reserves nothing at all: there would be no peer for a
  reserved host to rescue.

Worked through for the defaults, the interplay is easier to see than to state:

| partition size | 10 %, rounded down | after the floor of 1 | after "leave ≥ 1 running" | **reserved** |
|---:|---:|---:|---:|---:|
| 1  | — | — | — | **0** (no peer) |
| 2  | 0 | 1 | 1  | **1** |
| 10 | 1 | 1 | 9  | **1** |
| 15 | 1 | 1 | 14 | **1** |
| 25 | 2 | 2 | 24 | **2** |

The floor dominates small partitions; the ratio only starts to matter once a partition is large enough that
10 % of it exceeds 1 (here, around 20 hosts).

**Which** hosts become the targets comes down to the *order* in which the partition's hosts are considered.
Cortex builds one ordered slice of candidates and then simply takes the first `reserved`-many from it. The
order is what encodes the policy — and it is built to **retain existing reservations first and only create new
ones when the count demands it**:

```
candidate order  ──────────────────────────────────────────────────────────────▶
┌───────────────────────────────┬───────────────────────────────────────────────┐
│  already-reserved hosts       │  not-yet-reserved hosts                       │
│  (this partition), by name    │  by ascending workload (cheapest to empty)    │
├───────────────────────────────┼───────────────────────────────────────────────┤
│  R1   R2   R3        …        │  N1   N2   N3   N4   …                        │
└───────────────────────────────┴───────────────────────────────────────────────┘
        └──────────── take the first `reserved` of these ────────────┘
                 kept reservations are consumed before any new one is created
```

Reading the slice left to right:

- **Already-reserved hosts come first, so they stay reserved.** A host being prepared as a failover target may
  have VMs mid-way through being moved off it; flipping the designation each pass would throw that work away.
  Because they sit at the front, the planner only reaches a *new* host once every existing reservation has
  already been re-chosen. If there are *more* already-reserved hosts than the count needs, the surplus falls
  past the cut-off and is released back into normal service; if there are fewer, the remainder is filled from
  the new hosts.
- **New targets, when needed, are the cheapest to empty.** The not-yet-reserved hosts are ordered by ascending
  workload, so the first new host the planner reaches is the one whose VMs are cheapest to migrate away.

So in the common steady state where the right number of hosts is already reserved, the cut-off lands exactly
at the boundary between the two groups: every existing reservation is retained and no new one is created. New
reservations only appear when the count grows or an existing target leaves the partition.

Each chosen host is then reserved in full, recording the other partition members as the hosts it stands ready
to rescue.

Each reservation also carries a human-readable **partition description** (stored in the reservation's
`partitionDescription` field) made of two parts: what distinguishes the partition's hosts from other partitions
(e.g. `spec.aggregates=[hana]`), and **which flavors the partition can run** — the flavors every host in the
partition was seen to accept, so a listed flavor is guaranteed to fit anywhere in it. A long flavor list is
capped (e.g. `flavors: hana.4xl, hana.8xl (+3 more)`) to keep the field compact; the full set is logged each
pass. The description carries no scheduling semantics — it exists purely so an operator can tell, at a glance,
what a reserved host is standing by to absorb.

**A reserved host is listed as a source of its sibling reservations — and that is intentional.** When a
partition reserves more than one failover host, each reservation records *every* other partition member as a
source host, including the partition's *other* reserved hosts. At first glance this looks wrong: why list a
host that is itself being kept empty as a source to rescue? The reason is what happens after a failover
completes. Once a reserved host has absorbed an evacuation and is converted back into a normal host, it
instantly needs a failover target of its own — and because it was already recorded as a source on its sibling
reservations, that target is already in place. No existing reservation has to be rewritten to account for the
newly-returned host; the planner only needs to create *additional* reservations to rebuild the failover
headroom the partition just spent. Leaving the reserved hosts in each other's source lists is therefore a
deliberate shortcut: it keeps the post-failover reservations consistent without an extra adjustment pass, so
recovery reduces to "create what's now missing" rather than "re-derive who rescues whom".

## The reservation lifecycle

A failover host reservation blocks its target host from receiving ordinary placements, so the host stays empty
and available as a landing zone. Over its life a reservation moves through these stages:

```mermaid
stateDiagram-v2
    [*] --> Designated: host chosen as a failover target
    Designated --> Active: all VMs moved away, host now empty
    Designated --> Failed: draining fails or takes too long
    Failed --> Designated: cooldown elapses, eligible again
    Active --> InUse: a gang evacuation lands here
    InUse --> Used: evacuation complete → host returns to service
    Active --> Revoked: no longer part of any partition
    Designated --> Revoked: no longer part of any partition
    Used --> [*]
    Revoked --> [*]
```

- **Designated** — the host has been chosen as a target but may still have VMs that need draining off it. No
  *new* VMs are placed here. It stays in this stage until the host is empty. This is the *absence* of the
  `FailoverHostActive` condition rather than a condition of its own: a freshly created reservation is
  Designated by default.
- **Active** — the host is empty and ready to absorb an evacuation (the `FailoverHostActive` condition is
  True). Only from here on can an evacuation be routed onto it. **Active is sticky: once a host has drained
  successfully it is never demoted.** If a VM nonetheless appears on an Active, not-in-use host — the filter
  should have kept ordinary placements off it, so this is an invariant violation — the controller deliberately
  does *not* re-drain it (a stale in-use read could otherwise migrate a genuine evacuee off its rescue host).
  Instead it flags the host in the `cortex_failover_host_reservations_reoccupied` gauge so an alert escalates it
  for manual intervention.
- **Failed** — draining failed repeatedly or took too long; the host is held back from being re-chosen for a
  cooldown period.
- **In Use** — a gang evacuation onto this host is underway (and may absorb further failures until it
  completes).
- **Revoked** — the host no longer belongs to any partition (for example it became unschedulable), so the
  reservation is dropped.
- **Used** — the failover event finished; the host becomes a regular host again.

> [!NOTE]
> Treat this diagram as the *intended progression*, not a strict state machine. The stages are tracked
> independently rather than as a single current-state field, so the arrows show the normal path rather than the
> only possible transitions.

### Draining a host to Active

A freshly designated reservation already blocks new placements, but it is not yet a usable evacuation target:
its host may still run workload. Advancing it **Designated → Active** is the job of the **drain controller**
(the "failover host lifecycle reconciler"), a second, watch-driven controller separate from the partitioning
loop above. The partitioning controller only ever writes a reservation's *spec and labels*; the drain
controller owns its *status* — the `FailoverHostActive` condition and the deletion of reservations that can no
longer be made Active. (The Matcher, further down, only ever advances `Active → InUse` and only ever *selects*
`Active` reservations, so advancing `Designated → Active` is exclusively the drain controller's concern. Active
is never demoted back to Designated.)

Draining live-migrates workloads around the datacenter, so it is **opt-in and off by default**: when the
feature flag is disabled the drain controller is not even started, and no reservation is ever marked Active.
When enabled, it reconciles each failover host reservation like so:

- **Is the host empty?** Emptiness is read from the vm-operator `VirtualMachine` resources: a host counts as
  occupied only if Nova reports a VM on it that is neither being pruned nor already deleted. If the host is
  empty, the reservation is marked **Active** (`FailoverHostActive=True`, reason `HostDrained`) and
  reconciliation finishes. The controller watches the VMs, so any VM starting or stopping to occupy a host —
  being placed, migrating away, being pruned or reaching Nova status `DELETED` — re-evaluates that host's
  reservation right away.
- **Did a VM leak onto an already-Active host?** An Active reservation is left Active — once Active it may
  legitimately receive inbound VMs from a gang evacuation (`InUse`), and the controller never demotes it. A VM
  on an Active reservation that is *not* In Use is an anomaly: the `filter_failover_host` filter is supposed to
  keep ordinary placements off reserved hosts. The controller does **not** re-drain such a host — re-draining
  could migrate a genuine evacuee off its rescue host if an in-use write had not yet propagated to the
  controller's cache. Instead it counts the host in the `cortex_failover_host_reservations_reoccupied` gauge
  (in-use hosts excluded, as they legitimately hold evacuees), which an alert escalates for manual intervention:
  move the squatting VM off and investigate why the filter let it land.
- **If not, drain it.** The controller creates a `Rescheduling` with the `drain` action targeting the host; the
  rescheduler live-migrates the workloads away. Only the *inbound* reservation already blocks the host, so the
  drain does not take a second, independent host-block (`reserve: false`). **At most one failover host drain
  runs at a time across the whole fleet** — other designated hosts wait their turn, so the controller can never
  trigger drains everywhere at once. Every change to a drain wakes all not-yet-Active reservations, so the next
  host starts draining as soon as the slot frees up. A `Failed` drain keeps holding the slot until the
  rescheduler prunes it (it may carry partial state); the drained host's reservation retries after
  `retryBackoff`.
- **When the drain reports success, verify before trusting it.** A `Succeeded` drain is treated as a hint, not
  proof: the controller independently re-checks the host is empty (via the VM resources) before marking the
  reservation Active. A drain that succeeded but left workload behind leaves the reservation Designated; it is
  not re-drained, and either goes Active once the workload leaves or is cleaned up by the TTL.
- **Failure and timeouts delete the reservation.** If the host's hypervisor disappears, or the reservation
  cannot be made Active within a conservative TTL (default `24h`) since its creation, the reservation is simply
  **deleted** — the partitioning generator then designates a fresh one on its next pass. An *unready* (but still
  present) hypervisor is deliberately *not* a delete trigger: the generator lists every hypervisor regardless of
  readiness, so deleting on unready would race it recreating the reservation each interval; a genuinely
  unschedulable host instead stops being designated, and the TTL cleans up anything left stuck. A waiting
  reservation is requeued for its TTL deadline, so the TTL fires even if nothing else changes in the meantime.

### Completing an evacuation (InUse → Used)

Once a gang evacuation lands on an Active reservation the Matcher marks it **In Use** and records the failed
host in `status.failoverHostReservation.sourceHost`. From there the drain controller deliberately stops
touching it, so evacuated VMs pile onto the reserved host and stay. Releasing the reservation once the
evacuation has served its purpose is the job of the **completion controller**, a third watch-driven controller
alongside the partitioning and drain controllers. It owns the `FailoverHostCompleted` condition and the final
deletion of In Use reservations.

Like the drain controller, completion is **opt-in and off by default** (it deletes reservations, which changes
fleet capacity): when the feature flag is disabled the controller is not started. When enabled, it reconciles
each In Use failover host reservation like so:

- **Is the source host empty?** Emptiness is read from the same `VirtualMachine` resources the drain controller
  uses — but here for the *source* host (`status.failoverHostReservation.sourceHost`), not the reserved host. A
  host counts as occupied only if Nova reports a VM on it that is neither being pruned nor already deleted. The
  controller watches the VMs, so a VM leaving (or returning to) the source host re-evaluates the reservation
  right away. A source host whose hypervisor has disappeared has no VMs, so it simply counts as empty.
- **When it first goes empty, start a grace period.** The controller sets `FailoverHostCompleted=True` (reason
  `EvacuationComplete`) and requeues at the grace deadline. The grace period (default `24h`) is measured from
  that condition's `lastTransitionTime`, which survives controller restarts. It exists to avoid rapid
  evacuate ↔ reserve-new-host loops when workloads flap: a host that empties only briefly does not immediately
  lose its reservation.
- **If the source is re-occupied before grace elapses, reset the timer.** The controller flips the condition to
  `FailoverHostCompleted=False` (reason `SourceReoccupied`); the next time the source empties the condition goes
  True again with a fresh timestamp, restarting the clock.
- **When the grace period elapses with the source still empty, delete the reservation.** The reserved host
  becomes a regular host again, continuing to host the once-failed-over workload. The partitioning generator
  designates a fresh failover host on its next pass to restore the partition's headroom.

A gang evacuation that never drains its source host — a VM that will not live-migrate, no capacity on the
reserved host, or a wedged source — leaves the reservation stuck In Use and never completes. The
`cortex_failover_host_reservations_evacuating` gauge counts such reservations (In Use with a still-occupied
source host), and the `CortexNovaFailoverHostEvacuationStuck` alert escalates when the count stays non-zero.

## How failover host reservations affect scheduling

Creating a reservation is only half the story — the scheduler has to *honor* it. Three cooperating pieces
wire failover host reservations into the Nova scheduling pipeline, split along a natural seam: a **filter**
and a **weigher** shape the candidate list *inside* the pipeline, and a thin **post-pipeline step** records
the outcome. Together they keep new workload *off* reserved hosts and steer a dead host's workload *onto* the
one reserved host it should gang-evacuate to — while deliberately *keeping* the ordinary candidates so Nova
can retry if that landing fails.

### Selecting the target: one deterministic rule

For an evacuation (identified by the `evacuate` intent) the dead host is read from the `source_host`
scheduler hint, and a single **deterministic rule** picks which reserved host this source should evacuate
onto. Both the filter and the weigher apply this same rule, independently, over the *same* reservation
snapshot within one pipeline run, so they always agree without coordinating:

1. Keep the failover host reservations whose `sourceHosts` include the source host — the reservations this
   host may evacuate onto.
2. If one is already **In Use** *by this same source host*, that is the selected target (the
   gang-evacuation path — the whole gang lands on the same target).
3. Otherwise ignore any reservation **In Use** by a *different* source (pinning it would steal a landing
   zone actively absorbing another host's gang), and any reservation that is not yet **Active** (still
   draining, so not a usable landing zone yet — see the lifecycle below), then sort the rest by target
   host name and take the first.
4. If nothing remains, there is no match and the evacuation simply uses the ordinary candidates.

> [!NOTE]
> A reservation only becomes a *selectable* evacuation target once it is **Active** (its host has been
> fully drained). Before that — from the moment it is created, while it is still draining — it already
> **blocks** ordinary placements (the filter removes it for non-evacuation requests), but it is skipped by
> step 3 above. This keeps VMs off a host that is being emptied without yet routing an evacuation onto a
> host that is not actually empty.

### The filter: block reserved hosts, keep only the selected target

The **`filter_failover_host`** pipeline filter is purely *subtractive* — it only ever removes hosts — so its
position in the pipeline does not matter and all other filters and weighers still apply around it. It is
toggled by the `kvm.failoverHostMatching.enabled` Helm value (default `false`) and is a no-op when disabled. What it
removes depends on the request:

- **Ordinary (non-evacuation) requests:** it removes *every* failover host, so no new VM is placed on a host
  being drained or kept empty as a landing zone.
- **Evacuations:** it removes every failover host *except* the one selected by the rule above. The ordinary,
  non-reserved candidates are left untouched.

Pruning the *other* reserved hosts is the key to mutual exclusion between failover targets. Consider a VM
that could go to reserved `[h1, h2]` (both failover targets reachable from this source) plus ordinary
`[h3, h4]`, where only `h1` is selected. The filter emits `[h1, h3, h4]` — **not** `[h1, h2, h3, h4]`: `h2`
is held back so a *second*, concurrent host failure still has its own free landing zone rather than finding
it already consumed. Keeping `h3` and `h4` means that if the evacuation onto `h1` fails, Nova retries onto an
ordinary host instead of being forced onto an unmatched reserved slot.

The filter also honors the **call-time** `ignoredReservationTypes` request option. This is what keeps the
**path finder probe** working: the probe asks the scheduler which hosts would accept a flavor, and it passes
`ignoredReservationTypes: [FailoverHostReservation]` precisely so a reserved host is *not* hidden from its own
partition. Because the filter obeys that option, the probe still discovers already-reserved hosts and the
partitioning does not fragment.

### The weigher: lift the selected target to the front

The **`weigher_failover_host`** weigher gives the selected target a dominating weight — far larger than any
other weigher can contribute — so the pipeline's ordinary descending-weight sort places it first. It selects
the same host the filter kept (same rule, same snapshot), so it simply lifts the one reserved host that
survived filtering above the retained ordinary candidates. For non-evacuation requests, or when no target is
selected, it has no effect. Because the weigher only *weighs*, the ordinary candidates keep their relative
order behind the target and remain available as retry fallbacks.

### The post-pipeline step: pin the target for gang-evacuation

A thin **Matcher** runs *after* the pipeline has produced its ordered candidates, from inside the pipeline's
global execution lock, on write runs only. It does **not** touch the candidate list — the filter and weigher
have already shaped it. Its sole job is the stateful write: it re-derives the selected reservation with the
same rule and, **only if that target is actually the top candidate**, marks it **In Use** and records the
source host on its status.

Gating the write on the *outcome* is deliberate. If the selected target lost to other scheduling constraints
(the filter removed it, so an ordinary host ended up on top), the evacuation falls back to that ordinary host
and the Matcher writes nothing — no dangling `In Use` is left behind. In that case, and when no reservation
matches or all are taken by other sources, it increments a metric
(`cortex_failover_host_evacuations_without_reservation_total`) that drives an alert on frequent occurrences.

Running inside the global pipeline lock is what makes **gang-evacuation** work. The *first* VM evacuated from
a dead host pins its target and writes `In Use` while holding the lock; every subsequent VM from the same
source host — serialized behind the same lock — sees that `In Use` reservation, so the selection rule keeps
choosing it (step 2) and the whole gang lands on the *same* target. The reuse is idempotent: a reservation
already `In Use` by this source needs no second write. Writing the pin *after* the pipeline (rather than
inside the filter) is what lets the write be gated on the final ordering.

The Matcher only ever advances a reservation *to* `In Use`. Moving it back out — to `Active` once the
evacuation completes, or on cleanup — stays with the failover host lifecycle reconciler, based on the real
workload present on the host.

## Configuration

Enable the controllers and configure them under the nested `failoverhost` key, one sub-key per concern:

```yaml
enabledControllers:
  - failover-host-reservations-controller
  - failover-host-drain-controller        # optional: drains designated hosts to Active
  - failover-host-completion-controller    # optional: releases in-use hosts once evacuation completes
# ...
failoverhost:
  partitioning:
    ratio: 0.1                 # fraction of each partition to reserve (0, 1]
    minimum: 1                 # floor per partition, > 0
    generationInterval: 5m     # how often to recompute, ≥ 1m
  drain:
    enabled: false             # gates the whole drain controller (off by default)
    ttl: 24h                   # delete a reservation stuck non-Active this long
    retryBackoff: 1m           # wait before retrying after a failed drain
  completion:
    enabled: false             # gates the whole completion controller (off by default)
    gracePeriod: 24h           # source must stay empty this long before deletion
```

The flavors the partitioning is seeded from (see [Step 1](#step-1--discover-who-can-rescue-whom)) are read
from the `flavor-groups` Knowledge CRD, which Cortex already extracts into the cluster — so the controller
needs no OpenStack credentials of its own to enumerate them.

The **drain controller** that advances designated reservations to Active (see
[Draining a host to Active](#draining-a-host-to-active)) is a separate `enabledControllers` entry,
`failover-host-drain-controller`, and reads its settings from `failoverhost.drain`. It is off by default
(`enabled: false`): draining live-migrates workloads, so it is deliberately opt-in. When disabled the
controller is not started and no reservation is marked Active. The `ttl` bounds how long a reservation may stay
non-Active before it is deleted and regenerated, and is intentionally conservative.

The **completion controller** that releases In Use reservations once their evacuation finishes (see
[Completing an evacuation](#completing-an-evacuation-inuse--used)) is a third `enabledControllers` entry,
`failover-host-completion-controller`, and reads its settings from `failoverhost.completion`. It too is off by
default (`enabled: false`): completing a reservation deletes it, which returns the reserved host to regular
capacity. The `gracePeriod` (default `24h`) is how long the source host must stay empty before deletion,
guarding against rapid evacuate ↔ reserve loops.

> [!NOTE]
> The settings live under `failoverhost`, with one sub-key per concern (`partitioning`, `drain`, `completion`),
> as a deliberate example of grouping a controller's config under its own key rather than adding more flat
> top-level keys. This is independent of the (deprecated) slot-based failover controller configured alongside
> it. Bad values for `ratio`, `minimum`, or the interval are rejected at startup.

Like the other reservation kinds, failover host reservations make a host look fuller to the pipeline than its
raw capacity suggests — here by reserving the host **entirely**. The next page turns from the *kinds* of
reserved capacity to the concurrency problem all of them share: how the scheduler keeps parallel requests from
double-spending the same slot.

## Why does this host have no failover target?

Because the graph is narrowed in several steps, a host can end up unprotected for quite different reasons. This
table maps the symptom back to the step that caused it:

| Symptom | Why | Step |
|---|---|---|
| a host accepts no seed, gets no rescue links | every filter rejects it (e.g. it is unschedulable) | 1 · Discover rescue links |
| a target was discarded | it's smaller than the source host | 1 · Discover rescue links |
| a link doesn't lead to a partition | it only works one way, not both | 2 · Group into partitions |
| a host that accepts no seed is left on its own | not *every* member of a partition can reach it | 2 · Group into partitions |
| a host loses its partition | it was shared between two size-two partitions, and the one that lost it collapsed | 3 · Make partitions disjoint |
| a partition reserves nothing | it has fewer than two hosts | 4 · Pick failover hosts |

The controller logs each of these decisions as it makes them, so a host's fate through the steps can be traced
pass by pass.

## Next

[Prev: Failover reservations (deprecated)](03-failover-reservations.md) · [Next: Concurrency and in-flight reservations »](05-concurrency-and-in-flight-reservations.md)
