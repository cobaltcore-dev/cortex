// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

// Tool to visualize CommittedResource CRDs and their child Reservation slots.
//
// Usage:
//
//	go run tools/visualize-committed-resources/main.go [flags]
//
// Flags:
//
//	--context=ctx           Kubernetes context (default: current context)
//	--filter-project=id     Show only CRs for this project ID (substring match)
//	--filter-az=az          Show only CRs in this availability zone (substring match)
//	--filter-group=name     Show only CRs for this flavor group (substring match)
//	--filter-state=state    Show only CRs in this state (e.g. confirmed, reserving)
//	--active                Shorthand: show only confirmed/guaranteed CRs
//	--views=v1,v2,...       Views to show (default: all). Available: summary, commitments, reservations, allocations
//	--hide=v1,v2,...        Views to hide (applied after --views)
//	--watch=interval        Refresh interval (e.g. 2s, 5s). Clears screen between refreshes.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
}

// ── ANSI colours ──────────────────────────────────────────────────────────────

const (
	colReset  = "\033[0m"
	colBold   = "\033[1m"
	colGreen  = "\033[32m"
	colYellow = "\033[33m"
	colRed    = "\033[31m"
	colCyan   = "\033[36m"
	colGray   = "\033[90m"
)

func green(s string) string  { return colGreen + s + colReset }
func yellow(s string) string { return colYellow + s + colReset }
func red(s string) string    { return colRed + s + colReset }
func cyan(s string) string   { return colCyan + s + colReset }
func gray(s string) string   { return colGray + s + colReset }
func bold(s string) string   { return colBold + s + colReset }

// ── Views ─────────────────────────────────────────────────────────────────────

const (
	viewSummary      = "summary"
	viewCommitments  = "commitments"
	viewReservations = "reservations"
	viewAllocations  = "allocations"
)

var allViews = []string{viewSummary, viewCommitments, viewReservations, viewAllocations}

type viewSet map[string]bool

func parseViews(s string) viewSet {
	vs := make(viewSet)
	if s == "all" || s == "" {
		for _, v := range allViews {
			vs[v] = true
		}
		return vs
	}
	for _, v := range strings.Split(s, ",") {
		vs[strings.TrimSpace(v)] = true
	}
	return vs
}

func (vs viewSet) hide(s string) {
	if s == "" {
		return
	}
	for _, v := range strings.Split(s, ",") {
		delete(vs, strings.TrimSpace(v))
	}
}

func (vs viewSet) has(v string) bool { return vs[v] }

// ── k8s client ────────────────────────────────────────────────────────────────

func newClient(contextName string) (client.Client, error) {
	if contextName == "" {
		c, err := config.GetConfig()
		if err != nil {
			return nil, fmt.Errorf("getting kubeconfig: %w", err)
		}
		return client.New(c, client.Options{Scheme: scheme})
	}
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		&clientcmd.ConfigOverrides{CurrentContext: contextName},
	)
	c, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("getting kubeconfig for context %q: %w", contextName, err)
	}
	return client.New(c, client.Options{Scheme: scheme})
}

// ── helpers ───────────────────────────────────────────────────────────────────

