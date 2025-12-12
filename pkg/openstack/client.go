// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/gophercloud/gophercloud/v2"
)

type OpenstackClient struct {
	keystoneAPI   keystone.KeystoneAPI
	serviceClient *gophercloud.ServiceClient
	// Http Request headers for openstack api microversion, eg. "OpenStack-API-Version"
	apiVersionHeaderKey *string
	// Http Request header value for openstack api microversion, eg. "volume 3.70"
	apiVersionHeader *string
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
		if c.apiVersionHeaderKey != nil && c.apiVersionHeader != nil {
			req.Header.Set(*c.apiVersionHeaderKey, *c.apiVersionHeader)
		}
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
			if err := json.Unmarshal(linksData, &links); err != nil {
				return err
			}
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
