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
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/gophercloud/gophercloud/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const configMapKeyTraits = "traits"

func (s *Shim) staticTraitsConfigMapKey() client.ObjectKey {
	return client.ObjectKey{
		Namespace: os.Getenv("POD_NAMESPACE"),
		Name:      s.config.Traits.ConfigMapName,
	}
}

func (s *Shim) customTraitsConfigMapKey() client.ObjectKey {
	return client.ObjectKey{
		Namespace: os.Getenv("POD_NAMESPACE"),
		Name:      s.config.Traits.ConfigMapName + "-custom",
	}
}

func (s *Shim) traitsLockName() string {
	return s.config.Traits.ConfigMapName + "-custom-lock"
}

// traitsListResponse matches the OpenStack Placement GET /traits response.
type traitsListResponse struct {
	Traits []string `json:"traits"`
}

// HandleListTraits handles GET /traits requests.
//
// Returns a sorted list of trait strings merged from the static (Helm-managed)
// and dynamic (CUSTOM_*) ConfigMaps. Supports optional query parameter "name"
// for filtering: "in:TRAIT_A,TRAIT_B" returns only named traits,
// "startswith:CUSTOM_" returns prefix matches.
//
// See: https://docs.openstack.org/api-ref/placement/#list-traits
func (s *Shim) HandleListTraits(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	switch s.config.Features.Traits.orDefault() {
	case FeatureModePassthrough, FeatureModeHybrid:
		s.forward(w, r)
		return
	case FeatureModeCRD:
		// Serve from local ConfigMaps.
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
		return
	}

	traitSet, err := s.getAllTraits(ctx)
	if err != nil {
		log.Error(err, "failed to list traits from configmaps")
		http.Error(w, "failed to list traits", http.StatusInternalServerError)
		return
	}

	all := make([]string, 0, len(traitSet))
	for t := range traitSet {
		all = append(all, t)
	}
	sort.Strings(all)

	nameFilter := r.URL.Query().Get("name")
	if nameFilter == "" {
		log.Info("listing all traits", "count", len(all))
		s.writeJSON(w, http.StatusOK, traitsListResponse{Traits: all})
		return
	}
	if after, ok := strings.CutPrefix(nameFilter, "in:"); ok {
		wanted := make(map[string]struct{})
		for n := range strings.SplitSeq(after, ",") {
			wanted[n] = struct{}{}
		}
		filtered := make([]string, 0, len(wanted))
		for _, t := range all {
			if _, ok := wanted[t]; ok {
				filtered = append(filtered, t)
			}
		}
		log.Info("listing traits with in: filter", "filter", nameFilter, "count", len(filtered))
		s.writeJSON(w, http.StatusOK, traitsListResponse{Traits: filtered})
		return
	}
	if after, ok := strings.CutPrefix(nameFilter, "startswith:"); ok {
		filtered := make([]string, 0)
		for _, t := range all {
			if strings.HasPrefix(t, after) {
				filtered = append(filtered, t)
			}
		}
		log.Info("listing traits with startswith: filter", "filter", nameFilter, "count", len(filtered))
		s.writeJSON(w, http.StatusOK, traitsListResponse{Traits: filtered})
		return
	}
	log.Info("listing all traits, unrecognized filter ignored", "filter", nameFilter, "count", len(all))
	s.writeJSON(w, http.StatusOK, traitsListResponse{Traits: all})
}

// HandleShowTrait handles GET /traits/{name} requests.
//
// Checks whether a trait with the given name exists in either the static
// or dynamic ConfigMap. Returns 204 No Content if found, 404 Not Found otherwise.
//
// See: https://docs.openstack.org/api-ref/placement/#show-traits
func (s *Shim) HandleShowTrait(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	switch s.config.Features.Traits.orDefault() {
	case FeatureModePassthrough, FeatureModeHybrid:
		s.forward(w, r)
		return
	case FeatureModeCRD:
		// Serve from local ConfigMaps.
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
		return
	}

	name, ok := requiredPathParam(w, r, "name")
	if !ok {
		return
	}
	found, err := s.hasTrait(ctx, name)
	if err != nil {
		log.Error(err, "failed to check trait", "trait", name)
		http.Error(w, "failed to check trait", http.StatusInternalServerError)
		return
	}
	if !found {
		log.Info("trait not found", "trait", name)
		http.Error(w, "trait not found", http.StatusNotFound)
		return
	}
	log.Info("trait found", "trait", name)
	w.WriteHeader(http.StatusNoContent)
}

