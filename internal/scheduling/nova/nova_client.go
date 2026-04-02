// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/cobaltcore-dev/cortex/pkg/sso"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/sapcc/go-bits/liquidapi"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type NovaClientConfig struct {
	// Secret ref to keystone credentials stored in a k8s secret.
	KeystoneSecretRef corev1.SecretReference `json:"keystoneSecretRef"`

	// Secret ref to SSO credentials stored in a k8s secret, if applicable.
	SSOSecretRef *corev1.SecretReference `json:"ssoSecretRef,omitempty"`
}

type server struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	ComputeHost string `json:"OS-EXT-SRV-ATTR:host"`
}

type migration struct {
	InstanceUUID  string `json:"instance_uuid"`
	SourceCompute string `json:"source_compute"`
	DestCompute   string `json:"dest_compute"`
}

// ServerDetail contains extended server information for usage reporting.
type ServerDetail struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Status           string            `json:"status"`
	TenantID         string            `json:"tenant_id"`
	Created          string            `json:"created"`
	AvailabilityZone string            `json:"OS-EXT-AZ:availability_zone"`
	Hypervisor       string            `json:"OS-EXT-SRV-ATTR:hypervisor_hostname"`
	FlavorName       string            // Populated from nested flavor.original_name
	FlavorRAM        uint64            // Populated from nested flavor.ram
	FlavorVCPUs      uint64            // Populated from nested flavor.vcpus
	FlavorDisk       uint64            // Populated from nested flavor.disk
	Metadata         map[string]string // Server metadata key-value pairs
	Tags             []string          // Server tags
	OSType           string            // OS type determined by OSTypeProber
}

type NovaClient interface {
	// Initialize the Nova API with the Keystone authentication.
	Init(ctx context.Context, client client.Client, conf NovaClientConfig) error
	// Get a server by ID.
	Get(ctx context.Context, id string) (server, error)
	// Live migrate a server to a new host (doesnt wait for it to complete).
	LiveMigrate(ctx context.Context, id string) error
	// Get migrations for a server by ID.
	GetServerMigrations(ctx context.Context, id string) ([]migration, error)
	// List all servers for a project with detailed info.
	ListProjectServers(ctx context.Context, projectID string) ([]ServerDetail, error)
}

type novaClient struct {
	// Authenticated OpenStack service client to fetch the data.
	sc *gophercloud.ServiceClient
	// OS type prober for determining VM operating system type (for billing).
	osTypeProber *liquidapi.OSTypeProber
}

func NewNovaClient() NovaClient {
	return &novaClient{}
}

func (api *novaClient) Init(ctx context.Context, client client.Client, conf NovaClientConfig) error {
	var authenticatedHTTP = http.DefaultClient
	if conf.SSOSecretRef != nil {
		var err error
		authenticatedHTTP, err = sso.Connector{Client: client}.
			FromSecretRef(ctx, *conf.SSOSecretRef)
		if err != nil {
			return err
		}
	}
	authenticatedKeystone, err := keystone.
		Connector{Client: client, HTTPClient: authenticatedHTTP}.
		FromSecretRef(ctx, conf.KeystoneSecretRef)
	if err != nil {
		return err
	}
	// Automatically fetch the nova endpoint from the keystone service catalog.
	provider := authenticatedKeystone.Client()
	serviceType := "compute"
	url, err := authenticatedKeystone.FindEndpoint(
		authenticatedKeystone.Availability(), serviceType,
	)
	if err != nil {
		return err
	}
	slog.Info("using nova endpoint", "url", url)
	api.sc = &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		Type:           serviceType,
		// Since microversion 2.53, the hypervisor id and service id is a UUID.
		// We need that to find placement resource providers for hypervisors.
		Microversion: "2.53",
	}

	// Initialize OS type prober for determining VM operating system type.
	// Uses existing provider client to access Glance (image) and Cinder (volume) APIs.
	eo := gophercloud.EndpointOpts{Availability: gophercloud.Availability(authenticatedKeystone.Availability())}
	api.osTypeProber, err = liquidapi.NewOSTypeProber(provider, eo)
	if err != nil {
		slog.Warn("failed to initialize OS type prober - os_type will be empty", "error", err)
		// Non-fatal - continue without OS type probing
	}

	return nil
}

// Get a server by ID.
func (api *novaClient) Get(ctx context.Context, id string) (server, error) {
	var s server
	if err := servers.Get(ctx, api.sc, id).ExtractInto(&s); err != nil {
		return server{}, err
	}
	return s, nil
}

// Live migrate a server to a new host (doesn't wait for it to complete).
func (api *novaClient) LiveMigrate(ctx context.Context, id string) error {
	blockMigration := false
	lmo := servers.LiveMigrateOpts{
		Host:           nil,
		BlockMigration: &blockMigration, // required
	}
	result := servers.LiveMigrate(ctx, api.sc, id, lmo)
	return result.Err
}

