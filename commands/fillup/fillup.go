// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"math"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"fmt"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/api"
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
		//nolint:gosec
		server := originalServers[rand.Intn(len(originalServers))]
		flavor := flavors[server.FlavorID]
		if flavor.Name == "" {
			continue
		}
		// Choose all hosts that have enough resources to host the new server.
		var hosts []api.Host
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
			hosts = append(hosts, api.Host{
				ComputeHost:        hypervisor.ServiceHost,
				HypervisorHostname: hypervisor.Hostname,
			})
			weights[hypervisor.ServiceHost] = 1.0
		}

		request := api.Request{
			Spec: api.NovaObject[api.NovaSpec]{
				Data: api.NovaSpec{
					ProjectID:        server.TenantID,
					UserID:           server.UserID,
					AvailabilityZone: server.OSEXTAvailabilityZone,
					NInstances:       1,
					Flavor: api.NovaObject[api.NovaFlavor]{
						Data: api.NovaFlavor{
							Name:       flavor.Name,
							FlavorID:   flavor.ID,
							MemoryMB:   flavor.RAM,
							VCPUs:      flavor.VCPUs,
							RootDiskGB: flavor.Disk,
						},
					},
				},
			},
			// Also send a mock context to see the request in the logs.
			Context: api.NovaRequestContext{
				GlobalRequestID: "1234567890",
				UserID:          server.UserID,
				ProjectID:       server.TenantID,
			},
			Rebuild: false,
			//nolint:gosec
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
		respRaw, err := http.DefaultClient.Do(req)
		must.Succeed(err)
		defer respRaw.Body.Close()
		if respRaw.StatusCode != http.StatusOK {
			return
		}
		var resp api.Response
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
		//nolint:gosec
		newServer.ID = strconv.Itoa(rand.Intn(1000000))
		newServer.OSEXTSRVATTRHost = host
		servers[newServer.ID] = newServer

		var hostSorted []string
		for _, hypervisor := range hypervisors {
			hostSorted = append(hostSorted, hypervisor.ServiceHost)
		}
		sort.Strings(hostSorted)

		// How wide the data columns should be.
		columnWidth := 20

		fmt.Print("\033[2J\033[H")
		const full = "█"
		eighths := []string{"▏", "▎", "▍", "▌", "▋", "▊", "▉", "█"}
		lines := make([]string, len(hostSorted))
		linesTextOnly := make([]string, len(hostSorted))

		// Fillup whitespace to align the memory usage bars.
		addpadding := func(lines, linesTextOnly []string) {
			maxLineLength := 0
			for _, line := range linesTextOnly {
				if utf8.RuneCountInString(line) > maxLineLength {
					maxLineLength = utf8.RuneCountInString(line)
				}
			}
			for i, line := range linesTextOnly {
				for range maxLineLength - utf8.RuneCountInString(line) {
					lines[i] += " "
					linesTextOnly[i] += " "
				}
				lines[i] += " "
				linesTextOnly[i] += " "
			}
		}

		for i, h := range hostSorted {
			// Colorful if the host is the one that received the new server.
			if h == host {
				lines[i] += "\033[1;32m"
			}
			lines[i] += "\033[K" + h + ":"
			linesTextOnly[i] += h + ":"
		}
		addpadding(lines, linesTextOnly)
		maxMemoryMB := 0
		for _, h := range hostSorted {
			if hypervisors[h].MemoryMB > maxMemoryMB {
				maxMemoryMB = hypervisors[h].MemoryMB
			}
		}
		for i, h := range hostSorted {
			if hypervisors[h].MemoryMB == 0 {
				continue
			}
			capacityExceeded := hypervisors[h].MemoryMBUsed >= hypervisors[h].MemoryMB
			mbPerSymbol := float64(maxMemoryMB) / float64(columnWidth)
			symbolsFree := math.Max(0, float64(hypervisors[h].MemoryMB-hypervisors[h].MemoryMBUsed)/mbPerSymbol)
			symbolsUsed := float64(hypervisors[h].MemoryMBUsed) / mbPerSymbol
			if capacityExceeded {
				lines[i] += "\033[1;31m"
			}
			// Colorful if the host is the one that received the new server.
			if h == host {
				lines[i] += "\033[1;32m"
			}
			for range int(math.Floor(symbolsUsed)) {
				lines[i] += full
				linesTextOnly[i] += full
			}
			interp := eighths[int(math.Floor(8*(symbolsUsed-math.Floor(symbolsUsed))))]
			lines[i] += "\033[40m" + interp + "\033[0m"
			linesTextOnly[i] += interp
			for range int(math.Floor(symbolsFree)) {
				lines[i] += "\033[40m \033[0m"
				linesTextOnly[i] += " "
			}
			// Reset the color.
			if h == host || capacityExceeded {
				lines[i] += "\033[0m"
			}
		}
		addpadding(lines, linesTextOnly)
		for i, h := range hostSorted {
			info := fmt.Sprintf(" %d/%d GB", hypervisors[h].MemoryMBUsed/1_000, hypervisors[h].MemoryMB/1_000)
			lines[i] += info
			linesTextOnly[i] += info
		}
		addpadding(lines, linesTextOnly)
		maxVCPUs := 0
		for _, h := range hostSorted {
			if hypervisors[h].VCPUs > maxVCPUs {
				maxVCPUs = hypervisors[h].VCPUs
			}
		}
		for i, h := range hostSorted {
			if hypervisors[h].VCPUs == 0 {
				continue
			}
			capacityExceeded := hypervisors[h].VCPUsUsed >= hypervisors[h].VCPUs
			vcpuPerSymbol := float64(maxVCPUs) / float64(columnWidth)
			symbolsFree := math.Max(0, float64(hypervisors[h].VCPUs-hypervisors[h].VCPUsUsed)/vcpuPerSymbol)
			symbolsUsed := float64(hypervisors[h].VCPUsUsed) / vcpuPerSymbol
			if capacityExceeded {
				lines[i] += "\033[1;31m"
			}
			// Colorful if the host is the one that received the new server.
			if h == host {
				lines[i] += "\033[1;32m"
			}
			for range int(math.Floor(symbolsUsed)) {
				lines[i] += full
				linesTextOnly[i] += full
			}
			interp := eighths[int(math.Floor(8*(symbolsUsed-math.Floor(symbolsUsed))))]
			lines[i] += "\033[40m" + interp + "\033[0m"
			linesTextOnly[i] += interp
			for range int(math.Floor(symbolsFree)) {
				lines[i] += " "
				linesTextOnly[i] += " "
			}
			// Reset the color.
			if h == host || capacityExceeded {
				lines[i] += "\033[0m"
			}
		}
		addpadding(lines, linesTextOnly)
		for i, h := range hostSorted {
			info := fmt.Sprintf(" %d/%d VCPUs", hypervisors[h].VCPUsUsed, hypervisors[h].VCPUs)
			lines[i] += info
			linesTextOnly[i] += info
		}
		addpadding(lines, linesTextOnly)
		maxDiskGB := 0
		for _, h := range hostSorted {
			if hypervisors[h].LocalGB > maxDiskGB {
				maxDiskGB = hypervisors[h].LocalGB
			}
		}
		for i, h := range hostSorted {
			if hypervisors[h].LocalGB == 0 {
				continue
			}
			capacityExceeded := hypervisors[h].LocalGBUsed >= hypervisors[h].LocalGB
			diskPerSymbol := float64(maxDiskGB) / float64(columnWidth)
			symbolsFree := math.Max(0, float64(hypervisors[h].LocalGB-hypervisors[h].LocalGBUsed)/diskPerSymbol)
			symbolsUsed := float64(hypervisors[h].LocalGBUsed) / diskPerSymbol
			if capacityExceeded {
				lines[i] += "\033[1;31m"
			}
			// Colorful if the host is the one that received the new server.
			if h == host {
				lines[i] += "\033[1;32m"
			}
			for range int(math.Floor(symbolsUsed)) {
				lines[i] += full
				linesTextOnly[i] += full
			}
			interp := eighths[int(math.Floor(8*(symbolsUsed-math.Floor(symbolsUsed))))]
			lines[i] += "\033[40m" + interp + "\033[0m"
			linesTextOnly[i] += interp
			for range int(math.Floor(symbolsFree)) {
				lines[i] += "\033[40m \033[0m"
				linesTextOnly[i] += " "
			}
			// Reset the color.
			if h == host || capacityExceeded {
				lines[i] += "\033[0m"
			}
		}
		addpadding(lines, linesTextOnly)
		for i, h := range hostSorted {
			info := fmt.Sprintf(" %d/%d GB", hypervisors[h].LocalGBUsed, hypervisors[h].LocalGB)
			lines[i] += info
			linesTextOnly[i] += info
		}
		for _, line := range lines {
			fmt.Println(line)
		}
		fmt.Printf("Spawned flavor %s on host %s, continue? [y, N, default: y]", flavor.Name, host)
		reader := bufio.NewReader(os.Stdin)
		input := must.Return(reader.ReadString('\n'))
		input = strings.TrimSpace(input)
		if input == "" {
			input = "y"
		}
		if input != "y" {
			break
		}
	}
}
