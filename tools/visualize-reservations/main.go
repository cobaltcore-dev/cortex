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
//
// To connect to postgres when running locally, use kubectl port-forward:
//
//	kubectl port-forward svc/cortex-nova-postgresql 5432:5432 -n <namespace>
//	go run tools/visualize-reservations/main.go --postgres-host=localhost --postgres-port=5432
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	_ "github.com/lib/pq"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
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
	ID         string
	FlavorName string
}

// flavorInfo holds flavor data from postgres
type flavorInfo struct {
	Name   string
	VCPUs  int
	RAMMb  int
	DiskGb int
}

func main() {
	// Parse command line flags
	sortBy := flag.String("sort", "vm", "Sort VMs by: vm (UUID), vm-host (VM's host), res-host (reservation host)")
	postgresSecret := flag.String("postgres-secret", "cortex-nova-postgres", "Name of the kubernetes secret containing postgres credentials")
	namespace := flag.String("namespace", "", "Namespace of the postgres secret (defaults to 'default')")
	postgresHostOverride := flag.String("postgres-host", "", "Override postgres host (useful with port-forward, e.g., localhost)")
	postgresPortOverride := flag.String("postgres-port", "", "Override postgres port (useful with port-forward, e.g., 5432)")
	flag.Parse()

	ctx := context.Background()

	// Create kubernetes client
	cfg, err := config.GetConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting kubeconfig: %v\n", err)
		os.Exit(1)
	}

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating client: %v\n", err)
		os.Exit(1)
	}

	// Determine namespace
	ns := *namespace
	if ns == "" {
		ns = "default" // Default fallback
	}

	// Try to connect to postgres
	var db *sql.DB
	var serverMap map[string]serverInfo
	var flavorMap map[string]flavorInfo

	db, serverMap, flavorMap = connectToPostgres(ctx, k8sClient, *postgresSecret, ns, *postgresHostOverride, *postgresPortOverride)
	if db != nil {
		defer db.Close()
	}

	// Get all hypervisors to find all VMs
	var hypervisors hv1.HypervisorList
	if err := k8sClient.List(ctx, &hypervisors); err != nil {
		fmt.Fprintf(os.Stderr, "Error listing hypervisors: %v\n", err)
		return
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

	// Get all failover reservations
	var reservations v1alpha1.ReservationList
	if err := k8sClient.List(ctx, &reservations, client.MatchingLabels{
		"cortex.sap.com/type": "failover",
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Error listing reservations: %v\n", err)
		return
	}

	printHeader("Failover Reservations Visualization")
	fmt.Printf("Total Hypervisors: %d\n", len(hypervisors.Items))
	fmt.Printf("Total VMs (from hypervisors): %d\n", len(allVMs))
	fmt.Printf("Total Failover Reservations: %d\n", len(reservations.Items))
	fmt.Printf("Sort by: %s\n", *sortBy)
	if db != nil {
		fmt.Printf("Postgres: connected (servers: %d, flavors: %d)\n", len(serverMap), len(flavorMap))
	} else {
		fmt.Printf("Postgres: not connected\n")
	}
	fmt.Println()

	// Print Hypervisors and their VMs
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

	// Build VM -> Reservations mapping from reservations
	vmsWithReservations := make(map[string]bool)
	vmsInReservationsNotOnHypervisors := make([]*vmEntry, 0) // Track VMs in reservations but not on hypervisors

	for _, res := range reservations.Items {
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

	// Print VMs not in server table (only if postgres is connected)
	if db != nil {
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

	// Print VMs in reservations but NOT on any hypervisor (stale/deleted VMs)
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

	// Print Reservations and their VMs (multiline format)
	if len(reservations.Items) > 0 {
		printHeader("Reservations and their VMs")

		// Sort reservations by name
		sort.Slice(reservations.Items, func(i, j int) bool {
			return reservations.Items[i].Name < reservations.Items[j].Name
		})

		for _, res := range reservations.Items {
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

	// Summary Statistics
	printHeader("Summary Statistics")

	readyCount := 0
	notReadyCount := 0
	unknownCount := 0
	for _, res := range reservations.Items {
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
	for _, res := range reservations.Items {
		if res.Status.FailoverReservation != nil && res.Status.FailoverReservation.Allocations != nil {
			totalVMsInReservations += len(res.Status.FailoverReservation.Allocations)
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
	if len(vmsWithoutRes) > 0 {
		if db != nil && vmsWithoutResNotInDB > 0 {
			fmt.Printf("VMs without reservations: %d (%d not in DB) ⚠️\n", len(vmsWithoutRes), vmsWithoutResNotInDB)
		} else {
			fmt.Printf("VMs without reservations: %d ⚠️\n", len(vmsWithoutRes))
		}
	} else {
		fmt.Printf("VMs without reservations: %d\n", len(vmsWithoutRes))
	}
	fmt.Printf("Total Reservations: %d\n", len(reservations.Items))
	fmt.Printf("Total VM allocations across all reservations: %d\n", totalVMsInReservations)
	if len(reservations.Items) > 0 {
		fmt.Printf("Average VMs per reservation: %.2f\n", float64(totalVMsInReservations)/float64(len(reservations.Items)))
	}

	// Count unique hosts with reservations
	uniqueHosts := make(map[string]bool)
	for _, res := range reservations.Items {
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

	// Validation: Check for VMs with reservations on their own host
	printHeader("Validation: VMs with reservations on same host (ERRORS)")
	errorsFound := 0
	for _, res := range reservations.Items {
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

	// Reservations by Host
	printHeader("Reservations by Host")
	hostCounts := make(map[string]int)
	for _, res := range reservations.Items {
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

func connectToPostgres(ctx context.Context, k8sClient client.Client, secretName, namespace, hostOverride, portOverride string) (db *sql.DB, serverMap map[string]serverInfo, flavorMap map[string]flavorInfo) {
	// Get the postgres secret
	secret := &corev1.Secret{}
	if err := k8sClient.Get(ctx, client.ObjectKey{
		Namespace: namespace,
		Name:      secretName,
	}, secret); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not get postgres secret '%s' in namespace '%s': %v\n", secretName, namespace, err)
		fmt.Fprintf(os.Stderr, "         Postgres features will be disabled.\n")
		fmt.Fprintf(os.Stderr, "         Use --postgres-secret and --namespace flags to specify the secret.\n\n")
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

	// Query servers
	serverMap = make(map[string]serverInfo)
	rows, err := db.QueryContext(ctx, "SELECT id, flavor_name FROM openstack_servers")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not query openstack_servers: %v\n", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var s serverInfo
			if err := rows.Scan(&s.ID, &s.FlavorName); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Could not scan server row: %v\n", err)
				continue
			}
			serverMap[s.ID] = s
		}
		if err := rows.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Error iterating server rows: %v\n", err)
		}
	}

	// Query flavors
	flavorMap = make(map[string]flavorInfo)
	rows2, err := db.QueryContext(ctx, "SELECT name, vcpus, ram, disk FROM openstack_flavors_v2")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not query openstack_flavors_v2: %v\n", err)
	} else {
		defer rows2.Close()
		for rows2.Next() {
			var f flavorInfo
			if err := rows2.Scan(&f.Name, &f.VCPUs, &f.RAMMb, &f.DiskGb); err != nil {
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
