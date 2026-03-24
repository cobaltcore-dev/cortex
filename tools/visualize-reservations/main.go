// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

// Tool to visualize failover reservations and their VM connections
//
// Usage:
//
//	go run tools/visualize-reservations/main.go [flags]
//
// Flags:
//
//	--sort=vm|vm-host|res-host  Sort VMs by UUID, VM host, or reservation host
//	--postgres-secret=name      Name of the kubernetes secret containing postgres credentials (default: cortex-nova-postgres)
//	--namespace=ns              Namespace of the postgres secret (default: default)
//	--postgres-host=host        Override postgres host (useful with port-forward, e.g., localhost)
//	--postgres-port=port        Override postgres port (useful with port-forward, e.g., 5432)
//	--views=view1,view2,...     Comma-separated list of views to show (default: all)
//	                            Available views: hypervisors, vms, reservations, summary,
//	                            hypervisor-summary, validation, stale, without-res, not-in-db, by-host
//	--hide=view1,view2,...      Comma-separated list of views to hide (applied after --views)
//	--filter-name=pattern       Filter hypervisors by name (substring match)
//	--filter-trait=trait        Filter hypervisors by trait (e.g., CUSTOM_HANA_EXCLUSIVE_HOST)
//	--hypervisor-context=name   Kubernetes context for reading Hypervisors (default: current context)
//	--reservation-context=name  Kubernetes context for reading Reservations (default: current context)
//	--postgres-context=name     Kubernetes context for reading postgres secret (default: current context)
//
// To connect to postgres when running locally, use kubectl port-forward:
//
//	kubectl port-forward svc/cortex-nova-postgresql 5432:5432 -n <namespace>
//	go run tools/visualize-reservations/main.go --postgres-host=localhost --postgres-port=5432
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	_ "github.com/lib/pq"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(hv1.AddToScheme(scheme))
}

// vmEntry holds VM data for sorting
type vmEntry struct {
	UUID         string
	Host         string
	Reservations []string // reservation_name@host
	// From postgres
	InServerTable bool
	FlavorName    string
	VCPUs         int
	RAMMb         int
	DiskGb        int
	// Tracking fields
	NotOnHypervisors  bool   // VM is in reservation but not found on any hypervisor
	ReservationSource string // Name of reservation where this VM was found (if not on hypervisor)
}

// serverInfo holds server data from postgres
type serverInfo struct {
	ID               string
	FlavorName       string
	HostID           string // OS-EXT-SRV-ATTR:host_id (hypervisor ID)
	OSEXTSRVATTRHost string // OS-EXT-SRV-ATTR:host (hypervisor hostname)
}

// flavorInfo holds flavor data from postgres
type flavorInfo struct {
	Name       string
	VCPUs      int
	RAMMb      int
	DiskGb     int
	ExtraSpecs string // JSON string of extra specs
}

// hypervisorSummary holds aggregated data for a hypervisor
type hypervisorSummary struct {
	Name              string
	NumVMs            int
	NumReservations   int
	CapacityCPU       int64
	CapacityMemoryGB  int64
	UsedByVMsCPU      int64
	UsedByVMsMemoryGB int64
	FailoverResCPU    int64
	FailoverResMemGB  int64
	CommittedResCPU   int64
	CommittedResMemGB int64
	FreeCPU           int64
	FreeMemoryGB      int64
	Traits            []string
}

// viewSet tracks which views should be displayed
type viewSet map[string]bool

// Available view names
const (
	viewHypervisors       = "hypervisors"
	viewHypervisorsVMsRes = "hypervisors-vms-res"
	viewVMs               = "vms"
	viewReservations      = "reservations"
	viewSummary           = "summary"
	viewHypervisorSummary = "hypervisor-summary"
	viewFlavorSummary     = "flavor-summary"
	viewValidation        = "validation"
	viewStale             = "stale"
	viewWithoutRes        = "without-res"
	viewNotInDB           = "not-in-db"
	viewByHost            = "by-host"
	viewAllServers        = "all-servers"
)

var allViews = []string{
	viewHypervisors,
	viewHypervisorsVMsRes,
	viewVMs,
	viewReservations,
	viewSummary,
	viewHypervisorSummary,
	viewFlavorSummary,
	viewValidation,
	viewStale,
	viewWithoutRes,
	viewNotInDB,
	viewByHost,
	viewAllServers,
}

func parseViews(viewsFlag string) viewSet {
	views := make(viewSet)
	if viewsFlag == "all" || viewsFlag == "" {
		for _, v := range allViews {
			views[v] = true
		}
		return views
	}
	for _, v := range strings.Split(viewsFlag, ",") {
		v = strings.TrimSpace(v)
		if v != "" {
			views[v] = true
		}
	}
	return views
}

func (v viewSet) has(view string) bool {
	return v[view]
}

func applyHideViews(views viewSet, hideFlag string) {
	if hideFlag == "" {
		return
	}
	for _, v := range strings.Split(hideFlag, ",") {
		v = strings.TrimSpace(v)
		if v != "" {
			delete(views, v)
		}
	}
}

// getClientForContext creates a kubernetes client for the specified context.
// If contextName is empty, it uses the current/default context.
func getClientForContext(contextName string) (client.Client, error) {
	var cfg *rest.Config
	var err error

	if contextName == "" {
		// Use default context
		cfg, err = config.GetConfig()
		if err != nil {
			return nil, fmt.Errorf("getting default kubeconfig: %w", err)
		}
	} else {
		// Use specified context
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		configOverrides := &clientcmd.ConfigOverrides{
			CurrentContext: contextName,
		}
		kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
		cfg, err = kubeConfig.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("getting kubeconfig for context %q: %w", contextName, err)
		}
	}

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("creating client: %w", err)
	}
	return k8sClient, nil
}

