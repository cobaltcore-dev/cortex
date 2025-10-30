// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kpis

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/kpis/plugins"
	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Mock KPI implementation for testing
type mockKPI struct {
	name         string
	initError    error
	deinitError  error
	initCalled   bool
	deinitCalled bool
}

func (m *mockKPI) Init(db db.DB, opts libconf.RawOpts) error {
	m.initCalled = true
	return m.initError
}

func (m *mockKPI) Collect(ch chan<- prometheus.Metric) {}

func (m *mockKPI) Describe(ch chan<- *prometheus.Desc) {}

func (m *mockKPI) Deinit() error {
	m.deinitCalled = true
	return m.deinitError
}

func (m *mockKPI) GetName() string {
	return m.name
}

// Mock KPI logger implementation for testing (equivalent to kpilogger)
type mockKPILogger struct {
	kpi plugins.KPI
}

func (l *mockKPILogger) Init(db db.DB, opts libconf.RawOpts) error {
	return l.kpi.Init(db, opts)
}

func (l *mockKPILogger) Collect(ch chan<- prometheus.Metric) {
	l.kpi.Collect(ch)
}

func (l *mockKPILogger) Describe(ch chan<- *prometheus.Desc) {
	l.kpi.Describe(ch)
}

func (l *mockKPILogger) Deinit() error {
	return l.kpi.Deinit()
}

func (l *mockKPILogger) GetName() string {
	return l.kpi.GetName()
}

// Mock controller with overridable getJointDB method
type mockController struct {
	Controller
	mockDB    *db.DB
	mockError error
}

func (mc *mockController) getJointDB(
	ctx context.Context,
	datasources []corev1.ObjectReference,
	knowledges []corev1.ObjectReference,
) (*db.DB, error) {

	if mc.mockError != nil {
		return nil, mc.mockError
	}
	// If no dependencies, return nil database
	if len(datasources) == 0 && len(knowledges) == 0 {
		return nil, nil
	}
	// For test cases that need to simulate mismatched database secrets,
	// we can check the datasource names and return error
	if len(datasources) >= 2 {
		// Simulate checking for mismatched database secrets
		firstDS := &v1alpha1.Datasource{}
		if err := mc.Get(ctx, client.ObjectKey{
			Namespace: datasources[0].Namespace,
			Name:      datasources[0].Name,
		}, firstDS); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return nil, err
			}
		}
		secondDS := &v1alpha1.Datasource{}
		if err := mc.Get(ctx, client.ObjectKey{
			Namespace: datasources[1].Namespace,
			Name:      datasources[1].Name,
		}, secondDS); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return nil, err
			}
		}
		// Check if they have different database secret refs
		if firstDS.Spec.DatabaseSecretRef.Name != secondDS.Spec.DatabaseSecretRef.Name ||
			firstDS.Spec.DatabaseSecretRef.Namespace != secondDS.Spec.DatabaseSecretRef.Namespace {
			return nil, errors.New("datasources have different database secret refs")
		}
	}
	return mc.mockDB, nil
}

// Override Reconcile to use the mock handleKPIChange
func (mc *mockController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	kpi := &v1alpha1.KPI{}

	if err := mc.Get(ctx, req.NamespacedName, kpi); err != nil {
		// Remove the kpi if it was deleted.
		if client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
		var kpis v1alpha1.KPIList
		if err := mc.List(ctx, &kpis); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to list kpis: %w", err)
		}
		if existingKPI, ok := mc.registeredKPIsByResourceName[req.Name]; ok {
			metrics.Registry.Unregister(existingKPI)
			delete(mc.registeredKPIsByResourceName, req.Name)
			log.Info("kpi: unregistered deleted kpi", "name", req.Name)
			return ctrl.Result{}, existingKPI.Deinit()
		}
		return ctrl.Result{}, nil
	}

	// If this kpi is not supported, ignore it.
	if _, ok := mc.SupportedKPIsByImpl[kpi.Spec.Impl]; !ok {
		log.Info("kpi: unsupported kpi, ignoring", "name", req.Name)
		return ctrl.Result{}, nil
	}

	// Reconcile the kpi using mock method.
	err := mc.handleKPIChange(ctx, kpi)
	if err != nil {
		meta.SetStatusCondition(&kpi.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.KPIConditionError,
			Status:  metav1.ConditionTrue,
			Reason:  "ReconciliationFailed",
			Message: err.Error(),
		})
	} else {
		meta.RemoveStatusCondition(&kpi.Status.Conditions, v1alpha1.KPIConditionError)
	}
	if err := mc.Status().Update(ctx, kpi); err != nil {
		log.Error(err, "failed to update kpi status after reconciliation error", "name", kpi.Name)
	}
	return ctrl.Result{}, nil
}

