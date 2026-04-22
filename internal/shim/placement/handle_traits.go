// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	configMapKeyTraits       = "traits"
	traitsLeaseDuration      = 15
	traitsLeaseRetryInterval = 500 * time.Millisecond
	traitsLeaseRetryTimeout  = 5 * time.Second
)

// traitsListResponse matches the OpenStack Placement GET /traits response.
type traitsListResponse struct {
	Traits []string `json:"traits"`
}

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

// HandleListTraits handles GET /traits requests.
//
// Returns a sorted list of trait strings merged from the static (Helm-managed)
// and dynamic (CUSTOM_*) ConfigMaps. Supports optional query parameter "name"
// for filtering: "in:TRAIT_A,TRAIT_B" returns only named traits,
// "startswith:CUSTOM_" returns prefix matches.
func (s *Shim) HandleListTraits(w http.ResponseWriter, r *http.Request) {
	if !s.config.Features.EnableTraits {
		s.forward(w, r)
		return
	}
	traitSet, err := s.getAllTraits(r.Context())
	if err != nil {
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
		s.writeJSON(w, http.StatusOK, traitsListResponse{Traits: filtered})
		return
	}
	s.writeJSON(w, http.StatusOK, traitsListResponse{Traits: all})
}

// HandleShowTrait handles GET /traits/{name} requests.
//
// Checks whether a trait with the given name exists in either the static
// or dynamic ConfigMap. Returns 200 OK if found, 404 Not Found otherwise.
func (s *Shim) HandleShowTrait(w http.ResponseWriter, r *http.Request) {
	if !s.config.Features.EnableTraits {
		s.forward(w, r)
		return
	}
	name, ok := requiredPathParam(w, r, "name")
	if !ok {
		return
	}
	found, err := s.hasTrait(r.Context(), name)
	if err != nil {
		http.Error(w, "failed to check trait", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "trait not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// HandleUpdateTrait handles PUT /traits/{name} requests.
//
// Creates a new custom trait in the dynamic ConfigMap. Only traits prefixed
// with CUSTOM_ may be created. Returns 201 Created if the trait is newly
// inserted, or 204 No Content if it already exists (in either ConfigMap).
// Returns 400 Bad Request if the name does not carry the CUSTOM_ prefix.
func (s *Shim) HandleUpdateTrait(w http.ResponseWriter, r *http.Request) {
	if !s.config.Features.EnableTraits {
		s.forward(w, r)
		return
	}
	name, ok := requiredPathParam(w, r, "name")
	if !ok {
		return
	}
	if !strings.HasPrefix(name, "CUSTOM_") {
		http.Error(w, "trait name must start with CUSTOM_", http.StatusBadRequest)
		return
	}
	ctx := r.Context()

	// Fast path: trait already exists in either ConfigMap (no lock needed).
	allTraits, err := s.getAllTraits(ctx)
	if err != nil {
		http.Error(w, "failed to create trait", http.StatusInternalServerError)
		return
	}
	if _, exists := allTraits[name]; exists {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Slow path: acquire lock, read/create dynamic ConfigMap, add trait.
	if err := s.acquireTraitsLease(ctx); err != nil {
		http.Error(w, "failed to create trait", http.StatusInternalServerError)
		return
	}
	defer func() {
		if err := s.releaseTraitsLease(ctx); err != nil {
			logf.FromContext(ctx).Error(err, "Failed to release traits lease")
		}
	}()

	cm := &corev1.ConfigMap{}
	err = s.Get(ctx, s.customTraitsConfigMapKey(), cm)
	if apierrors.IsNotFound(err) {
		// Dynamic ConfigMap doesn't exist yet — create it with the new trait.
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      s.customTraitsConfigMapKey().Name,
				Namespace: s.customTraitsConfigMapKey().Namespace,
			},
			Data: map[string]string{configMapKeyTraits: "[]"},
		}
		current := map[string]struct{}{name: {}}
		if err := s.writeTraits(cm, current); err != nil {
			http.Error(w, "failed to create trait", http.StatusInternalServerError)
			return
		}
		if err := s.Create(ctx, cm); err != nil {
			http.Error(w, "failed to create trait", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		return
	}
	if err != nil {
		http.Error(w, "failed to create trait", http.StatusInternalServerError)
		return
	}

	current, err := parseTraits(cm)
	if err != nil {
		http.Error(w, "failed to create trait", http.StatusInternalServerError)
		return
	}
	if _, exists := current[name]; exists {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	current[name] = struct{}{}
	if err := s.writeTraits(cm, current); err != nil {
		http.Error(w, "failed to create trait", http.StatusInternalServerError)
		return
	}
	if err := s.Update(ctx, cm); err != nil {
		http.Error(w, "failed to create trait", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// HandleDeleteTrait handles DELETE /traits/{name} requests.
//
// Deletes a custom trait from the dynamic ConfigMap. Standard traits (those
// without the CUSTOM_ prefix) cannot be deleted and return 400 Bad Request.
// Returns 404 if the trait does not exist. Returns 204 No Content on success.
func (s *Shim) HandleDeleteTrait(w http.ResponseWriter, r *http.Request) {
	if !s.config.Features.EnableTraits {
		s.forward(w, r)
		return
	}
	name, ok := requiredPathParam(w, r, "name")
	if !ok {
		return
	}
	if !strings.HasPrefix(name, "CUSTOM_") {
		http.Error(w, "cannot delete standard traits", http.StatusBadRequest)
		return
	}
	ctx := r.Context()

	if err := s.acquireTraitsLease(ctx); err != nil {
		http.Error(w, "failed to delete trait", http.StatusInternalServerError)
		return
	}
	defer func() {
		if err := s.releaseTraitsLease(ctx); err != nil {
			logf.FromContext(ctx).Error(err, "Failed to release traits lease")
		}
	}()

	cm := &corev1.ConfigMap{}
	err := s.Get(ctx, s.customTraitsConfigMapKey(), cm)
	if apierrors.IsNotFound(err) {
		http.Error(w, "trait not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "failed to delete trait", http.StatusInternalServerError)
		return
	}
	current, err := parseTraits(cm)
	if err != nil {
		http.Error(w, "failed to delete trait", http.StatusInternalServerError)
		return
	}
	if _, exists := current[name]; !exists {
		http.Error(w, "trait not found", http.StatusNotFound)
		return
	}
	delete(current, name)
	if err := s.writeTraits(cm, current); err != nil {
		http.Error(w, "failed to delete trait", http.StatusInternalServerError)
		return
	}
	if err := s.Update(ctx, cm); err != nil {
		http.Error(w, "failed to delete trait", http.StatusInternalServerError)
		return
	}
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

// traitsLeaseName returns the Lease resource name used for locking writes.
func (s *Shim) traitsLeaseName() string {
	return s.config.Traits.ConfigMapName + "-lock"
}

// acquireTraitsLease attempts to acquire a Kubernetes Lease for serializing
// ConfigMap writes. It retries with backoff until the lease is acquired or
// the timeout expires.
func (s *Shim) acquireTraitsLease(ctx context.Context) error {
	log := logf.FromContext(ctx)
	name := s.traitsLeaseName()
	ns := os.Getenv("POD_NAMESPACE")
	holderID := fmt.Sprintf("shim-%d", time.Now().UnixNano())
	leaseDuration := int32(traitsLeaseDuration)

	deadline := time.Now().Add(traitsLeaseRetryTimeout)
	for {
		now := metav1.NewMicroTime(time.Now())
		lease := &coordinationv1.Lease{}
		err := s.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, lease)

		if apierrors.IsNotFound(err) {
			lease = &coordinationv1.Lease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: ns,
				},
				Spec: coordinationv1.LeaseSpec{
					HolderIdentity:       &holderID,
					LeaseDurationSeconds: &leaseDuration,
					AcquireTime:          &now,
					RenewTime:            &now,
				},
			}
			if err := s.Create(ctx, lease); err != nil {
				if apierrors.IsAlreadyExists(err) {
					if time.Now().After(deadline) {
						return fmt.Errorf("timeout acquiring traits lease %s", name)
					}
					time.Sleep(traitsLeaseRetryInterval)
					continue
				}
				return fmt.Errorf("create lease %s: %w", name, err)
			}
			log.V(2).Info("Acquired traits lease", "lease", name)
			return nil
		}
		if err != nil {
			return fmt.Errorf("get lease %s: %w", name, err)
		}

		// Check if the existing lease has expired.
		if lease.Spec.RenewTime != nil && lease.Spec.LeaseDurationSeconds != nil {
			expiry := lease.Spec.RenewTime.Add(time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second)
			if time.Now().Before(expiry) {
				if time.Now().After(deadline) {
					return fmt.Errorf("timeout acquiring traits lease %s", name)
				}
				time.Sleep(traitsLeaseRetryInterval)
				continue
			}
		}

		// Lease expired — claim it.
		lease.Spec.HolderIdentity = &holderID
		lease.Spec.LeaseDurationSeconds = &leaseDuration
		lease.Spec.AcquireTime = &now
		lease.Spec.RenewTime = &now
		if err := s.Update(ctx, lease); err != nil {
			if apierrors.IsConflict(err) {
				if time.Now().After(deadline) {
					return fmt.Errorf("timeout acquiring traits lease %s", name)
				}
				time.Sleep(traitsLeaseRetryInterval)
				continue
			}
			return fmt.Errorf("update lease %s: %w", name, err)
		}
		log.V(2).Info("Acquired expired traits lease", "lease", name)
		return nil
	}
}

// releaseTraitsLease deletes the Lease resource to allow other replicas to proceed.
func (s *Shim) releaseTraitsLease(ctx context.Context) error {
	ns := os.Getenv("POD_NAMESPACE")
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.traitsLeaseName(),
			Namespace: ns,
		},
	}
	if err := s.Delete(ctx, lease); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete lease %s: %w", s.traitsLeaseName(), err)
	}
	return nil
}
