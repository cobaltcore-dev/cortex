// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package library

type Conf struct {
	// The remote API servers to scrape hypervisors from.
	APIServers []Kubeconfig `json:"apiservers,omitempty"`
}

// Kubeconfig for connecting to the kubernetes apiserver.
// See: https://pkg.go.dev/k8s.io/client-go/rest#Config
type Kubeconfig struct {
	// The base path to the kubernetes apiserver. Can be retrieved with:
	// `kubectl config view --minify -o jsonpath="{.clusters[0].cluster.server}"`
	Host string `json:"host,omitempty"`
	// The API path that points to an API root. Defaults to `/api` if empty.
	APIPath string `json:"apiPath,omitempty"`
	// A valid bearer token for authentication. Can be generated with:
	// `kubectl create token <sa-name> -n <sa-namespace> --duration 10m -o json`
	BearerToken string `json:"bearerToken,omitempty"`
	// The CA certificate for the kubernetes apiserver. Can be retrieved with:
	// `kubectl get cm kube-root-ca.crt -o jsonpath="{['data']['ca\.crt']}"``
	CACrt string `json:"caCrt,omitempty"`
}