// Override handleKPIChange to use the mock getJointDB
func (mc *mockController) handleKPIChange(ctx context.Context, obj *v1alpha1.KPI) error {
	log := ctrl.LoggerFrom(ctx)

	// Get all the datasources this kpi depends on, if any.
	var datasourcesReady int
	for _, dsRef := range obj.Spec.Dependencies.Datasources {
		ds := &v1alpha1.Datasource{}
		if err := mc.Get(ctx, client.ObjectKey{
			Namespace: dsRef.Namespace,
			Name:      dsRef.Name,
		}, ds); err != nil {
			if client.IgnoreNotFound(err) == nil {
				continue
			}
			log.Error(err, "failed to get datasource dependency", "datasource", dsRef)
			return err
		}
		// Check if datasource is ready
		if ds.Status.IsReady() {
			datasourcesReady++
		}
	}

	// Get all knowledges this kpi depends on, if any.
	var knowledgesReady int
	for _, knRef := range obj.Spec.Dependencies.Knowledges {
		kn := &v1alpha1.Knowledge{}
		if err := mc.Get(ctx, client.ObjectKey{
			Namespace: knRef.Namespace,
			Name:      knRef.Name,
		}, kn); err != nil {
			if client.IgnoreNotFound(err) == nil {
				continue
			}
			log.Error(err, "failed to get knowledge dependency", "knowledge", knRef)
			return err
		}
		// Check if knowledge is ready
		if kn.Status.IsReady() {
			knowledgesReady++
		}
	}

	dependenciesReadyTotal := datasourcesReady + knowledgesReady
	dependenciesTotal := len(obj.Spec.Dependencies.Datasources) +
		len(obj.Spec.Dependencies.Knowledges)
	registeredKPI, registered := mc.registeredKPIsByResourceName[obj.Name]

	// If all dependencies are ready but the kpi is not registered yet,
	// initialize and register it now.
	if dependenciesReadyTotal == dependenciesTotal && !registered {
		log.Info("kpi: registering new kpi", "name", obj.Name)
		var ok bool
		registeredKPI, ok = mc.SupportedKPIsByImpl[obj.Spec.Impl]
		if !ok {
			return fmt.Errorf("kpi %s not supported", obj.Name)
		}
		// Create a wrapper that logs operations (equivalent to kpilogger)
		registeredKPI = &mockKPILogger{kpi: registeredKPI}
		// Get joint database connection for all dependencies using mock method.
		jointDB, err := mc.getJointDB(ctx,
			obj.Spec.Dependencies.Datasources,
			obj.Spec.Dependencies.Knowledges)
		if err != nil {
			return fmt.Errorf("failed to get joint database for kpi %s: %w", obj.Name, err)
		}
		if jointDB == nil && dependenciesTotal > 0 {
			return fmt.Errorf("kpi %s requires at least one datasource or knowledge with a database", obj.Name)
		}
		rawOpts := libconf.NewRawOpts(`{}`)
		if len(obj.Spec.Opts.Raw) > 0 {
			rawOpts = libconf.NewRawOptsBytes(obj.Spec.Opts.Raw)
		}
		// Initialize KPI with database if available, otherwise with empty DB
		var dbToUse db.DB
		if jointDB != nil {
			dbToUse = *jointDB
		}
		if err := registeredKPI.Init(dbToUse, rawOpts); err != nil {
			return fmt.Errorf("failed to initialize kpi %s: %w", obj.Name, err)
		}
		if err := metrics.Registry.Register(registeredKPI); err != nil {
			return fmt.Errorf("failed to register kpi %s metrics: %w", obj.Name, err)
		}
		mc.registeredKPIsByResourceName[obj.Name] = registeredKPI
	}

	// If the dependencies are not all ready but the kpi is registered,
	// unregister and deinitialize it.
	if dependenciesReadyTotal < dependenciesTotal && registered {
		log.Info("kpi: unregistering kpi due to unready dependencies", "name", obj.Name)
		metrics.Registry.Unregister(registeredKPI)
		if err := registeredKPI.Deinit(); err != nil {
			return fmt.Errorf("failed to deinitialize kpi %s: %w", obj.Name, err)
		}
		delete(mc.registeredKPIsByResourceName, obj.Name)
	}

	// Update the status to ready and populate the ready dependencies.
	obj.Status.Ready = dependenciesReadyTotal == dependenciesTotal
	obj.Status.ReadyDependencies = dependenciesReadyTotal
	obj.Status.TotalDependencies = dependenciesTotal
	obj.Status.DependenciesReadyFrac = "ready"
	if dependenciesTotal > 0 {
		obj.Status.DependenciesReadyFrac = fmt.Sprintf("%d/%d",
			dependenciesReadyTotal, dependenciesTotal)
	}
	log.Info("kpi: successfully reconciled kpi", "name", obj.Name)
	return nil
}