// HandleUpdateTrait handles PUT /traits/{name} requests.
//
// Creates a new custom trait in the dynamic ConfigMap. Only traits prefixed
// with CUSTOM_ may be created. Returns 201 Created if the trait is newly
// inserted, or 204 No Content if it already exists (in either ConfigMap).
// Returns 400 Bad Request if the name does not carry the CUSTOM_ prefix.
//
// See: https://docs.openstack.org/api-ref/placement/#update-trait
func (s *Shim) HandleUpdateTrait(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	switch s.config.Features.Traits.orDefault() {
	case FeatureModePassthrough, FeatureModeHybrid:
		s.forward(w, r)
		return
	case FeatureModeCRD:
		// Serve from local ConfigMaps.
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
		return
	}

	name, ok := requiredPathParam(w, r, "name")
	if !ok {
		return
	}
	if !strings.HasPrefix(name, "CUSTOM_") {
		log.Info("rejected trait without CUSTOM_ prefix", "trait", name)
		http.Error(w, "trait name must start with CUSTOM_", http.StatusBadRequest)
		return
	}

	// Fast path: trait already exists in either ConfigMap (no lock needed).
	allTraits, err := s.getAllTraits(ctx)
	if err != nil {
		log.Error(err, "failed to read traits for existence check", "trait", name)
		http.Error(w, "failed to create trait", http.StatusInternalServerError)
		return
	}
	if _, exists := allTraits[name]; exists {
		log.Info("trait already exists, nothing to do", "trait", name)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Slow path: acquire lock, read/create dynamic ConfigMap, add trait.
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	lockerID := fmt.Sprintf("shim-%s-%d", host, time.Now().UnixNano())
	if err := s.resourceLocker.AcquireLock(ctx, s.traitsLockName(), lockerID); err != nil {
		log.Error(err, "failed to acquire traits lock", "trait", name)
		http.Error(w, "failed to create trait", http.StatusInternalServerError)
		return
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.resourceLocker.ReleaseLock(releaseCtx, s.traitsLockName(), lockerID); err != nil {
			log.Error(err, "failed to release traits lock")
		}
	}()

	cm := &corev1.ConfigMap{}
	err = s.Get(ctx, s.customTraitsConfigMapKey(), cm)
	if apierrors.IsNotFound(err) {
		// Dynamic ConfigMap does not exist yet — create it with the new trait.
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      s.customTraitsConfigMapKey().Name,
				Namespace: s.customTraitsConfigMapKey().Namespace,
			},
			Data: map[string]string{configMapKeyTraits: "[]"},
		}
		current := map[string]struct{}{name: {}}
		if err := s.writeTraits(cm, current); err != nil {
			log.Error(err, "failed to serialize traits", "trait", name)
			http.Error(w, "failed to create trait", http.StatusInternalServerError)
			return
		}
		if err := s.Create(ctx, cm); err != nil {
			log.Error(err, "failed to create custom traits configmap", "trait", name)
			http.Error(w, "failed to create trait", http.StatusInternalServerError)
			return
		}
		log.Info("created custom traits configmap with new trait", "trait", name)
		s.syncTraitToUpstream(ctx, name, r.Header)
		w.WriteHeader(http.StatusCreated)
		return
	}
	if err != nil {
		log.Error(err, "failed to get custom traits configmap", "trait", name)
		http.Error(w, "failed to create trait", http.StatusInternalServerError)
		return
	}

	current, err := parseTraits(cm)
	if err != nil {
		log.Error(err, "failed to parse custom traits configmap", "trait", name)
		http.Error(w, "failed to create trait", http.StatusInternalServerError)
		return
	}
	if _, exists := current[name]; exists {
		log.Info("trait already exists in custom configmap after lock acquisition", "trait", name)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	current[name] = struct{}{}
	if err := s.writeTraits(cm, current); err != nil {
		log.Error(err, "failed to serialize traits", "trait", name)
		http.Error(w, "failed to create trait", http.StatusInternalServerError)
		return
	}
	if err := s.Update(ctx, cm); err != nil {
		log.Error(err, "failed to update custom traits configmap", "trait", name)
		http.Error(w, "failed to create trait", http.StatusInternalServerError)
		return
	}
	log.Info("added custom trait to configmap", "trait", name)
	s.syncTraitToUpstream(ctx, name, r.Header)
	w.WriteHeader(http.StatusCreated)
}

