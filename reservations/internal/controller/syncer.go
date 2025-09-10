package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	gosync "sync"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/keystone"
	"github.com/cobaltcore-dev/cortex/reservations/api/v1alpha1"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
	"github.com/sapcc/go-bits/jobloop"
	"github.com/sapcc/go-bits/must"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	syncLog = ctrl.Log.WithName("sync")
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

	// Resolved flavor if the commitment is for a specific instance,
	// i.e. has the unit instances_<flavor_name>.
	Flavor *Flavor
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

// Client to fetch commitments.
type CommitmentsClient interface {
	// Init the client.
	Init(ctx context.Context)
	// Get all commitments with resolved metadata (e.g. project, flavor, ...).
	GetComputeCommitments(ctx context.Context) ([]Commitment, error)
}

// Commitments client fetching commitments from openstack services.
type commitmentsClient struct {
	// Basic config to authenticate against openstack.
	conf conf.KeystoneConfig

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
func NewCommitmentsClient(conf conf.KeystoneConfig) CommitmentsClient {
	return &commitmentsClient{conf: conf}
}

// Init the client.
func (c *commitmentsClient) Init(ctx context.Context) {
	syncLog.Info("authenticating against openstack", "url", c.conf.URL)
	auth := keystone.NewKeystoneAPI(c.conf)
	must.Succeed(auth.Authenticate(ctx))
	c.provider = auth.Client()
	syncLog.Info("authenticated against openstack")

	// Get the keystone endpoint.
	url := must.Return(c.provider.EndpointLocator(gophercloud.EndpointOpts{
		Type:         "identity",
		Availability: "public",
	}))
	syncLog.Info("using identity endpoint", "url", url)
	c.keystone = &gophercloud.ServiceClient{
		ProviderClient: c.provider,
		Endpoint:       url,
		Type:           "identity",
	}

	// Get the nova endpoint.
	url = must.Return(c.provider.EndpointLocator(gophercloud.EndpointOpts{
		Type:         "compute",
		Availability: "public",
	}))
	syncLog.Info("using nova endpoint", "url", url)
	c.nova = &gophercloud.ServiceClient{
		ProviderClient: c.provider,
		Endpoint:       url,
		Type:           "compute",
		Microversion:   "2.61",
	}

	// Get the limes endpoint.
	url = must.Return(c.provider.EndpointLocator(gophercloud.EndpointOpts{
		Type:         "resources",
		Availability: "public",
	}))
	syncLog.Info("using limes endpoint", "url", url)
	c.limes = &gophercloud.ServiceClient{
		ProviderClient: c.provider,
		Endpoint:       url,
		Type:           "resources",
	}
}

// Get all Nova flavors by their name to resolve instance commitments.
func (c *commitmentsClient) GetAllFlavors(ctx context.Context) ([]Flavor, error) {
	syncLog.Info("fetching all flavors from nova")
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
	syncLog.Info("fetched flavors from nova", "count", len(data.Flavors))
	return data.Flavors, nil
}

// Get all projects from Keystone to resolve commitments.
func (c *commitmentsClient) GetAllProjects(ctx context.Context) ([]projects.Project, error) {
	syncLog.Info("fetching projects from keystone")
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
	syncLog.Info("fetched projects from keystone", "count", len(data.Projects))
	return data.Projects, nil
}

// Get all available commitments from limes + keystone + nova.
// This function fetches the commitments for each project in parallel.
func (c *commitmentsClient) GetComputeCommitments(ctx context.Context) ([]Commitment, error) {
	projects, err := c.GetAllProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get projects: %w", err)
	}
	syncLog.Info("fetching flavor commitments from limes", "projects", len(projects))
	commitmentsMutex := gosync.Mutex{}
	commitments := []Commitment{}
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
			newResults, err := c.getCommitments(ctx, project)
			if err != nil {
				errChan <- err
				cancel()
				return
			}
			commitmentsMutex.Lock()
			commitments = append(commitments, newResults...)
			commitmentsMutex.Unlock()
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
			syncLog.Error(err, "failed to resolve commitments")
			return nil, err
		}
	}
	syncLog.Info("resolved commitments from limes", "count", len(commitments))

	flavors, err := c.GetAllFlavors(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get flavors: %w", err)
	}
	// Resolve the flavor for each commitment.
	flavorsByName := make(map[string]Flavor, len(flavors))
	for _, flavor := range flavors {
		flavorsByName[flavor.Name] = flavor
	}
	for i := range commitments {
		if !strings.HasPrefix(commitments[i].ResourceName, "instances_") {
			// Not an instance commitment.
			continue
		}
		flavorName := strings.TrimPrefix(commitments[i].ResourceName, "instances_")
		if flavor, ok := flavorsByName[flavorName]; ok {
			commitments[i].Flavor = &flavor
		} else {
			syncLog.Info("flavor not found for commitment", "flavor", flavorName, "commitment_id", commitments[i].ID)
		}
	}
	return commitments, nil
}

