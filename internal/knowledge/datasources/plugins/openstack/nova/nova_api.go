// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/aggregates"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	glanceimages "github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/gophercloud/gophercloud/v2/pagination"
	"github.com/prometheus/client_golang/prometheus"
)

type NovaAPI interface {
	// Init the nova API.
	Init(ctx context.Context) error
	// Get all nova servers that are NOT deleted. (Includes ERROR, SHUTOFF etc)
	GetAllServers(ctx context.Context) ([]Server, error)
	// Get all deleted nova servers since the timestamp.
	GetDeletedServers(ctx context.Context, since time.Time) ([]DeletedServer, error)
	// Get all nova hypervisors.
	GetAllHypervisors(ctx context.Context) ([]Hypervisor, error)
	// Get all nova flavors.
	GetAllFlavors(ctx context.Context) ([]Flavor, error)
	// Get all nova migrations.
	GetAllMigrations(ctx context.Context) ([]Migration, error)
	// Get all aggregates.
	GetAllAggregates(ctx context.Context) ([]Aggregate, error)
	// Get all Glance images with pre-computed os_type.
	GetAllImages(ctx context.Context) ([]Image, error)
}

// API for OpenStack Nova.
type novaAPI struct {
	// Monitor to track the api.
	mon datasources.Monitor
	// Keystone api to authenticate against.
	keystoneClient keystone.KeystoneClient
	// Nova configuration.
	conf v1alpha1.NovaDatasource
	// Authenticated OpenStack compute service client.
	sc *gophercloud.ServiceClient
	// Authenticated Glance image service client (only used for NovaDatasourceTypeImages).
	glance *gophercloud.ServiceClient
}

func NewNovaAPI(mon datasources.Monitor, k keystone.KeystoneClient, conf v1alpha1.NovaDatasource) NovaAPI {
	return &novaAPI{mon: mon, keystoneClient: k, conf: conf}
}

// Init the nova API.
func (api *novaAPI) Init(ctx context.Context) error {
	if err := api.keystoneClient.Authenticate(ctx); err != nil {
		return err
	}
	// Automatically fetch the nova endpoint from the keystone service catalog.
	provider := api.keystoneClient.Client()
	serviceType := "compute"
	sameAsKeystone := api.keystoneClient.Availability()
	url, err := api.keystoneClient.FindEndpoint(sameAsKeystone, serviceType)
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
		// Since 2.61, the extra_specs are returned in the flavor details.
		Microversion: "2.61",
	}
	// Initialize the Glance client only when this datasource is used for images.
	if api.conf.Type == v1alpha1.NovaDatasourceTypeImages {
		glanceClient, err := openstack.NewImageV2(provider, gophercloud.EndpointOpts{
			Availability: gophercloud.Availability(sameAsKeystone),
		})
		if err != nil {
			return fmt.Errorf("failed to create Glance client: %w", err)
		}
		api.glance = glanceClient
	}
	return nil
}

// Get all Nova servers that are NOT deleted. (Includes ERROR, SHUTOFF etc)
func (api *novaAPI) GetAllServers(ctx context.Context) ([]Server, error) {
	label := Server{}.TableName()
	slog.Info("fetching nova data", "label", label)

	if api.mon.RequestTimer != nil {
		hist := api.mon.RequestTimer.WithLabelValues(label)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	initialURL := api.sc.Endpoint + "servers/detail?all_tenants=true"
	var nextURL = &initialURL
	var allServers []Server
	seen := make(map[string]struct{})

	for nextURL != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, *nextURL, http.NoBody)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Auth-Token", api.sc.Token())
		req.Header.Set("X-OpenStack-Nova-API-Version", api.sc.Microversion)
		resp, err := api.sc.HTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}
		var list struct {
			Servers []Server `json:"servers"`
			Links   []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"servers_links"`
		}
		err = json.NewDecoder(resp.Body).Decode(&list)
		if err != nil {
			return nil, err
		}
		for _, s := range list.Servers {
			if _, ok := seen[s.ID]; ok {
				slog.Warn("skipping duplicate server", "id", s.ID)
				continue
			}
			seen[s.ID] = struct{}{}
			allServers = append(allServers, s)
		}
		nextURL = nil
		for _, link := range list.Links {
			if link.Rel == "next" {
				nextURL = &link.Href
				break
			}
		}
	}

	slog.Info("fetched", "label", label, "count", len(allServers))
	return allServers, nil
}

// Get all deleted Nova servers.
// Note on Nova terminology: Nova uses "instance" internally in its database and code,
// but exposes these as "server" objects through the public API.
// Server lifecycle and cleanup:
//   - In SAP Cloud Infrastructure's Nova fork, orphaned servers are purged after 3 weeks
//   - This means historical server data is limited to 3 weeks
func (api *novaAPI) GetDeletedServers(ctx context.Context, since time.Time) ([]DeletedServer, error) {
	label := DeletedServer{}.TableName()
	slog.Info("fetching nova data", "label", label, "changedSince", since)

	if api.mon.RequestTimer != nil {
		hist := api.mon.RequestTimer.WithLabelValues(label)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	initialURL := api.sc.Endpoint + "servers/detail?status=DELETED&all_tenants=true&changes-since=" + url.QueryEscape(since.Format(time.RFC3339))
	var nextURL = &initialURL
	var deletedServers []DeletedServer
	seen := make(map[string]struct{})

	for nextURL != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, *nextURL, http.NoBody)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Auth-Token", api.sc.Token())
		req.Header.Set("X-OpenStack-Nova-API-Version", api.sc.Microversion)
		resp, err := api.sc.HTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}
		var list struct {
			Servers []DeletedServer `json:"servers"`
			Links   []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"servers_links"`
		}
		err = json.NewDecoder(resp.Body).Decode(&list)
		if err != nil {
			return nil, err
		}
		for _, s := range list.Servers {
			if _, ok := seen[s.ID]; ok {
				slog.Warn("skipping duplicate deleted server", "id", s.ID)
				continue
			}
			seen[s.ID] = struct{}{}
			deletedServers = append(deletedServers, s)
		}
		nextURL = nil
		for _, link := range list.Links {
			if link.Rel == "next" {
				nextURL = &link.Href
				break
			}
		}
	}

	slog.Info("fetched", "label", label, "count", len(deletedServers))
	return deletedServers, nil
}

