// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import "testing"

func TestValidConf(t *testing.T) {
	content := `
{
  "sync": {
    "prometheus": {
      "hosts": [
        {
          "name": "prometheus",
          "url": "http://prometheus:9090",
          "provides": ["metric_type_1"]
        }
      ],
      "metrics": [
        {
          "alias": "metric_1",
          "query": "metric_1",
          "type": "metric_type_1"
        }
      ]
    },
    "openstack": {
      "nova": {
        "types": ["servers"]
      }
    }
  },
  "extractor": {
    "plugins": [
      {
        "name": "extractor_1",
        "dependencies": {
          "sync": {
            "prometheus": {
              "metrics": [
                {
                  "alias": "metric_1"
                }
              ]
            },
            "openstack": {
              "nova": {
                "types": ["servers"]
              }
            }
          }
        }
      },
      {
        "name": "extractor_2",
        "dependencies": {
          "sync": {
            "prometheus": {
              "metrics": [
                {
                  "type": "metric_type_1"
                }
              ]
            }
          }
        }
      }
    ]
  },
  "scheduler": {
    "plugins": [
      {
        "name": "scheduler_1",
        "dependencies": {
          "extractors": ["extractor_1"]
        }
      },
      {
        "name": "scheduler_2"
      }
    ]
  }
}
`
	conf := newConfigFromBytes([]byte(content))
	if err := conf.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInvalidConf_MissingNovaDependency(t *testing.T) {
	content := `
{
  "sync": {
    "prometheus": {
      "metrics": [
        {
          "alias": "metric_1",
          "query": "metric_1",
          "type": "metric_type_1"
        }
      ]
    }
  },
  "extractor": {
    "plugins": [
      {
        "name": "extractor_1",
        "dependencies": {
          "sync": {
            "prometheus": {
              "metrics": [
                {
                  "alias": "metric_1"
                }
              ]
            },
            "openstack": {
              "nova": {
                "types": [
                  "hypervisors"
                ]
              }
            }
          }
        }
      }
    ]
  }
}
`
	conf := newConfigFromBytes([]byte(content))
	if err := conf.Validate(); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestInvalidConf_MissingResourceProviders(t *testing.T) {
	content := `
{
  "sync": {
    "openstack": {
      "placement": {
        "types": [
          "traits"
        ]
      }
    }
  }
}
`
	conf := newConfigFromBytes([]byte(content))
	if err := conf.Validate(); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestInvalidConf_InvalidServiceAvailability(t *testing.T) {
	content := `
{
  "sync": {
    "openstack": {
      "placement": {
        "availability": "whatever"
      }
    }
  }
}
`
	conf := newConfigFromBytes([]byte(content))
	if err := conf.Validate(); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestInvalidConf_MissingHost(t *testing.T) {
	content := `
{
  "sync": {
    "prometheus": {
      "metrics": [
        {
          "alias": "metric_1",
          "query": "metric_1",
          "type": "metric_type_1"
        }
      ]
    }
  }
}
`
	conf := newConfigFromBytes([]byte(content))
	if err := conf.Validate(); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestInvalidConf_MissingFeatureForKPI(t *testing.T) {
	content := `
{
  "kpis": {
    "plugins": [
      {
        "name": "vm_life_span_kpi",
        "dependencies": {
          "extractors": [
            "extractor_1"
          ]
        }
      }
    ]
  }
}
`
	conf := newConfigFromBytes([]byte(content))
	if len(conf.GetKPIsConfig().Plugins) == 0 {
		t.Fatalf("expected plugins, got none")
	}
	t.Log("conf.GetKPIsConfig().Plugins", conf.GetKPIsConfig().Plugins)
	if err := conf.Validate(); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestInvalidConf_NovaSchedulerDependency(t *testing.T) {
	content := `
{
  "extractor": {
    "plugins": [
      {
        "name": "extractor_1"
      }
    ]
  },
  "scheduler": {
    "nova": { "dependencies": { "extractors": ["extractor_2"] } }
  }
}
`
	conf := newConfigFromBytes([]byte(content))
	if err := conf.Validate(); err == nil {
		t.Fatalf("expected error, got nil")
	}
}