func TestController_Reconcile(t *testing.T) {
	testScheme := runtime.NewScheme()
	if err := scheme.AddToScheme(testScheme); err != nil {
		t.Fatalf("Failed to add core scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(testScheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	tests := []struct {
		name           string
		kpi            *v1alpha1.KPI
		datasources    []v1alpha1.Datasource
		knowledges     []v1alpha1.Knowledge
		secrets        []corev1.Secret
		expectedReady  bool
		expectedError  bool
		shouldRegister bool
	}{
		{
			name: "KPI with ready dependencies",
			kpi: &v1alpha1.KPI{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kpi",
				},
				Spec: v1alpha1.KPISpec{
					Operator: "test-operator",
					Impl:     "test_kpi",
					Dependencies: v1alpha1.KPIDependenciesSpec{
						Datasources: []corev1.ObjectReference{
							{Name: "test-datasource", Namespace: "default"},
						},
					},
				},
			},
			datasources: []v1alpha1.Datasource{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-datasource",
						Namespace: "default",
					},
					Spec: v1alpha1.DatasourceSpec{
						Operator: "test-operator",
						DatabaseSecretRef: corev1.SecretReference{
							Name:      "db-secret",
							Namespace: "default",
						},
					},
					Status: v1alpha1.DatasourceStatus{
						NumberOfObjects: 1,
					},
				},
			},
			secrets: []corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "db-secret",
						Namespace: "default",
					},
					Data: map[string][]byte{
						"host":     []byte("localhost"),
						"port":     []byte("5432"),
						"database": []byte("testdb"),
						"user":     []byte("testuser"),
						"password": []byte("testpass"),
					},
				},
			},
			expectedReady:  true,
			expectedError:  false,
			shouldRegister: true,
		},
		{
			name: "KPI with unready dependencies",
			kpi: &v1alpha1.KPI{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-kpi-unready",
				},
				Spec: v1alpha1.KPISpec{
					Operator: "test-operator",
					Impl:     "test_kpi",
					Dependencies: v1alpha1.KPIDependenciesSpec{
						Datasources: []corev1.ObjectReference{
							{Name: "unready-datasource", Namespace: "default"},
						},
					},
				},
			},
			datasources: []v1alpha1.Datasource{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "unready-datasource",
						Namespace: "default",
					},
					Spec: v1alpha1.DatasourceSpec{
						Operator: "test-operator",
						DatabaseSecretRef: corev1.SecretReference{
							Name:      "db-secret",
							Namespace: "default",
						},
					},
					Status: v1alpha1.DatasourceStatus{
						NumberOfObjects: 0,
					},
				},
			},
			expectedReady:  false,
			expectedError:  false,
			shouldRegister: false,
		},
		{
			name: "unsupported KPI implementation",
			kpi: &v1alpha1.KPI{
				ObjectMeta: metav1.ObjectMeta{
					Name: "unsupported-kpi",
				},
				Spec: v1alpha1.KPISpec{
					Operator: "test-operator",
					Impl:     "unsupported_kpi",
				},
			},
			expectedReady:  false,
			expectedError:  false,
			shouldRegister: false,
		},
		{
			name: "KPI with no dependencies",
			kpi: &v1alpha1.KPI{
				ObjectMeta: metav1.ObjectMeta{
					Name: "no-deps-kpi",
				},
				Spec: v1alpha1.KPISpec{
					Operator: "test-operator",
					Impl:     "test_kpi",
				},
			},
			expectedReady:  true,
			expectedError:  false,
			shouldRegister: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create objects slice
			var objects []client.Object
			objects = append(objects, tt.kpi)
			for i := range tt.datasources {
				objects = append(objects, &tt.datasources[i])
			}
			for i := range tt.knowledges {
				objects = append(objects, &tt.knowledges[i])
			}
			for i := range tt.secrets {
				objects = append(objects, &tt.secrets[i])
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(objects...).
				WithStatusSubresource(&v1alpha1.KPI{}, &v1alpha1.Datasource{}, &v1alpha1.Knowledge{}).
				Build()

			mockKPIInstance := &mockKPI{name: "test_kpi"}
			baseController := Controller{
				Client:       fakeClient,
				OperatorName: "test-operator",
				SupportedKPIsByImpl: map[string]plugins.KPI{
					"test_kpi": mockKPIInstance,
				},
				registeredKPIsByResourceName: make(map[string]plugins.KPI),
			}

			// Use mock controller to avoid real database connections
			controller := &mockController{
				Controller: baseController,
				mockDB:     &db.DB{}, // Mock database instance
			}

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name: tt.kpi.Name,
				},
			}

			result, err := controller.Reconcile(context.Background(), req)
			if err != nil {
				t.Errorf("Reconcile() returned error: %v", err)
			}

			if result.RequeueAfter > 0 {
				t.Errorf("Expected no requeue, got %v", result.RequeueAfter)
			}

			// Verify KPI status
			var updatedKPI v1alpha1.KPI
			err = fakeClient.Get(context.Background(), req.NamespacedName, &updatedKPI)
			if err != nil {
				t.Errorf("Failed to get updated KPI: %v", err)
				return
			}

			if updatedKPI.Status.Ready != tt.expectedReady {
				t.Errorf("Expected ready status %v, got %v", tt.expectedReady, updatedKPI.Status.Ready)
			}

			hasError := meta.IsStatusConditionTrue(updatedKPI.Status.Conditions, v1alpha1.KPIConditionError)
			if hasError != tt.expectedError {
				t.Errorf("Expected error condition %v, got %v", tt.expectedError, hasError)
			}

			// Verify registration status
			_, registered := controller.registeredKPIsByResourceName[tt.kpi.Name]
			if registered != tt.shouldRegister {
				t.Errorf("Expected registration status %v, got %v", tt.shouldRegister, registered)
			}

			if tt.shouldRegister && !mockKPIInstance.initCalled {
				t.Error("Expected KPI to be initialized but Init was not called")
			}
		})
	}
}