// Get all Nova hypervisors.
func (api *novaAPI) GetAllHypervisors(ctx context.Context) ([]Hypervisor, error) {
	label := Hypervisor{}.TableName()
	slog.Info("fetching nova data", "label", label)
	// Note: currently we need to fetch this without gophercloud.
	// Gophercloud will just assume the request is a single page even when
	// the response is paginated, returning only the first page.
	if api.mon.RequestTimer != nil {
		hist := api.mon.RequestTimer.WithLabelValues(label)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}
	initialURL := api.sc.Endpoint + "os-hypervisors/detail"
	var nextURL = &initialURL
	var hypervisors []Hypervisor
	seen := make(map[string]struct{})
	for nextURL != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, *nextURL, http.NoBody)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Auth-Token", api.sc.Token())
		req.Header.Set("X-OpenStack-Nova-API-Version", api.sc.Microversion)
		resp, err := api.sc.HTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}
		var list struct {
			Hypervisors []Hypervisor `json:"hypervisors"`
			Links       []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"hypervisors_links"`
		}
		err = json.NewDecoder(resp.Body).Decode(&list)
		if err != nil {
			return nil, err
		}
		for _, h := range list.Hypervisors {
			if _, ok := seen[h.ID]; ok {
				slog.Warn("skipping duplicate hypervisor", "id", h.ID)
				continue
			}
			seen[h.ID] = struct{}{}
			hypervisors = append(hypervisors, h)
		}
		nextURL = nil
		for _, link := range list.Links {
			if link.Rel == "next" {
				nextURL = &link.Href
				break
			}
		}
	}
	slog.Info("fetched", "label", label, "count", len(hypervisors))
	return hypervisors, nil
}

// Get all Nova flavors.
func (api *novaAPI) GetAllFlavors(ctx context.Context) ([]Flavor, error) {
	label := Flavor{}.TableName()
	slog.Info("fetching nova data", "label", label)
	// Fetch all pages.
	pages, err := func() (pagination.Page, error) {
		if api.mon.RequestTimer != nil {
			hist := api.mon.RequestTimer.WithLabelValues(label)
			timer := prometheus.NewTimer(hist)
			defer timer.ObserveDuration()
		}
		lo := flavors.ListOpts{AccessType: flavors.AllAccess} // Also private flavors.
		return flavors.ListDetail(api.sc, lo).AllPages(ctx)
	}()
	if err != nil {
		return nil, err
	}
	// Parse the json data into our custom model.
	var data = &struct {
		Flavors []Flavor `json:"flavors"`
	}{}
	if err := pages.(flavors.FlavorPage).ExtractInto(data); err != nil {
		return nil, err
	}
	slog.Info("fetched", "label", label, "count", len(data.Flavors))
	return data.Flavors, nil
}

// Get all Nova migrations from the OpenStack API.
//
// Note on Nova terminology: Nova uses "instance" internally in its database and code,
// but exposes these as "server" objects through the public API.
//
// Migration lifecycle and cleanup:
//   - Migrations are automatically deleted when their associated server is deleted
//     (see Nova source: https://github.com/openstack/nova/blob/1508cb39a2b12ef2d4f706b9c303a744ce40e707/nova/db/main/api.py#L1337-L1358)
//   - In SAP Cloud Infrastructure's Nova fork, orphaned migrations are purged after 3 weeks
//   - This means historical migration data has limited retention
func (api *novaAPI) GetAllMigrations(ctx context.Context) ([]Migration, error) {
	label := Migration{}.TableName()
	slog.Info("fetching nova data", "label", label)
	// Note: currently we need to fetch this without gophercloud.
	// See: https://github.com/gophercloud/gophercloud/pull/3244
	if api.mon.RequestTimer != nil {
		hist := api.mon.RequestTimer.WithLabelValues(label)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}
	initialURL := api.sc.Endpoint + "os-migrations"
	var nextURL = &initialURL
	var migrations []Migration
	seen := make(map[int]struct{})
	for nextURL != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, *nextURL, http.NoBody)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Auth-Token", api.sc.Token())
		// Needed for changes-since, user_id, and project_id.
		req.Header.Set("X-OpenStack-Nova-API-Version", "2.80")
		resp, err := api.sc.HTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}
		var list struct {
			Migrations []Migration `json:"migrations"`
			Links      []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"migrations_links"`
		}
		err = json.NewDecoder(resp.Body).Decode(&list)
		if err != nil {
			return nil, err
		}
		for _, m := range list.Migrations {
			if _, ok := seen[m.ID]; ok {
				slog.Warn("skipping duplicate migration", "id", m.ID)
				continue
			}
			seen[m.ID] = struct{}{}
			migrations = append(migrations, m)
		}
		nextURL = nil
		for _, link := range list.Links {
			if link.Rel == "next" {
				nextURL = &link.Href
				break
			}
		}
	}
	slog.Info("fetched", "label", label, "count", len(migrations))
	return migrations, nil
}

