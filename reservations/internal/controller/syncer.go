package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	gosync "sync"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/cobaltcore-dev/cortex/lib/keystone"
	"github.com/cobaltcore-dev/cortex/lib/sso"
	"github.com/cobaltcore-dev/cortex/reservations/api/v1alpha1"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
	"github.com/sapcc/go-bits/jobloop"
	"github.com/sapcc/go-bits/must"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Commitment model from the limes API.
// See: https://github.com/sapcc/limes/blob/5ea068b/docs/users/api-spec-resources.md?plain=1#L493
// See: https://github.com/sapcc/go-api-declarations/blob/94ee3e5/limes/resources/commitment.go#L19
type Commitment struct {
	// A unique numerical identifier for this commitment. This API uses this
	// numerical ID to refer to the commitment in other API calls.
	ID int `json:"id"`
	// A unique string identifier for this commitment. The next major version of
	// this API will use this UUID instead of the numerical ID to refer to
	// commitments in API calls.
	UUID string `json:"uuid"`
	// The resource for which usage is committed.
	ServiceType  string `json:"service_type"`
	ResourceName string `json:"resource_name"`
	// The availability zone in which usage is committed.
	AvailabilityZone string `json:"availability_zone"`
	// The amount of usage that was committed to.
	Amount uint64 `json:"amount"`
	// For measured resources, the unit for this resource. The value from the
	// amount field is measured in this unit.
	Unit string `json:"unit"`
	// The requested duration of this commitment, expressed as a comma-separated
	// sequence of positive integer multiples of time units like "1 year,
	// 3 months". Acceptable time units include "second", "minute", "hour",
	// "day", "month" and "year".
	Duration string `json:"duration"`
	// UNIX timestamp when this commitment was created.
	CreatedAt uint64 `json:"created_at"`
	// UNIX timestamp when this commitment should be confirmed. Only shown if
	// this was given when creating the commitment, to delay confirmation into
	// the future.
	ConfirmBy *uint64 `json:"confirm_by,omitempty"`
	// UNIX timestamp when this commitment was confirmed. Only shown after
	// confirmation.
	ConfirmedAt *uint64 `json:"confirmed_at,omitempty"`
	// UNIX timestamp when this commitment is set to expire. Note that the
	// duration counts from confirmBy (or from createdAt for immediately-
	// confirmed commitments) and is calculated at creation time, so this is
	// also shown on unconfirmed commitments.
	ExpiresAt uint64 `json:"expires_at"`
	// Whether the commitment is marked for transfer to a different project.
	// Transferable commitments do not count towards quota calculation in their
	// project, but still block capacity and still count towards billing. Not
	// shown if false.
	Transferable bool `json:"transferable"`
	// The current status of this commitment. If provided, one of "planned",
	// "pending", "guaranteed", "confirmed", "superseded", or "expired".
	Status string `json:"status,omitempty"`
	// Whether a mail notification should be sent if a created commitment is
	// confirmed. Can only be set if the commitment contains a confirmBy value.
	NotifyOnConfirm bool `json:"notify_on_confirm"`

	// Data from Keystone

	// The openstack project ID this commitment is for.
	ProjectID string `json:"project_id"`
	// The openstack domain ID this commitment is for.
	DomainID string `json:"domain_id"`
}

// OpenStack flavor model as returned by the Nova API under /flavors/detail.
// See: https://docs.openstack.org/api-ref/compute/#list-flavors
type Flavor struct {
	ID          string  `json:"id"`
	Disk        int     `json:"disk"` // in GB.
	RAM         int     `json:"ram"`  // in MB.
	Name        string  `json:"name"`
	RxTxFactor  float64 `json:"rxtx_factor"`
	VCPUs       int     `json:"vcpus"`
	IsPublic    bool    `json:"os-flavor-access:is_public"`
	Ephemeral   int     `json:"OS-FLV-EXT-DATA:ephemeral"`
	Description string  `json:"description"`

	// JSON string of extra specifications used when scheduling the flavor.
	ExtraSpecs map[string]string `json:"extra_specs" db:"extra_specs"`
}

// Commitment from limes where the flavor was resolved.
type FlavorCommitment struct {
	// The commitment as returned by the limes API.
	Commitment
	// Resolved flavor if the commitment is for a specific instance,
	// i.e. has the unit instances_<flavor_name>.
	Flavor Flavor
}

// Client to fetch commitments.
type CommitmentsClient interface {
	// Init the client.
	Init(ctx context.Context)
	// Get all commitments with resolved metadata (e.g. project, flavor, ...).
	GetFlavorCommitments(ctx context.Context) ([]FlavorCommitment, error)
}

// Commitments client fetching commitments from openstack services.
type commitmentsClient struct {
	// Basic config to authenticate against openstack.
	conf keystone.Config

	// Providerclient authenticated against openstack.
	provider *gophercloud.ProviderClient
	// Keystone service client for OpenStack.
	keystone *gophercloud.ServiceClient
	// Nova service client for OpenStack.
	nova *gophercloud.ServiceClient
	// Limes service client for OpenStack.
	limes *gophercloud.ServiceClient
}