// Resolve the commitments for the given project.
func (c *commitmentsClient) getCommitments(ctx context.Context, project projects.Project) ([]Commitment, error) {
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
	var commitments []Commitment
	for _, c := range list.Commitments {
		if c.ServiceType != "compute" {
			// Not a compute commitment.
			continue
		}
		c.ProjectID = project.ID
		c.DomainID = project.DomainID
		commitments = append(commitments, c)
	}
	return commitments, nil
}

type Syncer struct {
	// Client to fetch commitments.
	CommitmentsClient
	// Client for the kubernetes API.
	client.Client
}

// Create a new compute reservation syncer.
func NewSyncer(k8sClient client.Client) *Syncer {
	config := conf.NewConfig[Config]()
	return &Syncer{
		CommitmentsClient: NewCommitmentsClient(config.Keystone),
		Client:            k8sClient,
	}
}

// Initialize the syncer.
func (s *Syncer) Init(ctx context.Context) {
	// Initialize the syncer.
	s.CommitmentsClient.Init(ctx)
}

// Convert a limes unit to a resource quantity.
func limesUnitToResource(val int64, unit string) (resource.Quantity, error) {
	switch unit {
	case "":
		return *resource.NewQuantity(val, resource.DecimalSI), nil
	case "B":
		return *resource.NewQuantity(val, resource.BinarySI), nil
	case "KiB":
		return *resource.NewQuantity(val*1024, resource.BinarySI), nil
	case "MiB":
		return *resource.NewQuantity(val*1024*1024, resource.BinarySI), nil
	case "GiB":
		return *resource.NewQuantity(val*1024*1024*1024, resource.BinarySI), nil
	case "TiB":
		return *resource.NewQuantity(val*1024*1024*1024*1024, resource.BinarySI), nil
	default:
		return resource.Quantity{}, fmt.Errorf("unsupported limes unit: %s", unit)
	}
}

// Fetch commitments and update/create reservations for each of them.
func (s *Syncer) SyncReservations(ctx context.Context) error {
	computeCommitments, err := s.GetComputeCommitments(ctx)
	if err != nil {
		return err
	}
	var reservations []v1alpha1.ComputeReservation
	// Instance reservations for each commitment.
	for _, commitment := range computeCommitments {
		// Get only the 5 first characters from the uuid. This should be safe enough.
		if len(commitment.UUID) < 5 {
			err := errors.New("commitment UUID is too short")
			syncLog.Error(err, "uuid is less than 5 characters", "uuid", commitment.UUID)
			continue
		}
		commitmentUUIDShort := commitment.UUID[:5]

		if commitment.Flavor != nil {
			// Flavor (instance) commitment
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
			continue
		}

		// Bare resource commitment
		reservation := v1alpha1.ComputeReservation{
			ObjectMeta: ctrl.ObjectMeta{
				Name: fmt.Sprintf("commitment-%s", commitmentUUIDShort),
			},
			Spec: v1alpha1.ComputeReservationSpec{
				Kind:      v1alpha1.ComputeReservationSpecKindBareResource,
				ProjectID: commitment.ProjectID,
				DomainID:  commitment.DomainID,
			},
		}
		quantity, err := limesUnitToResource(int64(commitment.Amount), commitment.Unit)
		if err != nil {
			syncLog.Error(err, "failed to convert limes unit", "resource name", commitment.ResourceName)
			continue
		}
		switch commitment.ResourceName {
		case "cores":
			reservation.Spec.BareResource.CPU = quantity
		case "ram":
			reservation.Spec.BareResource.Memory = quantity
		default:
			syncLog.Info("unsupported bare resource commitment unit", "resource name", commitment.ResourceName)
			continue
		}
		reservations = append(reservations, reservation)
	}
	for _, res := range reservations {
		// Check if the reservation already exists.
		nn := types.NamespacedName{Name: res.Name, Namespace: res.Namespace}
		var existing v1alpha1.ComputeReservation
		if err := s.Get(ctx, nn, &existing); err != nil {
			if !k8serrors.IsNotFound(err) {
				syncLog.Error(err, "failed to get reservation", "name", nn.Name)
				return err
			}
			// Reservation does not exist, create it.
			if err := s.Create(ctx, &res); err != nil {
				return err
			}
			syncLog.Info("created reservation", "name", nn.Name)
			continue
		}
		// Reservation exists, update it.
		existing.Spec = res.Spec
		if err := s.Update(ctx, &existing); err != nil {
			syncLog.Error(err, "failed to update reservation", "name", nn.Name)
			return err
		}
		syncLog.Info("updated reservation", "name", nn.Name)
	}
	syncLog.Info("synced reservations", "count", len(reservations))
	return nil
}

// Run a sync loop for reservations.
func (s *Syncer) Run(ctx context.Context) {
	go func() {
		for {
			if err := s.SyncReservations(ctx); err != nil {
				syncLog.Error(err, "failed to sync reservations")
			}
			time.Sleep(jobloop.DefaultJitter(time.Hour))
		}
	}()
}
