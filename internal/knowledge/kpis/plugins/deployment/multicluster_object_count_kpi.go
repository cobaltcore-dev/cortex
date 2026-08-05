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
	gvk schema.GroupVersionKind
}

// multiclusterReader is the subset of *multicluster.Client this KPI needs.
// Keeping it narrow lets the KPI logic be unit-tested without wiring real or
// fake clusters into a multicluster.Client.
type multiclusterReader interface {
	ConfiguredRouteLabels(gvk schema.GroupVersionKind) []map[string]string
	ListMetadataPerCluster(ctx context.Context, gvk schema.GroupVersionKind, opts ...client.ListOption) ([]multicluster.ClusterObjectMetadata, error)
}

// MulticlusterObjectCountKPI reports the number of objects of each configured
// GVK per cluster (home and remote), labelled by the cluster's routing labels.
// Home-cluster metrics carry is_home=true with empty routing-label values.
type MulticlusterObjectCountKPI struct {
	plugins.BaseKPI[MulticlusterObjectCountKPIOpts]
	mcl        multiclusterReader
	descs      []gvkDesc
	labelKeys  []string         // global union of snake_case routing-label keys across all GVKs
	sharedDesc *prometheus.Desc // single descriptor shared by all GVKs
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

	// Parse all GVKs, then build the shared descriptor via buildObjectCountSchema.
	var gvks []schema.GroupVersionKind
	for _, raw := range k.Options.GVKs {
		gvk, err := parseGVK(raw)
		if err != nil {
			return fmt.Errorf("invalid GVK %q: %w", raw, err)
		}
		gvks = append(gvks, gvk)
	}

	var err error
	k.labelKeys, k.sharedDesc, err = buildObjectCountSchema(mcl, gvks)
	if err != nil {
		return err
	}

	for _, gvk := range gvks {
		k.descs = append(k.descs, gvkDesc{gvk: gvk})
	}
	return nil
}

func (k *MulticlusterObjectCountKPI) Describe(ch chan<- *prometheus.Desc) {
	if k.sharedDesc != nil {
		ch <- k.sharedDesc
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
			labelVals := make([]string, 0, 4+len(k.labelKeys))
			labelVals = append(labelVals, d.gvk.Group, d.gvk.Version, d.gvk.Kind, isHome)
			for _, key := range k.labelKeys {
				labelVals = append(labelVals, labelValueForSnakeKey(key, c.Labels))
			}
			ch <- prometheus.MustNewConstMetric(k.sharedDesc, prometheus.GaugeValue,
				float64(len(c.Items)), labelVals...)
		}
	}
}

// buildObjectCountSchema computes the union of snake_case routing-label keys
// across all given GVKs and returns the label keys and a single shared
// prometheus.Desc. Fails if any routing-label key collides with a fixed label
// (group, version, kind, is_home).
func buildObjectCountSchema(r multiclusterReader, gvks []schema.GroupVersionKind) ([]string, *prometheus.Desc, error) {
	fixedLabels := map[string]bool{"group": true, "version": true, "kind": true, "is_home": true}
	keySet := map[string]bool{}
	var labelKeys []string
	for _, gvk := range gvks {
		for _, lm := range r.ConfiguredRouteLabels(gvk) {
			for key := range lm {
				snake := toSnakeCase(key)
				if fixedLabels[snake] {
					return nil, nil, fmt.Errorf("routing label key %q collides with fixed label", snake)
				}
				if !keySet[snake] {
					keySet[snake] = true
					labelKeys = append(labelKeys, snake)
				}
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
	return labelKeys, desc, nil
}

// parseGVK parses a "group/version/Kind" string.
func parseGVK(s string) (schema.GroupVersionKind, error) {
	parts := strings.SplitN(s, "/", 3)
	if len(parts) != 3 {
		return schema.GroupVersionKind{}, fmt.Errorf("expected group/version/Kind, got: %s", s)
	}
	if parts[1] == "" || parts[2] == "" {
		return schema.GroupVersionKind{}, fmt.Errorf("expected group/version/Kind, got: %s", s)
	}
	if strings.ContainsRune(parts[2], '/') {
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
