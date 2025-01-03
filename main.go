package main

import (
	"log"

	"github.com/cobaltcore-dev/cortex/internal/openstack"
)

func main() {
	auth, err := openstack.GetKeystoneAuth()
	if err != nil {
		log.Fatalf("Failed to authenticate: %v", err)
	}

	hypervisorlist, err := openstack.GetHypervisors(auth)
	if err != nil {
		log.Fatalf("Failed to get hypervisor data: %v", err)
	}
	hypervisorsByHostname := make(map[string]openstack.OpenStackHypervisor)
	for _, hypervisor := range hypervisorlist.Hypervisors {
		hypervisorsByHostname[hypervisor.Service.Host] = hypervisor
	}

	serverlist, err := openstack.GetServers(auth)
	if err != nil {
		log.Fatalf("Failed to get nova data: %v", err)
	}
	serversByHostname := make(map[string][]openstack.OpenStackServer)
	for _, server := range serverlist.Servers {
		serversByHostname[server.OSEXTSRVATTRHost] = append(serversByHostname[server.OSEXTSRVATTRHost], server)
	}

	// Print a nice tree with statistics
	for hostname := range hypervisorsByHostname {
		hypervisor := hypervisorsByHostname[hostname]
		log.Printf(
			"Hypervisor: %s [%s, %d/%d vCPUs, %d/%d MB RAM, %d/%d GB disk]",
			hostname,
			hypervisor.State,
			hypervisor.VCPUsUsed,
			hypervisor.VCPUs,
			hypervisor.MemoryMBUsed,
			hypervisor.MemoryMB,
			hypervisor.LocalGBUsed,
			hypervisor.LocalGB,
		)
		for _, server := range serversByHostname[hostname] {
			log.Printf("  Server: %s [%s]", server.Name, server.Status)
		}
	}
}
