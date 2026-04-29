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
	"os"
	"time"

	"github.com/cobaltcore-dev/cortex/pkg/resourcelock"
	"github.com/gophercloud/gophercloud/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const configMapKeyResourceClasses = "resource_classes"

// resourceClassesListResponse matches the OpenStack Placement GET /resource_classes response.
type resourceClassesListResponse struct {
	ResourceClasses []resourceClassEntry `json:"resource_classes"`
}

type resourceClassEntry struct {
	Name string `json:"name"`
}

// ResourceClassSyncer manages the lifecycle of the resource classes ConfigMap.
// It creates the ConfigMap on startup and periodically syncs from upstream.
type ResourceClassSyncer struct {
	client          client.Client
	configMapName   string
	namespace       string
	placementClient *gophercloud.ServiceClient
	resourceLocker  *resourcelock.ResourceLocker
}

func NewResourceClassSyncer(
	cl client.Client,
	configMapName string,
	namespace string,
	placementClient *gophercloud.ServiceClient,
	resourceLocker *resourcelock.ResourceLocker,
) *ResourceClassSyncer {

	return &ResourceClassSyncer{
		client:          cl,
		configMapName:   configMapName,
		namespace:       namespace,
		placementClient: placementClient,
		resourceLocker:  resourceLocker,
	}
}

// Init creates the resource classes ConfigMap if it does not already exist.
func (rs *ResourceClassSyncer) Init(ctx context.Context) error {
	log := ctrl.Log.WithName("placement-shim").WithName("resource-class-syncer")
	cm := &corev1.ConfigMap{}
	key := client.ObjectKey{Namespace: rs.namespace, Name: rs.configMapName}
	err := rs.client.Get(ctx, key, cm)
	if err == nil {
		log.Info("Resource classes ConfigMap already exists", "name", rs.configMapName)
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("checking resource classes configmap: %w", err)
	}
	cm = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rs.configMapName,
			Namespace: rs.namespace,
		},
		Data: map[string]string{configMapKeyResourceClasses: "[]"},
	}
	if err := rs.client.Create(ctx, cm); err != nil {
		if apierrors.IsAlreadyExists(err) {
			log.Info("Resource classes ConfigMap was created concurrently", "name", rs.configMapName)
			return nil
		}
		return fmt.Errorf("creating resource classes configmap: %w", err)
	}
	log.Info("Created resource classes ConfigMap", "name", rs.configMapName)
	return nil
}

// Run starts the periodic background sync from upstream placement.
// Blocks until ctx is cancelled.
func (rs *ResourceClassSyncer) Run(ctx context.Context) {
	log := ctrl.Log.WithName("placement-shim").WithName("resource-class-syncer")
	if rs.placementClient == nil {
		log.Info("No placement service client configured, resource class sync loop will not run")
		return
	}

	jitter := time.Duration(rand.Int63n(int64(30 * time.Second))) //nolint:gosec
	log.Info("Starting resource class sync loop", "jitter", jitter)

	select {
	case <-ctx.Done():
		return
	case <-time.After(jitter):
	}

	rs.sync(ctx)

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rs.sync(ctx)
		}
	}
}

// sync fetches GET /resource_classes from upstream placement and writes the
// result into the ConfigMap under the resource lock.
func (rs *ResourceClassSyncer) sync(ctx context.Context) {
	log := ctrl.Log.WithName("placement-shim").WithName("resource-class-syncer")
	u, err := url.JoinPath(rs.placementClient.Endpoint, "/resource_classes")
	if err != nil {
		log.Error(err, "Failed to build upstream resource classes URL")
		return
	}
	resp, err := rs.placementClient.Request(ctx, http.MethodGet, u, &gophercloud.RequestOpts{
		OkCodes: []int{http.StatusOK},
		MoreHeaders: map[string]string{
			"OpenStack-API-Version": "placement 1.7",
		},
		KeepResponseBody: true,
	})
	if err != nil {
		log.Info("Upstream resource class sync failed", "error", err.Error())
		return
	}
	defer resp.Body.Close()
	var body resourceClassesListResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		log.Error(err, "Failed to decode upstream resource class list")
		return
	}

	host, err := os.Hostname()
	if err != nil {
		log.Error(err, "Failed to get hostname for resource class sync lock")
		return
	}
	lockerID := fmt.Sprintf("syncer-%s-%d", host, time.Now().UnixNano())
	lockName := rs.configMapName + "-lock"
	if err := rs.resourceLocker.AcquireLock(ctx, lockName, lockerID); err != nil {
		log.Error(err, "Failed to acquire lock for resource class sync")
		return
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := rs.resourceLocker.ReleaseLock(releaseCtx, lockName, lockerID); err != nil {
			log.Error(err, "Failed to release lock after resource class sync")
		}
	}()

	cm := &corev1.ConfigMap{}
	if err := rs.client.Get(ctx, client.ObjectKey{Namespace: rs.namespace, Name: rs.configMapName}, cm); err != nil {
		log.Error(err, "Failed to get resource classes ConfigMap for sync")
		return
	}
	rcSet := make(map[string]struct{}, len(body.ResourceClasses))
	for _, rc := range body.ResourceClasses {
		rcSet[rc.Name] = struct{}{}
	}
	if err := writeResourceClassesToConfigMap(cm, rcSet); err != nil {
		log.Error(err, "Failed to serialize synced resource classes")
		return
	}
	if err := rs.client.Update(ctx, cm); err != nil {
		log.Error(err, "Failed to update resource classes ConfigMap with upstream data")
		return
	}
	log.Info("Synced resource classes from upstream placement", "count", len(body.ResourceClasses))
}