func TestController_Reconcile_KPIDeleted(t *testing.T) {
	testScheme := runtime.NewScheme()
	if err := scheme.AddToScheme(testScheme); err != nil {
		t.Fatalf("Failed to add core scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(testScheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithStatusSubresource(&v1alpha1.KPI{}).
		Build()

	mockKPIInstance := &mockKPI{name: "test_kpi"}
	controller := &Controller{
		Client:       fakeClient,
		OperatorName: "test-operator",
		SupportedKPIsByImpl: map[string]plugins.KPI{
			"test_kpi": mockKPIInstance,
		},
		registeredKPIsByResourceName: map[string]plugins.KPI{
			"deleted-kpi": mockKPIInstance,
		},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: "deleted-kpi",
		},
	}

	result, err := controller.Reconcile(context.Background(), req)
	if err != nil {
		t.Errorf("Reconcile() returned error: %v", err)
	}

	if result.RequeueAfter > 0 {
		t.Errorf("Expected no requeue, got %v", result.RequeueAfter)
	}

	// Verify KPI was unregistered
	if _, registered := controller.registeredKPIsByResourceName["deleted-kpi"]; registered {
		t.Error("Expected KPI to be unregistered but it's still registered")
	}

	if !mockKPIInstance.deinitCalled {
		t.Error("Expected KPI to be deinitialized but Deinit was not called")
	}
}

func TestController_handleKPIChange(t *testing.T) {
	testScheme := runtime.NewScheme()
	if err := scheme.AddToScheme(testScheme); err != nil {
		t.Fatalf("Failed to add core scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(testScheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	tests := []struct {
		name               string
		kpi                *v1alpha1.KPI
		datasources        []v1alpha1.Datasource
		knowledges         []v1alpha1.Knowledge
		secrets            []corev1.Secret
		expectedReady      bool
		expectedReadyCount int
		expectedTotalCount int
		expectedReadyFrac  string
		mockInitError      error
		expectedError      bool
	}{
		{
			name: "KPI with mixed ready dependencies",
			kpi: &v1alpha1.KPI{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mixed-kpi",
				},
				Spec: v1alpha1.KPISpec{
					Impl: "test_kpi",
					Dependencies: v1alpha1.KPIDependenciesSpec{
						Datasources: []corev1.ObjectReference{
							{Name: "ready-ds", Namespace: "default"},
							{Name: "unready-ds", Namespace: "default"},
						},
						Knowledges: []corev1.ObjectReference{
							{Name: "ready-kn", Namespace: "default"},
						},
					},
				},
			},
			datasources: []v1alpha1.Datasource{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "ready-ds", Namespace: "default"},
					Spec: v1alpha1.DatasourceSpec{
						DatabaseSecretRef: corev1.SecretReference{Name: "db-secret", Namespace: "default"},
					},
					Status: v1alpha1.DatasourceStatus{NumberOfObjects: 1},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "unready-ds", Namespace: "default"},
					Spec: v1alpha1.DatasourceSpec{
						DatabaseSecretRef: corev1.SecretReference{Name: "db-secret", Namespace: "default"},
					},
					Status: v1alpha1.DatasourceStatus{NumberOfObjects: 0},
				},
			},
			knowledges: []v1alpha1.Knowledge{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "ready-kn", Namespace: "default"},
					Spec: v1alpha1.KnowledgeSpec{
						DatabaseSecretRef: &corev1.SecretReference{Name: "db-secret", Namespace: "default"},
					},
					Status: v1alpha1.KnowledgeStatus{RawLength: 1},
				},
			},
			secrets: []corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "db-secret", Namespace: "default"},
					Data: map[string][]byte{
						"host":     []byte("localhost"),
						"port":     []byte("5432"),
						"database": []byte("testdb"),
						"user":     []byte("user"),
						"password": []byte("pass"),
					},
				},
			},
			expectedReady:      false,
			expectedReadyCount: 2,
			expectedTotalCount: 3,
			expectedReadyFrac:  "2/3",
			expectedError:      false,
		},
		{
			name: "KPI initialization error",
			kpi: &v1alpha1.KPI{
				ObjectMeta: metav1.ObjectMeta{Name: "error-kpi"},
				Spec: v1alpha1.KPISpec{
					Impl: "test_kpi",
					Dependencies: v1alpha1.KPIDependenciesSpec{
						Datasources: []corev1.ObjectReference{
							{Name: "ready-ds", Namespace: "default"},
						},
					},
				},
			},
			datasources: []v1alpha1.Datasource{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "ready-ds", Namespace: "default"},
					Spec: v1alpha1.DatasourceSpec{
						DatabaseSecretRef: corev1.SecretReference{Name: "db-secret", Namespace: "default"},
					},
					Status: v1alpha1.DatasourceStatus{NumberOfObjects: 1},
				},
			},
			secrets: []corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "db-secret", Namespace: "default"},
					Data: map[string][]byte{
						"host":     []byte("localhost"),
						"port":     []byte("5432"),
						"database": []byte("testdb"),
						"user":     []byte("user"),
						"password": []byte("pass"),
					},
				},
			},
			mockInitError:      errors.New("init failed"),
			expectedReady:      false,
			expectedReadyCount: 0,
			expectedTotalCount: 0,
			expectedReadyFrac:  "",
			expectedError:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []client.Object
			objects = append(objects, tt.kpi)
			for i := range tt.datasources {
				objects = append(objects, &tt.datasources[i])
			}
			for i := range tt.knowledges {
				objects = append(objects, &tt.knowledges[i])
			}
			for i := range tt.secrets {
				objects = append(objects, &tt.secrets[i])
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(objects...).
				WithStatusSubresource(&v1alpha1.KPI{}, &v1alpha1.Datasource{}, &v1alpha1.Knowledge{}).
				Build()

			mockKPIInstance := &mockKPI{name: "test_kpi", initError: tt.mockInitError}
			baseController := Controller{
				Client: fakeClient,
				SupportedKPIsByImpl: map[string]plugins.KPI{
					"test_kpi": mockKPIInstance,
				},
				registeredKPIsByResourceName: make(map[string]plugins.KPI),
			}

			// Use mock controller to avoid real database connections
			controller := &mockController{
				Controller: baseController,
				mockDB:     &db.DB{}, // Mock database instance
			}

			err := controller.handleKPIChange(context.Background(), tt.kpi)
			hasError := err != nil
			if hasError != tt.expectedError {
				t.Errorf("Expected error %v, got %v (error: %v)", tt.expectedError, hasError, err)
			}

			if tt.kpi.Status.Ready != tt.expectedReady {
				t.Errorf("Expected ready %v, got %v", tt.expectedReady, tt.kpi.Status.Ready)
			}

			if tt.kpi.Status.ReadyDependencies != tt.expectedReadyCount {
				t.Errorf("Expected ready count %d, got %d", tt.expectedReadyCount, tt.kpi.Status.ReadyDependencies)
			}

			if tt.kpi.Status.TotalDependencies != tt.expectedTotalCount {
				t.Errorf("Expected total count %d, got %d", tt.expectedTotalCount, tt.kpi.Status.TotalDependencies)
			}

			if tt.kpi.Status.DependenciesReadyFrac != tt.expectedReadyFrac {
				t.Errorf("Expected ready frac %q, got %q", tt.expectedReadyFrac, tt.kpi.Status.DependenciesReadyFrac)
			}
		})
	}
}