// Get migrations for a server by ID.
func (api *novaClient) GetServerMigrations(ctx context.Context, id string) ([]migration, error) {
	// Note: currently we need to fetch this without gophercloud.
	// See: https://github.com/gophercloud/gophercloud/pull/3244
	initialURL := api.sc.Endpoint + "os-migrations" + "?instance_uuid=" + id
	var nextURL = &initialURL
	var migrations []migration
	for nextURL != nil {
		var list struct {
			Migrations []migration `json:"migrations"`
			Links      []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"migrations_links"`
		}
		resp, err := api.sc.Get(ctx, *nextURL, &list, &gophercloud.RequestOpts{
			OkCodes: []int{http.StatusOK},
			MoreHeaders: map[string]string{
				"X-OpenStack-Nova-API-Version": api.sc.Microversion,
			},
		})
		if resp != nil {
			defer resp.Body.Close()
		}
		if err != nil {
			return nil, err
		}
		nextURL = nil
		for _, link := range list.Links {
			if link.Rel == "next" {
				nextURL = &link.Href
				break
			}
		}
		migrations = append(migrations, list.Migrations...)
	}
	slog.Info("fetched migrations for vm", "id", id, "count", len(migrations))
	return migrations, nil
}

// ListProjectServers retrieves all servers for a project with detailed info.
func (api *novaClient) ListProjectServers(ctx context.Context, projectID string) ([]ServerDetail, error) {
	if api.sc == nil {
		return nil, errors.New("nova client not initialized - call Init first")
	}
	// Build URL with pagination support
	initialURL := api.sc.Endpoint + "servers/detail?all_tenants=true&tenant_id=" + projectID
	var nextURL = &initialURL
	var result []ServerDetail

	for nextURL != nil {
		// Response structure with nested flavor, metadata, tags, image, and volumes
		var list struct {
			Servers []struct {
				ID               string            `json:"id"`
				Name             string            `json:"name"`
				Status           string            `json:"status"`
				TenantID         string            `json:"tenant_id"`
				Created          string            `json:"created"`
				AvailabilityZone string            `json:"OS-EXT-AZ:availability_zone"`
				Hypervisor       string            `json:"OS-EXT-SRV-ATTR:hypervisor_hostname"`
				Metadata         map[string]string `json:"metadata"`
				Tags             []string          `json:"tags"`
				Flavor           struct {
					OriginalName string `json:"original_name"`
					RAM          uint64 `json:"ram"`
					VCPUs        uint64 `json:"vcpus"`
					Disk         uint64 `json:"disk"`
				} `json:"flavor"`
				// For OS type probing - use json.RawMessage because Nova returns
				// either a map (for image-booted VMs) or empty string "" (for volume-booted VMs)
				Image           json.RawMessage `json:"image"`
				AttachedVolumes []struct {
					ID                  string `json:"id"`
					DeleteOnTermination bool   `json:"delete_on_termination"`
				} `json:"os-extended-volumes:volumes_attached"`
			} `json:"servers"`
			Links []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"servers_links"`
		}

		resp, err := api.sc.Get(ctx, *nextURL, &list, &gophercloud.RequestOpts{
			OkCodes: []int{http.StatusOK},
			MoreHeaders: map[string]string{
				"X-OpenStack-Nova-API-Version": api.sc.Microversion,
			},
		})
		if resp != nil {
			defer resp.Body.Close()
		}
		if err != nil {
			return nil, err
		}

		// Convert to ServerDetail
		for _, s := range list.Servers {
			// Probe OS type if prober is available
			osType := ""
			if api.osTypeProber != nil {
				// Parse image field - Nova returns either a map or empty string ""
				var imageMap map[string]any
				if len(s.Image) > 0 && s.Image[0] == '{' {
					// Intentionally ignore parse errors - imageMap will remain nil for volume-booted VMs
					json.Unmarshal(s.Image, &imageMap) //nolint:errcheck // error expected for non-JSON values
				}
				// Build a minimal servers.Server for the prober
				vols := make([]servers.AttachedVolume, len(s.AttachedVolumes))
				for i, v := range s.AttachedVolumes {
					vols[i] = servers.AttachedVolume{ID: v.ID}
				}
				proberServer := servers.Server{
					ID:              s.ID,
					Image:           imageMap,
					AttachedVolumes: vols,
				}
				osType = api.osTypeProber.Get(ctx, proberServer)
			}

			result = append(result, ServerDetail{
				ID:               s.ID,
				Name:             s.Name,
				Status:           s.Status,
				TenantID:         s.TenantID,
				Created:          s.Created,
				AvailabilityZone: s.AvailabilityZone,
				Hypervisor:       s.Hypervisor,
				FlavorName:       s.Flavor.OriginalName,
				FlavorRAM:        s.Flavor.RAM,
				FlavorVCPUs:      s.Flavor.VCPUs,
				FlavorDisk:       s.Flavor.Disk,
				Metadata:         s.Metadata,
				Tags:             s.Tags,
				OSType:           osType,
			})
		}

		// Check for next page
		nextURL = nil
		for _, link := range list.Links {
			if link.Rel == "next" {
				nextURL = &link.Href
				break
			}
		}
	}

	slog.Info("fetched servers for project", "projectID", projectID, "count", len(result))
	return result, nil
}
