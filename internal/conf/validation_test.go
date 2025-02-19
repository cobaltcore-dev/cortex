// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import "testing"

func TestValidConf(t *testing.T) {
	content := `
sync:
  prometheus:
    metrics:
      - alias: metric_1
        query: metric_1
        type: metric_type_1
  openstack:
    nova:
      types:
        - servers
features:
  extractors:
    - name: extractor_1
      dependencies:
        sync:
          prometheus:
            metrics:
              - metric_1
          openstack:
            nova:
              types:
                - servers
scheduler:
  steps:
    - name: scheduler_1
      dependencies:
        features:
          extractors:
            - extractor_1
    - name: scheduler_2
`
	conf := newConfigFromBytes([]byte(content))
	if err := conf.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInvalidConf_MissingNovaDependency(t *testing.T) {
	content := `
sync:
  prometheus:
    metrics:
      - alias: metric_1
        query: metric_1
        type: metric_type_1
features:
  extractors:
    - name: extractor_1
      dependencies:
        sync:
          prometheus:
            metrics:
              - metric_1
          openstack:
            nova:
              types:
                # missing dependency
                - hypervisors
`
	conf := newConfigFromBytes([]byte(content))
	if err := conf.Validate(); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestInvalidConf_MissingResourceProviders(t *testing.T) {
	content := `
sync:
  openstack:
    placement:
      types:
        # missing resource_providers
        - traits
`
	conf := newConfigFromBytes([]byte(content))
	if err := conf.Validate(); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestInvalidConf_InvalidServiceAvailability(t *testing.T) {
	content := `
sync:
  openstack:
    placement:
      availability: whatever
`
	conf := newConfigFromBytes([]byte(content))
	if err := conf.Validate(); err == nil {
		t.Fatalf("expected error, got nil")
	}
}
