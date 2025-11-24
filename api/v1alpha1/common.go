package v1alpha1

// SchedulingDomain reflects the logical domain for scheduling, such as nova, cinder, manila.
// TODO: Or rename to type to avoid naming clash with openstack domains.
type SchedulingDomain string

const (
	// SchedulingDomainNova indicates an OpenStack Nova (compute) scheduling domain.
	SchedulingDomainNova SchedulingDomain = "nova"

	// SchedulingDomainCinder ...
	SchedulingDomainCinder SchedulingDomain = "cinder"
)
