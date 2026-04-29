// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TraitSyncer manages the lifecycle of the single traits ConfigMap.
// It creates the ConfigMap on startup and periodically syncs from upstream.
type TraitSyncer struct {
	client          client.Client
	configMapName   string
	namespace       string
	placementClient *gophercloud.ServiceClient
}

func NewTraitSyncer(
	cl client.Client,
	configMapName string,
	namespace string,
	placementClient *gophercloud.ServiceClient,
) *TraitSyncer {

	return &TraitSyncer{
		client:          cl,
		configMapName:   configMapName,
		namespace:       namespace,
		placementClient: placementClient,
	}
}

// Init creates the traits ConfigMap if it does not already exist.
func (ts *TraitSyncer) Init(ctx context.Context) error {
	log := ctrl.Log.WithName("placement-shim").WithName("trait-syncer")
	cm := &corev1.ConfigMap{}
	key := client.ObjectKey{Namespace: ts.namespace, Name: ts.configMapName}
	err := ts.client.Get(ctx, key, cm)
	if err == nil {
		log.Info("Traits ConfigMap already exists", "name", ts.configMapName)
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("checking traits configmap: %w", err)
	}
	cm = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ts.configMapName,
			Namespace: ts.namespace,
		},
		Data: map[string]string{configMapKeyTraits: "[]"},
	}
	if err := ts.client.Create(ctx, cm); err != nil {
		if apierrors.IsAlreadyExists(err) {
			log.Info("Traits ConfigMap was created concurrently", "name", ts.configMapName)
			return nil
		}
		return fmt.Errorf("creating traits configmap: %w", err)
	}
	log.Info("Created traits ConfigMap", "name", ts.configMapName)
	return nil
}

// Run starts the periodic background sync from upstream placement.
// Blocks until ctx is cancelled.
func (ts *TraitSyncer) Run(ctx context.Context) {
	log := ctrl.Log.WithName("placement-shim").WithName("trait-syncer")
	if ts.placementClient == nil {
		log.Info("No placement service client configured, trait sync loop will not run")
		return
	}

	jitter := time.Duration(rand.Int63n(int64(30 * time.Second))) //nolint:gosec
	log.Info("Starting trait sync loop", "jitter", jitter)

	select {
	case <-ctx.Done():
		return
	case <-time.After(jitter):
	}

	ts.sync(ctx)

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ts.sync(ctx)
		}
	}
}

// sync fetches GET /traits from upstream placement and writes the result
// into the ConfigMap.
func (ts *TraitSyncer) sync(ctx context.Context) {
	log := ctrl.Log.WithName("placement-shim").WithName("trait-syncer")
	u, err := url.JoinPath(ts.placementClient.Endpoint, "/traits")
	if err != nil {
		log.Error(err, "Failed to build upstream traits URL")
		return
	}
	resp, err := ts.placementClient.Request(ctx, http.MethodGet, u, &gophercloud.RequestOpts{
		OkCodes: []int{http.StatusOK},
		MoreHeaders: map[string]string{
			"OpenStack-API-Version": "placement 1.6",
		},
		KeepResponseBody: true,
	})
	if err != nil {
		log.Info("Upstream trait sync failed", "error", err.Error())
		return
	}
	defer resp.Body.Close()
	var body traitsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		log.Error(err, "Failed to decode upstream trait list")
		return
	}

	cm := &corev1.ConfigMap{}
	if err := ts.client.Get(ctx, client.ObjectKey{Namespace: ts.namespace, Name: ts.configMapName}, cm); err != nil {
		log.Error(err, "Failed to get traits ConfigMap for sync")
		return
	}
	traitSet := make(map[string]struct{}, len(body.Traits))
	for _, t := range body.Traits {
		traitSet[t] = struct{}{}
	}
	if err := writeTraitsToConfigMap(cm, traitSet); err != nil {
		log.Error(err, "Failed to serialize synced traits")
		return
	}
	if err := ts.client.Update(ctx, cm); err != nil {
		log.Error(err, "Failed to update traits ConfigMap with upstream data")
		return
	}
	log.Info("Synced traits from upstream placement", "count", len(body.Traits))
}
