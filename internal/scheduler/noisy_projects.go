package scheduler

func antiAffinityNoisyProjects(ctx pipelineContext) (pipelineContext, error) {
	// TODO:
	// - Get pairs of (noisy project, host) from the database.
	// - Check if we're spawning a VM for a noisy project.
	// - Downvote the hosts this project is currently running on.
	return ctx, nil
}