func main() {
	// Parse command line flags
	sortBy := flag.String("sort", "vm", "Sort VMs by: vm (UUID), vm-host (VM's host), res-host (reservation host)")
	postgresSecret := flag.String("postgres-secret", "cortex-nova-postgres", "Name of the kubernetes secret containing postgres credentials")
	namespace := flag.String("namespace", "", "Namespace of the postgres secret (defaults to 'default')")
	postgresHostOverride := flag.String("postgres-host", "", "Override postgres host (useful with port-forward, e.g., localhost)")
	postgresPortOverride := flag.String("postgres-port", "", "Override postgres port (useful with port-forward, e.g., 5432)")
	viewsFlag := flag.String("views", "all", "Comma-separated list of views to show (all, hypervisors, vms, reservations, summary, hypervisor-summary, validation, stale, without-res, not-in-db, by-host)")
	hideFlag := flag.String("hide", "", "Comma-separated list of views to hide (applied after --views)")
	filterName := flag.String("filter-name", "", "Filter hypervisors by name (substring match)")
	filterTrait := flag.String("filter-trait", "", "Filter hypervisors by trait (e.g., CUSTOM_HANA_EXCLUSIVE_HOST)")
	hypervisorContext := flag.String("hypervisor-context", "", "Kubernetes context for reading Hypervisors (default: current context)")
	reservationContext := flag.String("reservation-context", "", "Kubernetes context for reading Reservations (default: current context)")
	postgresContext := flag.String("postgres-context", "", "Kubernetes context for reading postgres secret (default: current context)")
	flag.Parse()

	views := parseViews(*viewsFlag)
	applyHideViews(views, *hideFlag)

	ctx := context.Background()

	// Create kubernetes clients for hypervisors and reservations
	// They may use different contexts if specified
	hvClient, err := getClientForContext(*hypervisorContext)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating hypervisor client: %v\n", err)
		os.Exit(1)
	}

	// Reuse the same client if contexts are the same, otherwise create a new one
	var resClient client.Client
	if *reservationContext == *hypervisorContext {
		resClient = hvClient
	} else {
		resClient, err = getClientForContext(*reservationContext)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating reservation client: %v\n", err)
			os.Exit(1)
		}
	}

	// Create postgres client (for reading the secret)
	// This is typically the local cluster where cortex runs
	var pgClient client.Client
	switch *postgresContext {
	case *hypervisorContext:
		pgClient = hvClient
	case *reservationContext:
		pgClient = resClient
	default:
		pgClient, err = getClientForContext(*postgresContext)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating postgres client: %v\n", err)
			os.Exit(1)
		}
	}

	// Determine namespace
	ns := *namespace
	if ns == "" {
		ns = "default" // Default fallback
	}

	// Try to connect to postgres (use pgClient for reading the secret)
	var db *sql.DB
	var serverMap map[string]serverInfo
	var flavorMap map[string]flavorInfo

	db, serverMap, flavorMap = connectToPostgres(ctx, pgClient, *postgresSecret, ns, *postgresHostOverride, *postgresPortOverride, *postgresContext)
	if db != nil {
		defer db.Close()
	}

	// Get all hypervisors to find all VMs (use hvClient)
	var allHypervisors hv1.HypervisorList
	if err := hvClient.List(ctx, &allHypervisors); err != nil {
		fmt.Fprintf(os.Stderr, "Error listing hypervisors: %v\n", err)
		return
	}

	// Apply filters to hypervisors
	var hypervisors hv1.HypervisorList
	filteredHosts := make(map[string]bool) // Track which hosts pass the filter
	for _, hv := range allHypervisors.Items {
		if matchesFilter(hv, *filterName, *filterTrait) {
			hypervisors.Items = append(hypervisors.Items, hv)
			filteredHosts[hv.Name] = true
		}
	}

	// Build map of all VMs from hypervisors
	allVMs := make(map[string]*vmEntry) // vm_uuid -> vmEntry
	for _, hv := range hypervisors.Items {
		for _, inst := range hv.Status.Instances {
			if inst.Active {
				entry := &vmEntry{
					UUID:         inst.ID,
					Host:         hv.Name,
					Reservations: []string{},
				}
				// Check if VM is in server table and get flavor info
				if serverMap != nil {
					if server, ok := serverMap[inst.ID]; ok {
						entry.InServerTable = true
						entry.FlavorName = server.FlavorName
						if flavorMap != nil {
							if flavor, ok := flavorMap[server.FlavorName]; ok {
								entry.VCPUs = flavor.VCPUs
								entry.RAMMb = flavor.RAMMb
								entry.DiskGb = flavor.DiskGb
							}
						}
					}
				}
				allVMs[inst.ID] = entry
			}
		}
	}

	// Get all reservations (both failover and committed) (use resClient)
	var allReservations v1alpha1.ReservationList
	if err := resClient.List(ctx, &allReservations); err != nil {
		fmt.Fprintf(os.Stderr, "Error listing reservations: %v\n", err)
		return
	}

	// Filter failover reservations for backward compatibility
	var failoverReservations []v1alpha1.Reservation
	for _, res := range allReservations.Items {
		if res.Spec.Type == v1alpha1.ReservationTypeFailover {
			failoverReservations = append(failoverReservations, res)
		}
	}

	// Filter reservations to only those on filtered hosts
	var filteredReservations []v1alpha1.Reservation
	var filteredFailoverReservations []v1alpha1.Reservation
	hasFilter := *filterName != "" || *filterTrait != ""
	if hasFilter {
		for _, res := range allReservations.Items {
			host := res.Status.Host
			if host == "" {
				host = res.Spec.TargetHost
			}
			if filteredHosts[host] {
				filteredReservations = append(filteredReservations, res)
				if res.Spec.Type == v1alpha1.ReservationTypeFailover {
					filteredFailoverReservations = append(filteredFailoverReservations, res)
				}
			}
		}
		// Replace the reservation lists with filtered ones
		allReservations.Items = filteredReservations
		failoverReservations = filteredFailoverReservations
	}

	printHeader("Failover Reservations Visualization")
	if hasFilter {
		fmt.Printf("Filter: name=%q, trait=%q\n", *filterName, *filterTrait)
		fmt.Printf("Matched Hypervisors: %d (of %d total)\n", len(hypervisors.Items), len(allHypervisors.Items))
	} else {
		fmt.Printf("Total Hypervisors: %d\n", len(hypervisors.Items))
	}
	fmt.Printf("Total VMs (from hypervisors): %d\n", len(allVMs))
	fmt.Printf("Total Failover Reservations: %d\n", len(failoverReservations))
	fmt.Printf("Total All Reservations: %d\n", len(allReservations.Items))
	fmt.Printf("Sort by: %s\n", *sortBy)
	fmt.Printf("Views: %s\n", *viewsFlag)
	if db != nil {
		fmt.Printf("Postgres: connected (servers: %d, flavors: %d)\n", len(serverMap), len(flavorMap))
	} else {
		fmt.Printf("Postgres: not connected\n")
	}
	fmt.Println()

	// Print Hypervisor Summary
	if views.has(viewHypervisorSummary) {
		printHypervisorSummary(hypervisors.Items, allReservations.Items)
	}

	// Print Flavor Summary (requires postgres)
	if db != nil && views.has(viewFlavorSummary) {
		printFlavorSummary(allVMs, flavorMap)
	}

	// Print Hypervisors and their VMs
	if views.has(viewHypervisors) {
		printHeader("Hypervisors and their VMs")

		// Build hypervisor -> VMs map
		hypervisorVMs := make(map[string][]string)
		for _, hv := range hypervisors.Items {
			hypervisorVMs[hv.Name] = []string{}
			for _, inst := range hv.Status.Instances {
				if inst.Active {
					hypervisorVMs[hv.Name] = append(hypervisorVMs[hv.Name], inst.ID)
				}
			}
		}

		// Sort hypervisor names
		hypervisorNames := make([]string, 0, len(hypervisorVMs))
		for name := range hypervisorVMs {
			hypervisorNames = append(hypervisorNames, name)
		}
		sort.Strings(hypervisorNames)

		for _, hvName := range hypervisorNames {
			vms := hypervisorVMs[hvName]
			sort.Strings(vms)
			fmt.Printf("🖥️  %s (%d VMs)\n", hvName, len(vms))
			for _, vmUUID := range vms {
				vmInfo := vmUUID
				if entry, ok := allVMs[vmUUID]; ok && entry.FlavorName != "" {
					ramGB := entry.RAMMb / 1024
					vmInfo = fmt.Sprintf("%s [%s, %dvcpu, %dGB]", vmUUID, entry.FlavorName, entry.VCPUs, ramGB)
				}
				fmt.Printf("     - %s\n", vmInfo)
			}
			fmt.Println()
		}
	}

	// Build VM -> Reservations mapping from reservations
	vmsWithReservations := make(map[string]bool)
	vmsInReservationsNotOnHypervisors := make([]*vmEntry, 0) // Track VMs in reservations but not on hypervisors

	// Build reservation host -> reservations map for the combined view
	reservationsByHost := make(map[string][]v1alpha1.Reservation)
	for _, res := range failoverReservations {
		host := res.Status.Host
		if host == "" {
			host = res.Spec.TargetHost
		}
		if host != "" {
			reservationsByHost[host] = append(reservationsByHost[host], res)
		}
	}

	// Build VM -> reservation names mapping (for the combined view)
	vmToReservationNames := make(map[string][]string) // vm_uuid -> []reservation_name@host
	for _, res := range failoverReservations {
		if res.Status.FailoverReservation == nil || res.Status.FailoverReservation.Allocations == nil {
			continue
		}
		resHost := res.Status.Host
		if resHost == "" {
			resHost = res.Spec.TargetHost
		}
		for vmUUID := range res.Status.FailoverReservation.Allocations {
			vmToReservationNames[vmUUID] = append(vmToReservationNames[vmUUID], fmt.Sprintf("%s@%s", res.Name, resHost))
		}
	}

	// Print Hypervisors with their VMs and Reservations (combined view)
	if views.has(viewHypervisorsVMsRes) {
		printHeader("Hypervisors and their VMs and Reservations")

		// Build hypervisor -> VMs map
		hypervisorVMs := make(map[string][]string)
		for _, hv := range hypervisors.Items {
			hypervisorVMs[hv.Name] = []string{}
			for _, inst := range hv.Status.Instances {
				if inst.Active {
					hypervisorVMs[hv.Name] = append(hypervisorVMs[hv.Name], inst.ID)
				}
			}
		}

		// Sort hypervisor names by BB then node
		hypervisorNames := make([]string, 0, len(hypervisorVMs))
		for name := range hypervisorVMs {
			hypervisorNames = append(hypervisorNames, name)
		}
		sort.Slice(hypervisorNames, func(i, j int) bool {
			bbI, nodeI := parseHypervisorName(hypervisorNames[i])
			bbJ, nodeJ := parseHypervisorName(hypervisorNames[j])
			if bbI != bbJ {
				return bbI < bbJ
			}
			return nodeI < nodeJ
		})

		for _, hvName := range hypervisorNames {
			vms := hypervisorVMs[hvName]
			reservations := reservationsByHost[hvName]
			sort.Strings(vms)

			fmt.Printf("🖥️  %s (%d VMs, %d Reservations)\n", hvName, len(vms), len(reservations))

			// Print VMs section
			if len(vms) > 0 {
				fmt.Println("   📋 VMs:")
				for _, vmUUID := range vms {
					vmInfo := vmUUID
					if entry, ok := allVMs[vmUUID]; ok && entry.FlavorName != "" {
						ramGB := entry.RAMMb / 1024
						vmInfo = fmt.Sprintf("%s [%s, %dvcpu, %dGB]", vmUUID, entry.FlavorName, entry.VCPUs, ramGB)
					}
					// Add reservation info for this VM
					if resNames, ok := vmToReservationNames[vmUUID]; ok && len(resNames) > 0 {
						vmInfo += " → " + strings.Join(resNames, ", ")
					} else {
						vmInfo += " → (no reservations)"
					}
					fmt.Printf("      - %s\n", vmInfo)
				}
			}

			// Print Reservations section
			if len(reservations) > 0 {
				// Sort reservations by name
				sort.Slice(reservations, func(i, j int) bool {
					return reservations[i].Name < reservations[j].Name
				})

				fmt.Println("   📦 Reservations (hosted here):")
				for _, res := range reservations {
					ready := "?"
					for _, cond := range res.Status.Conditions {
						if cond.Type == "Ready" {
							if cond.Status == "True" {
								ready = "✅"
							} else {
								ready = "❌"
							}
							break
						}
					}

					// Get reservation size
					var sizeStr string
					if len(res.Spec.Resources) > 0 {
						var parts []string
						if vcpus, ok := res.Spec.Resources["vcpus"]; ok {
							parts = append(parts, fmt.Sprintf("%dvcpu", vcpus.Value()))
						}
						if mem, ok := res.Spec.Resources["memory"]; ok {
							memGB := mem.Value() / (1024 * 1024 * 1024)
							parts = append(parts, fmt.Sprintf("%dGB", memGB))
						}
						sizeStr = strings.Join(parts, ", ")
					}

					// Get VMs allocated to this reservation
					var vmCount int
					var vmHosts []string
					if res.Status.FailoverReservation != nil && res.Status.FailoverReservation.Allocations != nil {
						vmCount = len(res.Status.FailoverReservation.Allocations)
						hostSet := make(map[string]bool)
						for _, vmHost := range res.Status.FailoverReservation.Allocations {
							hostSet[vmHost] = true
						}
						for h := range hostSet {
							vmHosts = append(vmHosts, h)
						}
						sort.Strings(vmHosts)
					}

					resInfo := fmt.Sprintf("%s %s", ready, res.Name)
					if sizeStr != "" {
						resInfo += fmt.Sprintf(" [%s]", sizeStr)
					}
					if vmCount > 0 {
						resInfo += fmt.Sprintf(" ← %d VMs from %s", vmCount, strings.Join(vmHosts, ", "))
					}
					fmt.Printf("      - %s\n", resInfo)
				}
			}

			fmt.Println()
		}
	}

	for _, res := range failoverReservations {
		if res.Status.FailoverReservation == nil || res.Status.FailoverReservation.Allocations == nil {
			continue
		}
		resHost := res.Status.Host
		if resHost == "" {
			resHost = res.Spec.TargetHost
		}
		for vmUUID, vmHost := range res.Status.FailoverReservation.Allocations {
			vmsWithReservations[vmUUID] = true
			// Update or create VM entry
			if allVMs[vmUUID] == nil {
				// VM not in hypervisors but in reservation (might be stale/deleted)
				entry := &vmEntry{
					UUID:              vmUUID,
					Host:              vmHost,
					Reservations:      []string{},
					NotOnHypervisors:  true,
					ReservationSource: res.Name,
				}
				// Check if VM is in server table
				if serverMap != nil {
					if server, ok := serverMap[vmUUID]; ok {
						entry.InServerTable = true
						entry.FlavorName = server.FlavorName
						if flavorMap != nil {
							if flavor, ok := flavorMap[server.FlavorName]; ok {
								entry.VCPUs = flavor.VCPUs
								entry.RAMMb = flavor.RAMMb
								entry.DiskGb = flavor.DiskGb
							}
						}
					}
				}
				allVMs[vmUUID] = entry
				vmsInReservationsNotOnHypervisors = append(vmsInReservationsNotOnHypervisors, entry)
			}
			allVMs[vmUUID].Reservations = append(allVMs[vmUUID].Reservations, fmt.Sprintf("%s@%s", res.Name, resHost))
		}
	}

	// Print VMs and their Reservations
	if views.has(viewVMs) {
		printHeader("VMs and their Reservations")
		if len(allVMs) == 0 {
			fmt.Println("No VMs found.")
		} else {
			if db != nil {
				fmt.Printf("%-40s %-25s %-5s %-20s %-6s %-8s %s\n",
					"VM UUID", "VM Host", "InDB", "Flavor", "VCPUs", "RAM(GB)", "Reservations")
				fmt.Printf("%-40s %-25s %-5s %-20s %-6s %-8s %s\n",
					strings.Repeat("-", 40), strings.Repeat("-", 25), strings.Repeat("-", 5),
					strings.Repeat("-", 20), strings.Repeat("-", 6), strings.Repeat("-", 8),
					strings.Repeat("-", 30))
			} else {
				fmt.Printf("%-40s %-25s %s\n", "VM UUID", "VM Host", "Reservations (name@host)")
				fmt.Printf("%-40s %-25s %s\n", strings.Repeat("-", 40), strings.Repeat("-", 25), strings.Repeat("-", 30))
			}

			// Convert map to slice for sorting
			vmList := make([]*vmEntry, 0, len(allVMs))
			for _, entry := range allVMs {
				vmList = append(vmList, entry)
			}

			// Sort based on flag
			switch *sortBy {
			case "vm-host":
				sort.Slice(vmList, func(i, j int) bool {
					if vmList[i].Host != vmList[j].Host {
						return vmList[i].Host < vmList[j].Host
					}
					return vmList[i].UUID < vmList[j].UUID
				})
			case "res-host":
				sort.Slice(vmList, func(i, j int) bool {
					// Get first reservation host for sorting (VMs without reservations sort last)
					iResHost := "zzz" // Sort VMs without reservations last
					jResHost := "zzz"
					if len(vmList[i].Reservations) > 0 {
						parts := strings.Split(vmList[i].Reservations[0], "@")
						if len(parts) == 2 {
							iResHost = parts[1]
						}
					}
					if len(vmList[j].Reservations) > 0 {
						parts := strings.Split(vmList[j].Reservations[0], "@")
						if len(parts) == 2 {
							jResHost = parts[1]
						}
					}
					if iResHost != jResHost {
						return iResHost < jResHost
					}
					return vmList[i].UUID < vmList[j].UUID
				})
			default: // "vm" - sort by UUID
				sort.Slice(vmList, func(i, j int) bool {
					return vmList[i].UUID < vmList[j].UUID
				})
			}

			for _, entry := range vmList {
				reservationsList := "(none)"
				if len(entry.Reservations) > 0 {
					reservationsList = strings.Join(entry.Reservations, ", ")
				}
				if db != nil {
					inDB := "❌"
					if entry.InServerTable {
						inDB = "✅"
					}
					ramGB := entry.RAMMb / 1024
					fmt.Printf("%-40s %-25s %-5s %-20s %-6d %-8d %s\n",
						truncate(entry.UUID, 40), truncate(entry.Host, 25), inDB,
						truncate(entry.FlavorName, 20), entry.VCPUs, ramGB,
						reservationsList)
				} else {
					fmt.Printf("%-40s %-25s %s\n", truncate(entry.UUID, 40), truncate(entry.Host, 25), reservationsList)
				}
			}
		}
		fmt.Println()
	}

	// Print VMs not in server table (only if postgres is connected)
	if db != nil && views.has(viewNotInDB) {
		printHeader("VMs NOT in Server Table (Hypervisor only)")
		vmsNotInDB := make([]*vmEntry, 0)
		for _, entry := range allVMs {
			if !entry.InServerTable {
				vmsNotInDB = append(vmsNotInDB, entry)
			}
		}
		sort.Slice(vmsNotInDB, func(i, j int) bool {
			if vmsNotInDB[i].Host != vmsNotInDB[j].Host {
				return vmsNotInDB[i].Host < vmsNotInDB[j].Host
			}
			return vmsNotInDB[i].UUID < vmsNotInDB[j].UUID
		})

		if len(vmsNotInDB) == 0 {
			fmt.Println("  ✅ All VMs from hypervisors are in the server table")
		} else {
			fmt.Printf("  ⚠️  %d VMs not in server table:\n\n", len(vmsNotInDB))
			fmt.Printf("  %-40s %s\n", "VM UUID", "VM Host")
			fmt.Printf("  %-40s %s\n", strings.Repeat("-", 40), strings.Repeat("-", 25))
			for _, entry := range vmsNotInDB {
				fmt.Printf("  %-40s %s\n", truncate(entry.UUID, 40), entry.Host)
			}
		}
		fmt.Println()
	}

	// Print VMs without reservations
	if views.has(viewWithoutRes) {
		printHeader("VMs WITHOUT Reservations")
		vmsWithoutRes := make([]*vmEntry, 0)
		for _, entry := range allVMs {
			if len(entry.Reservations) == 0 {
				vmsWithoutRes = append(vmsWithoutRes, entry)
			}
		}
		sort.Slice(vmsWithoutRes, func(i, j int) bool {
			if vmsWithoutRes[i].Host != vmsWithoutRes[j].Host {
				return vmsWithoutRes[i].Host < vmsWithoutRes[j].Host
			}
			return vmsWithoutRes[i].UUID < vmsWithoutRes[j].UUID
		})

		// Count VMs without reservations that are not in DB
		vmsWithoutResNotInDB := 0
		for _, entry := range vmsWithoutRes {
			if !entry.InServerTable {
				vmsWithoutResNotInDB++
			}
		}

		if len(vmsWithoutRes) == 0 {
			fmt.Println("  ✅ All VMs have at least one reservation")
		} else {
			if db != nil && vmsWithoutResNotInDB > 0 {
				fmt.Printf("  ⚠️  %d VMs without any reservations (%d not in DB):\n\n", len(vmsWithoutRes), vmsWithoutResNotInDB)
			} else {
				fmt.Printf("  ⚠️  %d VMs without any reservations:\n\n", len(vmsWithoutRes))
			}
			if db != nil {
				fmt.Printf("  %-40s %-25s %-5s %-20s %-6s %-8s\n", "VM UUID", "VM Host", "InDB", "Flavor", "VCPUs", "RAM(GB)")
				fmt.Printf("  %-40s %-25s %-5s %-20s %-6s %-8s\n",
					strings.Repeat("-", 40), strings.Repeat("-", 25), strings.Repeat("-", 5),
					strings.Repeat("-", 20), strings.Repeat("-", 6), strings.Repeat("-", 8))
				for _, entry := range vmsWithoutRes {
					inDB := "❌"
					if entry.InServerTable {
						inDB = "✅"
					}
					ramGB := entry.RAMMb / 1024
					fmt.Printf("  %-40s %-25s %-5s %-20s %-6d %-8d\n",
						truncate(entry.UUID, 40), entry.Host, inDB, truncate(entry.FlavorName, 20),
						entry.VCPUs, ramGB)
				}
			} else {
				fmt.Printf("  %-40s %s\n", "VM UUID", "VM Host")
				fmt.Printf("  %-40s %s\n", strings.Repeat("-", 40), strings.Repeat("-", 25))
				for _, entry := range vmsWithoutRes {
					fmt.Printf("  %-40s %s\n", truncate(entry.UUID, 40), entry.Host)
				}
			}
		}
		fmt.Println()
	}

	// Print VMs in reservations but NOT on any hypervisor (stale/deleted VMs)
	if views.has(viewStale) {
		printHeader("VMs in Reservations but NOT on Hypervisors (STALE)")
		if len(vmsInReservationsNotOnHypervisors) == 0 {
			fmt.Println("  ✅ All VMs in reservations are found on hypervisors")
		} else {
			fmt.Printf("  ⚠️  %d VMs in reservations but not on any hypervisor:\n\n", len(vmsInReservationsNotOnHypervisors))

			// Sort by reservation source
			sort.Slice(vmsInReservationsNotOnHypervisors, func(i, j int) bool {
				if vmsInReservationsNotOnHypervisors[i].ReservationSource != vmsInReservationsNotOnHypervisors[j].ReservationSource {
					return vmsInReservationsNotOnHypervisors[i].ReservationSource < vmsInReservationsNotOnHypervisors[j].ReservationSource
				}
				return vmsInReservationsNotOnHypervisors[i].UUID < vmsInReservationsNotOnHypervisors[j].UUID
			})

			if db != nil {
				fmt.Printf("  %-40s %-25s %-5s %-25s %s\n", "VM UUID", "Claimed Host", "InDB", "Found in Reservation", "All Reservations")
				fmt.Printf("  %-40s %-25s %-5s %-25s %s\n",
					strings.Repeat("-", 40), strings.Repeat("-", 25), strings.Repeat("-", 5),
					strings.Repeat("-", 25), strings.Repeat("-", 30))
				for _, entry := range vmsInReservationsNotOnHypervisors {
					inDB := "❌"
					if entry.InServerTable {
						inDB = "✅"
					}
					fmt.Printf("  %-40s %-25s %-5s %-25s %s\n",
						truncate(entry.UUID, 40), truncate(entry.Host, 25), inDB,
						truncate(entry.ReservationSource, 25), strings.Join(entry.Reservations, ", "))
				}
			} else {
				fmt.Printf("  %-40s %-25s %-25s %s\n", "VM UUID", "Claimed Host", "Found in Reservation", "All Reservations")
				fmt.Printf("  %-40s %-25s %-25s %s\n",
					strings.Repeat("-", 40), strings.Repeat("-", 25), strings.Repeat("-", 25), strings.Repeat("-", 30))
				for _, entry := range vmsInReservationsNotOnHypervisors {
					fmt.Printf("  %-40s %-25s %-25s %s\n",
						truncate(entry.UUID, 40), truncate(entry.Host, 25),
						truncate(entry.ReservationSource, 25), strings.Join(entry.Reservations, ", "))
				}
			}
			fmt.Println()
			fmt.Println("  Note: These VMs may have been deleted or moved. The failover controller should clean them up.")
		}
		fmt.Println()
	}

	// Print Reservations and their VMs (multiline format)
	if views.has(viewReservations) && len(failoverReservations) > 0 {
		printHeader("Reservations and their VMs")

		// Sort reservations by name
		sort.Slice(failoverReservations, func(i, j int) bool {
			return failoverReservations[i].Name < failoverReservations[j].Name
		})

		for _, res := range failoverReservations {
			host := res.Status.Host
			if host == "" {
				host = res.Spec.TargetHost
			}
			if host == "" {
				host = "N/A"
			}

			ready := "Unknown"
			for _, cond := range res.Status.Conditions {
				if cond.Type == "Ready" {
					ready = string(cond.Status)
					break
				}
			}

			fmt.Printf("📦 %s\n", res.Name)
			fmt.Printf("   Host:  %s\n", host)
			fmt.Printf("   Ready: %s\n", ready)

			// Print reservation size from Spec.Resources if available
			if len(res.Spec.Resources) > 0 {
				var sizeInfo []string
				for name, qty := range res.Spec.Resources {
					// Convert to appropriate units
					switch name {
					case "memory":
						// Memory is in bytes (resource.Quantity.Value() returns bytes)
						// 4080Mi = 4080 * 1024 * 1024 bytes
						memBytes := qty.Value()
						memGB := memBytes / (1024 * 1024 * 1024)
						if memGB > 0 {
							sizeInfo = append(sizeInfo, fmt.Sprintf("%s: %dGB", name, memGB))
						} else {
							// Less than 1GB, show in MB
							memMB := memBytes / (1024 * 1024)
							sizeInfo = append(sizeInfo, fmt.Sprintf("%s: %dMB", name, memMB))
						}
					case "cpu":
						sizeInfo = append(sizeInfo, fmt.Sprintf("%s: %d", name, qty.Value()))
					default:
						sizeInfo = append(sizeInfo, fmt.Sprintf("%s: %s", name, qty.String()))
					}
				}
				sort.Strings(sizeInfo)
				fmt.Printf("   Size:  %s\n", strings.Join(sizeInfo, ", "))
			} else if db != nil && res.Status.FailoverReservation != nil && res.Status.FailoverReservation.Allocations != nil {
				// Calculate size from the largest VM in the reservation
				var maxVCPUs, maxRAMGB int
				for vmUUID := range res.Status.FailoverReservation.Allocations {
					if entry, ok := allVMs[vmUUID]; ok {
						if entry.VCPUs > maxVCPUs {
							maxVCPUs = entry.VCPUs
						}
						ramGB := entry.RAMMb / 1024
						if ramGB > maxRAMGB {
							maxRAMGB = ramGB
						}
					}
				}
				if maxVCPUs > 0 || maxRAMGB > 0 {
					fmt.Printf("   Size:  cpu: %d, memory: %dGB (from largest VM)\n", maxVCPUs, maxRAMGB)
				}
			}

			if res.Status.FailoverReservation != nil && res.Status.FailoverReservation.Allocations != nil {
				vmList := make([]string, 0, len(res.Status.FailoverReservation.Allocations))
				for vmUUID, vmHost := range res.Status.FailoverReservation.Allocations {
					vmInfo := fmt.Sprintf("%s @ %s", vmUUID, vmHost)
					// Add flavor info if available
					if entry, ok := allVMs[vmUUID]; ok && entry.FlavorName != "" {
						ramGB := entry.RAMMb / 1024
						vmInfo += fmt.Sprintf(" [%s, %dvcpu, %dGB]", entry.FlavorName, entry.VCPUs, ramGB)
					}
					vmList = append(vmList, vmInfo)
				}
				sort.Strings(vmList)
				fmt.Printf("   VMs (%d):\n", len(vmList))
				for _, vm := range vmList {
					fmt.Printf("     - %s\n", vm)
				}
			} else {
				fmt.Println("   VMs: (none)")
			}
			fmt.Println()
		}
	}

	// Validation: Check for VMs with reservations on their own host
	if views.has(viewValidation) {
		printHeader("Validation: VMs with reservations on same host (ERRORS)")
		errorsFound := 0
		for _, res := range failoverReservations {
			resHost := res.Status.Host
			if resHost == "" {
				resHost = res.Spec.TargetHost
			}
			if res.Status.FailoverReservation != nil && res.Status.FailoverReservation.Allocations != nil {
				for vmUUID, vmHost := range res.Status.FailoverReservation.Allocations {
					if vmHost == resHost {
						fmt.Printf("  ❌ ERROR: VM %s is on host %s, but has reservation %s on SAME host %s\n",
							vmUUID, vmHost, res.Name, resHost)
						errorsFound++
					}
				}
			}
		}
		if errorsFound == 0 {
			fmt.Println("  ✅ No errors found - all VMs have reservations on different hosts")
		} else {
			fmt.Printf("\n  Total errors: %d\n", errorsFound)
		}
		fmt.Println()
	}

	// Reservations by Host
	if views.has(viewByHost) {
		printHeader("Reservations by Host")
		hostCounts := make(map[string]int)
		for _, res := range failoverReservations {
			host := res.Status.Host
			if host == "" {
				host = res.Spec.TargetHost
			}
			if host == "" {
				host = "N/A"
			}
			hostCounts[host]++
		}

		// Sort hosts by count (descending)
		type hostCount struct {
			host  string
			count int
		}
		hostCountList := make([]hostCount, 0, len(hostCounts))
		for host, count := range hostCounts {
			hostCountList = append(hostCountList, hostCount{host, count})
		}
		sort.Slice(hostCountList, func(i, j int) bool {
			return hostCountList[i].count > hostCountList[j].count
		})

		for _, hc := range hostCountList {
			fmt.Printf("  %s: %d reservation(s)\n", hc.host, hc.count)
		}
		fmt.Println()
	}

	// Summary Statistics
	if views.has(viewSummary) {
		printHeader("Summary Statistics")

		// Kubernetes context information
		hvCtx := *hypervisorContext
		if hvCtx == "" {
			hvCtx = "(current context)"
		}
		resCtx := *reservationContext
		if resCtx == "" {
			resCtx = "(current context)"
		}
		pgCtx := *postgresContext
		if pgCtx == "" {
			pgCtx = "(current context)"
		}
		fmt.Printf("Hypervisor context:   %s\n", hvCtx)
		fmt.Printf("Reservation context:  %s\n", resCtx)
		fmt.Printf("Postgres context:     %s\n", pgCtx)
		fmt.Println()

		// Database connection status
		if db != nil {
			fmt.Printf("Database: ✅ connected (servers: %d, flavors: %d)\n", len(serverMap), len(flavorMap))
		} else {
			fmt.Printf("Database: ❌ not connected\n")
		}
		fmt.Println()

		readyCount := 0
		notReadyCount := 0
		unknownCount := 0
		for _, res := range failoverReservations {
			found := false
			for _, cond := range res.Status.Conditions {
				if cond.Type == "Ready" {
					found = true
					if cond.Status == "True" {
						readyCount++
					} else {
						notReadyCount++
					}
					break
				}
			}
			if !found {
				unknownCount++
			}
		}

		fmt.Println("Reservations by Status:")
		fmt.Printf("  Ready:     %d\n", readyCount)
		fmt.Printf("  Not Ready: %d\n", notReadyCount)
		fmt.Printf("  Unknown:   %d\n", unknownCount)
		fmt.Println()

		// Count VMs per reservation
		totalVMsInReservations := 0
		for _, res := range failoverReservations {
			if res.Status.FailoverReservation != nil && res.Status.FailoverReservation.Allocations != nil {
				totalVMsInReservations += len(res.Status.FailoverReservation.Allocations)
			}
		}

		// Count VMs without reservations
		vmsWithoutRes := 0
		vmsWithoutResNotInDB := 0
		for _, entry := range allVMs {
			if len(entry.Reservations) == 0 {
				vmsWithoutRes++
				if !entry.InServerTable {
					vmsWithoutResNotInDB++
				}
			}
		}

		fmt.Printf("Total Hypervisors: %d\n", len(hypervisors.Items))
		fmt.Printf("Total VMs (from hypervisors): %d\n", len(allVMs))
		if db != nil {
			vmsInDB := 0
			for _, entry := range allVMs {
				if entry.InServerTable {
					vmsInDB++
				}
			}
			fmt.Printf("VMs in server table: %d\n", vmsInDB)
			fmt.Printf("VMs NOT in server table: %d\n", len(allVMs)-vmsInDB)
		}
		fmt.Printf("VMs with reservations: %d\n", len(vmsWithReservations))
		if vmsWithoutRes > 0 {
			if db != nil && vmsWithoutResNotInDB > 0 {
				fmt.Printf("VMs without reservations: %d (%d not in DB) ⚠️\n", vmsWithoutRes, vmsWithoutResNotInDB)
			} else {
				fmt.Printf("VMs without reservations: %d ⚠️\n", vmsWithoutRes)
			}
		} else {
			fmt.Printf("VMs without reservations: %d\n", vmsWithoutRes)
		}
		fmt.Printf("Total Reservations: %d\n", len(failoverReservations))
		fmt.Printf("Total VM allocations across all reservations: %d\n", totalVMsInReservations)
		if len(failoverReservations) > 0 {
			fmt.Printf("Average VMs per reservation: %.2f\n", float64(totalVMsInReservations)/float64(len(failoverReservations)))
		}

		// Count unique hosts with reservations
		uniqueHosts := make(map[string]bool)
		for _, res := range failoverReservations {
			host := res.Status.Host
			if host == "" {
				host = res.Spec.TargetHost
			}
			if host != "" {
				uniqueHosts[host] = true
			}
		}
		fmt.Printf("Unique hosts with reservations: %d\n", len(uniqueHosts))
		fmt.Println()

		// Resource usage summary (VMs vs Reservations)
		var totalVMCPU int64
		var totalVMRAMGB int64
		var totalResCPU int64
		var totalResRAMGB int64

		// Calculate total VM resources
		for _, entry := range allVMs {
			if !entry.NotOnHypervisors {
				totalVMCPU += int64(entry.VCPUs)
				totalVMRAMGB += int64(entry.RAMMb / 1024)
			}
		}

		// Calculate total reservation resources
		for _, res := range failoverReservations {
			if len(res.Spec.Resources) > 0 {
				if vcpus, ok := res.Spec.Resources["vcpus"]; ok {
					totalResCPU += vcpus.Value()
				}
				if mem, ok := res.Spec.Resources["memory"]; ok {
					totalResRAMGB += mem.Value() / (1024 * 1024 * 1024)
				}
			}
		}

		fmt.Println("Resource Usage (VMs vs Failover Reservations):")
		fmt.Printf("  VMs Total:          %4d vCPUs, %6d GB RAM\n", totalVMCPU, totalVMRAMGB)
		fmt.Printf("  Reservations Total: %4d vCPUs, %6d GB RAM\n", totalResCPU, totalResRAMGB)

		// Calculate and display ratios
		if totalVMCPU > 0 {
			cpuRatio := float64(totalResCPU) / float64(totalVMCPU)
			fmt.Printf("  CPU Ratio (Res/VM): %.2f (%.0f%% of VM capacity reserved for failover)\n", cpuRatio, cpuRatio*100)
		}
		if totalVMRAMGB > 0 {
			ramRatio := float64(totalResRAMGB) / float64(totalVMRAMGB)
			fmt.Printf("  RAM Ratio (Res/VM): %.2f (%.0f%% of VM capacity reserved for failover)\n", ramRatio, ramRatio*100)
		}
		fmt.Println()
	}

	// Print all servers from postgres (for debugging data sync issues)
	if db != nil && views.has(viewAllServers) {
		printAllServers(serverMap, flavorMap, allVMs, filteredHosts)
	}
}

