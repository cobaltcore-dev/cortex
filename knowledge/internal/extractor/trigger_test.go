// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"context"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/conf"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestTrigger(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Trigger Suite")
}

var _ = Describe("TriggerReconciler", func() {
	var (
		ctx        context.Context
		reconciler *TriggerReconciler
		fakeClient client.Client
		scheme     *runtime.Scheme
		testConf   conf.Config
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		testConf = conf.Config{
			Operator: "test-operator",
		}

		fakeClient = fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		reconciler = &TriggerReconciler{
			Client: fakeClient,
			Scheme: scheme,
			Conf:   testConf,
		}
	})

	Describe("findDependentKnowledge", func() {
		It("should find knowledge dependent on a datasource", func() {
			// Create a datasource
			datasource := &v1alpha1.Datasource{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-datasource",
				},
				Spec: v1alpha1.DatasourceSpec{
					Operator: "test-operator",
					Type:     v1alpha1.DatasourceTypePrometheus,
				},
			}

			// Create knowledge that depends on the datasource
			knowledge := &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name: "dependent-knowledge",
				},
				Spec: v1alpha1.KnowledgeSpec{
					Operator: "test-operator",
					Dependencies: v1alpha1.KnowledgeDependenciesSpec{
						Datasources: []corev1.ObjectReference{
							{Name: "test-datasource"},
						},
					},
					Recency: metav1.Duration{Duration: time.Minute},
				},
			}

			// Create knowledge that doesn't depend on the datasource
			independentKnowledge := &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name: "independent-knowledge",
				},
				Spec: v1alpha1.KnowledgeSpec{
					Operator: "test-operator",
					Recency:  metav1.Duration{Duration: time.Minute},
				},
			}

			Expect(fakeClient.Create(ctx, datasource)).To(Succeed())
			Expect(fakeClient.Create(ctx, knowledge)).To(Succeed())
			Expect(fakeClient.Create(ctx, independentKnowledge)).To(Succeed())

			dependents, err := reconciler.findDependentKnowledge(ctx, datasource)
			Expect(err).NotTo(HaveOccurred())
			Expect(dependents).To(HaveLen(1))
			Expect(dependents[0].Name).To(Equal("dependent-knowledge"))
		})

		It("should find knowledge dependent on another knowledge", func() {
			// Create a knowledge resource
			sourceKnowledge := &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name: "source-knowledge",
				},
				Spec: v1alpha1.KnowledgeSpec{
					Operator: "test-operator",
					Recency:  metav1.Duration{Duration: time.Minute},
				},
			}

			// Create knowledge that depends on the source knowledge
			dependentKnowledge := &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name: "dependent-knowledge",
				},
				Spec: v1alpha1.KnowledgeSpec{
					Operator: "test-operator",
					Dependencies: v1alpha1.KnowledgeDependenciesSpec{
						Knowledges: []corev1.ObjectReference{
							{Name: "source-knowledge"},
						},
					},
					Recency: metav1.Duration{Duration: time.Minute},
				},
			}

			Expect(fakeClient.Create(ctx, sourceKnowledge)).To(Succeed())
			Expect(fakeClient.Create(ctx, dependentKnowledge)).To(Succeed())

			dependents, err := reconciler.findDependentKnowledge(ctx, sourceKnowledge)
			Expect(err).NotTo(HaveOccurred())
			Expect(dependents).To(HaveLen(1))
			Expect(dependents[0].Name).To(Equal("dependent-knowledge"))
		})

		It("should only return knowledge for the same operator", func() {
			// Create a datasource
			datasource := &v1alpha1.Datasource{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-datasource",
				},
				Spec: v1alpha1.DatasourceSpec{
					Operator: "test-operator",
					Type:     v1alpha1.DatasourceTypePrometheus,
				},
			}

			// Create knowledge for our operator
			ourKnowledge := &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name: "our-knowledge",
				},
				Spec: v1alpha1.KnowledgeSpec{
					Operator: "test-operator",
					Dependencies: v1alpha1.KnowledgeDependenciesSpec{
						Datasources: []corev1.ObjectReference{
							{Name: "test-datasource"},
						},
					},
					Recency: metav1.Duration{Duration: time.Minute},
				},
			}

			// Create knowledge for different operator
			otherKnowledge := &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name: "other-knowledge",
				},
				Spec: v1alpha1.KnowledgeSpec{
					Operator: "other-operator",
					Dependencies: v1alpha1.KnowledgeDependenciesSpec{
						Datasources: []corev1.ObjectReference{
							{Name: "test-datasource"},
						},
					},
					Recency: metav1.Duration{Duration: time.Minute},
				},
			}

			Expect(fakeClient.Create(ctx, datasource)).To(Succeed())
			Expect(fakeClient.Create(ctx, ourKnowledge)).To(Succeed())
			Expect(fakeClient.Create(ctx, otherKnowledge)).To(Succeed())

			dependents, err := reconciler.findDependentKnowledge(ctx, datasource)
			Expect(err).NotTo(HaveOccurred())
			Expect(dependents).To(HaveLen(1))
			Expect(dependents[0].Name).To(Equal("our-knowledge"))
		})
	})

	Describe("triggerKnowledgeReconciliation", func() {
		It("should trigger immediate reconciliation when recency threshold is exceeded", func() {
			// Create knowledge that was last extracted longer ago than recency
			pastTime := time.Now().Add(-2 * time.Minute)
			knowledge := v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-knowledge",
				},
				Spec: v1alpha1.KnowledgeSpec{
					Operator: "test-operator",
					Recency:  metav1.Duration{Duration: time.Minute},
				},
				Status: v1alpha1.KnowledgeStatus{
					LastExtracted: metav1.NewTime(pastTime),
				},
			}

			Expect(fakeClient.Create(ctx, &knowledge)).To(Succeed())

			err := reconciler.triggerKnowledgeReconciliation(ctx, knowledge)
			Expect(err).NotTo(HaveOccurred())

			// Verify the knowledge was updated with trigger annotation
			var updatedKnowledge v1alpha1.Knowledge
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "test-knowledge"}, &updatedKnowledge)).To(Succeed())
			Expect(updatedKnowledge.Annotations).To(HaveKey("cortex.knowledge/trigger-reconciliation"))
		})

		It("should schedule future reconciliation when recency threshold is not yet reached", func() {
			// Create knowledge that was last extracted recently
			recentTime := time.Now().Add(-30 * time.Second)
			knowledge := v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-knowledge",
				},
				Spec: v1alpha1.KnowledgeSpec{
					Operator: "test-operator",
					Recency:  metav1.Duration{Duration: time.Minute},
				},
				Status: v1alpha1.KnowledgeStatus{
					LastExtracted: metav1.NewTime(recentTime),
				},
			}

			Expect(fakeClient.Create(ctx, &knowledge)).To(Succeed())

			err := reconciler.triggerKnowledgeReconciliation(ctx, knowledge)
			Expect(err).NotTo(HaveOccurred())

			// Verify the knowledge was updated with trigger annotation
			var updatedKnowledge v1alpha1.Knowledge
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "test-knowledge"}, &updatedKnowledge)).To(Succeed())
			Expect(updatedKnowledge.Annotations).To(HaveKey("cortex.knowledge/trigger-reconciliation"))
		})
	})

	Describe("Reconcile", func() {
		It("should handle datasource changes and trigger dependent knowledge reconciliation", func() {
			// Create a datasource
			datasource := &v1alpha1.Datasource{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-datasource",
				},
				Spec: v1alpha1.DatasourceSpec{
					Operator: "test-operator",
					Type:     v1alpha1.DatasourceTypePrometheus,
				},
			}

			// Create knowledge that depends on the datasource
			pastTime := time.Now().Add(-2 * time.Minute)
			knowledge := &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name: "dependent-knowledge",
				},
				Spec: v1alpha1.KnowledgeSpec{
					Operator: "test-operator",
					Dependencies: v1alpha1.KnowledgeDependenciesSpec{
						Datasources: []corev1.ObjectReference{
							{Name: "test-datasource"},
						},
					},
					Recency: metav1.Duration{Duration: time.Minute},
				},
				Status: v1alpha1.KnowledgeStatus{
					LastExtracted: metav1.NewTime(pastTime),
				},
			}

			Expect(fakeClient.Create(ctx, datasource)).To(Succeed())
			Expect(fakeClient.Create(ctx, knowledge)).To(Succeed())

			// Trigger reconcile for the datasource
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name: "test-datasource",
				},
			}

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())

			// Verify the dependent knowledge was updated with trigger annotation
			var updatedKnowledge v1alpha1.Knowledge
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "dependent-knowledge"}, &updatedKnowledge)).To(Succeed())
			Expect(updatedKnowledge.Annotations).To(HaveKey("cortex.knowledge/trigger-reconciliation"))
		})

		It("should handle knowledge changes and trigger dependent knowledge reconciliation", func() {
			// Create source knowledge
			sourceKnowledge := &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name: "source-knowledge",
				},
				Spec: v1alpha1.KnowledgeSpec{
					Operator: "test-operator",
					Recency:  metav1.Duration{Duration: time.Minute},
				},
			}

			// Create dependent knowledge
			pastTime := time.Now().Add(-2 * time.Minute)
			dependentKnowledge := &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name: "dependent-knowledge",
				},
				Spec: v1alpha1.KnowledgeSpec{
					Operator: "test-operator",
					Dependencies: v1alpha1.KnowledgeDependenciesSpec{
						Knowledges: []corev1.ObjectReference{
							{Name: "source-knowledge"},
						},
					},
					Recency: metav1.Duration{Duration: time.Minute},
				},
				Status: v1alpha1.KnowledgeStatus{
					LastExtracted: metav1.NewTime(pastTime),
				},
			}

			Expect(fakeClient.Create(ctx, sourceKnowledge)).To(Succeed())
			Expect(fakeClient.Create(ctx, dependentKnowledge)).To(Succeed())

			// Trigger reconcile for the source knowledge
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name: "source-knowledge",
				},
			}

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())

			// Verify the dependent knowledge was updated with trigger annotation
			var updatedKnowledge v1alpha1.Knowledge
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "dependent-knowledge"}, &updatedKnowledge)).To(Succeed())
			Expect(updatedKnowledge.Annotations).To(HaveKey("cortex.knowledge/trigger-reconciliation"))
		})

		It("should handle non-existent resources gracefully", func() {
			// Try to reconcile a non-existent resource
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name: "non-existent",
				},
			}

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())
		})
	})

	Describe("getResourceType", func() {
		It("should return correct type for datasource", func() {
			datasource := &v1alpha1.Datasource{}
			Expect(getResourceType(datasource)).To(Equal("Datasource"))
		})

		It("should return correct type for knowledge", func() {
			knowledge := &v1alpha1.Knowledge{}
			Expect(getResourceType(knowledge)).To(Equal("Knowledge"))
		})

		It("should return Unknown for other types", func() {
			secret := &corev1.Secret{}
			Expect(getResourceType(secret)).To(Equal("Unknown"))
		})
	})

	Describe("mapDatasourceToKnowledge", func() {
		It("should map datasource for correct operator", func() {
			datasource := &v1alpha1.Datasource{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-datasource",
				},
				Spec: v1alpha1.DatasourceSpec{
					Operator: "test-operator",
					Type:     v1alpha1.DatasourceTypePrometheus,
				},
			}

			requests := reconciler.mapDatasourceToKnowledge(ctx, datasource)
			Expect(requests).To(HaveLen(1))
			Expect(requests[0].Name).To(Equal("test-datasource"))
		})

		It("should not map datasource for different operator", func() {
			datasource := &v1alpha1.Datasource{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-datasource",
				},
				Spec: v1alpha1.DatasourceSpec{
					Operator: "other-operator",
					Type:     v1alpha1.DatasourceTypePrometheus,
				},
			}

			requests := reconciler.mapDatasourceToKnowledge(ctx, datasource)
			Expect(requests).To(BeEmpty())
		})

		It("should handle non-datasource objects", func() {
			secret := &corev1.Secret{}
			requests := reconciler.mapDatasourceToKnowledge(ctx, secret)
			Expect(requests).To(BeNil())
		})
	})

	Describe("mapKnowledgeToKnowledge", func() {
		It("should map knowledge for correct operator", func() {
			knowledge := &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-knowledge",
				},
				Spec: v1alpha1.KnowledgeSpec{
					Operator: "test-operator",
					Recency:  metav1.Duration{Duration: time.Minute},
				},
			}

			requests := reconciler.mapKnowledgeToKnowledge(ctx, knowledge)
			Expect(requests).To(HaveLen(1))
			Expect(requests[0].Name).To(Equal("test-knowledge"))
		})

		It("should not map knowledge for different operator", func() {
			knowledge := &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-knowledge",
				},
				Spec: v1alpha1.KnowledgeSpec{
					Operator: "other-operator",
					Recency:  metav1.Duration{Duration: time.Minute},
				},
			}

			requests := reconciler.mapKnowledgeToKnowledge(ctx, knowledge)
			Expect(requests).To(BeEmpty())
		})

		It("should handle non-knowledge objects", func() {
			secret := &corev1.Secret{}
			requests := reconciler.mapKnowledgeToKnowledge(ctx, secret)
			Expect(requests).To(BeNil())
		})
	})
})
