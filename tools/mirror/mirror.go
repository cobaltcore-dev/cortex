// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	// Import to initialize client-go plugins (e.g., exec, oidc)
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

const (
	// Default period for watch reconnection attempts.
	defaultWatchRetryDelay = 5 * time.Second
	// Default period between watch restarts.
	defaultWatchRestartDelay = 1 * time.Second
	// Default period for graceful shutdown cleanup.
	defaultShutdownGracePeriod = 1 * time.Second
)

// usage describes the command-line interface.
const usage = `Usage: mirror [options]

Mirror resources from one Kubernetes cluster to another in real-time.

This tool watches resources in a source cluster and automatically synchronizes
them to a target cluster. It performs an initial sync of all existing resources,
then continuously watches for changes (add, modify, delete) and mirrors them.

Options:
  -h, --help            Show this help message and exit
  --source-kubeconfig   Path to the kubeconfig file for the source cluster (required)
  --source-context      Context name for the source cluster (optional)
  --target-kubeconfig   Path to the kubeconfig file for the target cluster (required)
  --target-context      Context name for the target cluster (optional)
  --gvr                 GroupVersionResource to mirror in format resource.group/version
                        Example: decisions.cortex.cloud/v1alpha1
                        Multiple GVRs can be comma-separated (required)
  --namespace           Namespace to mirror resources from (optional)
                        Omit for cluster-scoped resources

Examples:
  # Mirror decisions from cluster-b to cluster-a
  mirror --source-kubeconfig ~/.kube/config --source-context kind-cluster-b \
         --target-kubeconfig ~/.kube/config --target-context kind-cluster-a \
         --gvr decisions.cortex.cloud/v1alpha1

  # Mirror multiple resource types
  mirror --source-kubeconfig source.yaml --target-kubeconfig target.yaml \
         --gvr decisions.cortex.cloud/v1alpha1,kpis.cortex.cloud/v1alpha1
`

// Holds the configuration for the mirror tool.
type config struct {
	sourceKubeconfig string
	sourceContext    string
	targetKubeconfig string
	targetContext    string
	gvr              string
	namespace        string
}

// Check that all required configuration is present.
func (c *config) validate() error {
	if c.sourceKubeconfig == "" {
		return errors.New("--source-kubeconfig is required")
	}
	if c.targetKubeconfig == "" {
		return errors.New("--target-kubeconfig is required")
	}
	if c.gvr == "" {
		return errors.New("--gvr is required")
	}
	return nil
}

// Parse command-line flags and returns a validated config.
func parseFlags() (*config, error) {
	cfg := &config{}

	flag.StringVar(&cfg.sourceKubeconfig, "source-kubeconfig", "", "Path to the kubeconfig file for the source cluster")
	flag.StringVar(&cfg.sourceContext, "source-context", "", "Context name for the source cluster")
	flag.StringVar(&cfg.targetKubeconfig, "target-kubeconfig", "", "Path to the kubeconfig file for the target cluster")
	flag.StringVar(&cfg.targetContext, "target-context", "", "Context name for the target cluster")
	flag.StringVar(&cfg.gvr, "gvr", "", "GroupVersionResource to mirror")
	flag.StringVar(&cfg.namespace, "namespace", "", "Namespace to mirror resources from")

	help := flag.Bool("help", false, "Show help message")
	flag.BoolVar(help, "h", false, "Show help message")

	flag.Parse()

	if *help {
		fmt.Print(usage)
		os.Exit(0)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Create a dynamic Kubernetes client for the given kubeconfig and context.
func createDynamicClient(kubeconfigPath, contextName string) (dynamic.Interface, error) {
	config, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}

	if contextName != "" {
		config.CurrentContext = contextName
	}

	restConfig, err := clientcmd.NewNonInteractiveClientConfig(
		*config,
		config.CurrentContext,
		&clientcmd.ConfigOverrides{},
		nil,
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("create rest config: %w", err)
	}

	client, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create dynamic client: %w", err)
	}

	return client, nil
}

