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

// HandleListResourceClasses handles GET /resource_classes requests.
//
// Feature modes:
//   - passthrough: forwards to upstream placement.
//   - hybrid: forwards to upstream placement.
//   - crd: serves the resource class list from the local ConfigMap.
//
// See: https://docs.openstack.org/api-ref/placement/#list-resource-classes
func (s *Shim) HandleListResourceClasses(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	switch s.featureModeFromConfOrHeader(r, s.config.Features.ResourceClasses) {
	case FeatureModePassthrough, FeatureModeHybrid:
		s.forward(w, r)
		return
	case FeatureModeCRD:
		// Serve from local ConfigMap.
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
		return
	}

	rcSet, err := s.getResourceClasses(ctx)
	if err != nil {
		log.Error(err, "failed to list resource classes from configmap")
		http.Error(w, "failed to list resource classes", http.StatusInternalServerError)
		return
	}

	entries := make([]resourceClassEntry, 0, len(rcSet))
	for name := range rcSet {
		entries = append(entries, resourceClassEntry{Name: name})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	log.Info("listing all resource classes", "count", len(entries))
	s.writeJSON(w, http.StatusOK, resourceClassesListResponse{ResourceClasses: entries})
}

// HandleCreateResourceClass handles POST /resource_classes requests.
//
// Feature modes:
//   - passthrough: forwards to upstream placement.
//   - hybrid: forwards to upstream; on success, adds the class to the local ConfigMap.
//   - crd: writes the class to the local ConfigMap (CUSTOM_ prefix required).
//
// See: https://docs.openstack.org/api-ref/placement/#create-resource-class
func (s *Shim) HandleCreateResourceClass(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	mode := s.featureModeFromConfOrHeader(r, s.config.Features.ResourceClasses)
	switch mode {
	case FeatureModePassthrough:
		s.forward(w, r)
		return
	case FeatureModeHybrid:
		s.handleCreateResourceClassHybrid(w, r)
		return
	case FeatureModeCRD:
		// Handle locally.
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
		return
	}

	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, "request body must contain a valid 'name' field", http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(body.Name, "CUSTOM_") {
		log.Info("rejected resource class without CUSTOM_ prefix", "class", body.Name)
		http.Error(w, "resource class name must start with CUSTOM_", http.StatusBadRequest)
		return
	}

	exists, err := s.hasResourceClass(ctx, body.Name)
	if err != nil {
		log.Error(err, "failed to check resource class", "class", body.Name)
		http.Error(w, "failed to check resource class", http.StatusInternalServerError)
		return
	}
	if exists {
		http.Error(w, "resource class already exists", http.StatusConflict)
		return
	}

	if _, err := s.addResourceClassToConfigMap(ctx, body.Name); err != nil {
		log.Error(err, "failed to create resource class", "class", body.Name)
		http.Error(w, "failed to create resource class", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// handleCreateResourceClassHybrid forwards POST /resource_classes to upstream,
// then updates the local ConfigMap on success.
func (s *Shim) handleCreateResourceClassHybrid(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	s.forwardWithHook(w, r, func(w http.ResponseWriter, resp *http.Response) {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Error(err, "hybrid: failed to read upstream response body")
		}
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		if _, err := w.Write(body); err != nil {
			log.Error(err, "hybrid: failed to write response body")
		}

		if resp.StatusCode == http.StatusCreated {
			var created struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(body, &created); err == nil && created.Name != "" {
				if _, err := s.addResourceClassToConfigMap(ctx, created.Name); err != nil {
					log.Error(err, "hybrid: failed to add resource class to local configmap", "class", created.Name)
				}
			}
		}
	})
}

// HandleShowResourceClass handles GET /resource_classes/{name} requests.
//
// Feature modes:
//   - passthrough: forwards to upstream placement.
//   - hybrid: forwards to upstream placement.
//   - crd: checks the local ConfigMap for the resource class.
//
// See: https://docs.openstack.org/api-ref/placement/#show-resource-class
func (s *Shim) HandleShowResourceClass(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	switch s.featureModeFromConfOrHeader(r, s.config.Features.ResourceClasses) {
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
	found, err := s.hasResourceClass(ctx, name)
	if err != nil {
		log.Error(err, "failed to check resource class", "class", name)
		http.Error(w, "failed to check resource class", http.StatusInternalServerError)
		return
	}
	if !found {
		log.Info("resource class not found", "class", name)
		http.Error(w, "resource class not found", http.StatusNotFound)
		return
	}
	log.Info("resource class found", "class", name)
	s.writeJSON(w, http.StatusOK, resourceClassEntry{Name: name})
}

// HandleUpdateResourceClass handles PUT /resource_classes/{name} requests.
//
// Feature modes:
//   - passthrough: forwards to upstream placement.
//   - hybrid: forwards to upstream; on success, adds the class to the local ConfigMap.
//   - crd: writes the class to the local ConfigMap (CUSTOM_ prefix required).
//
// See: https://docs.openstack.org/api-ref/placement/#update-resource-class
func (s *Shim) HandleUpdateResourceClass(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	mode := s.featureModeFromConfOrHeader(r, s.config.Features.ResourceClasses)
	switch mode {
	case FeatureModePassthrough:
		s.forward(w, r)
		return
	case FeatureModeHybrid:
		s.handleUpdateResourceClassHybrid(w, r)
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
		log.Info("rejected resource class without CUSTOM_ prefix", "class", name)
		http.Error(w, "resource class name must start with CUSTOM_", http.StatusBadRequest)
		return
	}

	created, err := s.addResourceClassToConfigMap(ctx, name)
	if err != nil {
		log.Error(err, "failed to create resource class", "class", name)
		http.Error(w, "failed to create resource class", http.StatusInternalServerError)
		return
	}
	if created {
		w.WriteHeader(http.StatusCreated)
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleUpdateResourceClassHybrid forwards PUT /resource_classes/{name} to
// upstream, then updates the local ConfigMap on success.
func (s *Shim) handleUpdateResourceClassHybrid(w http.ResponseWriter, r *http.Request) {
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
			if _, err := s.addResourceClassToConfigMap(ctx, name); err != nil {
				log.Error(err, "hybrid: failed to add resource class to local configmap", "class", name)
			}
		}
	})
}

// HandleDeleteResourceClass handles DELETE /resource_classes/{name} requests.
//
// Feature modes:
//   - passthrough: forwards to upstream placement.
//   - hybrid: forwards to upstream; on success, removes the class from the local ConfigMap.
//   - crd: removes the class from the local ConfigMap (CUSTOM_ prefix required).
//
// See: https://docs.openstack.org/api-ref/placement/#delete-resource-class
func (s *Shim) HandleDeleteResourceClass(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	mode := s.featureModeFromConfOrHeader(r, s.config.Features.ResourceClasses)
	switch mode {
	case FeatureModePassthrough:
		s.forward(w, r)
		return
	case FeatureModeHybrid:
		s.handleDeleteResourceClassHybrid(w, r)
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
		log.Info("rejected deletion of standard resource class", "class", name)
		http.Error(w, "cannot delete standard resource classes", http.StatusBadRequest)
		return
	}

	removed, err := s.removeResourceClassFromConfigMap(ctx, name)
	if err != nil {
		log.Error(err, "failed to delete resource class", "class", name)
		http.Error(w, "failed to delete resource class", http.StatusInternalServerError)
		return
	}
	if !removed {
		log.Info("resource class not found in configmap", "class", name)
		http.Error(w, "resource class not found", http.StatusNotFound)
		return
	}
	log.Info("deleted resource class from configmap", "class", name)
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteResourceClassHybrid forwards DELETE /resource_classes/{name} to
// upstream, then updates the local ConfigMap on success.
func (s *Shim) handleDeleteResourceClassHybrid(w http.ResponseWriter, r *http.Request) {
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
			if _, err := s.removeResourceClassFromConfigMap(ctx, name); err != nil {
				log.Error(err, "hybrid: failed to remove resource class from local configmap", "class", name)
			}
		}
	})
}

// getResourceClasses reads resource classes from the single ConfigMap.
func (s *Shim) getResourceClasses(ctx context.Context) (map[string]struct{}, error) {
	cm := &corev1.ConfigMap{}
	if err := s.Get(ctx, client.ObjectKey{Namespace: os.Getenv("POD_NAMESPACE"), Name: s.config.ResourceClasses.ConfigMapName}, cm); err != nil {
		return nil, fmt.Errorf("get resource classes configmap %s: %w", s.config.ResourceClasses.ConfigMapName, err)
	}
	return parseResourceClasses(cm)
}

// parseResourceClasses extracts the resource class set from a ConfigMap.
func parseResourceClasses(cm *corev1.ConfigMap) (map[string]struct{}, error) {
	raw, ok := cm.Data[configMapKeyResourceClasses]
	if !ok || raw == "" {
		return make(map[string]struct{}), nil
	}
	var classes []string
	if err := json.Unmarshal([]byte(raw), &classes); err != nil {
		return nil, fmt.Errorf("unmarshal resource classes from configmap: %w", err)
	}
	m := make(map[string]struct{}, len(classes))
	for _, c := range classes {
		m[c] = struct{}{}
	}
	return m, nil
}

func (s *Shim) hasResourceClass(ctx context.Context, name string) (bool, error) {
	classes, err := s.getResourceClasses(ctx)
	if err != nil {
		return false, err
	}
	_, ok := classes[name]
	return ok, nil
}

// writeResourceClassesToConfigMap serializes the resource class set into the ConfigMap.
func writeResourceClassesToConfigMap(cm *corev1.ConfigMap, rcSet map[string]struct{}) error {
	classes := make([]string, 0, len(rcSet))
	for c := range rcSet {
		classes = append(classes, c)
	}
	sort.Strings(classes)

	data, err := json.Marshal(classes)
	if err != nil {
		return fmt.Errorf("marshal resource classes: %w", err)
	}
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data[configMapKeyResourceClasses] = string(data)
	return nil
}

// addResourceClassToConfigMap adds a resource class to the ConfigMap under the
// resource lock. Returns true if the class was newly created, false if it
// already existed.
func (s *Shim) addResourceClassToConfigMap(ctx context.Context, name string) (bool, error) {
	classes, err := s.getResourceClasses(ctx)
	if err != nil {
		return false, err
	}
	if _, exists := classes[name]; exists {
		return false, nil
	}

	host, err := os.Hostname()
	if err != nil {
		return false, fmt.Errorf("get hostname: %w", err)
	}
	lockerID := fmt.Sprintf("shim-%s-%d", host, time.Now().UnixNano())
	if err := s.resourceLocker.AcquireLock(ctx, s.config.ResourceClasses.ConfigMapName+"-lock", lockerID); err != nil {
		return false, fmt.Errorf("acquire resource classes lock: %w", err)
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.resourceLocker.ReleaseLock(releaseCtx, s.config.ResourceClasses.ConfigMapName+"-lock", lockerID); err != nil {
			ctrl.Log.WithName("placement-shim").Error(err, "failed to release resource classes lock")
		}
	}()

	cm := &corev1.ConfigMap{}
	key := client.ObjectKey{Namespace: os.Getenv("POD_NAMESPACE"), Name: s.config.ResourceClasses.ConfigMapName}
	if err := s.Get(ctx, key, cm); err != nil {
		if apierrors.IsNotFound(err) {
			cm = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Data: map[string]string{configMapKeyResourceClasses: "[]"},
			}
			current := map[string]struct{}{name: {}}
			if err := writeResourceClassesToConfigMap(cm, current); err != nil {
				return false, err
			}
			if err := s.Create(ctx, cm); err != nil {
				return false, fmt.Errorf("create resource classes configmap: %w", err)
			}
			return true, nil
		}
		return false, fmt.Errorf("get resource classes configmap: %w", err)
	}

	current, err := parseResourceClasses(cm)
	if err != nil {
		return false, err
	}
	if _, exists := current[name]; exists {
		return false, nil
	}
	current[name] = struct{}{}
	if err := writeResourceClassesToConfigMap(cm, current); err != nil {
		return false, err
	}
	if err := s.Update(ctx, cm); err != nil {
		return false, fmt.Errorf("update resource classes configmap: %w", err)
	}
	return true, nil
}