func printHypervisorSummary(hypervisors []hv1.Hypervisor, reservations []v1alpha1.Reservation) {
	printHeader("Hypervisor Capacity Summary")

	// Build reservation resources per host
	failoverResPerHost := make(map[string]map[string]int64)  // host -> resource -> value
	committedResPerHost := make(map[string]map[string]int64) // host -> resource -> value
	reservationCountPerHost := make(map[string]int)          // host -> count

	for _, res := range reservations {
		host := res.Status.Host
		if host == "" {
			host = res.Spec.TargetHost
		}
		if host == "" {
			continue
		}

		reservationCountPerHost[host]++

		// Get resources from spec
		if len(res.Spec.Resources) == 0 {
			continue
		}

		var targetMap map[string]map[string]int64
		switch res.Spec.Type {
		case v1alpha1.ReservationTypeFailover:
			targetMap = failoverResPerHost
		case v1alpha1.ReservationTypeCommittedResource:
			targetMap = committedResPerHost
		default:
			continue
		}

		if targetMap[host] == nil {
			targetMap[host] = make(map[string]int64)
		}

		for name, qty := range res.Spec.Resources {
			targetMap[host][string(name)] += qty.Value()
		}
	}

	// Build summary for each hypervisor
	summaries := make([]hypervisorSummary, 0, len(hypervisors))

	for _, hv := range hypervisors {
		summary := hypervisorSummary{
			Name:            hv.Name,
			NumVMs:          hv.Status.NumInstances,
			NumReservations: reservationCountPerHost[hv.Name],
			Traits:          hv.Status.Traits,
		}

		// Get capacity from hypervisor status
		if cpu, ok := hv.Status.Capacity["cpu"]; ok {
			summary.CapacityCPU = cpu.Value()
		}
		if mem, ok := hv.Status.Capacity["memory"]; ok {
			// Memory is in bytes, convert to GB
			summary.CapacityMemoryGB = mem.Value() / (1024 * 1024 * 1024)
		}

		// Get allocation (used by VMs) from hypervisor status
		if cpu, ok := hv.Status.Allocation["cpu"]; ok {
			summary.UsedByVMsCPU = cpu.Value()
		}
		if mem, ok := hv.Status.Allocation["memory"]; ok {
			// Memory is in bytes, convert to GB
			summary.UsedByVMsMemoryGB = mem.Value() / (1024 * 1024 * 1024)
		}

		// Get failover reservation resources
		// Note: Reservations use "vcpus" key, not "cpu"
		if failoverRes, ok := failoverResPerHost[hv.Name]; ok {
			if cpu, ok := failoverRes["vcpus"]; ok {
				summary.FailoverResCPU = cpu
			}
			if mem, ok := failoverRes["memory"]; ok {
				// Memory from reservations is in bytes
				summary.FailoverResMemGB = mem / (1024 * 1024 * 1024)
			}
		}

		// Get committed reservation resources
		// Note: Reservations use "vcpus" key, not "cpu"
		if committedRes, ok := committedResPerHost[hv.Name]; ok {
			if cpu, ok := committedRes["vcpus"]; ok {
				summary.CommittedResCPU = cpu
			}
			if mem, ok := committedRes["memory"]; ok {
				// Memory from reservations is in bytes
				summary.CommittedResMemGB = mem / (1024 * 1024 * 1024)
			}
		}

		// Calculate free resources
		summary.FreeCPU = summary.CapacityCPU - summary.UsedByVMsCPU - summary.FailoverResCPU - summary.CommittedResCPU
		summary.FreeMemoryGB = summary.CapacityMemoryGB - summary.UsedByVMsMemoryGB - summary.FailoverResMemGB - summary.CommittedResMemGB

		summaries = append(summaries, summary)
	}

	// Sort by BB (blade block) then by node number
	// Hypervisor names follow the pattern: nodeXXX-bbYYY (e.g., node001-bb086)
	sort.Slice(summaries, func(i, j int) bool {
		bbI, nodeI := parseHypervisorName(summaries[i].Name)
		bbJ, nodeJ := parseHypervisorName(summaries[j].Name)
		if bbI != bbJ {
			return bbI < bbJ
		}
		return nodeI < nodeJ
	})

	// Print table header
	fmt.Printf("%-30s %5s %5s %15s %15s %15s %15s %15s  %s\n",
		"Hypervisor", "#VMs", "#Res", "Capacity", "Used by VMs", "Failover Res", "Committed Res", "Free", "Traits")
	fmt.Printf("%-30s %5s %5s %15s %15s %15s %15s %15s  %s\n",
		"", "", "", "(CPU / RAM)", "(CPU / RAM)", "(CPU / RAM)", "(CPU / RAM)", "(CPU / RAM)", "")
	fmt.Printf("%-30s %5s %5s %15s %15s %15s %15s %15s  %s\n",
		strings.Repeat("-", 30), strings.Repeat("-", 5), strings.Repeat("-", 5),
		strings.Repeat("-", 15), strings.Repeat("-", 15), strings.Repeat("-", 15),
		strings.Repeat("-", 15), strings.Repeat("-", 15), strings.Repeat("-", 40))

	// Print each hypervisor
	for _, s := range summaries {
		capacityStr := fmt.Sprintf("%d / %dGB", s.CapacityCPU, s.CapacityMemoryGB)
		usedByVMsStr := fmt.Sprintf("%d / %dGB", s.UsedByVMsCPU, s.UsedByVMsMemoryGB)
		failoverStr := fmt.Sprintf("%d / %dGB", s.FailoverResCPU, s.FailoverResMemGB)
		committedStr := fmt.Sprintf("%d / %dGB", s.CommittedResCPU, s.CommittedResMemGB)
		freeStr := fmt.Sprintf("%d / %dGB", s.FreeCPU, s.FreeMemoryGB)
		traitsStr := formatTraits(s.Traits)

		fmt.Printf("%-30s %5d %5d %15s %15s %15s %15s %15s  %s\n",
			truncate(s.Name, 30), s.NumVMs, s.NumReservations,
			capacityStr, usedByVMsStr, failoverStr, committedStr, freeStr, traitsStr)
	}

	// Print totals
	var totalCapCPU, totalCapMem, totalUsedCPU, totalUsedMem int64
	var totalFailoverCPU, totalFailoverMem, totalCommittedCPU, totalCommittedMem int64
	var totalFreeCPU, totalFreeMem int64
	var totalVMs, totalRes int

	for _, s := range summaries {
		totalVMs += s.NumVMs
		totalRes += s.NumReservations
		totalCapCPU += s.CapacityCPU
		totalCapMem += s.CapacityMemoryGB
		totalUsedCPU += s.UsedByVMsCPU
		totalUsedMem += s.UsedByVMsMemoryGB
		totalFailoverCPU += s.FailoverResCPU
		totalFailoverMem += s.FailoverResMemGB
		totalCommittedCPU += s.CommittedResCPU
		totalCommittedMem += s.CommittedResMemGB
		totalFreeCPU += s.FreeCPU
		totalFreeMem += s.FreeMemoryGB
	}

	fmt.Printf("%-30s %5s %5s %15s %15s %15s %15s %15s\n",
		strings.Repeat("-", 30), strings.Repeat("-", 5), strings.Repeat("-", 5),
		strings.Repeat("-", 15), strings.Repeat("-", 15), strings.Repeat("-", 15),
		strings.Repeat("-", 15), strings.Repeat("-", 15))
	fmt.Printf("%-30s %5d %5d %15s %15s %15s %15s %15s\n",
		"TOTAL", totalVMs, totalRes,
		fmt.Sprintf("%d / %dGB", totalCapCPU, totalCapMem),
		fmt.Sprintf("%d / %dGB", totalUsedCPU, totalUsedMem),
		fmt.Sprintf("%d / %dGB", totalFailoverCPU, totalFailoverMem),
		fmt.Sprintf("%d / %dGB", totalCommittedCPU, totalCommittedMem),
		fmt.Sprintf("%d / %dGB", totalFreeCPU, totalFreeMem))

	fmt.Println()
}