// HandleDeleteTrait handles DELETE /traits/{name} requests.
//
// Deletes a custom trait from the dynamic ConfigMap. Standard traits (those
// without the CUSTOM_ prefix) cannot be deleted and return 400 Bad Request.
// Returns 404 if the trait does not exist. Returns 204 No Content on success.
//
// See: https://docs.openstack.org/api-ref/placement/#delete-traits
func (s *Shim) HandleDeleteTrait(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	switch s.config.Features.Traits.orDefault() {
	case FeatureModePassthrough, FeatureModeHybrid:
		s.forward(w, r)
		return
	case FeatureModeCRD:
		// Serve from local ConfigMaps.
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
		return
	}

	name, ok := requiredPathParam(w, r, "name")
	if !ok {
		return
	}
	if !strings.HasPrefix(name, "CUSTOM_") {
		log.Info("rejected deletion of standard trait", "trait", name)
		http.Error(w, "cannot delete standard traits", http.StatusBadRequest)
		return
	}

	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	lockerID := fmt.Sprintf("shim-%s-%d", host, time.Now().UnixNano())
	if err := s.resourceLocker.AcquireLock(ctx, s.traitsLockName(), lockerID); err != nil {
		log.Error(err, "failed to acquire traits lock", "trait", name)
		http.Error(w, "failed to delete trait", http.StatusInternalServerError)
		return
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.resourceLocker.ReleaseLock(releaseCtx, s.traitsLockName(), lockerID); err != nil {
			log.Error(err, "failed to release traits lock")
		}
	}()

	cm := &corev1.ConfigMap{}
	err = s.Get(ctx, s.customTraitsConfigMapKey(), cm)
	if apierrors.IsNotFound(err) {
		log.Info("custom traits configmap not found, trait does not exist", "trait", name)
		http.Error(w, "trait not found", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Error(err, "failed to get custom traits configmap", "trait", name)
		http.Error(w, "failed to delete trait", http.StatusInternalServerError)
		return
	}
	current, err := parseTraits(cm)
	if err != nil {
		log.Error(err, "failed to parse custom traits configmap", "trait", name)
		http.Error(w, "failed to delete trait", http.StatusInternalServerError)
		return
	}
	if _, exists := current[name]; !exists {
		log.Info("trait not found in custom configmap", "trait", name)
		http.Error(w, "trait not found", http.StatusNotFound)
		return
	}
	delete(current, name)
	if err := s.writeTraits(cm, current); err != nil {
		log.Error(err, "failed to serialize traits", "trait", name)
		http.Error(w, "failed to delete trait", http.StatusInternalServerError)
		return
	}
	if err := s.Update(ctx, cm); err != nil {
		log.Error(err, "failed to update custom traits configmap", "trait", name)
		http.Error(w, "failed to delete trait", http.StatusInternalServerError)
		return
	}
	log.Info("deleted custom trait from configmap", "trait", name)
	w.WriteHeader(http.StatusNoContent)
}

// getStaticTraits reads traits from the Helm-managed static ConfigMap.
func (s *Shim) getStaticTraits(ctx context.Context) (map[string]struct{}, error) {
	cm := &corev1.ConfigMap{}
	if err := s.Get(ctx, s.staticTraitsConfigMapKey(), cm); err != nil {
		return nil, fmt.Errorf("get static configmap %s: %w", s.config.Traits.ConfigMapName, err)
	}
	return parseTraits(cm)
}

// getCustomTraits reads traits from the dynamic ConfigMap created by the shim.
// Returns an empty set if the ConfigMap does not exist yet.
func (s *Shim) getCustomTraits(ctx context.Context) (map[string]struct{}, error) {
	cm := &corev1.ConfigMap{}
	err := s.Get(ctx, s.customTraitsConfigMapKey(), cm)
	if apierrors.IsNotFound(err) {
		return make(map[string]struct{}), nil
	}
	if err != nil {
		return nil, fmt.Errorf("get custom configmap %s-custom: %w", s.config.Traits.ConfigMapName, err)
	}
	return parseTraits(cm)
}

// getAllTraits merges static and custom traits into a single set.
func (s *Shim) getAllTraits(ctx context.Context) (map[string]struct{}, error) {
	static, err := s.getStaticTraits(ctx)
	if err != nil {
		return nil, err
	}
	custom, err := s.getCustomTraits(ctx)
	if err != nil {
		return nil, err
	}
	for t := range custom {
		static[t] = struct{}{}
	}
	return static, nil
}

// parseTraits extracts the trait set from a ConfigMap.
func parseTraits(cm *corev1.ConfigMap) (map[string]struct{}, error) {
	raw, ok := cm.Data[configMapKeyTraits]
	if !ok || raw == "" {
		return make(map[string]struct{}), nil
	}
	var traits []string
	if err := json.Unmarshal([]byte(raw), &traits); err != nil {
		return nil, fmt.Errorf("unmarshal traits from configmap: %w", err)
	}
	m := make(map[string]struct{}, len(traits))
	for _, t := range traits {
		m[t] = struct{}{}
	}
	return m, nil
}

func (s *Shim) hasTrait(ctx context.Context, name string) (bool, error) {
	traits, err := s.getAllTraits(ctx)
	if err != nil {
		return false, err
	}
	_, ok := traits[name]
	return ok, nil
}

// writeTraits serializes the trait set into the ConfigMap's data field.
func (s *Shim) writeTraits(cm *corev1.ConfigMap, traitSet map[string]struct{}) error {
	traits := make([]string, 0, len(traitSet))
	for t := range traitSet {
		traits = append(traits, t)
	}
	sort.Strings(traits)

	data, err := json.Marshal(traits)
	if err != nil {
		return fmt.Errorf("marshal traits: %w", err)
	}
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data[configMapKeyTraits] = string(data)
	return nil
}

// syncTraitToUpstream best-effort creates the trait in upstream placement so
// that endpoints forwarded to upstream (e.g. PUT /resource_providers/{uuid}/traits)
// can reference locally-created custom traits. Errors are logged but never
// propagated — upstream may be unreachable and that is acceptable.
func (s *Shim) syncTraitToUpstream(ctx context.Context, name string, incomingHeader http.Header) {
	log := logf.FromContext(ctx)
	if s.httpClient == nil {
		log.V(1).Info("skipping upstream trait sync, no http client configured", "trait", name)
		return
	}
	u, err := url.Parse(s.config.PlacementURL)
	if err != nil {
		log.Error(err, "failed to parse placement URL for trait sync", "trait", name)
		return
	}
	u.Path, err = url.JoinPath(u.Path, "/traits/"+name)
	if err != nil {
		log.Error(err, "failed to build upstream trait URL", "trait", name)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), http.NoBody)
	if err != nil {
		log.Error(err, "failed to create upstream trait request", "trait", name)
		return
	}
	// Forward authentication headers so upstream placement accepts the request.
	req.Header = incomingHeader.Clone()
	resp, err := s.httpClient.Do(req)
	if err != nil {
		log.Info("best-effort upstream trait sync failed, upstream may be down", "trait", name, "error", err.Error())
		return
	}
	defer resp.Body.Close()
	log.Info("synced custom trait to upstream placement", "trait", name, "status", resp.StatusCode)
}