// Create a new commitments client.
// By default, this client will fetch commitments from the limes API.
func NewCommitmentsClient(conf keystone.Config) CommitmentsClient {
	return &commitmentsClient{conf: conf}
}

// Init the client.
func (c *commitmentsClient) Init(ctx context.Context) {
	slog.Info("authenticating against openstack", "url", c.conf.URL)
	authOptions := gophercloud.AuthOptions{
		IdentityEndpoint: c.conf.URL,
		Username:         c.conf.OSUsername,
		DomainName:       c.conf.OSUserDomainName,
		Password:         c.conf.OSPassword,
		AllowReauth:      true,
		Scope: &gophercloud.AuthScope{
			ProjectName: c.conf.OSProjectName,
			DomainName:  c.conf.OSProjectDomainName,
		},
	}
	httpClient := must.Return(sso.NewHTTPClient(c.conf.SSO))
	provider := must.Return(openstack.NewClient(authOptions.IdentityEndpoint))
	provider.HTTPClient = *httpClient
	must.Succeed(openstack.Authenticate(ctx, provider, authOptions))
	c.provider = provider
	slog.Info("authenticated against openstack")

	// Get the keystone endpoint.
	url := must.Return(c.provider.EndpointLocator(gophercloud.EndpointOpts{
		Type:         "identity",
		Availability: "public",
	}))
	slog.Info("using identity endpoint", "url", url)
	c.keystone = &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		Type:           "identity",
	}

	// Get the nova endpoint.
	url = must.Return(c.provider.EndpointLocator(gophercloud.EndpointOpts{
		Type:         "compute",
		Availability: "public",
	}))
	slog.Info("using nova endpoint", "url", url)
	c.nova = &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		Type:           "compute",
		Microversion:   "2.61",
	}

	// Get the limes endpoint.
	url = must.Return(c.provider.EndpointLocator(gophercloud.EndpointOpts{
		Type:         "resources",
		Availability: "public",
	}))
	slog.Info("using limes endpoint", "url", url)
	c.limes = &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		Type:           "resources",
	}
}

// Get all Nova flavors by their name to resolve instance commitments.
func (c *commitmentsClient) GetAllFlavors(ctx context.Context) ([]Flavor, error) {
	slog.Info("fetching all flavors from nova")
	flo := flavors.ListOpts{AccessType: flavors.AllAccess}
	pages, err := flavors.ListDetail(c.nova, flo).AllPages(ctx)
	if err != nil {
		return nil, err
	}
	// Parse the json data into our custom model.
	var data = &struct {
		Flavors []Flavor `json:"flavors"`
	}{}
	if err := pages.(flavors.FlavorPage).ExtractInto(data); err != nil {
		return nil, err
	}
	slog.Info("fetched flavors from nova", "count", len(data.Flavors))
	return data.Flavors, nil
}

// Get all projects from Keystone to resolve commitments.
func (c *commitmentsClient) GetAllProjects(ctx context.Context) ([]projects.Project, error) {
	slog.Info("fetching projects from keystone")
	allPages, err := projects.List(c.keystone, nil).AllPages(ctx)
	if err != nil {
		return nil, err
	}
	var data = &struct {
		Projects []projects.Project `json:"projects"`
	}{}
	if err := allPages.(projects.ProjectPage).ExtractInto(data); err != nil {
		return nil, err
	}
	slog.Info("fetched projects from keystone", "count", len(data.Projects))
	return data.Projects, nil
}

// Get all available flavor commitments from limes + keystone + nova.
// This function fetches the commitments for each project in parallel.
func (c *commitmentsClient) GetFlavorCommitments(ctx context.Context) ([]FlavorCommitment, error) {
	projects, err := c.GetAllProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get projects: %w", err)
	}
	slog.Info("fetching flavor commitments from limes", "projects", len(projects))
	instanceCommitmentsMutex := gosync.Mutex{}
	instanceCommitments := []Commitment{}
	var wg gosync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Channel to communicate errors from goroutines.
	errChan := make(chan error, len(projects))
	for _, project := range projects {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Fetch instance commitments for the project.
			newResults, err := c.getInstanceCommitments(ctx, project)
			if err != nil {
				errChan <- err
				cancel()
				return
			}
			instanceCommitmentsMutex.Lock()
			instanceCommitments = append(instanceCommitments, newResults...)
			instanceCommitmentsMutex.Unlock()
		}()
		time.Sleep(jobloop.DefaultJitter(50 * time.Millisecond)) // Don't overload the API.
	}
	// Wait for all goroutines to finish and close the error channel.
	go func() {
		wg.Wait()
		close(errChan)
	}()
	// Return the first error encountered, if any.
	for err := range errChan {
		if err != nil {
			slog.Error("failed to resolve commitments", "error", err)
			return nil, err
		}
	}
	slog.Info("resolved instance commitments from limes", "count", len(instanceCommitments))

	flavors, err := c.GetAllFlavors(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get flavors: %w", err)
	}
	// Resolve the instance into the actual flavor spec.
	flavorsByName := make(map[string]Flavor, len(flavors))
	for _, flavor := range flavors {
		flavorsByName[flavor.Name] = flavor
	}
	var flavorCommitments = make([]FlavorCommitment, len(instanceCommitments))
	for i := range instanceCommitments {
		flavorCommitments[i] = FlavorCommitment{
			Commitment: instanceCommitments[i],
		}
		flavorName := strings.TrimPrefix(instanceCommitments[i].ResourceName, "instances_")
		if flavor, ok := flavorsByName[flavorName]; ok {
			flavorCommitments[i].Flavor = flavor
		} else {
			slog.Warn(
				"flavor not found for commitment",
				"commitment", instanceCommitments[i].ID,
				"flavor", flavorName,
			)
		}
	}
	return flavorCommitments, nil
}

