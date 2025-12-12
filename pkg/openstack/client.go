package openstack

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
)

type OpenstackClient struct {
	keystoneAPI         keystone.KeystoneAPI
	serviceClient       *gophercloud.ServiceClient
	apiVersionHeaderKey string
	apiVersionHeader    string
}

func NovaClient(ctx context.Context, keystoneAPI keystone.KeystoneAPI) (*OpenstackClient, error) {
	if err := keystoneAPI.Authenticate(ctx); err != nil {
		return nil, fmt.Errorf("failed to authenticate keystone: %w", err)
	}
	// Automatically fetch the nova endpoint from the keystone service catalog.
	provider := keystoneAPI.Client()
	serviceType := "compute"
	sameAsKeystone := keystoneAPI.Availability()
	url, err := keystoneAPI.FindEndpoint(sameAsKeystone, serviceType)
	if err != nil {
		return nil, fmt.Errorf("failed to find nova endpoint: %w", err)
	}

	microversion := "2.61"
	slog.Info("using nova endpoint", "url", url)
	serviceClient := &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		// Since microversion 2.53, the hypervisor id and service id is a UUID.
		// We need that to find placement resource providers for hypervisors.
		// Since 2.61, the extra_specs are returned in the flavor details.
		Microversion: microversion,
	}
	return &OpenstackClient{
		keystoneAPI:         keystoneAPI,
		serviceClient:       serviceClient,
		apiVersionHeaderKey: "X-OpenStack-Nova-API-Version",
		apiVersionHeader:    microversion,
	}, nil
}

func ManilaClient(ctx context.Context, keystoneAPI keystone.KeystoneAPI) (*OpenstackClient, error) {
	if err := keystoneAPI.Authenticate(ctx); err != nil {
		return nil, fmt.Errorf("failed to authenticate keystone: %w", err)
	}
	// Automatically fetch the nova endpoint from the keystone service catalog.
	provider := keystoneAPI.Client()

	gophercloud.ServiceTypeAliases["shared-file-system"] = []string{"sharev2"}
	manilaSC, err := openstack.NewSharedFileSystemV2(provider, gophercloud.EndpointOpts{
		Type:         "sharev2",
		Availability: gophercloud.Availability(keystoneAPI.Availability()),
	})
	if err != nil {
		return nil, err
	}

	microversion := "2.65"
	manilaSC.Microversion = microversion

	slog.Info("using manila endpoint", "url", manilaSC.Endpoint)
	return &OpenstackClient{
		keystoneAPI:         keystoneAPI,
		serviceClient:       manilaSC,
		apiVersionHeaderKey: "X-OpenStack-Manila-API-Version",
		apiVersionHeader:    microversion,
	}, nil
}

func CinderClient(ctx context.Context, keystoneAPI keystone.KeystoneAPI) (*OpenstackClient, error) {
	if err := keystoneAPI.Authenticate(ctx); err != nil {
		return nil, fmt.Errorf("failed to authenticate keystone: %w", err)
	}
	// Automatically fetch the nova endpoint from the keystone service catalog.
	provider := keystoneAPI.Client()
	serviceType := "volumev3"
	sameAsKeystone := keystoneAPI.Availability()
	url, err := keystoneAPI.FindEndpoint(sameAsKeystone, serviceType)
	if err != nil {
		return nil, fmt.Errorf("failed to find cinder endpoint: %w", err)
	}

	microversion := "3.70"
	slog.Info("using cinder endpoint", "url", url)
	serviceClient := &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		Microversion:   microversion,
	}
	return &OpenstackClient{
		keystoneAPI:         keystoneAPI,
		serviceClient:       serviceClient,
		apiVersionHeaderKey: "OpenStack-API-Version",
		apiVersionHeader:    fmt.Sprintf("volume %s", microversion),
	}, nil
}

func (c *OpenstackClient) List(ctx context.Context, path string, query url.Values, resource string, result interface{}) error {
	// Generate url for the request
	baseURL, err := url.Parse(c.serviceClient.Endpoint)
	if err != nil {
		return err
	}
	baseURL.Path = strings.TrimSuffix(baseURL.Path, "/") + "/" + strings.TrimPrefix(path, "/")
	baseURL.RawQuery = query.Encode()

	initialURL := baseURL.String()
	nextURL := &initialURL

	var allItemsRaw []json.RawMessage
	for nextURL != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, *nextURL, http.NoBody)
		if err != nil {
			return err
		}
		req.Header.Set("X-Auth-Token", c.keystoneAPI.Client().Token())
		req.Header.Set("OpenStack-API-Version", c.apiVersionHeader)
		resp, err := c.serviceClient.HTTPClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}

		// Parse response als generic map
		var raw map[string]json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			return err
		}

		// Extract items
		var items []json.RawMessage
		if err := json.Unmarshal(raw[resource], &items); err != nil {
			return err
		}
		allItemsRaw = append(allItemsRaw, items...)

		// Extract next link
		var links []struct {
			Rel  string `json:"rel"`
			Href string `json:"href"`
		}
		linksKey := resource + "_links"
		if linksData, ok := raw[linksKey]; ok {
			json.Unmarshal(linksData, &links)
		}

		nextURL = nil
		for _, link := range links {
			if link.Rel == "next" {
				nextURL = &link.Href
				break
			}
		}
	}
	allItemsJSON, err := json.Marshal(allItemsRaw)
	if err != nil {
		return err
	}
	return json.Unmarshal(allItemsJSON, result)
}