// Parse the GVR string into a GroupVersionResource structure.
// Expected format: resource.group/version (e.g., decisions.cortex.cloud/v1alpha1)
func parseGVR(gvrStr string) (schema.GroupVersionResource, error) {
	parts := strings.Split(gvrStr, "/")
	if len(parts) != 2 {
		return schema.GroupVersionResource{}, fmt.Errorf(
			"invalid format (expected resource.group/version): %s", gvrStr)
	}

	version := parts[1]
	resourceGroupParts := strings.Split(parts[0], ".")
	if len(resourceGroupParts) < 2 {
		return schema.GroupVersionResource{}, fmt.Errorf(
			"invalid resource.group format: %s", parts[0])
	}

	return schema.GroupVersionResource{
		Resource: resourceGroupParts[0],
		Group:    strings.Join(resourceGroupParts[1:], "."),
		Version:  version,
	}, nil
}

// Watches resources in a source cluster and mirrors them to a target cluster.
type mirrorer struct {
	sourceClient        dynamic.Interface
	targetClient        dynamic.Interface
	gvr                 schema.GroupVersionResource
	namespace           string
	lastResourceVersion string
}

// Return the appropriate resource interface based on namespace scope.
func (m *mirrorer) getResourceInterface(client dynamic.Interface) dynamic.ResourceInterface {
	if m.namespace != "" {
		return client.Resource(m.gvr).Namespace(m.namespace)
	}
	return client.Resource(m.gvr)
}

// Begin the mirroring process and block until the context is cancelled.
func (m *mirrorer) start(ctx context.Context) error {
	scope := "cluster-scoped"
	if m.namespace != "" {
		scope = fmt.Sprintf("namespace %q", m.namespace)
	}
	log.Printf("Starting mirrorer for %s (%s)", m.gvr.Resource, scope)

	if err := m.initialSync(ctx); err != nil {
		return fmt.Errorf("initial sync: %w", err)
	}

	return m.watchLoop(ctx)
}

// Perform an initial synchronization of all existing resources.
func (m *mirrorer) initialSync(ctx context.Context) error {
	log.Printf("Performing initial sync for %s", m.gvr.Resource)

	resourceInterface := m.getResourceInterface(m.sourceClient)
	list, err := resourceInterface.List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list resources: %w", err)
	}

	log.Printf("Found %d resource(s) to sync", len(list.Items))

	for i := range list.Items {
		if err := m.mirrorResource(ctx, &list.Items[i]); err != nil {
			log.Printf("Warning: failed to mirror resource %s: %v", list.Items[i].GetName(), err)
		}
	}

	// Store the resource version from the list to start watching from this point
	m.lastResourceVersion = list.GetResourceVersion()
	log.Printf("Initial sync complete for %s (resource version: %s)", m.gvr.Resource, m.lastResourceVersion)
	return nil
}

// Continuously watch for resource changes and mirror them.
func (m *mirrorer) watchLoop(ctx context.Context) error {
	resourceInterface := m.getResourceInterface(m.sourceClient)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// Start watch from last seen resource version to avoid "required revision has been compacted" errors
		// and prevent duplicate creates after initial sync
		watchOpts := metav1.ListOptions{
			ResourceVersion: m.lastResourceVersion,
		}

		watcher, err := resourceInterface.Watch(ctx, watchOpts)
		if err != nil {
			log.Printf("Failed to start watch: %v, retrying in %v...", err, defaultWatchRetryDelay)
			time.Sleep(defaultWatchRetryDelay)
			continue
		}

		log.Printf("Watch started for %s (from resource version: %s)", m.gvr.Resource, m.lastResourceVersion)

		// Process events and get the last resource version
		lastRV, err := m.processEvents(ctx, watcher)
		if err != nil {
			log.Printf("Watch error: %v, restarting...", err)
		}

		// Update resource version for next watch
		if lastRV != "" {
			m.lastResourceVersion = lastRV
		}

		watcher.Stop()
		time.Sleep(defaultWatchRestartDelay)
	}
}