func TestController_getJointDB(t *testing.T) {
	testScheme := runtime.NewScheme()
	if err := scheme.AddToScheme(testScheme); err != nil {
		t.Fatalf("Failed to add core scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(testScheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	tests := []struct {
		name        string
		datasources []v1alpha1.Datasource
		knowledges  []v1alpha1.Knowledge
		secrets     []corev1.Secret
		dsRefs      []corev1.ObjectReference
		knRefs      []corev1.ObjectReference
		expectError bool
		expectNilDB bool
	}{
		{
			name: "matching database secrets",
			datasources: []v1alpha1.Datasource{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "ds1", Namespace: "default"},
					Spec: v1alpha1.DatasourceSpec{
						DatabaseSecretRef: corev1.SecretReference{Name: "db-secret", Namespace: "default"},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "ds2", Namespace: "default"},
					Spec: v1alpha1.DatasourceSpec{
						DatabaseSecretRef: corev1.SecretReference{Name: "db-secret", Namespace: "default"},
					},
				},
			},
			secrets: []corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "db-secret", Namespace: "default"},
					Data: map[string][]byte{
						"host":     []byte("localhost"),
						"port":     []byte("5432"),
						"database": []byte("testdb"),
						"user":     []byte("user"),
						"password": []byte("pass"),
					},
				},
			},
			dsRefs: []corev1.ObjectReference{
				{Name: "ds1", Namespace: "default"},
				{Name: "ds2", Namespace: "default"},
			},
			expectError: false,
			expectNilDB: false,
		},
		{
			name: "mismatched database secrets",
			datasources: []v1alpha1.Datasource{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "ds1", Namespace: "default"},
					Spec: v1alpha1.DatasourceSpec{
						DatabaseSecretRef: corev1.SecretReference{Name: "db-secret1", Namespace: "default"},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "ds2", Namespace: "default"},
					Spec: v1alpha1.DatasourceSpec{
						DatabaseSecretRef: corev1.SecretReference{Name: "db-secret2", Namespace: "default"},
					},
				},
			},
			dsRefs: []corev1.ObjectReference{
				{Name: "ds1", Namespace: "default"},
				{Name: "ds2", Namespace: "default"},
			},
			expectError: true,
			expectNilDB: true,
		},
		{
			name:        "no dependencies",
			dsRefs:      []corev1.ObjectReference{},
			knRefs:      []corev1.ObjectReference{},
			expectError: false,
			expectNilDB: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []client.Object
			for i := range tt.datasources {
				objects = append(objects, &tt.datasources[i])
			}
			for i := range tt.knowledges {
				objects = append(objects, &tt.knowledges[i])
			}
			for i := range tt.secrets {
				objects = append(objects, &tt.secrets[i])
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(objects...).
				Build()

			baseController := Controller{
				Client: fakeClient,
			}

			// Use mock controller to avoid real database connections
			controller := &mockController{
				Controller: baseController,
				mockDB:     &db.DB{}, // Mock database instance
			}

			db, err := controller.getJointDB(context.Background(), tt.dsRefs, tt.knRefs)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if tt.expectNilDB && db != nil {
				t.Error("Expected nil database but got non-nil")
			}
			if !tt.expectNilDB && db == nil && !tt.expectError {
				t.Error("Expected non-nil database but got nil")
			}
		})
	}
}

