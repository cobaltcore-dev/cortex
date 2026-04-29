// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const configMapKeyTraits = "traits"

// traitsListResponse matches the OpenStack Placement GET /traits response.
type traitsListResponse struct {
	Traits []string `json:"traits"`
}

// HandleListTraits handles GET /traits requests.
//
// Feature modes:
//   - passthrough: forwards to upstream placement.
//   - hybrid: forwards to upstream placement.
//   - crd: serves the trait list from the local ConfigMap.
//
// See: https://docs.openstack.org/api-ref/placement/#list-traits
func (s *Shim) HandleListTraits(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	switch s.featureModeFromConfOrHeader(r, s.config.Features.Traits) {
	case FeatureModePassthrough, FeatureModeHybrid:
		s.forward(w, r)
		return
	case FeatureModeCRD:
		// Serve from local ConfigMap.
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
		return
	}

	traitSet, err := s.getTraits(ctx)
	if err != nil {
		log.Error(err, "failed to list traits from configmap")
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
// Feature modes:
//   - passthrough: forwards to upstream placement.
//   - hybrid: forwards to upstream placement.
//   - crd: checks the local ConfigMap for the trait.
//
// See: https://docs.openstack.org/api-ref/placement/#show-traits
func (s *Shim) HandleShowTrait(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	switch s.featureModeFromConfOrHeader(r, s.config.Features.Traits) {
	case FeatureModePassthrough, FeatureModeHybrid:
		s.forward(w, r)
		return
	case FeatureModeCRD:
		// Serve from local ConfigMap.
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
// Feature modes:
//   - passthrough: forwards to upstream placement.
//   - hybrid: forwards to upstream; on success, adds the trait to the local ConfigMap.
//   - crd: writes the trait to the local ConfigMap (CUSTOM_ prefix required).
//
// See: https://docs.openstack.org/api-ref/placement/#update-trait
func (s *Shim) HandleUpdateTrait(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	mode := s.featureModeFromConfOrHeader(r, s.config.Features.Traits)
	switch mode {
	case FeatureModePassthrough:
		s.forward(w, r)
		return
	case FeatureModeHybrid:
		s.handleUpdateTraitHybrid(w, r)
		return
	case FeatureModeCRD:
		// Handle locally.
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

	created, err := s.addTraitToConfigMap(ctx, name)
	if err != nil {
		log.Error(err, "failed to create trait", "trait", name)
		http.Error(w, "failed to create trait", http.StatusInternalServerError)
		return
	}
	if created {
		w.WriteHeader(http.StatusCreated)
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleUpdateTraitHybrid forwards PUT /traits/{name} to upstream, then
// updates the local ConfigMap on success.
func (s *Shim) handleUpdateTraitHybrid(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	name, ok := requiredPathParam(w, r, "name")
	if !ok {
		return
	}

	s.forwardWithHook(w, r, func(w http.ResponseWriter, resp *http.Response) {
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		if resp.Body != nil {
			if _, err := io.Copy(w, resp.Body); err != nil {
				log.Error(err, "hybrid: failed to copy upstream response body")
			}
		}

		if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusNoContent {
			if _, err := s.addTraitToConfigMap(ctx, name); err != nil {
				log.Error(err, "hybrid: failed to add trait to local configmap", "trait", name)
			}
		}
	})
}

// HandleDeleteTrait handles DELETE /traits/{name} requests.
//
// Feature modes:
//   - passthrough: forwards to upstream placement.
//   - hybrid: forwards to upstream; on success, removes the trait from the local ConfigMap.
//   - crd: removes the trait from the local ConfigMap (CUSTOM_ prefix required).
//
// See: https://docs.openstack.org/api-ref/placement/#delete-traits
func (s *Shim) HandleDeleteTrait(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	mode := s.featureModeFromConfOrHeader(r, s.config.Features.Traits)
	switch mode {
	case FeatureModePassthrough:
		s.forward(w, r)
		return
	case FeatureModeHybrid:
		s.handleDeleteTraitHybrid(w, r)
		return
	case FeatureModeCRD:
		// Handle locally.
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

	removed, err := s.removeTraitFromConfigMap(ctx, name)
	if err != nil {
		log.Error(err, "failed to delete trait", "trait", name)
		http.Error(w, "failed to delete trait", http.StatusInternalServerError)
		return
	}
	if !removed {
		log.Info("trait not found in configmap", "trait", name)
		http.Error(w, "trait not found", http.StatusNotFound)
		return
	}
	log.Info("deleted trait from configmap", "trait", name)
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteTraitHybrid forwards DELETE /traits/{name} to upstream, then
// updates the local ConfigMap on success.
func (s *Shim) handleDeleteTraitHybrid(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	name, ok := requiredPathParam(w, r, "name")
	if !ok {
		return
	}

	s.forwardWithHook(w, r, func(w http.ResponseWriter, resp *http.Response) {
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		if resp.Body != nil {
			if _, err := io.Copy(w, resp.Body); err != nil {
				log.Error(err, "hybrid: failed to copy upstream response body")
			}
		}

		if resp.StatusCode == http.StatusNoContent {
			if _, err := s.removeTraitFromConfigMap(ctx, name); err != nil {
				log.Error(err, "hybrid: failed to remove trait from local configmap", "trait", name)
			}
		}
	})
}

// getTraits reads traits from the single ConfigMap.
func (s *Shim) getTraits(ctx context.Context) (map[string]struct{}, error) {
	cm := &corev1.ConfigMap{}
	if err := s.Get(ctx, client.ObjectKey{Namespace: os.Getenv("POD_NAMESPACE"), Name: s.config.Traits.ConfigMapName}, cm); err != nil {
		return nil, fmt.Errorf("get traits configmap %s: %w", s.config.Traits.ConfigMapName, err)
	}
	return parseTraits(cm)
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
	traits, err := s.getTraits(ctx)
	if err != nil {
		return false, err
	}
	_, ok := traits[name]
	return ok, nil
}

// writeTraitsToConfigMap serializes the trait set into the ConfigMap's data field.
func writeTraitsToConfigMap(cm *corev1.ConfigMap, traitSet map[string]struct{}) error {
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

// addTraitToConfigMap adds a trait to the ConfigMap under the resource lock.
// Returns true if the trait was newly created, false if it already existed.
func (s *Shim) addTraitToConfigMap(ctx context.Context, name string) (bool, error) {
	// Fast path: trait already exists (no lock needed).
	traits, err := s.getTraits(ctx)
	if err != nil {
		return false, err
	}
	if _, exists := traits[name]; exists {
		return false, nil
	}

	// Slow path: acquire lock, re-read, add trait.
	host, err := os.Hostname()
	if err != nil {
		return false, fmt.Errorf("get hostname: %w", err)
	}
	lockerID := fmt.Sprintf("shim-%s-%d", host, time.Now().UnixNano())
	if err := s.resourceLocker.AcquireLock(ctx, s.config.Traits.ConfigMapName+"-lock", lockerID); err != nil {
		return false, fmt.Errorf("acquire traits lock: %w", err)
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.resourceLocker.ReleaseLock(releaseCtx, s.config.Traits.ConfigMapName+"-lock", lockerID); err != nil {
			ctrl.Log.WithName("placement-shim").Error(err, "failed to release traits lock")
		}
	}()

	cm := &corev1.ConfigMap{}
	key := client.ObjectKey{Namespace: os.Getenv("POD_NAMESPACE"), Name: s.config.Traits.ConfigMapName}
	if err := s.Get(ctx, key, cm); err != nil {
		if apierrors.IsNotFound(err) {
			cm = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Data: map[string]string{configMapKeyTraits: "[]"},
			}
			current := map[string]struct{}{name: {}}
			if err := writeTraitsToConfigMap(cm, current); err != nil {
				return false, err
			}
			if err := s.Create(ctx, cm); err != nil {
				return false, fmt.Errorf("create traits configmap: %w", err)
			}
			return true, nil
		}
		return false, fmt.Errorf("get traits configmap: %w", err)
	}

	current, err := parseTraits(cm)
	if err != nil {
		return false, err
	}
	if _, exists := current[name]; exists {
		return false, nil
	}
	current[name] = struct{}{}
	if err := writeTraitsToConfigMap(cm, current); err != nil {
		return false, err
	}
	if err := s.Update(ctx, cm); err != nil {
		return false, fmt.Errorf("update traits configmap: %w", err)
	}
	return true, nil
}

// removeTraitFromConfigMap removes a trait from the ConfigMap under the
// resource lock. Returns true if the trait was found and removed.
func (s *Shim) removeTraitFromConfigMap(ctx context.Context, name string) (bool, error) {
	host, err := os.Hostname()
	if err != nil {
		return false, fmt.Errorf("get hostname: %w", err)
	}
	lockerID := fmt.Sprintf("shim-%s-%d", host, time.Now().UnixNano())
	if err := s.resourceLocker.AcquireLock(ctx, s.config.Traits.ConfigMapName+"-lock", lockerID); err != nil {
		return false, fmt.Errorf("acquire traits lock: %w", err)
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.resourceLocker.ReleaseLock(releaseCtx, s.config.Traits.ConfigMapName+"-lock", lockerID); err != nil {
			ctrl.Log.WithName("placement-shim").Error(err, "failed to release traits lock")
		}
	}()

	cm := &corev1.ConfigMap{}
	if err := s.Get(ctx, client.ObjectKey{Namespace: os.Getenv("POD_NAMESPACE"), Name: s.config.Traits.ConfigMapName}, cm); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("get traits configmap: %w", err)
	}
	current, err := parseTraits(cm)
	if err != nil {
		return false, err
	}
	if _, exists := current[name]; !exists {
		return false, nil
	}
	delete(current, name)
	if err := writeTraitsToConfigMap(cm, current); err != nil {
		return false, err
	}
	if err := s.Update(ctx, cm); err != nil {
		return false, fmt.Errorf("update traits configmap: %w", err)
	}
	return true, nil
}
