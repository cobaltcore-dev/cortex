// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"net/http"

	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/cobaltcore-dev/cortex/pkg/sso"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// Flavor is the slice of a Nova flavor the seeder needs to build a scheduler
// probe: its name, size, and extra specs (which carry the hypervisor type,
// traits and other constraints the pipeline filters on). It deliberately mirrors
// only the fields fed into api.NovaFlavor, so the rest of gophercloud's flavor
// shape never leaks into the pathfinder.
type Flavor struct {
	// Name is the Nova flavor name, forwarded as the probe's flavor name so
	// flavor-name-sensitive filters behave as in a real placement.
	Name string
	// MemoryMB and VCPUs size the probe. Nova reports RAM in MiB (despite the
	// "MB" label), matching what the scheduler expects on NovaFlavor.MemoryMB.
	MemoryMB uint64
	VCPUs    uint64
	// ExtraSpecs carries the flavor's extra specs (hypervisor type, custom
	// traits, hardware requirements) so the same filters apply as for a real vm.
	ExtraSpecs map[string]string
}

// FlavorClient fetches the Nova flavors the seeder permutes into probes. It is a
// deliberately tiny surface over Nova: the failover controller only ever needs
// the flavor catalogue, never servers or hypervisors (those come from the CRDs).
type FlavorClient interface {
	// Connect authenticates against OpenStack and resolves the Nova endpoint. It
	// must be called once before ListFlavors. The k8s client is used only to read
	// the credential secrets.
	Connect(ctx context.Context, k8s client.Client) error
	// ListFlavors returns every flavor (public and private) known to Nova. A
	// failure is returned rather than a partial list, so a transient Nova outage
	// aborts the whole calculation pass instead of shrinking the seed set.
	ListFlavors(ctx context.Context) ([]Flavor, error)
}

// NovaFlavorClientConfig supplies the credentials the flavor client needs. It
// mirrors the other reservation clients (see inflight.NovaVMClientConfig): a
// keystone secret ref plus an optional SSO secret ref, both resolved from the
// cluster at Connect time rather than baked into the config.
type NovaFlavorClientConfig struct {
	// KeystoneSecretRef references the k8s secret holding the technical user's
	// OpenStack credentials.
	KeystoneSecretRef corev1.SecretReference `json:"keystoneSecretRef"`
	// SSOSecretRef optionally references an SSO client certificate secret, used
	// to wrap the HTTP client before authenticating. Nil when SSO is not in use.
	SSOSecretRef *corev1.SecretReference `json:"ssoSecretRef"`
}

// novaFlavorClient is the default FlavorClient, backed by gophercloud and the
// shared keystone.Connector, so it authenticates exactly like the other
// OpenStack-facing reservation controllers rather than reimplementing auth.
type novaFlavorClient struct {
	config NovaFlavorClientConfig
	// sc is the authenticated Nova compute service client, set by Connect.
	sc *gophercloud.ServiceClient
}

// Compile-time assertion that novaFlavorClient satisfies the interface.
var _ FlavorClient = (*novaFlavorClient)(nil)

func NewNovaFlavorClient(config NovaFlavorClientConfig) FlavorClient {
	return &novaFlavorClient{config: config}
}

// Connect authenticates the technical user against Keystone and builds the Nova
// compute service client from the service catalogue. It follows the same shape
// as the other reservation clients: optionally wrap the HTTP client with SSO,
// authenticate via keystone.Connector, then resolve the compute endpoint.
func (c *novaFlavorClient) Connect(ctx context.Context, k8s client.Client) error {
	log := logf.FromContext(ctx)
	var authenticatedHTTP = http.DefaultClient
	if c.config.SSOSecretRef != nil {
		var err error
		authenticatedHTTP, err = sso.Connector{Client: k8s}.
			FromSecretRef(ctx, *c.config.SSOSecretRef)
		if err != nil {
			return err
		}
	}
	authenticatedKeystone, err := keystone.
		Connector{Client: k8s, HTTPClient: authenticatedHTTP}.
		FromSecretRef(ctx, c.config.KeystoneSecretRef)
	if err != nil {
		return err
	}
	provider := authenticatedKeystone.Client()
	serviceType := "compute"
	url, err := authenticatedKeystone.FindEndpoint(authenticatedKeystone.Availability(), serviceType)
	if err != nil {
		return err
	}
	log.Info("using nova endpoint", "url", url)
	c.sc = &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		Type:           serviceType,
		// Since microversion 2.61 the flavor list embeds extra_specs, so we read
		// them straight off the list response rather than fetching them per flavor.
		Microversion: "2.61",
	}
	return nil
}

// ListFlavors lists all flavors (public and private — the technical user is an
// admin) with their extra specs. Flavor sets are small and stable, so the
// gophercloud AllPages accumulation is fine here; there is none of the
// large-response memory concern that forces hand-pagination elsewhere.
func (c *novaFlavorClient) ListFlavors(ctx context.Context) ([]Flavor, error) {
	pages, err := flavors.ListDetail(c.sc, flavors.ListOpts{AccessType: flavors.AllAccess}).AllPages(ctx)
	if err != nil {
		return nil, err
	}
	extracted, err := flavors.ExtractFlavors(pages)
	if err != nil {
		return nil, err
	}
	out := make([]Flavor, 0, len(extracted))
	for _, f := range extracted {
		out = append(out, Flavor{
			Name: f.Name,
			// Nova reports RAM in MiB even though the field is labelled "MB".
			MemoryMB:   uint64(f.RAM),
			VCPUs:      uint64(f.VCPUs),
			ExtraSpecs: f.ExtraSpecs,
		})
	}
	return out, nil
}
