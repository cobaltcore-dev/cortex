// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"sort"

	"fmt"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack"
	"github.com/sapcc/go-bits/must"
)

// Simulate filling the datacenter with vms.
// The vm specs will be randomly picked based on existing vms in the datacenter.
func main() {
	db := db.NewPostgresDB(conf.DBConfig{
		Host:     "localhost",
		Port:     "5432",
		User:     "postgres",
		Password: "secret",
		Database: "postgres",
	})
	defer db.Close()

	// Get openstack objects from the database. We will use this data to simulate
	// scheduling requests for new servers and keep track of the datacenter state.
	var originalServers []openstack.Server
	must.Return(db.Select(&originalServers, `SELECT * FROM openstack_servers`))
	servers := make(map[string]openstack.Server)
	for _, server := range originalServers {
		servers[server.ID] = server
	}
	var originalFlavors []openstack.Flavor
	must.Return(db.Select(&originalFlavors, `SELECT * FROM openstack_flavors`))
	flavors := make(map[string]openstack.Flavor)
	for _, flavor := range originalFlavors {
		flavors[flavor.ID] = flavor
	}
	var originalHypervisors []openstack.Hypervisor
	// We can't schedule on bare-metal hypervisors.
	must.Return(db.Select(&originalHypervisors, `
		SELECT * FROM openstack_hypervisors WHERE hypervisor_type != 'ironic'
	`))
	hypervisors := map[string]*openstack.Hypervisor{}
	for _, hypervisor := range originalHypervisors {
		hypervisors[hypervisor.ServiceHost] = &hypervisor
	}
	if len(servers) == 0 || len(flavors) == 0 || len(hypervisors) == 0 {
		fmt.Println("error: this script requires openstack servers, flavors, and hypervisors to be synced")
		return
	}

	for {
		// Choose a random server spec from the currently available servers.
		// In this way we can simulate a scheduling request for a new server.
		// The request should be somewhat representative of the existing landscape.
		server := originalServers[rand.Intn(len(originalServers))]
		flavor := flavors[server.FlavorID]
		// Choose all hosts that have enough resources to host the new server.
		var hosts []scheduler.APINovaExternalSchedulerRequestHost
		weights := make(map[string]float64)
		for _, hypervisor := range hypervisors {
			if hypervisor.MemoryMB-hypervisor.MemoryMBUsed < flavor.RAM {
				continue
			}
			if hypervisor.LocalGB-hypervisor.LocalGBUsed < flavor.Disk {
				continue
			}
			if hypervisor.VCPUs-hypervisor.VCPUsUsed < flavor.VCPUs {
				continue
			}
			hosts = append(hosts, scheduler.APINovaExternalSchedulerRequestHost{
				ComputeHost:        hypervisor.ServiceHost,
				HypervisorHostname: hypervisor.Hostname,
			})
			weights[hypervisor.ServiceHost] = 1.0
		}

		request := scheduler.APINovaExternalSchedulerRequest{
			Spec: scheduler.NovaObject[scheduler.NovaSpec]{
				Data: scheduler.NovaSpec{
					ProjectID:        server.TenantID,
					UserID:           server.UserID,
					AvailabilityZone: server.OSEXTAvailabilityZone,
					NInstances:       1,
					Flavor: scheduler.NovaObject[scheduler.NovaFlavor]{
						Data: scheduler.NovaFlavor{
							FlavorID: flavor.ID,
						},
					},
				},
			},
			Rebuild: false,
			VMware:  rand.Intn(2) == 0,
			Hosts:   hosts,
			Weights: weights,
		}

		// Send the request to the scheduler.
		url := "http://localhost:8080/scheduler/nova/external"
		requestBody := must.Return(json.Marshal(request))
		ctx := context.Background()
		req := must.Return(http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(requestBody)))
		req.Header.Set("Content-Type", "application/json")
		respRaw := must.Return(http.DefaultClient.Do(req))
		defer respRaw.Body.Close()
		if respRaw.StatusCode != http.StatusOK {
			return
		}
		var resp scheduler.APINovaExternalSchedulerResponse
		must.Succeed(json.NewDecoder(respRaw.Body).Decode(&resp))

		// Update the datacenter state based on the response.
		if len(resp.Hosts) == 0 {
			continue
		}
		host := resp.Hosts[0]
		hypervisors[host].FreeRAMMB -= flavor.RAM
		hypervisors[host].MemoryMBUsed += flavor.RAM
		hypervisors[host].VCPUsUsed += flavor.VCPUs
		hypervisors[host].FreeDiskGB -= flavor.Disk
		hypervisors[host].LocalGBUsed += flavor.Disk
		// Copy the original server but set a new ID and service host.
		newServer := server
		newServer.ID = fmt.Sprint(rand.Intn(1000000))
		newServer.OSEXTSRVATTRHost = host
		servers[newServer.ID] = newServer

		var hostSorted []string
		for _, hypervisor := range hypervisors {
			hostSorted = append(hostSorted, hypervisor.ServiceHost)
		}
		sort.Strings(hostSorted)
		fmt.Println("------------------------------------------------")
		for _, h := range hostSorted {
			// 1 symbol = 1TB
			symbolsFree := hypervisors[h].FreeRAMMB / 100_000
			symbolsUsed := hypervisors[h].MemoryMBUsed / 100_000
			memUsage := ""
			// Colorful if the host is the one that received the new server.
			if h == host {
				memUsage = "\033[1;32m"
			}
			memUsage += "["
			for range symbolsUsed {
				memUsage += "█"
			}
			for range symbolsFree {
				memUsage += " "
			}
			memUsage += "]"
			memUsage += fmt.Sprintf(" %d/%d GB", hypervisors[h].MemoryMBUsed/1_000, hypervisors[h].MemoryMB/1_000)
			if h == host {
				memUsage += "\033[0m"
			}
			fmt.Printf("Host %s: %s\n", h, memUsage)
		}
		fmt.Println("------------------------------------------------")
	}
}
