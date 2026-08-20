// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package collector

import (
	"context"
	"fmt"

	hvv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(hvv1.AddToScheme(scheme))
}

// NewK8sClient builds a controller-runtime client configured for the
// Hypervisor CRD. Resolution order:
//  1. --kubeconfig flag (explicit path)
//  2. In-cluster config (KUBERNETES_SERVICE_HOST / SERVICE_PORT env vars)
//  3. Default kubeconfig loading rules (KUBECONFIG env var, ~/.kube/config)
func NewK8sClient(kubeconfig string) (client.Client, error) {
	var cfg *rest.Config
	var err error

	if kubeconfig != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		cfg, err = rest.InClusterConfig()
		if err != nil {
			cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
				clientcmd.NewDefaultClientConfigLoadingRules(),
				&clientcmd.ConfigOverrides{},
			).ClientConfig()
		}
	}
	if err != nil {
		return nil, fmt.Errorf("build rest config: %w", err)
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}
	return c, nil
}

// listHypervisors returns all Hypervisor CRs in the cluster.
func listHypervisors(ctx context.Context, c client.Client) ([]hvv1.Hypervisor, error) {
	var list hvv1.HypervisorList
	if err := c.List(ctx, &list); err != nil {
		return nil, fmt.Errorf("list hypervisors: %w", err)
	}
	return list.Items, nil
}
