// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	"context"
	"errors"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
)

// RemoteEndpoint identifies a unique remote cluster to probe for reachability.
type RemoteEndpoint struct {
	// Host is the remote apiserver URL (from the cluster's rest.Config).
	Host string
	// Cluster is the controller-runtime cluster whose apiserver is probed.
	Cluster cluster.Cluster
}

// UniqueRemotes returns each distinct remote cluster exactly once, across all
// configured GVKs. The same remote apiserver is stored once per GVK it serves,
// so this dedupes by cluster identity (the same policy IndexField uses to dedupe
// caches). The home cluster is not included.
func (c *Client) UniqueRemotes() []RemoteEndpoint {
	c.remoteClustersMu.RLock()
	defer c.remoteClustersMu.RUnlock()
	seen := make(map[cluster.Cluster]bool)
	var out []RemoteEndpoint
	for _, remotes := range c.remoteClusters {
		for _, r := range remotes {
			if r.cluster == nil || seen[r.cluster] {
				continue
			}
			seen[r.cluster] = true
			out = append(out, RemoteEndpoint{Host: r.cluster.GetConfig().Host, Cluster: r.cluster})
		}
	}
	return out
}

// ProbeOptions configures the per-remote reachability probe.
type ProbeOptions struct {
	// Interval is the time between reachability probes for each remote.
	Interval time.Duration
	// Timeout bounds a single probe request so it never blocks on a dead socket
	// longer than intended.
	Timeout time.Duration
	// FailureThreshold is the number of consecutive unreachable probes that must
	// occur before a remote is considered lost and onLost is called. It is chosen
	// above the supervisor's backoff floor so a doomed manager cycle outlives the
	// growing backoff, keeping the rebuild loop period bounded rather than hot.
	FailureThreshold int
}

// DefaultProbeOptions probes every 10s with a 5s per-probe timeout and treats a
// remote as lost after 3 consecutive failures (~30s of sustained unreachability).
var DefaultProbeOptions = ProbeOptions{
	Interval:         10 * time.Second,
	Timeout:          5 * time.Second,
	FailureThreshold: 3,
}

// ProbeRemotes runs one reachability probe goroutine per unique remote apiserver
// until ctx is cancelled. Each goroutine periodically probes its remote, updates
// the reachability gauge on the Monitor (if any), and tracks consecutive
// failures; when a remote has been unreachable FailureThreshold times in a row it
// calls onLost(host) once and stops probing that remote (the caller is expected
// to tear down the manager cycle in response).
//
// A single transient blip does not trigger onLost: the failure counter resets on
// the first reachable probe. "Unreachable" means a transport-level failure
// (connection refused, no route, TLS/handshake failure, timeout) — an apiserver
// that responds with any HTTP status (including 401/403/5xx) is considered
// reachable, since a manager rebuild cannot fix an authz/server error and would
// only cause a restart storm.
//
// ProbeRemotes returns immediately when no remotes are configured.
func (c *Client) ProbeRemotes(ctx context.Context, opts ProbeOptions, onLost func(host string)) {
	log := ctrl.LoggerFrom(ctx)
	remotes := c.UniqueRemotes()
	hosts := make([]string, 0, len(remotes))
	for _, r := range remotes {
		hosts = append(hosts, r.Host)
	}
	if len(remotes) == 0 {
		// No remotes to watch: log it explicitly so an operator who deletes a
		// cluster and sees "nothing happening" can tell this apart from the probe
		// simply not finding the remote it expected to watch.
		log.Info("reachability probe: no remote apiservers configured, nothing to probe")
		return
	}
	log.Info("reachability probe: starting", "remoteCount", len(remotes), "hosts", hosts,
		"interval", opts.Interval.String(), "timeout", opts.Timeout.String(), "failureThreshold", opts.FailureThreshold)
	var wg wait.Group
	for _, remote := range remotes {
		httpClient, err := reachabilityClient(remote.Cluster.GetConfig(), opts.Timeout)
		if err != nil {
			// A remote we cannot even build a probe client for is treated as
			// lost immediately: its config is unusable, so a rebuild (which
			// re-reads config) is the right response.
			log.Error(err, "reachability probe: unable to build probe client for remote; declaring it lost", "host", remote.Host)
			onLost(remote.Host)
			continue
		}
		wg.StartWithContext(ctx, func(ctx context.Context) {
			c.probeRemoteLoop(ctx, opts, remote, httpClient, onLost)
		})
	}
	wg.Wait()
	log.Info("reachability probe: stopped", "hosts", hosts)
}