func connectToPostgres(ctx context.Context, k8sClient client.Client, secretName, namespace, hostOverride, portOverride, contextName string) (db *sql.DB, serverMap map[string]serverInfo, flavorMap map[string]flavorInfo) {
	ctxDisplay := contextName
	if ctxDisplay == "" {
		ctxDisplay = "(current context)"
	}
	fmt.Fprintf(os.Stderr, "Postgres: Reading secret '%s' from namespace '%s' using context '%s'\n", secretName, namespace, ctxDisplay)

	// Get the postgres secret
	secret := &corev1.Secret{}
	if err := k8sClient.Get(ctx, client.ObjectKey{
		Namespace: namespace,
		Name:      secretName,
	}, secret); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not get postgres secret '%s' in namespace '%s' (context: %s): %v\n", secretName, namespace, ctxDisplay, err)
		fmt.Fprintf(os.Stderr, "         Postgres features will be disabled.\n")
		fmt.Fprintf(os.Stderr, "         Use --postgres-secret, --namespace, and --postgres-context flags to specify the secret location.\n\n")
		return nil, nil, nil
	}

	// Extract connection details
	host := string(secret.Data["host"])
	port := string(secret.Data["port"])
	user := string(secret.Data["user"])
	password := string(secret.Data["password"])
	database := string(secret.Data["database"])

	if user == "" || password == "" || database == "" {
		fmt.Fprintf(os.Stderr, "Warning: Postgres secret is missing required fields (user, password, database)\n")
		return nil, nil, nil
	}

	if port == "" {
		port = "5432"
	}

	// Strip newlines from values
	strip := func(s string) string { return strings.ReplaceAll(strings.TrimSpace(s), "\n", "") }
	host = strip(host)
	port = strip(port)
	user = strip(user)
	password = strip(password)
	database = strip(database)

	// Apply overrides if provided
	if hostOverride != "" {
		host = hostOverride
	}
	if portOverride != "" {
		port = portOverride
	}

	if host == "" {
		fmt.Fprintf(os.Stderr, "Warning: Postgres host is empty. Use --postgres-host to specify.\n")
		return nil, nil, nil
	}

	// Connect to postgres
	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, database)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not connect to postgres: %v\n", err)
		return nil, nil, nil
	}

	// Test connection
	if err := db.PingContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not ping postgres at %s:%s: %v\n", host, port, err)
		fmt.Fprintf(os.Stderr, "         If running locally, use kubectl port-forward:\n")
		fmt.Fprintf(os.Stderr, "           kubectl port-forward svc/%s %s:%s -n %s\n", host, port, port, namespace)
		fmt.Fprintf(os.Stderr, "           ./visualize-reservations --postgres-host=localhost --postgres-port=%s\n\n", port)
		db.Close()
		return nil, nil, nil
	}

	// Query servers with host information
	serverMap = make(map[string]serverInfo)
	rows, err := db.QueryContext(ctx, "SELECT id, flavor_name, COALESCE(host_id, ''), COALESCE(os_ext_srv_attr_host, '') FROM openstack_servers")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not query openstack_servers: %v\n", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var s serverInfo
			if err := rows.Scan(&s.ID, &s.FlavorName, &s.HostID, &s.OSEXTSRVATTRHost); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Could not scan server row: %v\n", err)
				continue
			}
			serverMap[s.ID] = s
		}
		if err := rows.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Error iterating server rows: %v\n", err)
		}
	}

	// Query flavors (including extra_specs for traits)
	flavorMap = make(map[string]flavorInfo)
	rows2, err := db.QueryContext(ctx, "SELECT name, vcpus, ram, disk, COALESCE(extra_specs, '') FROM openstack_flavors_v2")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not query openstack_flavors_v2: %v\n", err)
	} else {
		defer rows2.Close()
		for rows2.Next() {
			var f flavorInfo
			if err := rows2.Scan(&f.Name, &f.VCPUs, &f.RAMMb, &f.DiskGb, &f.ExtraSpecs); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Could not scan flavor row: %v\n", err)
				continue
			}
			flavorMap[f.Name] = f
		}
		if err := rows2.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Error iterating flavor rows: %v\n", err)
		}
	}

	return db, serverMap, flavorMap
}