// Process watch events from the source cluster and return the last resource version seen.
func (m *mirrorer) processEvents(ctx context.Context, watcher watch.Interface) (string, error) {
	var lastResourceVersion string

	for {
		select {
		case <-ctx.Done():
			return lastResourceVersion, nil
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return lastResourceVersion, errors.New("watch channel closed")
			}

			// Track the resource version from each event
			if obj, ok := event.Object.(*unstructured.Unstructured); ok {
				if rv := obj.GetResourceVersion(); rv != "" {
					lastResourceVersion = rv
				}
			}

			if err := m.handleEvent(ctx, event); err != nil {
				log.Printf("Error handling event: %v", err)
			}
		}
	}
}

// Handle a single watch event.
func (m *mirrorer) handleEvent(ctx context.Context, event watch.Event) error {
	obj, ok := event.Object.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("unexpected object type: %T", event.Object)
	}

	switch event.Type {
	case watch.Added, watch.Modified:
		return m.mirrorResource(ctx, obj)
	case watch.Deleted:
		return m.deleteResource(ctx, obj)
	case watch.Error:
		return fmt.Errorf("watch error event: %v", event.Object)
	default:
		log.Printf("Unknown event type: %s", event.Type)
		return nil
	}
}

// Create or update a resource in the target cluster.
func (m *mirrorer) mirrorResource(ctx context.Context, obj *unstructured.Unstructured) error {
	name := obj.GetName()
	namespace := obj.GetNamespace()
	resourceID := formatResourceID(namespace, name)

	// Prepare the object for the target cluster
	targetObj := m.prepareTargetObject(obj)

	targetInterface := m.getResourceInterface(m.targetClient)

	// Check if resource exists in target
	existing, err := targetInterface.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			// Check if this is a CRD not found error
			if apierrors.IsNotFound(err) || apierrors.ReasonForError(err) == metav1.StatusReasonNotFound {
				return fmt.Errorf("get resource: %w (hint: ensure CRD %s.%s is installed in target cluster)", err, m.gvr.Resource, m.gvr.Group)
			}
			return fmt.Errorf("get resource: %w", err)
		}
		// Resource doesn't exist, create it
		if _, err := targetInterface.Create(ctx, targetObj, metav1.CreateOptions{}); err != nil {
			// Provide helpful error message if CRD doesn't exist
			if apierrors.IsNotFound(err) {
				return fmt.Errorf("create resource: %w (hint: ensure CRD %s.%s/%s is installed in target cluster)", err, m.gvr.Resource, m.gvr.Group, m.gvr.Version)
			}
			return fmt.Errorf("create resource: %w", err)
		}
		log.Printf("Created: %s", resourceID)
		return nil
	}

	// Resource exists, update it
	targetObj.SetResourceVersion(existing.GetResourceVersion())
	if _, err := targetInterface.Update(ctx, targetObj, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update resource: %w", err)
	}

	// Update status subresource if present
	if err := m.updateStatus(ctx, targetInterface, obj, name); err != nil {
		log.Printf("Warning: failed to update status for %s: %v", resourceID, err)
	}

	log.Printf("Updated: %s", resourceID)
	return nil
}

// Update the status subresource of a resource in the target cluster.
func (m *mirrorer) updateStatus(ctx context.Context, targetInterface dynamic.ResourceInterface, sourceObj *unstructured.Unstructured, name string) error {
	// Check if source object has a status field
	status, found, err := unstructured.NestedMap(sourceObj.Object, "status")
	if err != nil || !found || len(status) == 0 {
		// No status to update
		return err
	}

	// Get the current resource from target to get its latest resourceVersion
	current, err := targetInterface.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get current resource: %w", err)
	}

	// Set the status on the current object
	if err := unstructured.SetNestedMap(current.Object, status, "status"); err != nil {
		return fmt.Errorf("set status: %w", err)
	}

	// Update the status subresource
	_, err = targetInterface.UpdateStatus(ctx, current, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	return nil
}