func (api *novaAPI) GetAllAggregates(ctx context.Context) ([]Aggregate, error) {
	label := Aggregate{}.TableName()
	slog.Info("fetching nova data", "label", label)

	pages, err := func() (pagination.Page, error) {
		if api.mon.RequestTimer != nil {
			hist := api.mon.RequestTimer.WithLabelValues(label)
			timer := prometheus.NewTimer(hist)
			defer timer.ObserveDuration()
		}
		return aggregates.List(api.sc).AllPages(ctx)
	}()
	if err != nil {
		return nil, err
	}

	// Parse the json data into our custom model.
	type AggregatesPage struct {
		Aggregate []RawAggregate `json:"aggregates"`
	}

	data := &AggregatesPage{}
	if err := pages.(aggregates.AggregatesPage).ExtractInto(data); err != nil {
		return nil, err
	}

	slog.Info("fetched", "label", label, "count", len(data.Aggregate))

	aggregates := []Aggregate{}

	// Convert RawAggregate to Aggregate
	for _, rawAggregate := range data.Aggregate {
		properties, err := json.Marshal(rawAggregate.Metadata)
		if err != nil {
			slog.Warn(
				"failed to marshal aggregate properties",
				"aggregate", rawAggregate.UUID, "error", err,
			)
			properties = []byte{}
		}
		if len(rawAggregate.Hosts) == 0 {
			// If no host is assigned to the aggregate, add it as empty.
			aggregates = append(aggregates, Aggregate{
				UUID:             rawAggregate.UUID,
				Name:             rawAggregate.Name,
				AvailabilityZone: rawAggregate.AvailabilityZone,
				ComputeHost:      nil,
				Metadata:         string(properties),
			})
		}
		for _, host := range rawAggregate.Hosts {
			computeHost := host // Make it safe.
			aggregates = append(aggregates, Aggregate{
				UUID:             rawAggregate.UUID,
				Name:             rawAggregate.Name,
				AvailabilityZone: rawAggregate.AvailabilityZone,
				ComputeHost:      &computeHost,
				Metadata:         string(properties),
			})
		}
	}
	return aggregates, nil
}

// GetAllImages fetches all Glance images and returns them with pre-computed os_type.
// See deriveOSType for the derivation logic.
func (api *novaAPI) GetAllImages(ctx context.Context) ([]Image, error) {
	var result []Image
	opts := glanceimages.ListOpts{Limit: 1000}
	err := glanceimages.List(api.glance, opts).EachPage(ctx, func(_ context.Context, page pagination.Page) (bool, error) {
		imgs, err := glanceimages.ExtractImages(page)
		if err != nil {
			return false, err
		}
		for _, img := range imgs {
			result = append(result, Image{
				ID:     img.ID,
				OSType: deriveOSType(img.Properties, img.Tags),
			})
		}
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list Glance images: %w", err)
	}
	return result, nil
}

// deriveOSType computes os_type from image properties and tags.
// Mirrors the logic of OSTypeProber.findFromImage in github.com/sapcc/go-bits/liquidapi,
// with two intentional simplifications:
//  1. No regex validation on vmware_ostype — Nova validates that field at VM boot time,
//     so any value stored in Glance is already valid.
//  2. Volume-booted VMs are not yet supported — os_type will be "unknown" for them.
//     Supporting them would require per-VM Cinder calls (volume_image_metadata.vmware_ostype)
//     either at server sync time or via a dedicated datasource.
func deriveOSType(properties map[string]any, tags []string) string {
	if v, ok := properties["vmware_ostype"]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	var osType string
	for _, tag := range tags {
		if after, ok := strings.CutPrefix(tag, "ostype:"); ok {
			if osType == "" {
				osType = after
			} else {
				// multiple ostype: tags → ambiguous, fall through to unknown
				osType = ""
				break
			}
		}
	}
	if osType != "" {
		return osType
	}
	return "unknown"
}