func printHeader(title string) {
	fmt.Println("==============================================")
	fmt.Printf("  %s\n", title)
	fmt.Println("==============================================")
	fmt.Println()
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// parseHypervisorName extracts the BB (blade block) and node parts from a hypervisor name.
// Hypervisor names follow the pattern: nodeXXX-bbYYY (e.g., node001-bb086)
// Returns (bb, node) where bb is the blade block suffix and node is the node prefix.
// If the name doesn't match the expected pattern, returns the full name as both values.
func parseHypervisorName(name string) (bb, node string) {
	// Look for the pattern: anything-bbXXX
	parts := strings.Split(name, "-")
	if len(parts) >= 2 {
		// Find the bb part (could be at any position)
		for i, part := range parts {
			if strings.HasPrefix(part, "bb") {
				bb = part
				// Node is everything before the bb part
				node = strings.Join(parts[:i], "-")
				return bb, node
			}
		}
	}
	// Fallback: return the full name for both (will sort alphabetically)
	return name, name
}

// formatTraits formats the traits slice for display.
// It filters out common/standard traits and shows only custom or interesting ones.
func formatTraits(traits []string) string {
	if len(traits) == 0 {
		return ""
	}

	// Filter out common standard traits that are not interesting
	// Keep custom traits and important hardware traits
	var interesting []string
	for _, trait := range traits {
		// Skip common standard traits
		if strings.HasPrefix(trait, "COMPUTE_") ||
			strings.HasPrefix(trait, "HW_CPU_X86_") ||
			strings.HasPrefix(trait, "MISC_SHARES_") ||
			trait == "COMPUTE_NET_ATTACH_INTERFACE" ||
			trait == "COMPUTE_VOLUME_ATTACH" ||
			trait == "COMPUTE_VOLUME_EXTEND" ||
			trait == "COMPUTE_VOLUME_MULTI_ATTACH" {
			continue
		}
		interesting = append(interesting, trait)
	}

	if len(interesting) == 0 {
		return ""
	}

	// Sort for consistent output
	sort.Strings(interesting)

	// Join with commas
	return strings.Join(interesting, ", ")
}

// printFlavorSummary prints a summary of flavors with VM counts and traits
func printFlavorSummary(allVMs map[string]*vmEntry, flavorMap map[string]flavorInfo) {
	printHeader("Flavor Summary")

	// Count VMs per flavor
	flavorVMCount := make(map[string]int)
	for _, vm := range allVMs {
		if vm.FlavorName != "" {
			flavorVMCount[vm.FlavorName]++
		}
	}

	// Build summary list
	type flavorSummary struct {
		Name       string
		VMCount    int
		VCPUs      int
		RAMGb      int
		DiskGb     int
		Traits     string
		ExtraSpecs string
	}

	summaries := make([]flavorSummary, 0)
	for flavorName, count := range flavorVMCount {
		summary := flavorSummary{
			Name:    flavorName,
			VMCount: count,
		}
		if flavor, ok := flavorMap[flavorName]; ok {
			summary.VCPUs = flavor.VCPUs
			summary.RAMGb = flavor.RAMMb / 1024
			summary.DiskGb = flavor.DiskGb
			summary.ExtraSpecs = flavor.ExtraSpecs
			summary.Traits = extractTraitsFromExtraSpecs(flavor.ExtraSpecs)
		}
		summaries = append(summaries, summary)
	}

	// Sort by VM count (descending), then by name
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].VMCount != summaries[j].VMCount {
			return summaries[i].VMCount > summaries[j].VMCount
		}
		return summaries[i].Name < summaries[j].Name
	})

	// Print table
	fmt.Printf("%-40s %6s %15s  %s\n", "Flavor", "#VMs", "Size", "Traits")
	fmt.Printf("%-40s %6s %15s  %s\n", "", "", "(CPU/RAM/Disk)", "")
	fmt.Printf("%-40s %6s %15s  %s\n",
		strings.Repeat("-", 40), strings.Repeat("-", 6), strings.Repeat("-", 15), strings.Repeat("-", 50))

	for _, s := range summaries {
		sizeStr := fmt.Sprintf("%d / %dGB / %dGB", s.VCPUs, s.RAMGb, s.DiskGb)
		fmt.Printf("%-40s %6d %15s  %s\n",
			truncate(s.Name, 40), s.VMCount, sizeStr, s.Traits)
	}

	// Print total
	totalVMs := 0
	for _, s := range summaries {
		totalVMs += s.VMCount
	}
	fmt.Printf("%-40s %6s %15s\n",
		strings.Repeat("-", 40), strings.Repeat("-", 6), strings.Repeat("-", 15))
	fmt.Printf("%-40s %6d\n", "TOTAL", totalVMs)
	fmt.Println()
}