// Resolve the instance commitments for the given project.
func (c *commitmentsClient) getInstanceCommitments(ctx context.Context, project projects.Project) ([]Commitment, error) {
	url := c.limes.Endpoint + "v1" +
		"/domains/" + project.DomainID +
		"/projects/" + project.ID +
		"/commitments"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", c.limes.Token())
	resp, err := c.limes.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	var list struct {
		Commitments []Commitment `json:"commitments"`
	}
	err = json.NewDecoder(resp.Body).Decode(&list)
	if err != nil {
		return nil, err
	}
	// Add the project information to each commitment.
	var instanceCommitments []Commitment
	for _, c := range list.Commitments {
		if c.ServiceType != "compute" {
			// Not a compute commitment.
			continue
		}
		if !strings.HasPrefix(c.ResourceName, "instances_") {
			// Not an instance commitment.
			continue
		}
		c.ProjectID = project.ID
		c.DomainID = project.DomainID
		instanceCommitments = append(instanceCommitments, c)
	}
	return instanceCommitments, nil
}

type ComputeReservationSyncer struct {
	// Client to fetch commitments.
	CommitmentsClient
	// Client for the kubernetes API.
	client.Client
}

// Fetch commitments and update/create reservations for each of them.
func (s *ComputeReservationSyncer) SyncReservations(ctx context.Context) error {
	// Commitments for a specific flavor.
	flavorCommitments, err := s.GetFlavorCommitments(ctx)
	if err != nil {
		return err
	}
	var reservations []v1alpha1.ComputeReservation
	// Instance reservations for each commitment.
	for _, commitment := range flavorCommitments {
		// Get only the 5 first characters from the uuid. This should be safe enough.
		if len(commitment.UUID) < 5 {
			slog.Error("commitment UUID is too short", "uuid", commitment.UUID)
			continue
		}
		commitmentUUIDShort := commitment.UUID[:5]
		for n := range commitment.Amount { // N instances
			reservations = append(reservations, v1alpha1.ComputeReservation{
				ObjectMeta: ctrl.ObjectMeta{
					Name: fmt.Sprintf("commitment-%s-%d", commitmentUUIDShort, n),
				},
				Spec: v1alpha1.ComputeReservationSpec{
					Kind:      v1alpha1.ComputeReservationSpecKindInstance,
					ProjectID: commitment.ProjectID,
					DomainID:  commitment.DomainID,
					Instance: v1alpha1.ComputeReservationSpecInstance{
						Flavor:     commitment.Flavor.Name,
						ExtraSpecs: commitment.Flavor.ExtraSpecs,
						Memory:     *resource.NewQuantity(int64(commitment.Flavor.RAM)*1024*1024, resource.BinarySI),
						VCPUs:      *resource.NewQuantity(int64(commitment.Flavor.VCPUs), resource.DecimalSI),
						Disk:       *resource.NewQuantity(int64(commitment.Flavor.Disk)*1024*1024*1024, resource.BinarySI),
					},
				},
			})
		}
	}
	for _, res := range reservations {
		// Check if the reservation already exists.
		nn := types.NamespacedName{Name: res.Name, Namespace: res.Namespace}
		var existing v1alpha1.ComputeReservation
		if err := s.Get(ctx, nn, &existing); err != nil {
			if !k8serrors.IsNotFound(err) {
				slog.Error("failed to get reservation", "error", err, "name", nn.Name)
				return err
			}
			// Reservation does not exist, create it.
			if err := s.Create(ctx, &res); err != nil {
				return err
			}
			slog.Info("created reservation", "name", nn.Name)
			continue
		}
		// Reservation exists, update it.
		existing.Spec = res.Spec
		if err := s.Update(ctx, &existing); err != nil {
			slog.Error("failed to update reservation", "error", err, "name", nn.Name)
			return err
		}
		slog.Info("updated reservation", "name", nn.Name)
	}
	return nil
}
