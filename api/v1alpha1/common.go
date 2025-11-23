package v1alpha1

// SchedulingDomain reflects the logical domain for scheduling, such as nova, cinder, manila.
type SchedulingDomain string

const (
	// SchedulingDomainNova indicates an OpenStack Nova (compute) scheduling domain.
	SchedulingDomainNova SchedulingDomain = "nova"

	// SchedulingDomainCinder ...
	SchedulingDomainCinder SchedulingDomain = "cinder"
)
