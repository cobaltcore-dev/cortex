// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package deployment

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"unicode"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MulticlusterObjectCountKPIOpts configures which GVKs to count per cluster.
// Each entry is a "group/version/Kind" string, e.g.
// "cortex.cloud/v1alpha1/HypervisorList".
type MulticlusterObjectCountKPIOpts struct {
	GVKs []string `json:"gvks"`
}

type gvkDesc struct {
	gvk       schema.GroupVersionKind
	labelKeys []string // snake_cased routing label keys (stable order)
	desc      *prometheus.Desc
}

// multiclusterReader is the subset of *multicluster.Client this KPI needs.
// Keeping it narrow lets the KPI logic be unit-tested without wiring real or
// fake clusters into a multicluster.Client.
type multiclusterReader interface {
	ConfiguredRouteLabels(gvk schema.GroupVersionKind) []map[string]string
	ListMetadataPerCluster(ctx context.Context, gvk schema.GroupVersionKind, opts ...client.ListOption) ([]multicluster.ClusterObjectMetadata, error)
}

// MulticlusterObjectCountKPI reports the number of objects of each configured
// GVK per remote cluster, labelled by the cluster's routing labels.
type MulticlusterObjectCountKPI struct {
	plugins.BaseKPI[MulticlusterObjectCountKPIOpts]
	mcl   multiclusterReader
	descs []gvkDesc
}

func (MulticlusterObjectCountKPI) GetName() string { return "multicluster_object_count_kpi" }

func (k *MulticlusterObjectCountKPI) Init(_ *db.DB, c client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(nil, c, opts); err != nil {
		return err
	}
	mcl, ok := c.(*multicluster.Client)
	if !ok {
		return fmt.Errorf("multicluster_object_count_kpi requires a *multicluster.Client, got %T", c)
	}
	k.mcl = mcl

	for _, raw := range k.Options.GVKs {
		gvk, err := parseGVK(raw)
		if err != nil {
			return fmt.Errorf("invalid GVK %q: %w", raw, err)
		}
		routeLabels := mcl.ConfiguredRouteLabels(gvk)
		// Each remote cluster is registered with a routing label map (e.g.
		// {"availabilityZone": "eu-de-1a"}). We collect the union of all key names
		// across every remote cluster and snake_case them to produce Prometheus label
		// names (availabilityZone → availability_zone). These become variable labels
		// on the descriptor so each cluster gets its own time series.
		keySet := map[string]bool{}
		var labelKeys []string
		for _, lm := range routeLabels {
			for k := range lm {
				snake := toSnakeCase(k)
				if !keySet[snake] {
					keySet[snake] = true
					labelKeys = append(labelKeys, snake)
				}
			}
		}
		// Fixed labels: group/version/kind identify the resource type; is_home
		// distinguishes the home cluster (no routing labels) from remote clusters.
		// The routing label keys follow (e.g. availability_zone).
		varLabels := append([]string{"group", "version", "kind", "is_home"}, labelKeys...)
		desc := prometheus.NewDesc(
			"cortex_multicluster_object_count",
			"Number of objects of a given GVK per cluster",
			varLabels,
			nil,
		)
		k.descs = append(k.descs, gvkDesc{gvk: gvk, labelKeys: labelKeys, desc: desc})
	}
	return nil
}

func (k *MulticlusterObjectCountKPI) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range k.descs {
		ch <- d.desc
	}
}

func (k *MulticlusterObjectCountKPI) Collect(ch chan<- prometheus.Metric) {
	ctx := context.Background()
	for _, d := range k.descs {
		perCluster, err := k.mcl.ListMetadataPerCluster(ctx, d.gvk)
		if err != nil {
			slog.Error("multicluster_object_count_kpi: failed to list object metadata",
				"gvk", d.gvk, "err", err)
			continue
		}
		for _, c := range perCluster {
			isHome := "false"
			if c.IsHome {
				isHome = "true"
			}
			// Label values must match the descriptor order: group, version, kind,
			// is_home, then one value per routing label key. For remote clusters the
			// routing label value comes from the cluster's registration labels (e.g.
			// Labels["availabilityZone"] → label value for "availability_zone"). The
			// home cluster has no routing labels, so those positions are empty strings.
			labelVals := make([]string, 0, 4+len(d.labelKeys))
			labelVals = append(labelVals, d.gvk.Group, d.gvk.Version, d.gvk.Kind, isHome)
			for _, key := range d.labelKeys {
				labelVals = append(labelVals, labelValueForSnakeKey(key, c.Labels))
			}
			ch <- prometheus.MustNewConstMetric(d.desc, prometheus.GaugeValue,
				float64(len(c.Items)), labelVals...)
		}
	}
}

// parseGVK parses a "group/version/Kind" string.
func parseGVK(s string) (schema.GroupVersionKind, error) {
	parts := strings.SplitN(s, "/", 3)
	if len(parts) != 3 {
		return schema.GroupVersionKind{}, fmt.Errorf("expected group/version/Kind, got: %s", s)
	}
	return schema.GroupVersionKind{Group: parts[0], Version: parts[1], Kind: parts[2]}, nil
}

// toSnakeCase converts camelCase to snake_case (e.g. availabilityZone → availability_zone).
func toSnakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) && i > 0 {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// labelValueForSnakeKey finds the value in labels whose key, when snake_cased,
// matches snakeKey. Returns empty string for nil labels or no match (home cluster).
func labelValueForSnakeKey(snakeKey string, labels map[string]string) string {
	for k, v := range labels {
		if toSnakeCase(k) == snakeKey {
			return v
		}
	}
	return ""
}