func printHeader(title string) {
	line := strings.Repeat("─", 80)
	fmt.Println()
	fmt.Println(bold(line))
	fmt.Printf("%s %s\n", bold("▶"), bold(title))
	fmt.Println(bold(line))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func age(t *metav1.Time) string {
	if t == nil {
		return gray("—")
	}
	d := time.Since(t.Time).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func crReadyStatus(cr v1alpha1.CommittedResource) string {
	cond := apimeta.FindStatusCondition(cr.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
	if cond == nil {
		return gray("unknown")
	}
	switch cond.Reason {
	case v1alpha1.CommittedResourceReasonAccepted:
		return green("Accepted")
	case v1alpha1.CommittedResourceReasonRejected:
		return red("Rejected")
	case v1alpha1.CommittedResourceReasonReserving:
		return yellow("Reserving")
	case v1alpha1.CommittedResourceReasonPlanned:
		return gray("Planned")
	default:
		return yellow(cond.Reason)
	}
}

func resReadyStatus(res v1alpha1.Reservation) string {
	cond := apimeta.FindStatusCondition(res.Status.Conditions, v1alpha1.ReservationConditionReady)
	if cond == nil {
		return gray("pending")
	}
	if cond.Status == metav1.ConditionTrue {
		return green("Ready")
	}
	return red("NotReady: " + truncate(cond.Message, 40))
}

func stateColour(state v1alpha1.CommitmentStatus) string {
	switch state {
	case v1alpha1.CommitmentStatusConfirmed, v1alpha1.CommitmentStatusGuaranteed:
		return green(string(state))
	case v1alpha1.CommitmentStatusPlanned, v1alpha1.CommitmentStatusPending:
		return yellow(string(state))
	case v1alpha1.CommitmentStatusExpired, v1alpha1.CommitmentStatusSuperseded:
		return gray(string(state))
	default:
		return string(state)
	}
}

// ── filters ───────────────────────────────────────────────────────────────────

type filters struct {
	project string
	az      string
	group   string
	state   string
	active  bool
}

func (f filters) match(cr v1alpha1.CommittedResource) bool {
	if f.project != "" && !strings.Contains(cr.Spec.ProjectID, f.project) {
		return false
	}
	if f.az != "" && !strings.Contains(cr.Spec.AvailabilityZone, f.az) {
		return false
	}
	if f.group != "" && !strings.Contains(cr.Spec.FlavorGroupName, f.group) {
		return false
	}
	if f.state != "" && !strings.EqualFold(string(cr.Spec.State), f.state) {
		return false
	}
	if f.active {
		s := cr.Spec.State
		if s != v1alpha1.CommitmentStatusConfirmed && s != v1alpha1.CommitmentStatusGuaranteed {
			return false
		}
	}
	return true
}

// ── views ─────────────────────────────────────────────────────────────────────

func printSummary(crs []v1alpha1.CommittedResource, reservations []v1alpha1.Reservation) {
	printHeader("Summary")

	byState := make(map[v1alpha1.CommitmentStatus]int)
	byReady := map[string]int{"Accepted": 0, "Reserving": 0, "Rejected": 0, "Planned": 0, "Unknown": 0}
	for _, cr := range crs {
		byState[cr.Spec.State]++
		cond := apimeta.FindStatusCondition(cr.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
		if cond == nil {
			byReady["Unknown"]++
		} else {
			byReady[cond.Reason]++
		}
	}

	resReady, resNotReady, resPending := 0, 0, 0
	for _, res := range reservations {
		cond := apimeta.FindStatusCondition(res.Status.Conditions, v1alpha1.ReservationConditionReady)
		switch {
		case cond == nil:
			resPending++
		case cond.Status == metav1.ConditionTrue:
			resReady++
		default:
			resNotReady++
		}
	}

	fmt.Printf("  CommittedResources : %s\n", bold(fmt.Sprintf("%d total", len(crs))))
	for _, s := range []v1alpha1.CommitmentStatus{
		v1alpha1.CommitmentStatusConfirmed,
		v1alpha1.CommitmentStatusGuaranteed,
		v1alpha1.CommitmentStatusPending,
		v1alpha1.CommitmentStatusPlanned,
		v1alpha1.CommitmentStatusExpired,
		v1alpha1.CommitmentStatusSuperseded,
	} {
		if n := byState[s]; n > 0 {
			fmt.Printf("    %-14s %d\n", string(s)+":", n)
		}
	}
	fmt.Println()
	fmt.Printf("  Ready conditions   : %s accepted, %s reserving, %s rejected\n",
		green(strconv.Itoa(byReady["Accepted"])),
		yellow(strconv.Itoa(byReady["Reserving"])),
		red(strconv.Itoa(byReady["Rejected"])),
	)
	fmt.Println()
	fmt.Printf("  Reservation slots  : %s total — %s ready, %s not-ready, %s pending\n",
		bold(strconv.Itoa(len(reservations))),
		green(strconv.Itoa(resReady)),
		red(strconv.Itoa(resNotReady)),
		yellow(strconv.Itoa(resPending)),
	)
}

func printCommitments(crs []v1alpha1.CommittedResource) {
	printHeader(fmt.Sprintf("CommittedResources (%d)", len(crs)))

	if len(crs) == 0 {
		fmt.Println(gray("  (none)"))
		return
	}

	for _, cr := range crs {
		fmt.Printf("\n  %s  %s\n",
			bold(cyan(cr.Spec.CommitmentUUID)),
			crReadyStatus(cr),
		)
		fmt.Printf("    project=%-36s  group=%-20s  az=%s\n",
			cr.Spec.ProjectID, cr.Spec.FlavorGroupName, cr.Spec.AvailabilityZone)
		fmt.Printf("    state=%-14s  amount=%-10s  accepted=%s\n",
			stateColour(cr.Spec.State),
			cr.Spec.Amount.String(),
			func() string {
				if cr.Status.AcceptedAmount == nil {
					return gray("—")
				}
				return cr.Status.AcceptedAmount.String()
			}(),
		)

		if cr.Status.UsedAmount != nil {
			fmt.Printf("    used=%-12s\n", cr.Status.UsedAmount.String())
		}

		endStr := gray("no expiry")
		if cr.Spec.EndTime != nil {
			remaining := time.Until(cr.Spec.EndTime.Time).Round(time.Minute)
			if remaining < 0 {
				endStr = red(fmt.Sprintf("expired %s ago", age(cr.Spec.EndTime)))
			} else {
				endStr = fmt.Sprintf("expires in %s (at %s)", remaining, cr.Spec.EndTime.Format(time.RFC3339))
			}
		}
		fmt.Printf("    age=%-8s  %s\n", age(&cr.CreationTimestamp), endStr)
	}
}

func printReservations(crs []v1alpha1.CommittedResource, reservations []v1alpha1.Reservation, showAllocations bool) {
	// Index reservations by CommitmentUUID for display under each CR.
	byUUID := make(map[string][]v1alpha1.Reservation)
	for _, res := range reservations {
		if res.Spec.CommittedResourceReservation == nil {
			continue
		}
		uuid := res.Spec.CommittedResourceReservation.CommitmentUUID
		byUUID[uuid] = append(byUUID[uuid], res)
	}

	printHeader("Reservation Slots")

	if len(reservations) == 0 {
		fmt.Println(gray("  (none)"))
		return
	}

	for _, cr := range crs {
		slots := byUUID[cr.Spec.CommitmentUUID]
		if len(slots) == 0 {
			continue
		}
		fmt.Printf("\n  %s  %s  %s\n",
			bold(cyan(cr.Spec.CommitmentUUID)),
			gray(cr.Spec.FlavorGroupName),
			gray(fmt.Sprintf("%d slot(s)", len(slots))),
		)

		sort.Slice(slots, func(i, j int) bool {
			return slots[i].Name < slots[j].Name
		})

		for _, res := range slots {
			targetHost := res.Spec.TargetHost
			statusHost := res.Status.Host
			var hostStr string
			switch {
			case statusHost == "":
				hostStr = yellow(targetHost) + gray(" (not yet placed)")
			case statusHost != targetHost:
				hostStr = red(fmt.Sprintf("target=%s status=%s (migrating?)", targetHost, statusHost))
			default:
				hostStr = green(targetHost)
			}

			genOK := ""
			if s := res.Status.CommittedResourceReservation; s != nil {
				spec := res.Spec.CommittedResourceReservation
				if spec != nil && s.ObservedParentGeneration != spec.ParentGeneration {
					genOK = yellow(fmt.Sprintf(" [gen: spec=%d observed=%d]",
						spec.ParentGeneration, s.ObservedParentGeneration))
				}
			}

			resources := ""
			var resourcesSb391 strings.Builder
			for rname, qty := range res.Spec.Resources {
				fmt.Fprintf(&resourcesSb391, "%s=%s ", rname, qty.String())
			}
			resources += resourcesSb391.String()

			fmt.Printf("    %s  host=%s  %s  %s%s\n",
				truncate(res.Name, 40),
				hostStr,
				resReadyStatus(res),
				gray(strings.TrimSpace(resources)),
				genOK,
			)

			if showAllocations {
				specAllocs := 0
				statusAllocs := 0
				if res.Spec.CommittedResourceReservation != nil {
					specAllocs = len(res.Spec.CommittedResourceReservation.Allocations)
				}
				if res.Status.CommittedResourceReservation != nil {
					statusAllocs = len(res.Status.CommittedResourceReservation.Allocations)
				}

				if specAllocs > 0 || statusAllocs > 0 {
					fmt.Printf("      allocations: spec=%d confirmed=%d\n", specAllocs, statusAllocs)
					if res.Spec.CommittedResourceReservation != nil {
						statusAlloc := map[string]string{}
						if res.Status.CommittedResourceReservation != nil {
							statusAlloc = res.Status.CommittedResourceReservation.Allocations
						}
						for vmUUID, alloc := range res.Spec.CommittedResourceReservation.Allocations {
							resources := ""
							var resourcesSb422 strings.Builder
							for rname, qty := range alloc.Resources {
								fmt.Fprintf(&resourcesSb422, "%s=%s ", rname, qty.String())
							}
							resources += resourcesSb422.String()
							confirmedHost, confirmed := statusAlloc[vmUUID]
							state := ""
							if confirmed {
								state = green("confirmed on " + confirmedHost)
							} else {
								state = yellow(fmt.Sprintf("spec-only (grace since %s)", age(&alloc.CreationTimestamp)))
							}
							fmt.Printf("        vm=%s  %s  %s\n",
								truncate(vmUUID, 36),
								gray(strings.TrimSpace(resources)),
								state,
							)
						}
					}
				}
			}
		}
	}
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	k8sContext := flag.String("context", "", "Kubernetes context (default: current context)")
	filterProject := flag.String("filter-project", "", "Show only CRs for this project ID (substring match)")
	filterAZ := flag.String("filter-az", "", "Show only CRs in this availability zone (substring match)")
	filterGroup := flag.String("filter-group", "", "Show only CRs for this flavor group (substring match)")
	filterState := flag.String("filter-state", "", "Show only CRs in this state")
	activeOnly := flag.Bool("active", false, "Show only confirmed/guaranteed CRs")
	viewsFlag := flag.String("views", "all", "Views: all, summary, commitments, reservations, allocations")
	hideFlag := flag.String("hide", "", "Views to hide (applied after --views)")
	watchInterval := flag.Duration("watch", 0, "Refresh interval (e.g. 2s, 5s). 0 = run once.")
	limitFlag := flag.Int("limit", 200, "Max CRs to fetch (0 = unlimited)")
	flag.Parse()

	views := parseViews(*viewsFlag)
	views.hide(*hideFlag)

	f := filters{
		project: *filterProject,
		az:      *filterAZ,
		group:   *filterGroup,
		state:   *filterState,
		active:  *activeOnly,
	}

	cl, err := newClient(*k8sContext)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	var prevDigest string
	first := true
	for {
		crs, reservations := fetchSnapshot(ctx, cl, f, *limitFlag)
		if d := snapshotDigest(crs, reservations); first || d != prevDigest {
			if !first {
				fmt.Printf("\n%s %s %s\n",
					bold("━━━ changed at"),
					bold(time.Now().Format(time.RFC3339)),
					bold("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"),
				)
			}
			printSnapshot(crs, reservations, f, views)
			prevDigest = d
			first = false
		}
		if *watchInterval == 0 {
			break
		}
		time.Sleep(*watchInterval)
	}
}

// snapshotDigest returns a string that changes whenever any CRD is added, removed, or updated.
func snapshotDigest(crs []v1alpha1.CommittedResource, reservations []v1alpha1.Reservation) string {
	var b strings.Builder
	for _, cr := range crs {
		fmt.Fprintf(&b, "%s:%s ", cr.Name, cr.ResourceVersion)
	}
	for _, res := range reservations {
		fmt.Fprintf(&b, "%s:%s ", res.Name, res.ResourceVersion)
	}
	return b.String()
}

func fetchSnapshot(ctx context.Context, cl client.Client, f filters, limit int) ([]v1alpha1.CommittedResource, []v1alpha1.Reservation) {
	var listOpts []client.ListOption
	if limit > 0 {
		listOpts = append(listOpts, client.Limit(int64(limit)))
	}

	var crList v1alpha1.CommittedResourceList
	if err := cl.List(ctx, &crList, listOpts...); err != nil {
		fmt.Fprintf(os.Stderr, "error listing CommittedResources: %v\n", err)
		os.Exit(1)
	}

	var resList v1alpha1.ReservationList
	if err := cl.List(ctx, &resList, append(listOpts, client.MatchingLabels{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
	})...); err != nil {
		fmt.Fprintf(os.Stderr, "error listing Reservations: %v\n", err)
		os.Exit(1)
	}

	if crList.Continue != "" {
		fmt.Fprintf(os.Stderr, yellow("warning: CR list truncated at %d — use --limit=0 or a higher value to see all\n"), limit)
	}
	if resList.Continue != "" {
		fmt.Fprintf(os.Stderr, yellow("warning: Reservation list truncated at %d — use --limit=0 or a higher value to see all\n"), limit)
	}
	var crs []v1alpha1.CommittedResource
	for _, cr := range crList.Items {
		if f.match(cr) {
			crs = append(crs, cr)
		}
	}
	sort.Slice(crs, func(i, j int) bool {
		if crs[i].Spec.FlavorGroupName != crs[j].Spec.FlavorGroupName {
			return crs[i].Spec.FlavorGroupName < crs[j].Spec.FlavorGroupName
		}
		return crs[i].Spec.CommitmentUUID < crs[j].Spec.CommitmentUUID
	})

	matchedUUIDs := make(map[string]bool, len(crs))
	for _, cr := range crs {
		matchedUUIDs[cr.Spec.CommitmentUUID] = true
	}
	var reservations []v1alpha1.Reservation
	for _, res := range resList.Items {
		if res.Spec.CommittedResourceReservation == nil {
			continue
		}
		if matchedUUIDs[res.Spec.CommittedResourceReservation.CommitmentUUID] {
			reservations = append(reservations, res)
		}
	}
	return crs, reservations
}

func printSnapshot(crs []v1alpha1.CommittedResource, reservations []v1alpha1.Reservation, f filters, views viewSet) {
	fmt.Printf("\n%s — %s\n",
		bold("visualize-committed-resources"),
		gray(time.Now().Format(time.RFC3339)),
	)
	if f.project != "" || f.az != "" || f.group != "" || f.state != "" || f.active {
		fmt.Printf("%s project=%q az=%q group=%q state=%q active=%v\n",
			gray("filters:"), f.project, f.az, f.group, f.state, f.active)
	}

	if views.has(viewSummary) {
		printSummary(crs, reservations)
	}
	if views.has(viewCommitments) {
		printCommitments(crs)
	}
	if views.has(viewReservations) {
		printReservations(crs, reservations, views.has(viewAllocations))
	}

	fmt.Println()
}
