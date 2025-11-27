// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/cobaltcore-dev/cortex/tools/spawner/defaults"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/hypervisors"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/domains"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/sapcc/go-bits/must"
)

type CLI interface {
	ChooseAZ([]string) string
	ChooseDomain([]domains.Domain) domains.Domain
	ChooseProject([]projects.Project) projects.Project
	ChooseFlavor([]flavors.Flavor) flavors.Flavor
	ChooseImage([]images.Image) images.Image
	ChooseHypervisorType([]string) string
	ChooseHypervisor([]hypervisors.Hypervisor) hypervisors.Hypervisor
}

type cli struct {
	defaults defaults.Defaults
}

func NewCLI(d defaults.Defaults) CLI {
	return &cli{defaults: d}
}

func (c *cli) ChooseAZ(azs []string) string {
	f := func(az string) string {
		return az
	}
	return choose(c.defaults, "WS_AVAILABILITY_ZONE", "📂 Availability Zones", azs, f)
}

func (c *cli) ChooseDomain(ds []domains.Domain) domains.Domain {
	f := func(d domains.Domain) string {
		return d.Name
	}
	return choose(c.defaults, "WS_DOMAIN", "📂 Domains", ds, f)
}

func (c *cli) ChooseProject(ps []projects.Project) projects.Project {
	f := func(p projects.Project) string {
		return p.Name
	}
	return choose(c.defaults, "WS_PROJECT", "📂 Projects", ps, f)
}

func (c *cli) ChooseFlavor(fs []flavors.Flavor) flavors.Flavor {
	f := func(f flavors.Flavor) string {
		o := fmt.Sprintf("%s (%d vCPUs, %d MB RAM) id:%s", f.Name, f.VCPUs, f.RAM, f.ID)
		if !f.IsPublic {
			o += " (private)"
		}
		return o
	}
	return choose(c.defaults, "WS_FLAVOR", "📂 Flavors", fs, f)
}

func (c *cli) ChooseImage(is []images.Image) images.Image {
	f := func(i images.Image) string {
		return fmt.Sprintf("%s (%s) id:%s", i.Name, i.Status, i.ID[:5])
	}
	return choose(c.defaults, "WS_IMAGE", "📂 Images", is, f)
}

func (c *cli) ChooseHypervisorType(ts []string) string {
	f := func(t string) string {
		return t
	}
	return choose(c.defaults, "WS_HYPERVISOR_TYPE", "📂 Hypervisor Types", ts, f)
}

func (c *cli) ChooseHypervisor(hs []hypervisors.Hypervisor) hypervisors.Hypervisor {
	f := func(h hypervisors.Hypervisor) string {
		// Host, type and first 5 characters of the id.
		return fmt.Sprintf("%s (%s) id:%s", h.Service.Host, h.HypervisorType, h.ID[:5])
	}
	return choose(c.defaults, "WS_HYPERVISOR", "📂 Hypervisors", hs, f)
}

// Choose asks the user to choose one of the given options.
// The user can choose by index or by name. The user can also choose the default value.
// If the user chooses to input a name, the mapping is done by the displayname function.
func choose[T any](
	d defaults.Defaults,
	defaultKey string,
	header string,
	ts []T,
	displayname func(T) string,
) T {

	sort.Slice(ts, func(i, j int) bool {
		return displayname(ts[i]) < displayname(ts[j])
	})
	fmt.Printf("🔍 %s\n", header)
	for i, t := range ts {
		fmt.Printf("   - [\033[1;34m%d\033[0m] \033[1;34m%s\033[0m\n", i, displayname(t))
	}
	tByName := make(map[string]T)
	for _, t := range ts {
		tByName[displayname(t)] = t
	}
	if len(ts) != len(tByName) {
		panic("displayname is not unique")
	}
	var defaultChoice = d.GetDefault(defaultKey)
	var defaultChoicePresent bool
	if _, ok := tByName[defaultChoice]; ok {
		defaultChoicePresent = true
	}
	if defaultChoice != "" && defaultChoicePresent {
		fmt.Printf("📥 Index or name [default: \033[1;34m%s\033[0m]: ", defaultChoice)
	} else {
		fmt.Printf("📥 Index or name: ")
	}
	reader := bufio.NewReader(os.Stdin)
	input := must.Return(reader.ReadString('\n'))
	input = strings.TrimSpace(input)
	if input == "" {
		input = defaultChoice
	}
	var t T
	if i, err := strconv.Atoi(input); err == nil {
		t = ts[i]
	} else {
		t = tByName[input]
	}
	d.SetDefault(defaultKey, displayname(t))
	return t
}
