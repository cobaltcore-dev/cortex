// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"fmt"
	"html/template"
	"math/rand"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/tools/spawner/cli"
	"github.com/cobaltcore-dev/cortex/tools/spawner/defaults"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/blockstorage/v3/volumes"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/aggregates"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/hypervisors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/keypairs"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/domains"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/subnets"
	"github.com/sapcc/go-bits/gophercloudext"
	"github.com/sapcc/go-bits/must"
)

func main() {
	ctx := context.Background()
	def := defaults.NewDefaults("tools/spawner/defaults.json")
	cli := cli.NewCLI(def)

	// Get the number of vms to spawn from the user.
	fmt.Printf("❓ Number of VMs to spawn [default: \033[1;34m1\033[0m]: ")
	reader := bufio.NewReader(os.Stdin)
	input := must.Return(reader.ReadString('\n'))
	input = strings.TrimSpace(input)
	if input == "" {
		input = "1"
	}
	vmsToSpawn := must.Return(strconv.Atoi(input))

	// Prefix for the vms and network.
	prefix := os.Getenv("OS_PREFIX")
	if prefix == "" {
		prefix = "cortex-workload-spawner"
	}

	// Some endpoint opts.
	region := os.Getenv("OS_REGION_NAME")
	computeEO := gophercloud.EndpointOpts{Region: region, Type: "compute"}
	imageEO := gophercloud.EndpointOpts{Region: region, Type: "image"}
	networkEO := gophercloud.EndpointOpts{Region: region, Type: "network"}
	keystoneEO := gophercloud.EndpointOpts{Region: region, Type: "identity"}
	blockstorageEO := gophercloud.EndpointOpts{Region: region, Type: "volumev3"}

	// Authenticate with the admin project.
	fmt.Printf("🔄 Resolving openstack endpoints and logging into admin project ...")

	// Use OS_PASSWORD or OS_PWD_CMD to get the password.
	password := os.Getenv("OS_PASSWORD")

	if password == "" {
		// Use OS_PW_CMD to get the password.
		pwdCmd := os.Getenv("OS_PW_CMD")
		if pwdCmd == "" {
			panic("No password set in OS_PASSWORD or OS_PW_CMD env.")
		}

		cmd := exec.Command("sh", "-c", pwdCmd)
		cmd.Stdin = os.Stdin
		output := must.Return(cmd.Output())
		password = strings.TrimSpace(string(output))
	}

	adminAuth := gophercloud.AuthOptions{
		IdentityEndpoint: os.Getenv("OS_AUTH_URL"),
		Username:         os.Getenv("OS_USERNAME"),
		DomainName:       os.Getenv("OS_USER_DOMAIN_NAME"),
		Password:         password,
		AllowReauth:      true,
		Scope: &gophercloud.AuthScope{
			ProjectName: os.Getenv("OS_PROJECT_NAME"),
			DomainName:  os.Getenv("OS_PROJECT_DOMAIN_NAME"),
		},
	}
	adminProvider := must.Return(openstack.NewClient(adminAuth.IdentityEndpoint))
	must.Succeed(openstack.Authenticate(ctx, adminProvider, adminAuth))
	adminKeystone := must.Return(openstack.NewIdentityV3(adminProvider, keystoneEO))
	adminNova := must.Return(openstack.NewComputeV2(adminProvider, computeEO))
	adminNova.Microversion = "2.88" // Needed to correctly fetch hypervisors.
	adminGlance := must.Return(openstack.NewImageV2(adminProvider, imageEO))
	fmt.Printf(" ✅ Done!\n")

	// Get all domains and let the user choose one.
	fmt.Println("🔄 Looking up projects")
	domainPages := must.Return(domains.List(adminKeystone, domains.ListOpts{}).AllPages(ctx))
	domainsAll := must.Return(domains.ExtractDomains(domainPages))
	domain := cli.ChooseDomain(domainsAll)

	// Get all projects and let the user choose one.
	fmt.Printf("🔄 Looking up projects in domain %s\n", domain.Name)
	projectPages := must.Return(projects.List(adminKeystone, projects.ListOpts{DomainID: domain.ID}).AllPages(ctx))
	projectsAll := must.Return(projects.ExtractProjects(projectPages))
	project := cli.ChooseProject(projectsAll)

	// Authenticate with that project.
	fmt.Printf("🔄 Logging into project %s ...", project.Name)
	projectAuth := gophercloud.AuthOptions{
		IdentityEndpoint: os.Getenv("OS_AUTH_URL"),
		Username:         os.Getenv("OS_USERNAME"),
		DomainID:         project.DomainID,
		Password:         password,
		AllowReauth:      true,
		Scope:            &gophercloud.AuthScope{ProjectID: project.ID},
	}
	projectProvider := must.Return(openstack.NewClient(projectAuth.IdentityEndpoint))
	must.Succeed(openstack.Authenticate(ctx, projectProvider, projectAuth))
	projectCompute := must.Return(openstack.NewComputeV2(projectProvider, computeEO))
	projectCompute.Microversion = "2.88" // Needed to correctly fetch hypervisors.
	projectNetwork := must.Return(openstack.NewNetworkV2(projectProvider, networkEO))
	projectCinder := must.Return(openstack.NewBlockStorageV3(projectProvider, blockstorageEO))
	fmt.Printf(" ✅ Done!\n")

	// Delete existing vms.
	fmt.Println("🔄 Looking up existing VMs")
	serverPages := must.Return(servers.List(projectCompute, nil).AllPages(ctx))
	serversAll := must.Return(servers.ExtractServers(serverPages))
	var serversToDelete []servers.Server
	var serversToDeleteNames []string
	for _, s := range serversAll {
		// Make some basic checks to ensure that we only delete the workload-spawner vms.
		if strings.Contains(s.Name, prefix) {
			serversToDelete = append(serversToDelete, s)
			serversToDeleteNames = append(serversToDeleteNames, s.Name)
		}
	}
	if len(serversToDelete) > 0 {
		// Get manual input to delete the vm.
		fmt.Printf("❓ Delete existing VMs %v? [y/N, default: \033[1;34my\033[0m]: ", serversToDeleteNames)
		serverReader := bufio.NewReader(os.Stdin)
		serverInput := must.Return(serverReader.ReadString('\n'))
		serverInput = strings.TrimSpace(serverInput)
		if serverInput == "y" || serverInput == "" {
			var wg sync.WaitGroup
			for _, s := range serversToDelete {
				wg.Go(func() {
					fmt.Printf("🧨 Deleting VM %s on %s\n", s.Name, s.HypervisorHostname)
					result := servers.Delete(ctx, adminNova, s.ID)
					must.Succeed(result.Err)
					// Wait until the vm is deleted.
					for {
						s, err := servers.Get(ctx, projectCompute, s.ID).Extract()
						if err != nil {
							// Assume the vm is gone.
							break
						}
						if s.Status == "DELETED" {
							break
						}
					}
					fmt.Printf("💥 Deleted VM %s on %s\n", s.Name, s.HypervisorHostname)
				})
			}
			wg.Wait()
			fmt.Println("🧨 Deleted all existing VMs")
		}
	}

	// Delete existing volumes.
	fmt.Println("🔄 Looking up existing volumes")
	volumePages := must.Return(volumes.List(projectCinder, volumes.ListOpts{}).AllPages(ctx))
	volumesAll := must.Return(volumes.ExtractVolumes(volumePages))
	var volumesToDelete []volumes.Volume
	var volumesToDeleteNames []string
	for _, v := range volumesAll {
		// Make some basic checks to ensure that we only delete the workload-spawner volumes.
		if strings.Contains(v.Name, prefix) {
			volumesToDelete = append(volumesToDelete, v)
			volumesToDeleteNames = append(volumesToDeleteNames, v.Name)
		}
	}
	if len(volumesToDelete) > 0 {
		// Get manual input to delete the volumes.
		fmt.Printf("❓ Delete existing volumes %v? [y/N, default: \033[1;34my\033[0m]: ", volumesToDeleteNames)
		volumeReader := bufio.NewReader(os.Stdin)
		volumeInput := must.Return(volumeReader.ReadString('\n'))
		volumeInput = strings.TrimSpace(volumeInput)
		if volumeInput == "y" || volumeInput == "" {
			var wg sync.WaitGroup
			for _, v := range volumesToDelete {
				wg.Go(func() {
					fmt.Printf("🧨 Deleting volume %s\n", v.Name)
					result := volumes.Delete(ctx, projectCinder, v.ID, volumes.DeleteOpts{})
					must.Succeed(result.Err)
					// Wait until the volume is deleted.
					for {
						vol, err := volumes.Get(ctx, projectCinder, v.ID).Extract()
						if err != nil {
							// Assume the volume is gone.
							break
						}
						if vol.Status == "DELETED" {
							break
						}
					}
					fmt.Printf("💥 Deleted volume %s\n", v.Name)
				})
			}
			wg.Wait()
			fmt.Println("🧨 Deleted all existing volumes")
		}
	}

	if vmsToSpawn <= 0 {
		fmt.Println("🎉 Done! - Not spawning VMs.")
		return
	}

	fmt.Printf("❓ Spawn on specific host [default: \033[1;34mn\033[0m]: ")
	reader = bufio.NewReader(os.Stdin)
	input = must.Return(reader.ReadString('\n'))
	input = strings.TrimSpace(input)
	if input == "" {
		input = "n"
	}
	var hypervisor *hypervisors.Hypervisor
	var az = ""
	aggregatePages := must.Return(aggregates.List(adminNova).AllPages(ctx))
	aggregatesAll := must.Return(aggregates.ExtractAggregates(aggregatePages))
	if input == "y" {
		// List all hypervisors with the given type.
		fmt.Println("🔄 Looking up hypervisors")
		withServers := true
		hlo := hypervisors.ListOpts{WithServers: &withServers}
		hypervisorPages := must.Return(hypervisors.List(adminNova, hlo).AllPages(ctx))
		hypervisorsAll := must.Return(hypervisors.ExtractHypervisors(hypervisorPages))
		hypervisorTypes := []string{}
		for _, h := range hypervisorsAll {
			if !slices.Contains(hypervisorTypes, h.HypervisorType) {
				hypervisorTypes = append(hypervisorTypes, h.HypervisorType)
			}
		}
		hypervisorType := cli.ChooseHypervisorType(hypervisorTypes)
		var hypervisorsFiltered []hypervisors.Hypervisor
		for _, h := range hypervisorsAll {
			if h.Status == "enabled" && h.State == "up" && h.HypervisorType == hypervisorType {
				hypervisorsFiltered = append(hypervisorsFiltered, h)
			}
		}
		h := cli.ChooseHypervisor(hypervisorsFiltered)
		hypervisor = &h
		// Resolve the availability zones of the hypervisor.
		fmt.Printf("🔄 Resolving availability zone of host %s\n", hypervisor.Service.Host)
		for _, a := range aggregatesAll {
			if a.AvailabilityZone == "" {
				continue
			}
			if slices.Contains(a.Hosts, hypervisor.Service.Host) {
				az = a.AvailabilityZone
			}
			if az != "" {
				break
			}
		}
		fmt.Printf("🗺️ Using availability zone '%s'\n", az)
	} else {
		// Let the user choose an az.
		azs := []string{}
		for _, a := range aggregatesAll {
			if !slices.Contains(azs, a.AvailabilityZone) && a.AvailabilityZone != "" {
				azs = append(azs, a.AvailabilityZone)
			}
		}
		az = cli.ChooseAZ(azs)
		fmt.Printf("🗺️ Using availability zone '%s'\n", az)
	}

	// Get flavors.
	fmt.Println("🔄 Looking up flavors to use")
	floPublic := flavors.ListOpts{AccessType: flavors.PublicAccess}
	flavorPagesPublic := must.Return(flavors.ListDetail(adminNova, floPublic).AllPages(ctx))
	flavorsAll := must.Return(flavors.ExtractFlavors(flavorPagesPublic))
	floPrivate := flavors.ListOpts{AccessType: flavors.PrivateAccess}
	flavorPagesPrivate := must.Return(flavors.ListDetail(adminNova, floPrivate).AllPages(ctx))
	// Add flavors that are not in the public list.
	for _, f1 := range must.Return(flavors.ExtractFlavors(flavorPagesPrivate)) {
		if !slices.ContainsFunc(flavorsAll, func(f2 flavors.Flavor) bool { return f1.ID == f2.ID }) {
			flavorsAll = append(flavorsAll, f1)
		}
	}
	flavor := cli.ChooseFlavor(flavorsAll)

	// Get a suitable image.
	fmt.Println("🔄 Looking up image to use")
	ilo := images.ListOpts{Status: images.ImageStatusActive, Visibility: images.ImageVisibilityPublic}
	imagePages := must.Return(images.List(adminGlance, ilo).AllPages(ctx))
	imagesAll := must.Return(images.ExtractImages(imagePages))
	image := cli.ChooseImage(imagesAll)

	// Create the necessary network in the target availability zone.
	networkName := prefix + "-network"
	subnetworkName := networkName + "-subnet"
	fmt.Println("🔄 Looking up networks to use")
	nlo := networks.ListOpts{
		Name:      networkName,
		ProjectID: must.Return(gophercloudext.GetProjectIDFromTokenScope(projectProvider)),
	}
	networksPages := must.Return(networks.List(projectNetwork, nlo).AllPages(ctx))
	networksAll := must.Return(networks.ExtractNetworks(networksPages))
	if len(networksAll) > 1 {
		fmt.Printf("🚫 Found more than one network matching %s\n", networkName)
		return
	}
	var network *networks.Network
	if len(networksAll) == 1 {
		fmt.Printf("❓ Delete existing network %s [y/N, default: \033[1;34mN\033[0m]: ", networkName)
		reader := bufio.NewReader(os.Stdin)
		input := must.Return(reader.ReadString('\n'))
		input = strings.TrimSpace(input)
		if input == "y" {
			// Delete the subnets.
			fmt.Printf("🔄 Looking up subnets in network %s\n", networkName)
			slo := subnets.ListOpts{NetworkID: networksAll[0].ID}
			subnetPages := must.Return(subnets.List(projectNetwork, slo).AllPages(ctx))
			subnetsAll := must.Return(subnets.ExtractSubnets(subnetPages))
			for _, s := range subnetsAll {
				fmt.Printf("🧨 Deleting subnet %s\n", s.ID)
				result := subnets.Delete(ctx, projectNetwork, s.ID)
				must.Succeed(result.Err)
				fmt.Printf("💥 Deleted subnet %s\n", s.ID)
			}
			// Delete the network.
			fmt.Printf("🧨 Deleting network %s\n", networkName)
			result := networks.Delete(ctx, projectNetwork, networksAll[0].ID)
			must.Succeed(result.Err)
			fmt.Printf("💥 Deleted network %s\n", networkName)
			networksAll = nil
		}
	}
	if len(networksAll) == 1 {
		network = &networksAll[0]
		fmt.Printf("🛜 Using network %s\n", networkName)
	}
	if len(networksAll) == 0 {
		fmt.Printf("🆕 Creating network %s\n", networkName)
		no := networks.CreateOpts{
			Name: networkName,
		}
		network = must.Return(networks.Create(ctx, projectNetwork, no).Extract())
		res := subnets.Create(ctx, projectNetwork, subnets.CreateOpts{
			NetworkID: network.ID,
			Name:      subnetworkName,
			IPVersion: 4,
			CIDR:      "10.180.1.0/16",
		})
		must.Succeed(res.Err)
		fmt.Printf("🛜 Using new network %s\n", networkName)
	}

	// Create an ssh key pair in case we want to login later on.
	fmt.Println("🔄 Looking up existing keypairs")
	keyName := prefix + "-key"
	kplo := keypairs.ListOpts{}
	keypairPages := must.Return(keypairs.List(projectCompute, kplo).AllPages(ctx))
	keypairsAll := must.Return(keypairs.ExtractKeyPairs(keypairPages))
	var keypairsFiltered []keypairs.KeyPair
	for _, kp := range keypairsAll {
		if kp.Name == keyName {
			keypairsFiltered = append(keypairsFiltered, kp)
		}
	}
	// Delete all existing keypairs with the same name.
	if len(keypairsFiltered) > 0 {
		fmt.Printf("❓ Delete existing keypairs %v? [y/N, default: \033[1;34my\033[0m]: ", keyName)
		reader := bufio.NewReader(os.Stdin)
		input := must.Return(reader.ReadString('\n'))
		input = strings.TrimSpace(input)
		if input == "" {
			input = "y"
		}
		if input != "y" {
			fmt.Println("🚫 Aborted")
			return
		}
		var wg sync.WaitGroup
		for _, kp := range keypairsFiltered {
			wg.Go(func() {
				fmt.Printf("🧨 Deleting keypair %s\n", kp.Name)
				result := keypairs.Delete(ctx, projectCompute, kp.Name, keypairs.DeleteOpts{})
				must.Succeed(result.Err)
				fmt.Printf("💥 Deleted keypair %s\n", kp.Name)
			})
		}
		wg.Wait()
		fmt.Println("🧨 Deleted all existing keypairs")
	}
	// Create a new keypair.
	fmt.Printf("🆕 Creating keypair %s\n", keyName)
	kpo := keypairs.CreateOpts{Name: keyName}
	keypair := must.Return(keypairs.Create(ctx, projectCompute, kpo).Extract())
	fmt.Printf("🛜 Using keypair %s\n", keyName)

	// Load the script template
	tmpl, err := template.ParseFiles("tools/spawner/script.sh.tpl")
	must.Succeed(err)

	// Spawn new VMs.
	var wg sync.WaitGroup
	for i := range vmsToSpawn {
		wg.Go(func() {
			//nolint:gosec // We don't care if the id is cryptographically secure.
			name := fmt.Sprintf("%s-%05d", prefix, rand.Intn(100000))
			var scriptBuilder strings.Builder
			must.Succeed(tmpl.Execute(&scriptBuilder, map[string]any{
				"VCPUs": flavor.VCPUs,
				"RAM":   flavor.RAM * 1_000,
			}))

			var so keypairs.CreateOptsExt
			fmt.Println("💾 Creating boot volume for server")
			// Create a boot volume for zero-disk flavors
			volumeName := name + "-boot-volume"
			bootVolume := must.Return(volumes.Create(ctx, projectCinder, volumes.CreateOpts{
				Size:             16, // 16GB boot volume should be sufficient for most OSes
				Name:             volumeName,
				ImageID:          image.ID,
				AvailabilityZone: az,
				VolumeType:       "nfs",
			}, nil).Extract())

			// Wait for volume to be available
			for {
				vol, err := volumes.Get(ctx, projectCinder, bootVolume.ID).Extract()
				if err != nil {
					break
				}
				if vol.Status == "available" {
					break
				}
			}

			// Create server with block device mapping (volume-backed)
			sco := servers.CreateOpts{
				Name:             name,
				FlavorRef:        flavor.ID,
				UserData:         []byte(scriptBuilder.String()),
				Networks:         []servers.Network{{UUID: network.ID}},
				AvailabilityZone: az,
				BlockDevice: []servers.BlockDevice{{
					UUID:                bootVolume.ID,
					SourceType:          servers.SourceVolume,
					DestinationType:     servers.DestinationVolume,
					BootIndex:           0,
					DeleteOnTermination: true, // Remove the boot volume when deleted
				}},
			}
			if hypervisor != nil {
				sco.AvailabilityZone = az + ":" + hypervisor.Service.Host
			}
			so = keypairs.CreateOptsExt{
				KeyName:           keyName,
				CreateOptsBuilder: sco,
			}
			ho := servers.SchedulerHintOpts{}
			serverCreateResult, err := servers.Create(ctx, projectCompute, so, ho).Extract()
			baseMsg := fmt.Sprintf(
				"... (%d/%d) Spawning VM %s on %s with flavor %s, image %s ",
				i+1, vmsToSpawn, name, az, image.Name, flavor.Name,
			)
			if err != nil {
				fmt.Printf("%s🚫 Error: %s\n", baseMsg, err)
				return
			}
			// Wait for the instance to become active.
			for {
				time.Sleep(1 * time.Second)
				s, err := servers.Get(ctx, projectCompute, serverCreateResult.ID).Extract()
				if err != nil {
					fmt.Printf("%s🚫 Error while waiting for server to become active: %s\n", baseMsg, err)
					break
				}
				if s.Status == "ACTIVE" {
					fmt.Printf("%s✅ VM is active\n", baseMsg)
					break
				}
				if s.Status == "ERROR" {
					// Get additional error details from the server's fault message if available.
					fmt.Printf("%s🚫 VM entered error state, fault: %s (%s)\n", baseMsg, s.Fault.Message, s.Fault.Details)
					break
				}
			}
		})
	}
	wg.Wait()

	// Write the keypair to a file, so the user can ssh into the vms.
	fmt.Println("📝 Writing keypair to ssh.pem", keyName)
	must.Succeed(os.WriteFile("tools/spawner/ssh.pem", []byte(keypair.PrivateKey), 0600))
	fmt.Println("🔑 Add the following ssh key to your ssh agent:")
	fmt.Println("💲 eval $(ssh-agent -s) && ssh-add tools/spawner/ssh.pem")
	fmt.Printf("📝 To ssh into your VMs, create a new router that assigns the subnet %s to a floating IP network. Then assign a floating IP to your VM.\n", subnetworkName)

	fmt.Println("🎉 Done!")
}