// probeRemoteLoop probes a single remote until ctx is cancelled or the remote is
// declared lost.
func (c *Client) probeRemoteLoop(ctx context.Context, opts ProbeOptions, remote RemoteEndpoint, httpClient *rest.RESTClient, onLost func(host string)) {
	log := ctrl.LoggerFrom(ctx).WithValues("host", remote.Host)
	log.Info("reachability probe: watching remote apiserver")
	failures := 0
	wasReachable := true // assume reachable at start; log the first real transition
	// PollUntilContextCancel with immediate=true runs the first probe right away
	// rather than waiting a full interval, so a remote that is already gone at
	// cycle start is detected promptly. The poll func never returns an error, so
	// the only error PollUntilContextCancel can return is the context error once
	// ctx is cancelled or the loop stops after declaring the remote lost — both
	// expected, so it is intentionally not surfaced.
	err := wait.PollUntilContextCancel(ctx, opts.Interval, true, func(ctx context.Context) (bool, error) {
		reachable, probeErr := probeReachable(ctx, httpClient, opts.Timeout)
		if c.Monitor != nil {
			c.Monitor.recordRemoteReachable(remote.Host, reachable)
		}
		// Per-probe outcome at V(1): verbose, but the single most useful line when
		// diagnosing "why didn't it detect the deletion" — it shows the classified
		// result and the underlying transport/HTTP error for each tick.
		if probeErr != nil {
			log.V(1).Info("reachability probe: tick", "reachable", reachable, "error", probeErr.Error())
		} else {
			log.V(1).Info("reachability probe: tick", "reachable", reachable)
		}
		if reachable {
			if !wasReachable {
				log.Info("reachability probe: remote apiserver recovered", "afterConsecutiveFailures", failures)
			}
			wasReachable = true
			failures = 0
			return false, nil
		}
		wasReachable = false
		failures++
		if probeErr != nil {
			log.Info("reachability probe: remote apiserver unreachable",
				"consecutiveFailures", failures, "threshold", opts.FailureThreshold, "error", probeErr.Error())
		} else {
			log.Info("reachability probe: remote apiserver unreachable",
				"consecutiveFailures", failures, "threshold", opts.FailureThreshold)
		}
		if failures >= opts.FailureThreshold {
			log.Info("reachability probe: failure threshold reached, declaring remote lost", "consecutiveFailures", failures)
			onLost(remote.Host)
			return true, nil
		}
		return false, nil
	})
	if err != nil && ctx.Err() == nil {
		// A non-context error is not expected here (the poll func never returns
		// one), but log it rather than swallow it if the invariant ever changes.
		log.Error(err, "remote reachability poll stopped unexpectedly")
	}
}

// reachabilityClient builds a REST client for the given remote config that is
// used only to probe reachability. It copies the config (so the caller's shared
// config is never mutated), sets a per-probe timeout, and supplies a codec so the
// unversioned REST client can be constructed (we only read the transport outcome,
// never decode a body, but rest requires a NegotiatedSerializer).
func reachabilityClient(cfg *rest.Config, timeout time.Duration) (*rest.RESTClient, error) {
	cfgCopy := *cfg
	cfgCopy.Timeout = timeout
	cfgCopy.NegotiatedSerializer = scheme.Codecs.WithoutConversion()
	if cfgCopy.GroupVersion == nil {
		cfgCopy.GroupVersion = &schema.GroupVersion{}
	}
	httpClient, err := rest.HTTPClientFor(&cfgCopy)
	if err != nil {
		return nil, err
	}
	return rest.UnversionedRESTClientForConfigAndClient(&cfgCopy, httpClient)
}

// probeReachable issues a lightweight GET /readyz against the remote apiserver
// and reports whether the apiserver is reachable. Any HTTP response — including
// non-2xx statuses surfaced as a Kubernetes StatusError — counts as reachable;
// only a transport-level error (connection refused, no route, TLS failure,
// timeout) counts as unreachable, which is the signature of a deleted cluster.
// probeReachable issues a lightweight GET /readyz against the remote apiserver
// and reports whether the apiserver is reachable, along with the underlying
// error (nil on a 2xx, otherwise the transport or HTTP error even when the result
// is classified reachable) for logging. Any HTTP response — including non-2xx
// statuses surfaced as a Kubernetes StatusError — counts as reachable; only a
// transport-level error (connection refused, no route, TLS failure, timeout)
// counts as unreachable, which is the signature of a deleted cluster.
func probeReachable(ctx context.Context, client *rest.RESTClient, timeout time.Duration) (bool, error) {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := client.Get().AbsPath("/readyz").Do(probeCtx).Error()
	if err == nil {
		return true, nil
	}
	// The apiserver answered with a non-2xx status: it is up and reachable. Return
	// the error too so callers can log the status the apiserver returned.
	var statusErr *apierrors.StatusError
	return errors.As(err, &statusErr), err
}