// extractTraitsFromExtraSpecs extracts trait requirements from flavor extra_specs JSON
// Shows both required and forbidden traits with indicators
func extractTraitsFromExtraSpecs(extraSpecsJSON string) string {
	if extraSpecsJSON == "" {
		return ""
	}

	// Parse JSON
	var extraSpecs map[string]string
	if err := json.Unmarshal([]byte(extraSpecsJSON), &extraSpecs); err != nil {
		return ""
	}

	// Extract traits (keys starting with "trait:")
	var requiredTraits []string
	var forbiddenTraits []string
	for key, value := range extraSpecs {
		if strings.HasPrefix(key, "trait:") {
			trait := strings.TrimPrefix(key, "trait:")
			// Skip common traits
			if strings.HasPrefix(trait, "COMPUTE_") ||
				strings.HasPrefix(trait, "HW_CPU_X86_") {
				continue
			}
			switch value {
			case "required":
				requiredTraits = append(requiredTraits, trait)
			case "forbidden":
				forbiddenTraits = append(forbiddenTraits, trait)
			}
		}
	}

	if len(requiredTraits) == 0 && len(forbiddenTraits) == 0 {
		return ""
	}

	// Sort for consistent output
	sort.Strings(requiredTraits)
	sort.Strings(forbiddenTraits)

	// Build output with indicators
	var parts []string
	for _, t := range requiredTraits {
		parts = append(parts, "+"+t) // + for required
	}
	for _, t := range forbiddenTraits {
		parts = append(parts, "-"+t) // - for forbidden
	}

	return strings.Join(parts, ", ")
}