// startTraitSyncLoop runs a periodic goroutine that fetches traits from
// upstream placement and writes them into the static ConfigMap. Only active
// when features.traits is hybrid. The loop exits when ctx is cancelled.
func (s *Shim) startTraitSyncLoop(ctx context.Context) {
	if s.config.Features.Traits.orDefault() != FeatureModeHybrid {
		return
	}
	log := ctrl.Log.WithName("placement-shim").WithName("trait-sync")
	jitter := time.Duration(rand.Int63n(int64(30 * time.Second))) //nolint:gosec
	log.Info("starting trait sync loop", "jitter", jitter)

	select {
	case <-ctx.Done():
		return
	case <-time.After(jitter):
	}

	s.syncTraitsFromUpstream(ctx, log)

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.syncTraitsFromUpstream(ctx, log)
		}
	}
}

// syncTraitsFromUpstream fetches GET /traits from upstream placement and
// writes the result into the static ConfigMap so that the shim's local
// view stays in sync with upstream. Uses the gophercloud ServiceClient
// for automatic token management (including reauth on 401).
func (s *Shim) syncTraitsFromUpstream(ctx context.Context, log logr.Logger) {
	if s.placementServiceClient == nil {
		log.V(1).Info("skipping upstream trait sync, no placement service client configured")
		return
	}
	u, err := url.JoinPath(s.placementServiceClient.Endpoint, "/traits")
	if err != nil {
		log.Error(err, "failed to build upstream traits URL")
		return
	}
	resp, err := s.placementServiceClient.Request(ctx, http.MethodGet, u, &gophercloud.RequestOpts{
		OkCodes: []int{http.StatusOK},
		MoreHeaders: map[string]string{
			"OpenStack-API-Version": "placement 1.6",
		},
		KeepResponseBody: true,
	})
	if err != nil {
		log.Info("upstream trait sync failed", "error", err.Error())
		return
	}
	defer resp.Body.Close()
	var body traitsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		log.Error(err, "failed to decode upstream trait list")
		return
	}

	cm := &corev1.ConfigMap{}
	if err := s.Get(ctx, s.staticTraitsConfigMapKey(), cm); err != nil {
		log.Error(err, "failed to get static traits configmap for sync")
		return
	}
	traitSet := make(map[string]struct{}, len(body.Traits))
	for _, t := range body.Traits {
		traitSet[t] = struct{}{}
	}
	if err := s.writeTraits(cm, traitSet); err != nil {
		log.Error(err, "failed to serialize synced traits")
		return
	}
	if err := s.Update(ctx, cm); err != nil {
		log.Error(err, "failed to update static traits configmap with upstream data")
		return
	}
	log.Info("synced traits from upstream placement", "count", len(body.Traits))
}