// removeResourceClassFromConfigMap removes a resource class from the ConfigMap
// under the resource lock. Returns true if the class was found and removed.
func (s *Shim) removeResourceClassFromConfigMap(ctx context.Context, name string) (bool, error) {
	host, err := os.Hostname()
	if err != nil {
		return false, fmt.Errorf("get hostname: %w", err)
	}
	lockerID := fmt.Sprintf("shim-%s-%d", host, time.Now().UnixNano())
	if err := s.resourceLocker.AcquireLock(ctx, s.config.ResourceClasses.ConfigMapName+"-lock", lockerID); err != nil {
		return false, fmt.Errorf("acquire resource classes lock: %w", err)
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.resourceLocker.ReleaseLock(releaseCtx, s.config.ResourceClasses.ConfigMapName+"-lock", lockerID); err != nil {
			ctrl.Log.WithName("placement-shim").Error(err, "failed to release resource classes lock")
		}
	}()

	cm := &corev1.ConfigMap{}
	if err := s.Get(ctx, client.ObjectKey{Namespace: os.Getenv("POD_NAMESPACE"), Name: s.config.ResourceClasses.ConfigMapName}, cm); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("get resource classes configmap: %w", err)
	}
	current, err := parseResourceClasses(cm)
	if err != nil {
		return false, err
	}
	if _, exists := current[name]; !exists {
		return false, nil
	}
	delete(current, name)
	if err := writeResourceClassesToConfigMap(cm, current); err != nil {
		return false, err
	}
	if err := s.Update(ctx, cm); err != nil {
		return false, fmt.Errorf("update resource classes configmap: %w", err)
	}
	return true, nil
}