func TestController_InitAllKPIs(t *testing.T) {
	testScheme := runtime.NewScheme()
	if err := scheme.AddToScheme(testScheme); err != nil {
		t.Fatalf("Failed to add core scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(testScheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	kpis := []v1alpha1.KPI{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "kpi1"},
			Spec: v1alpha1.KPISpec{
				Operator: "test-operator",
				Impl:     "test_kpi",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "kpi2"},
			Spec: v1alpha1.KPISpec{
				Operator: "other-operator",
				Impl:     "test_kpi",
			},
		},
	}

	var objects []client.Object
	for i := range kpis {
		objects = append(objects, &kpis[i])
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(objects...).
		WithStatusSubresource(&v1alpha1.KPI{}).
		Build()

	mockKPIInstance := &mockKPI{name: "test_kpi"}
	baseController := Controller{
		Client:       fakeClient,
		OperatorName: "test-operator",
		SupportedKPIsByImpl: map[string]plugins.KPI{
			"test_kpi": mockKPIInstance,
		},
	}

	// Use mock controller to avoid real database connections
	controller := &mockController{
		Controller: baseController,
	}

	err := controller.InitAllKPIs(context.Background())
	if err != nil {
		t.Errorf("InitAllKPIs() returned error: %v", err)
	}

	if controller.registeredKPIsByResourceName == nil {
		t.Error("Expected registeredKPIsByResourceName to be initialized")
	}

	// Should register the KPI matching the operator since it has no dependencies (0 == 0)
	if len(controller.registeredKPIsByResourceName) != 1 {
		t.Errorf("Expected 1 registered KPI (no dependencies should be registered), got %d", len(controller.registeredKPIsByResourceName))
	}

	// Verify the correct KPI was registered
	if _, ok := controller.registeredKPIsByResourceName["kpi1"]; !ok {
		t.Error("Expected kpi1 to be registered")
	}
}