// matchesFilter checks if a hypervisor matches the given name and trait filters.
// If both filters are empty, all hypervisors match.
// Name filter uses substring matching (case-insensitive).
// Trait filter checks if the hypervisor has the specified trait.
func matchesFilter(hv hv1.Hypervisor, nameFilter, traitFilter string) bool {
	// If no filters, match all
	if nameFilter == "" && traitFilter == "" {
		return true
	}

	// Check name filter (substring match, case-insensitive)
	if nameFilter != "" {
		if !strings.Contains(strings.ToLower(hv.Name), strings.ToLower(nameFilter)) {
			return false
		}
	}

	// Check trait filter
	if traitFilter != "" {
		hasTrait := false
		for _, trait := range hv.Status.Traits {
			if strings.EqualFold(trait, traitFilter) {
				hasTrait = true
				break
			}
		}
		if !hasTrait {
			return false
		}
	}

	return true
}

// printAllServers prints servers from postgres that are on hypervisors
// This is useful for debugging data sync issues between nova and hypervisor operator
func printAllServers(serverMap map[string]serverInfo, _ map[string]flavorInfo, allVMs map[string]*vmEntry, _ map[string]bool) {
	printHeader("Servers on Hypervisors (data sync debugging)")

	if len(allVMs) == 0 {
		fmt.Println("  No VMs found on hypervisors")
		fmt.Println()
		return
	}

	// Build list of VMs on hypervisors with their postgres info
	type vmWithPGInfo struct {
		UUID       string
		ActualHost string // From hypervisor CRD
		PGHost     string // From postgres (OSEXTSRVATTRHost)
		FlavorName string
		InPostgres bool
		HostMatch  bool
		Status     string
	}

	vms := make([]vmWithPGInfo, 0, len(allVMs))
	for uuid, vm := range allVMs {
		if vm.NotOnHypervisors {
			continue // Skip VMs that are only in reservations but not on hypervisors
		}

		info := vmWithPGInfo{
			UUID:       uuid,
			ActualHost: vm.Host,
			FlavorName: vm.FlavorName,
		}

		// Check if VM is in postgres
		server, inPostgres := serverMap[uuid]
		switch {
		case !inPostgres:
			info.Status = "NOT_IN_PG"
		default:
			info.InPostgres = true
			info.PGHost = server.OSEXTSRVATTRHost
			if info.FlavorName == "" {
				info.FlavorName = server.FlavorName
			}

			// Check host match
			switch server.OSEXTSRVATTRHost {
			case "":
				info.Status = "PG_NO_HOST"
			case vm.Host:
				info.HostMatch = true
				info.Status = "OK"
			default:
				info.Status = "WRONG_HOST"
			}
		}

		vms = append(vms, info)
	}

	// Sort by status (errors first), then by host, then by ID
	statusOrder := map[string]int{
		"WRONG_HOST": 0,
		"NOT_IN_PG":  1,
		"PG_NO_HOST": 2,
		"OK":         3,
	}
	sort.Slice(vms, func(i, j int) bool {
		if statusOrder[vms[i].Status] != statusOrder[vms[j].Status] {
			return statusOrder[vms[i].Status] < statusOrder[vms[j].Status]
		}
		if vms[i].ActualHost != vms[j].ActualHost {
			return vms[i].ActualHost < vms[j].ActualHost
		}
		return vms[i].UUID < vms[j].UUID
	})

	// Count statistics
	var wrongHost, notInPG, pgNoHost, ok int
	for _, v := range vms {
		switch v.Status {
		case "WRONG_HOST":
			wrongHost++
		case "NOT_IN_PG":
			notInPG++
		case "PG_NO_HOST":
			pgNoHost++
		case "OK":
			ok++
		}
	}

	fmt.Printf("  Total VMs on hypervisors: %d\n", len(vms))
	fmt.Printf("  - OK (host matches): %d\n", ok)
	fmt.Printf("  - WRONG_HOST (postgres != hypervisor): %d ⚠️\n", wrongHost)
	fmt.Printf("  - NOT_IN_PG (not in postgres): %d\n", notInPG)
	fmt.Printf("  - PG_NO_HOST (postgres has empty host): %d\n", pgNoHost)
	fmt.Println()

	// Print table header
	fmt.Printf("  %-40s %-25s %-25s %-12s %-20s\n",
		"Server ID", "Actual Host (HV)", "OSEXTSRVATTRHost (PG)", "Status", "Flavor")
	fmt.Printf("  %-40s %-25s %-25s %-12s %-20s\n",
		strings.Repeat("-", 40), strings.Repeat("-", 25), strings.Repeat("-", 25),
		strings.Repeat("-", 12), strings.Repeat("-", 20))

	for _, v := range vms {
		statusIcon := ""
		switch v.Status {
		case "OK":
			statusIcon = "✅ OK"
		case "WRONG_HOST":
			statusIcon = "❌ WRONG"
		case "NOT_IN_PG":
			statusIcon = "❓ NO_PG"
		case "PG_NO_HOST":
			statusIcon = "❓ NO_HOST"
		}

		fmt.Printf("  %-40s %-25s %-25s %-12s %-20s\n",
			truncate(v.UUID, 40),
			truncate(v.ActualHost, 25),
			truncate(v.PGHost, 25),
			statusIcon,
			truncate(v.FlavorName, 20))
	}
	fmt.Println()
}

// Ensure resource.Quantity is used (for compile check)
var _ = resource.Quantity{}
