// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import "github.com/cobaltcore-dev/cortex/pkg/pendingcache"

type ClientConfig struct {
	// Apiserver configuration mapping GVKs to home or remote clusters.
	// Every GVK used through the multicluster client must be listed
	// in either Home or Remotes. Unknown GVKs will cause an error.
	APIServers APIServersConfig `json:"apiservers"`

	// PendingCache configures the optional transparent in-process overlay cache. When
	// PendingCache.Enabled is false (the default), clusters are used unwrapped.
	PendingCache pendingcache.Config `json:"pendingcache"`
}

// APIServersConfig separates resources into home and remote clusters.
type APIServersConfig struct {
	// Resources managed in the cluster where cortex is deployed.
	Home HomeConfig `json:"home"`
	// Resources managed in remote clusters.
	Remotes []RemoteConfig `json:"remotes,omitempty"`
}

// HomeConfig lists GVKs that are managed in the home cluster.
type HomeConfig struct {
	// The resource GVKs formatted as "<group>/<version>/<Kind>".
	GVKs []string `json:"gvks"`
}

// RemoteConfig maps multiple GVKs to a remote kubernetes apiserver with
// routing labels. It is assumed that the remote apiserver accepts the
// serviceaccount tokens issued by the local cluster.
type RemoteConfig struct {
	// The remote kubernetes apiserver url, e.g. "https://my-apiserver:6443".
	Host string `json:"host"`
	// The root CA certificate to verify the remote apiserver.
	// Ignored if InsecureSkipTLSVerify is true.
	CACert string `json:"caCert,omitempty"`
	// InsecureSkipTLSVerify disables verification of the remote apiserver's
	// TLS certificate. Use this for apiservers whose CA certificate rotates
	// frequently and does not chain to a stable root. Mutually exclusive
	// with CACert: when true, CACert is ignored.
	InsecureSkipTLSVerify bool `json:"insecureSkipTLSVerify,omitempty"`
	// The resource GVKs this apiserver serves, formatted as "<group>/<version>/<Kind>".
	GVKs []string `json:"gvks"`
	// Labels used by ResourceRouters to match resources to this cluster
	// for write operations (Create/Update/Delete/Patch).
	Labels map[string]string `json:"labels,omitempty"`
}