// Prepare a copy of the object with metadata fields removed.
func (m *mirrorer) prepareTargetObject(obj *unstructured.Unstructured) *unstructured.Unstructured {
	targetObj := obj.DeepCopy()

	// Remove server-managed metadata fields that shouldn't be copied
	fieldsToRemove := [][]string{
		{"metadata", "resourceVersion"},
		{"metadata", "uid"},
		{"metadata", "creationTimestamp"},
		{"metadata", "generation"},
		{"metadata", "managedFields"},
		{"metadata", "selfLink"},
		{"metadata", "ownerReferences"}, // Remove owner references to prevent garbage collection
	}

	for _, fields := range fieldsToRemove {
		unstructured.RemoveNestedField(targetObj.Object, fields...)
	}

	return targetObj
}

// Delete a resource from the target cluster.
func (m *mirrorer) deleteResource(ctx context.Context, obj *unstructured.Unstructured) error {
	name := obj.GetName()
	namespace := obj.GetNamespace()
	resourceID := formatResourceID(namespace, name)

	targetInterface := m.getResourceInterface(m.targetClient)

	err := targetInterface.Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Already deleted, no action needed
			return nil
		}
		return fmt.Errorf("delete resource: %w", err)
	}

	log.Printf("Deleted: %s", resourceID)
	return nil
}

// Format a resource identifier for logging.
func formatResourceID(namespace, name string) string {
	if namespace != "" {
		return fmt.Sprintf("%s/%s", namespace, name)
	}
	return name
}

// Setup a context that is cancelled on SIGINT/SIGTERM.
func setupSignalHandler() context.Context {
	ctx, cancel := context.WithCancel(context.Background())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Printf("Received signal %v, initiating shutdown...", sig)
		cancel()
	}()

	return ctx
}

// Main execution function.
func run(cfg *config) error {
	ctx := setupSignalHandler()

	// Create source client
	log.Printf("Connecting to source cluster (kubeconfig: %s, context: %s)",
		cfg.sourceKubeconfig, cfg.sourceContext)
	sourceClient, err := createDynamicClient(cfg.sourceKubeconfig, cfg.sourceContext)
	if err != nil {
		return fmt.Errorf("create source client: %w", err)
	}

	// Create target client
	log.Printf("Connecting to target cluster (kubeconfig: %s, context: %s)",
		cfg.targetKubeconfig, cfg.targetContext)
	targetClient, err := createDynamicClient(cfg.targetKubeconfig, cfg.targetContext)
	if err != nil {
		return fmt.Errorf("create target client: %w", err)
	}

	// Parse and start mirroring for each GVR
	gvrStrings := strings.Split(cfg.gvr, ",")
	errCh := make(chan error, len(gvrStrings))

	for _, gvrStr := range gvrStrings {
		gvrStr = strings.TrimSpace(gvrStr)
		if gvrStr == "" {
			continue
		}

		gvr, err := parseGVR(gvrStr)
		if err != nil {
			log.Printf("Warning: skipping invalid GVR %q: %v", gvrStr, err)
			continue
		}

		m := &mirrorer{
			sourceClient: sourceClient,
			targetClient: targetClient,
			gvr:          gvr,
			namespace:    cfg.namespace,
		}

		go func(gvrString string) {
			if err := m.start(ctx); err != nil && ctx.Err() == nil {
				errCh <- fmt.Errorf("mirrorer for %s: %w", gvrString, err)
			}
		}(gvrStr)
	}

	// Wait for shutdown or error
	select {
	case <-ctx.Done():
		log.Println("Shutting down gracefully...")
	case err := <-errCh:
		return err
	}

	// Allow time for cleanup
	time.Sleep(defaultShutdownGracePeriod)
	log.Println("Shutdown complete")
	return nil
}

// Entry point of the program.
func main() {
	cfg, err := parseFlags()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n\n%s", err, usage)
		os.Exit(1)
	}

	if err := run(cfg); err != nil {
		log.Fatalf("Fatal error: %v", err)
	}
}
